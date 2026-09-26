import { lstat } from "node:fs/promises";
import { request as httpRequest } from "node:http";
import { join } from "node:path";
import type { DesiredState } from "#/domain/state.ts";
import { isPhpApp } from "#/domain/state.ts";

const MAX_REQUEST_BYTES = 1024 * 1024;
const MAX_RESPONSE_BYTES = 32 * 1024 * 1024;
const MAX_STREAM_BYTES = 8 * 1024 * 1024;
const REQUEST_HEADERS = [
  "accept",
  "accept-encoding",
  "content-type",
  "if-match",
  "if-none-match",
  "last-event-id",
] as const;
const RESPONSE_HEADERS = [
  "content-type",
  "content-encoding",
  "content-disposition",
  "etag",
  "last-modified",
  "vary",
] as const;
const STREAM_TIMEOUT_MS = 15 * 60 * 1000;

/** An authorization result, issued by Bento's per-app session layer, never from a URL/header. */
export type SchedulerAccess = {
  app: string;
  /** Exact origin of the Bento request. */
  origin: string;
  /** Trusted app prefix, including trailing slash. */
  basePath: string;
};

/** No HTTP route should call this without authenticating and authorizing access first. */
export async function proxyMinicrond(
  request: Request,
  state: DesiredState,
  access: SchedulerAccess,
  stackRoot: string,
): Promise<Response> {
  const deny = (status: number) => new Response(null, { status, headers: { "cache-control": "no-store" } });
  const app = state.apps[access.app];
  if (!app || !app.enabled || !isPhpApp(app)) return deny(404);
  // Never trust forwarded host/proto or an origin supplied as part of the request.
  const url = new URL(request.url);
  if (
    url.origin !== access.origin ||
    !access.basePath.endsWith("/") ||
    (url.pathname !== access.basePath.slice(0, -1) && !url.pathname.startsWith(access.basePath)) ||
    !["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"].includes(request.method)
  )
    return deny(403);
  if (url.username || url.password || url.pathname.startsWith("//")) return deny(400);
  let decodedPath: string;
  try {
    decodedPath = decodeURIComponent(url.pathname);
  } catch {
    return deny(400);
  }
  if (decodedPath.includes("\\") || decodedPath.startsWith("//")) return deny(400);
  // The browser never receives, rotates, or supplies a minicrond credential.
  if (/^\/api\/v1\/(?:auth|token)(?:\/|$)/.test(decodedPath.slice(access.basePath.length - 1))) return deny(404);
  // Reject encoded path separators/aliases; authorization must cover the exact upstream path.
  if (decodedPath !== url.pathname || decodedPath.includes("/../") || decodedPath.endsWith("/..")) return deny(400);
  if (request.method !== "GET" && request.method !== "HEAD") {
    if (
      request.headers.get("origin") !== access.origin ||
      (request.headers.has("sec-fetch-site") && request.headers.get("sec-fetch-site") !== "same-origin")
    )
      return deny(403);
  }
  const declared = request.headers.get("content-length");
  if (declared !== null && (!/^\d+$/.test(declared) || Number(declared) > MAX_REQUEST_BYTES)) return deny(413);
  const chunks: Uint8Array[] = [];
  let total = 0;
  if (request.body) {
    const reader = request.body.getReader();
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > MAX_REQUEST_BYTES) {
        await reader.cancel();
        return deny(413);
      }
      chunks.push(value);
    }
  }

  // A privileged Bento server can connect directly (minicrond permits a root
  // peer). For a non-root server, never weaken SO_PEERCRED or use a shared
  // socket: a dedicated UID-matched relay will be required before enabling UI.
  const peerUid = process.getuid?.();
  if (peerUid === undefined || (peerUid !== 0 && peerUid !== app.uid)) return deny(503);
  const home = join(stackRoot, "homes", access.app);
  const data = join(home, ".local", "share", "minicron");
  const socket = join(data, "minicron.sock");
  try {
    const [homeStat, dataStat, socketStat] = await Promise.all([lstat(home), lstat(data), lstat(socket)]);
    if (
      !homeStat.isDirectory() ||
      homeStat.isSymbolicLink() ||
      homeStat.uid !== app.uid ||
      !dataStat.isDirectory() ||
      dataStat.isSymbolicLink() ||
      dataStat.uid !== app.uid ||
      !socketStat.isSocket() ||
      socketStat.uid !== app.uid ||
      (socketStat.mode & 0o077) !== 0
    )
      return deny(503);
  } catch {
    return deny(503);
  }

  const headers: Record<string, string> = {};
  for (const name of REQUEST_HEADERS) {
    const value = request.headers.get(name);
    if (value !== null) headers[name] = value;
  }
  if (total) headers["content-length"] = String(total);
  const output = await new Promise<Response>((resolve) => {
    const upstream = httpRequest(
      {
        socketPath: socket,
        path: `${url.pathname}${url.search}`,
        method: request.method,
        headers,
        timeout: STREAM_TIMEOUT_MS,
      },
      (upstreamResponse) => {
        const outgoingHeaders = new Headers({
          "cache-control": "no-store",
          "x-content-type-options": "nosniff",
        });
        for (const name of RESPONSE_HEADERS) {
          const value = upstreamResponse.headers[name];
          if (typeof value === "string") outgoingHeaders.set(name, value);
        }
        outgoingHeaders.delete("set-cookie");
        outgoingHeaders.set(
          "content-security-policy",
          `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'self'`,
        );
        outgoingHeaders.set("referrer-policy", "no-referrer");
        // Never let an upstream redirect the browser outside this app's prefix.
        const location = upstreamResponse.headers.location;
        if (location) {
          try {
            const target = new URL(location, url);
            if (target.origin === access.origin && target.pathname.startsWith(access.basePath))
              outgoingHeaders.set("location", `${target.pathname}${target.search}${target.hash}`);
          } catch {
            // Ignore malformed upstream redirects.
          }
        }
        const sse = upstreamResponse.headers["content-type"]?.startsWith("text/event-stream") ?? false;
        let received = 0;
        let closed = false;
        const limit = sse ? MAX_STREAM_BYTES : MAX_RESPONSE_BYTES;
        const body = new ReadableStream<Uint8Array>({
          start(controller) {
            upstreamResponse.on("data", (chunk: Buffer) => {
              if (closed) return;
              received += chunk.byteLength;
              if (received > limit) {
                closed = true;
                upstreamResponse.destroy();
                controller.error(new Error("scheduler response limit exceeded"));
                return;
              }
              controller.enqueue(chunk);
              if ((controller.desiredSize ?? 0) <= 0) upstreamResponse.pause();
            });
            upstreamResponse.on("end", () => {
              if (closed) return;
              closed = true;
              controller.close();
            });
            upstreamResponse.on("error", (error) => {
              if (closed) return;
              closed = true;
              controller.error(error);
            });
          },
          pull() {
            upstreamResponse.resume();
          },
          cancel() {
            closed = true;
            upstreamResponse.destroy();
          },
        });
        resolve(
          new Response(request.method === "HEAD" ? null : body, {
            status: upstreamResponse.statusCode ?? 502,
            headers: outgoingHeaders,
          }),
        );
        if (request.method === "HEAD") upstreamResponse.destroy();
      },
    );
    upstream.on("timeout", () => upstream.destroy());
    upstream.on("error", () => resolve(deny(502)));
    request.signal.addEventListener("abort", () => upstream.destroy(), { once: true });
    upstream.end(total ? Buffer.concat(chunks) : undefined);
  });
  return output;
}
