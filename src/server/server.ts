import { extname, join } from "node:path";
import { RPCHandler } from "@orpc/server/fetch";
import type { CliContext } from "../commands/context.ts";
import { resolveAssetRoot } from "../platform/assets.ts";
import { createWebRouter } from "./router.ts";
// Bun's file loader embeds these assets in standalone builds.
import webIndex from "../../web/dist/index.html" with { type: "file" };
// @ts-expect-error generated browser asset
import webScript from "../../web/dist/app.js" with { type: "file" };
// @ts-expect-error generated browser asset
import webStyle from "../../web/dist/app.css" with { type: "file" };

export type ServeOptions = { hostname: string; port: number; open: boolean };

const SECURITY_HEADERS = {
  "content-security-policy":
    "default-src 'self'; script-src 'self'; style-src 'self' https://cdn.jsdelivr.net https://fonts.googleapis.com; font-src https://cdn.jsdelivr.net https://fonts.gstatic.com; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
  "referrer-policy": "no-referrer",
  "x-content-type-options": "nosniff",
  "x-frame-options": "DENY",
} as const;

export async function runWebServer(ctx: CliContext, options: ServeOptions): Promise<number> {
  const router = createWebRouter(ctx);
  const rpc = new RPCHandler(router);
  const assetRoot = join(resolveAssetRoot(), "web", "dist");
  // Static URL references let Bun preserve the exact paths in compiled executables.
  const embeddedAssets: Record<string, string> = {
    "index.html": webIndex as unknown as string,
    "app.js": webScript,
    "app.css": webStyle,
  };

  const server = Bun.serve({
    hostname: options.hostname,
    port: options.port,
    idleTimeout: 255,
    async fetch(request) {
      const url = new URL(request.url);
      if (url.pathname === "/healthz") return withSecurity(Response.json({ ok: true }));
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
  await server.stop();
  return 0;
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
