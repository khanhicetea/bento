import { timingSafeEqual } from "node:crypto";
import { extname, join } from "node:path";
import { RPCHandler } from "@orpc/server/fetch";
import type { CliContext } from "#/commands/context.ts";
import { resolveAssetRoot } from "#/platform/assets.ts";
import { proxyMinicrond } from "#/server/minicrond_proxy.ts";
import { createWebRouter } from "#/server/router.ts";
import {
  closeTerminalSession,
  handleTerminalMessage,
  isSameOriginRequest,
  prepareTerminalSession,
  startTerminalSession,
  type TerminalSession,
} from "#/server/terminal.ts";
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

const SCHEDULER_SESSION_COOKIE = "bento_scheduler_session";
const SCHEDULER_PREFIX = "/scheduler/apps";
const SCHEDULER_SESSION_TTL_MS = 8 * 60 * 60 * 1000;

const SECURITY_HEADERS = {
  "content-security-policy":
    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://fonts.googleapis.com; font-src https://cdn.jsdelivr.net https://fonts.gstatic.com; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
  "referrer-policy": "no-referrer",
  "x-content-type-options": "nosniff",
  "x-frame-options": "DENY",
} as const;

export async function runWebServer(ctx: CliContext, options: ServeOptions): Promise<number> {
  const schedulerSocketAccess = process.getuid?.() === 0;
  const schedulerGatewayEnabled =
    options.basicAuth !== undefined &&
    options.port > 0 &&
    isLocalSchedulerHost(options.hostname) &&
    schedulerSocketAccess;
  const schedulerGatewayReason =
    options.basicAuth === undefined
      ? "Set WEB_BASIC_AUTH and restart Bento to enable browser schedulers."
      : options.port === 0
        ? "Browser schedulers require a fixed Bento web port."
        : !isLocalSchedulerHost(options.hostname)
          ? "Browser schedulers are currently limited to a local listener or loopback-published container."
          : !schedulerSocketAccess
            ? "Browser schedulers require Bento's root control-plane container to preserve minicrond Unix peer authentication."
            : undefined;
  const schedulerPathFor = (app: string) => `${SCHEDULER_PREFIX}/${app}/`;
  const router = createWebRouter(ctx, {
    schedulerWebAccess: {
      enabled: schedulerGatewayEnabled,
      ...(schedulerGatewayReason ? { reason: schedulerGatewayReason } : {}),
      pathFor: schedulerPathFor,
    },
  });
  const rpc = new RPCHandler(router);
  const schedulerSessions = new SchedulerSessions((length) => ctx.platform.random.hex(length));
  const withSecurity = (response: Response) => applySecurity(response);
  const assetRoot = join(resolveAssetRoot(), "..", "web", "dist");
  // Static URL references let Bun preserve the exact paths in compiled executables.
  const embeddedAssets: Record<string, string> = {
    "index.html": webIndex as unknown as string,
    "app.js": webScript,
    "app.css": webStyle,
  };

  const expectedAuthorization =
    options.basicAuth === undefined ? undefined : `Basic ${Buffer.from(options.basicAuth, "utf8").toString("base64")}`;

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
      const url = new URL(request.url);
      const schedulerApp = schedulerAppFromPath(url.pathname);
      const cookieHeader = request.headers.get("cookie");
      const hasSchedulerSession = schedulerGatewayEnabled && schedulerSessions.has(cookieHeader);
      // Browsers do not reliably carry cached Basic credentials into iframes.
      // An established session authenticates only the app-scoped scheduler paths.
      if (
        !acceptsWebAuthorization(
          request.headers.get("authorization"),
          expectedAuthorization,
          schedulerApp,
          hasSchedulerSession,
        )
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

      {
        // Establish the session on the authenticated response instead of redirecting
        // to the same URL. Clients that reject cookies must not get stuck in a loop.
        const sessionCookie = schedulerGatewayEnabled
          ? schedulerSessionCookie(schedulerSessions, cookieHeader, url.pathname)
          : undefined;
        const withSecurity = (response: Response, cookie = sessionCookie) => applySecurity(response, cookie);

        if (schedulerApp !== undefined) {
          if (!schedulerGatewayEnabled) return schedulerDenied(404);
          if (!schedulerSessions.authorizes(cookieHeader, schedulerApp) && sessionCookie === undefined)
            return schedulerDenied(401);
          try {
            const state = await ctx.store.load();
            return attachSessionCookie(
              await proxyMinicrond(
                request,
                state,
                { app: schedulerApp, origin: url.origin, basePath: schedulerPathFor(schedulerApp) },
                ctx.stackRoot,
              ),
              sessionCookie,
            );
          } catch {
            return schedulerDenied(502);
          }
        }
        // Never fall through to the SPA for malformed or unknown scheduler paths.
        if (
          url.pathname === SCHEDULER_PREFIX ||
          url.pathname.startsWith(`${SCHEDULER_PREFIX}/`) ||
          url.pathname === "/apps" ||
          url.pathname.startsWith("/apps/")
        )
          return schedulerDenied(404);

        if (url.pathname === "/api/session/logout" && request.method === "POST") {
          if (!isSameOriginRequest(request)) {
            return withSecurity(new Response("Request origin denied", { status: 403 }));
          }
          schedulerSessions.revoke(request.headers.get("cookie"));
          return withSecurity(
            new Response(null, { status: 204 }),
            `${SCHEDULER_SESSION_COOKIE}=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0`,
          );
        }
        if (url.pathname === "/healthz") return withSecurity(Response.json({ ok: true }));
        if (url.pathname === "/api/terminal" && request.method === "POST") {
          if (!isSameOriginRequest(request))
            return withSecurity(new Response("Request origin denied", { status: 403 }));
          if (activeTerminals.size + pendingTerminalCount >= maxTerminals)
            return withSecurity(Response.json({ error: "Too many active terminal sessions" }, { status: 503 }));
          if (Number(request.headers.get("content-length") ?? 0) > 4_096)
            return withSecurity(new Response("Request too large", { status: 413 }));

          let target: { app: string } | { service: string } | undefined;
          try {
            const body = await readBoundedRequestText(request, 4_096);
            const input: unknown = body === null ? null : JSON.parse(body);
            if (input && typeof input === "object" && !("app" in input && "service" in input)) {
              if ("app" in input && typeof input.app === "string" && input.app.trim())
                target = { app: input.app.trim() };
              if ("service" in input && typeof input.service === "string" && input.service.trim())
                target = { service: input.service.trim() };
            }
          } catch {
            // The validation response below covers malformed JSON.
          }
          if (!target)
            return withSecurity(Response.json({ error: "Application or service is required" }, { status: 400 }));

          pendingTerminalCount += 1;
          try {
            const session = await prepareTerminalSession(ctx, target);
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
              Response.json({ id }, { headers: { "cache-control": "no-store", "x-content-type-options": "nosniff" } }),
            );
          } catch {
            pendingTerminalCount -= 1;
            return withSecurity(Response.json({ error: "Unable to prepare shell for this target" }, { status: 400 }));
          }
        }

        const terminalRoute = /^\/api\/terminal\/([a-f0-9]{48})(?:\/(output|input))?$/.exec(url.pathname);
        if (terminalRoute) {
          const id = terminalRoute[1]!;
          const action = terminalRoute[2];
          const active = activeTerminals.get(id);
          if (!active) return withSecurity(Response.json({ error: "Terminal session not found" }, { status: 404 }));

          if (action === "output" && request.method === "GET") {
            if (active.attached)
              return withSecurity(new Response("Terminal output is already attached", { status: 409 }));
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
                          encoder.encode(`\r\n\x1b[90mShell exited (code ${exitCode ?? "unknown"}).\x1b[0m\r\n`),
                        );
                      } catch {
                        // The output request may already be closed.
                      }
                      closeOutput();
                      void removeTerminal(id);
                    },
                    error() {
                      try {
                        controller.enqueue(encoder.encode("\r\n\x1b[31mUnable to start shell.\x1b[0m\r\n"));
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
            if (body === null) return withSecurity(new Response("Request too large", { status: 413 }));
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
        if (!(await file.exists())) file = Bun.file(embeddedAssets["index.html"] ?? join(assetRoot, "index.html"));
        if (!(await file.exists()))
          return withSecurity(new Response("Web assets are missing. Run 'bun run web:build'.", { status: 503 }));
        const headers = new Headers({
          "content-type": contentType(file.name ?? requested),
          "cache-control": requested === "index.html" ? "no-cache" : "public, max-age=3600",
        });
        return withSecurity(new Response(request.method === "HEAD" ? null : file, { headers }));
      }
    },
    error(error) {
      console.error(`web server error: ${error instanceof Error ? error.message : String(error)}`);
      return withSecurity(new Response("Internal server error", { status: 500 }));
    },
  });

  const listenerAddress = `http://${formatHost(options.hostname)}:${server.port}`;
  const address = listenerAddress;
  ctx.log.info(`Bento web UI: ${address}`);
  if (schedulerGatewayEnabled) {
    ctx.log.info("Authenticated same-origin app scheduler paths are enabled (no browser origin isolation)");
  } else if (schedulerGatewayReason) {
    ctx.log.info(`Browser schedulers disabled: ${schedulerGatewayReason}`);
  }
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

export function acceptsWebAuthorization(
  authorization: string | null,
  expected: string | undefined,
  schedulerApp: string | undefined,
  hasSchedulerSession: boolean,
): boolean {
  return (
    expected === undefined ||
    (schedulerApp !== undefined && hasSchedulerSession) ||
    matchesBasicAuthorization(authorization, expected)
  );
}

export function matchesBasicAuthorization(authorization: string | null, expectedAuthorization: string): boolean {
  if (authorization === null) return false;
  const actual = Buffer.from(authorization, "utf8");
  const expected = Buffer.from(expectedAuthorization, "utf8");
  return actual.length === expected.length && timingSafeEqual(actual, expected);
}

export class SchedulerSessions {
  readonly #sessions = new Map<string, number>();

  constructor(private readonly randomHex: (length: number) => string) {}

  issue(): string {
    this.#removeExpired();
    const token = this.randomHex(32);
    this.#sessions.set(token, Date.now() + SCHEDULER_SESSION_TTL_MS);
    return `${SCHEDULER_SESSION_COOKIE}=${token}; Path=/; HttpOnly; SameSite=Strict; Max-Age=${SCHEDULER_SESSION_TTL_MS / 1000}`;
  }

  has(cookieHeader: string | null): boolean {
    const token = cookieValue(cookieHeader, SCHEDULER_SESSION_COOKIE);
    if (token === undefined) return false;
    const expiresAt = this.#sessions.get(token);
    if (expiresAt === undefined || expiresAt <= Date.now()) {
      this.#sessions.delete(token);
      return false;
    }
    return true;
  }

  authorizes(cookieHeader: string | null, app: string): boolean {
    return /^[a-z0-9][a-z0-9-]{0,62}$/.test(app) && this.has(cookieHeader);
  }

  revoke(cookieHeader: string | null): void {
    const token = cookieValue(cookieHeader, SCHEDULER_SESSION_COOKIE);
    if (token !== undefined) this.#sessions.delete(token);
  }

  #removeExpired(): void {
    const now = Date.now();
    for (const [token, expiresAt] of this.#sessions) {
      if (expiresAt <= now) this.#sessions.delete(token);
    }
  }
}

function cookieValue(header: string | null, name: string): string | undefined {
  if (header === null) return undefined;
  for (const part of header.split(";")) {
    const separator = part.indexOf("=");
    if (separator < 0 || part.slice(0, separator).trim() !== name) continue;
    const value = part.slice(separator + 1).trim();
    if (/^[a-f0-9]{64}$/.test(value)) return value;
  }
  return undefined;
}

export function schedulerSessionCookie(
  sessions: SchedulerSessions,
  cookieHeader: string | null,
  pathname: string,
): string | undefined {
  // Called only after Basic authentication (or with an existing scheduler session).
  // A direct scheduler navigation can establish its session without a redirect.
  if ((pathname === SCHEDULER_PREFIX || pathname.startsWith(`${SCHEDULER_PREFIX}/`)) && !schedulerAppFromPath(pathname))
    return undefined;
  if (sessions.has(cookieHeader)) return undefined;
  return sessions.issue();
}

export function schedulerAppFromPath(pathname: string): string | undefined {
  const match = /^\/scheduler\/apps\/([a-z0-9][a-z0-9-]{0,62})(?:\/|$)/.exec(pathname);
  return match?.[1];
}

function isLocalSchedulerHost(hostname: string): boolean {
  return ["127.0.0.1", "localhost", "::1", "0.0.0.0", "::"].includes(hostname);
}

function schedulerDenied(status: number): Response {
  return new Response(null, {
    status,
    headers: {
      "cache-control": "no-store",
      "content-security-policy": "default-src 'none'; frame-ancestors 'none'",
      "referrer-policy": "no-referrer",
      "x-content-type-options": "nosniff",
    },
  });
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

function applySecurity(response: Response, sessionCookie?: string): Response {
  const headers = new Headers(response.headers);
  for (const [name, value] of Object.entries(SECURITY_HEADERS)) headers.set(name, value);
  headers.set("content-security-policy", `${SECURITY_HEADERS["content-security-policy"]}; frame-src 'self'`);
  return attachSessionCookie(
    new Response(response.body, {
      status: response.status,
      statusText: response.statusText,
      headers,
    }),
    sessionCookie,
  );
}

function attachSessionCookie(response: Response, cookie?: string): Response {
  if (cookie === undefined) return response;
  const headers = new Headers(response.headers);
  headers.set("set-cookie", cookie);
  headers.set("cache-control", "no-store");
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
