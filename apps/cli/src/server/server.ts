import { timingSafeEqual } from "node:crypto";
import { extname, join } from "node:path";
import { RPCHandler } from "@orpc/server/fetch";
import type { CliContext } from "../commands/context.ts";
import { resolveAssetRoot } from "../platform/assets.ts";
import { createWebRouter } from "./router.ts";
import {
  closeTerminalSession,
  handleTerminalMessage,
  isSameOriginRequest,
  prepareTerminalSession,
  startTerminalSession,
  type TerminalSession,
} from "./terminal.ts";
// Bun's file loader embeds these assets in standalone builds.
import webIndex from "../../../web/dist/index.html" with { type: "file" };
// @ts-expect-error generated browser asset
import webScript from "../../../web/dist/app.js" with { type: "file" };
// @ts-expect-error generated browser asset
import webStyle from "../../../web/dist/app.css" with { type: "file" };

export type ServeOptions = {
  hostname: string;
  port: number;
  open: boolean;
  basicAuth?: string;
};

type ActiveTerminal = {
  session: TerminalSession;
  attached: boolean;
  idleTimer: ReturnType<typeof setTimeout>;
  removing?: Promise<void>;
};

const SECURITY_HEADERS = {
  "content-security-policy":
    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://fonts.googleapis.com; font-src https://cdn.jsdelivr.net https://fonts.gstatic.com; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
  "referrer-policy": "no-referrer",
  "x-content-type-options": "nosniff",
  "x-frame-options": "DENY",
} as const;

export async function runWebServer(ctx: CliContext, options: ServeOptions): Promise<number> {
  const router = createWebRouter(ctx);
  const rpc = new RPCHandler(router);
  const assetRoot = join(resolveAssetRoot(), "..", "web", "dist");
  // Static URL references let Bun preserve the exact paths in compiled executables.
  const embeddedAssets: Record<string, string> = {
    "index.html": webIndex as unknown as string,
    "app.js": webScript,
    "app.css": webStyle,
  };

  const expectedAuthorization =
    options.basicAuth === undefined
      ? undefined
      : `Basic ${Buffer.from(options.basicAuth, "utf8").toString("base64")}`;

  const maxTerminals = 4;
  let pendingTerminalCount = 0;
  const activeTerminals = new Map<string, ActiveTerminal>();

  async function removeTerminal(id: string): Promise<void> {
    const active = activeTerminals.get(id);
    if (!active) return;
    if (active.removing) return await active.removing;
    clearTimeout(active.idleTimer);
    active.removing = closeTerminalSession(active.session);
    await active.removing;
    if (activeTerminals.get(id) === active) activeTerminals.delete(id);
  }

  const server = Bun.serve({
    hostname: options.hostname,
    port: options.port,
    idleTimeout: 255,
    async fetch(request) {
      if (
        expectedAuthorization !== undefined &&
        !matchesBasicAuthorization(request.headers.get("authorization"), expectedAuthorization)
      ) {
        return withSecurity(
          new Response("Authentication required", {
            status: 401,
            headers: {
              "cache-control": "no-store",
              "www-authenticate": 'Basic realm="Bento", charset="UTF-8"',
            },
          }),
        );
      }

      const url = new URL(request.url);
      if (url.pathname === "/healthz") return withSecurity(Response.json({ ok: true }));
      if (url.pathname === "/api/terminal" && request.method === "POST") {
        if (!isSameOriginRequest(request))
          return withSecurity(new Response("Request origin denied", { status: 403 }));
        if (activeTerminals.size + pendingTerminalCount >= maxTerminals)
          return withSecurity(
            Response.json({ error: "Too many active terminal sessions" }, { status: 503 }),
          );
        if (Number(request.headers.get("content-length") ?? 0) > 4_096)
          return withSecurity(new Response("Request too large", { status: 413 }));

        let app: string | undefined;
        try {
          const body = await readBoundedRequestText(request, 4_096);
          const input: unknown = body === null ? null : JSON.parse(body);
          if (input && typeof input === "object" && "app" in input && typeof input.app === "string")
            app = input.app.trim();
        } catch {
          // The validation response below covers malformed JSON.
        }
        if (!app)
          return withSecurity(Response.json({ error: "Application is required" }, { status: 400 }));

        pendingTerminalCount += 1;
        try {
          const session = await prepareTerminalSession(ctx, app);
          if (request.signal.aborted) {
            pendingTerminalCount -= 1;
            await closeTerminalSession(session);
            return withSecurity(new Response(null, { status: 499 }));
          }
          const id = ctx.platform.random.hex(24);
          const active: ActiveTerminal = {
            session,
            attached: false,
            idleTimer: setTimeout(() => void removeTerminal(id), 30_000),
          };
          activeTerminals.set(id, active);
          pendingTerminalCount -= 1;
          return withSecurity(
            Response.json(
              { id },
              { headers: { "cache-control": "no-store", "x-content-type-options": "nosniff" } },
            ),
          );
        } catch {
          pendingTerminalCount -= 1;
          return withSecurity(
            Response.json(
              { error: "Unable to prepare shell for this application" },
              { status: 400 },
            ),
          );
        }
      }

      const terminalRoute = /^\/api\/terminal\/([a-f0-9]{48})(?:\/(output|input))?$/.exec(
        url.pathname,
      );
      if (terminalRoute) {
        const id = terminalRoute[1]!;
        const action = terminalRoute[2];
        const active = activeTerminals.get(id);
        if (!active)
          return withSecurity(
            Response.json({ error: "Terminal session not found" }, { status: 404 }),
          );

        if (action === "output" && request.method === "GET") {
          if (active.attached)
            return withSecurity(
              new Response("Terminal output is already attached", { status: 409 }),
            );
          active.attached = true;
          clearTimeout(active.idleTimer);
          const encoder = new TextEncoder();
          let streamClosed = false;
          const stream = new ReadableStream<Uint8Array>(
            {
              start(controller) {
                const closeOutput = () => {
                  if (streamClosed) return;
                  streamClosed = true;
                  try {
                    controller.close();
                  } catch {
                    // The output request may already be closed.
                  }
                };
                startTerminalSession(active.session, {
                  data(data) {
                    if (controller.desiredSize !== null && controller.desiredSize <= 0) {
                      closeOutput();
                      void removeTerminal(id);
                      return;
                    }
                    try {
                      controller.enqueue(data);
                    } catch {
                      void removeTerminal(id);
                    }
                  },
                  exit(exitCode) {
                    try {
                      controller.enqueue(
                        encoder.encode(
                          `\r\n\x1b[90mShell exited (code ${exitCode ?? "unknown"}).\x1b[0m\r\n`,
                        ),
                      );
                    } catch {
                      // The output request may already be closed.
                    }
                    closeOutput();
                    void removeTerminal(id);
                  },
                  error() {
                    try {
                      controller.enqueue(
                        encoder.encode("\r\n\x1b[31mUnable to start shell.\x1b[0m\r\n"),
                      );
                    } catch {
                      // The output request may already be closed.
                    }
                    closeOutput();
                    void removeTerminal(id);
                  },
                  close: closeOutput,
                });
              },
              cancel() {
                streamClosed = true;
                void removeTerminal(id);
              },
            },
            {
              highWaterMark: 1024 * 1024,
              size(chunk) {
                return chunk?.byteLength ?? 0;
              },
            },
          );
          return withSecurity(
            new Response(stream, {
              headers: {
                "content-type": "application/octet-stream",
                "cache-control": "no-store",
                "x-accel-buffering": "no",
              },
            }),
          );
        }

        if (action === "input" && request.method === "POST") {
          if (!isSameOriginRequest(request))
            return withSecurity(new Response("Request origin denied", { status: 403 }));
          if (Number(request.headers.get("content-length") ?? 0) > 64 * 1024)
            return withSecurity(new Response("Request too large", { status: 413 }));
          const body = await readBoundedRequestText(request, 64 * 1024);
          if (body === null)
            return withSecurity(new Response("Request too large", { status: 413 }));
          const accepted = handleTerminalMessage(active.session, body);
          return withSecurity(new Response(null, { status: accepted ? 204 : 400 }));
        }

        if (!action && request.method === "DELETE") {
          if (!isSameOriginRequest(request))
            return withSecurity(new Response("Request origin denied", { status: 403 }));
          await removeTerminal(id);
          return withSecurity(new Response(null, { status: 204 }));
        }

        return withSecurity(new Response("Method not allowed", { status: 405 }));
      }
      if (url.pathname === "/rpc" || url.pathname.startsWith("/rpc/")) {
        const result = await rpc.handle(request, { prefix: "/rpc" });
        if (result.matched) return withSecurity(result.response);
        return withSecurity(Response.json({ error: "RPC procedure not found" }, { status: 404 }));
      }
      if (request.method !== "GET" && request.method !== "HEAD")
        return withSecurity(new Response("Method not allowed", { status: 405 }));
      const requested = url.pathname === "/" ? "index.html" : url.pathname.slice(1);
      if (!/^[a-zA-Z0-9._/-]+$/.test(requested) || requested.includes(".."))
        return withSecurity(new Response("Not found", { status: 404 }));
      let file = Bun.file(embeddedAssets[requested] ?? join(assetRoot, requested));
      if (!(await file.exists()))
        file = Bun.file(embeddedAssets["index.html"] ?? join(assetRoot, "index.html"));
      if (!(await file.exists()))
        return withSecurity(
          new Response("Web assets are missing. Run 'bun run web:build'.", { status: 503 }),
        );
      const headers = new Headers({
        "content-type": contentType(file.name ?? requested),
        "cache-control": requested === "index.html" ? "no-cache" : "public, max-age=3600",
      });
      return withSecurity(new Response(request.method === "HEAD" ? null : file, { headers }));
    },
    error(error) {
      console.error(`web server error: ${error instanceof Error ? error.message : String(error)}`);
      return withSecurity(new Response("Internal server error", { status: 500 }));
    },
  });

  const address = `http://${formatHost(options.hostname)}:${server.port}`;
  ctx.log.info(`Bento web UI: ${address}`);
  ctx.log.info(`Managing stack: ${ctx.stackRoot}`);
  if (options.open) void openBrowser(address);

  await new Promise<void>((resolve) => {
    const stop = () => resolve();
    process.once("SIGINT", stop);
    process.once("SIGTERM", stop);
  });
  await Promise.all([...activeTerminals.keys()].map(removeTerminal));
  await server.stop();
  return 0;
}

export function matchesBasicAuthorization(
  authorization: string | null,
  expectedAuthorization: string,
): boolean {
  if (authorization === null) return false;
  const actual = Buffer.from(authorization, "utf8");
  const expected = Buffer.from(expectedAuthorization, "utf8");
  return actual.length === expected.length && timingSafeEqual(actual, expected);
}

async function readBoundedRequestText(request: Request, maxBytes: number): Promise<string | null> {
  const declaredLength = Number(request.headers.get("content-length") ?? 0);
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) return null;
  if (!request.body) return "";

  const reader = request.body.getReader();
  const decoder = new TextDecoder();
  let total = 0;
  let text = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > maxBytes) {
      await reader.cancel();
      return null;
    }
    text += decoder.decode(value, { stream: true });
  }
  return text + decoder.decode();
}

function withSecurity(response: Response): Response {
  const headers = new Headers(response.headers);
  for (const [name, value] of Object.entries(SECURITY_HEADERS)) headers.set(name, value);
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

function contentType(path: string): string {
  return (
    (
      {
        ".html": "text/html; charset=utf-8",
        ".js": "text/javascript; charset=utf-8",
        ".css": "text/css; charset=utf-8",
        ".svg": "image/svg+xml",
        ".png": "image/png",
        ".ico": "image/x-icon",
      } as Record<string, string>
    )[extname(path)] ?? "application/octet-stream"
  );
}

function formatHost(hostname: string): string {
  return hostname.includes(":") ? `[${hostname}]` : hostname;
}

async function openBrowser(url: string): Promise<void> {
  const command =
    process.platform === "darwin"
      ? ["open", url]
      : process.platform === "win32"
        ? ["cmd", "/c", "start", "", url]
        : ["xdg-open", url];
  try {
    Bun.spawn(command, { stdin: "ignore", stdout: "ignore", stderr: "ignore" }).unref();
  } catch {
    /* optional convenience */
  }
}
