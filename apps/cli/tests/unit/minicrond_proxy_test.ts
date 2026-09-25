import { createServer, type Server } from "node:http";
import { chmod, mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { runtime, assertEquals } from "../runtime.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { proxyMinicrond, type SchedulerAccess } from "../../src/server/minicrond_proxy.ts";

runtime.test(
  "scheduler socket proxy scopes app, filters auth, protects writes and streams",
  async () => {
    const root = await runtime.makeTempDir({ prefix: "bento-scheduler-proxy-" });
    const platform = createPlatform(root, runtime.cwd());
    const first = provisionApp(platform, createEmptyState(), {
      slug: "alpha",
      domain: "alpha.test",
    });
    const second = provisionApp(platform, first.state, { slug: "beta", domain: "beta.test" });
    const state = second.state;
    // Exercise the peer-UID check as the current test identity; live stacks use
    // the separately allocated app UID and a privileged or UID-matched bridge.
    const uid = process.getuid?.() ?? 0;
    for (const slug of ["alpha", "beta"] as const) {
      state.apps[slug]!.uid = uid as typeof first.app.uid;
      state.apps[slug]!.enabled = true;
    }
    const access: SchedulerAccess = {
      app: "alpha",
      origin: "http://127.0.0.1:8080",
      basePath: "/scheduler/apps/alpha/",
    };
    let server: Server | undefined;
    try {
      const data = join(root, "homes", "alpha", ".local", "share", "minicron");
      await mkdir(data, { recursive: true });
      const socket = join(data, "minicron.sock");
      let receivedAuth = "";
      let receivedPath = "";
      server = createServer((req, res) => {
        receivedAuth = req.headers.authorization ?? "";
        receivedPath = req.url ?? "";
        if (req.url === "/scheduler/apps/alpha/redirect") {
          res.writeHead(302, { location: "/scheduler/apps/beta/" });
          res.end();
        } else if (req.url === "/scheduler/apps/alpha/events") {
          res.writeHead(200, { "content-type": "text/event-stream", "x-frame-options": "DENY" });
          res.write("data: first\n\n");
          setTimeout(() => res.end("data: second\n\n"), 10);
        } else if (req.url === "/scheduler/apps/alpha/download") {
          res.writeHead(200, {
            "content-type": "application/toml",
            "content-disposition": 'attachment; filename="jobs.toml"',
          });
          res.end("[[job]]\n");
        } else {
          res.writeHead(200, { "content-type": "application/json", "set-cookie": "token=secret" });
          res.end(JSON.stringify({ app: "alpha" }));
        }
      });
      await new Promise<void>((resolve, reject) =>
        server!.listen(socket, () => resolve()).once("error", reject),
      );
      await chmod(socket, 0o600);

      const request = (path: string, options?: RequestInit) =>
        proxyMinicrond(new Request(`${access.origin}${path}`, options), state, access, root);
      const response = await request("/scheduler/apps/alpha/api/v1/daemon?app=beta", {
        headers: { authorization: "Bearer should-not-pass", "x-forwarded-user": "root" },
      });
      assertEquals(response.status, 200);
      assertEquals(await response.json(), { app: "alpha" });
      assertEquals(receivedPath, "/scheduler/apps/alpha/api/v1/daemon?app=beta");
      assertEquals(receivedAuth, "");
      assertEquals(response.headers.has("set-cookie"), false);
      assertEquals(response.headers.get("x-frame-options"), null);
      assertEquals(response.headers.get("cache-control"), "no-store");
      assertEquals(
        response.headers.get("content-security-policy")?.includes("frame-ancestors 'self'"),
        true,
      );
      assertEquals(
        (
          await request("/scheduler/apps/alpha/api/v1/token/rotate", {
            method: "POST",
            headers: { origin: access.origin },
          })
        ).status,
        404,
      );
      assertEquals((await request("/scheduler/apps/alpha/api/v1/%74oken/rotate")).status, 404);
      assertEquals(
        (
          await request("/scheduler/apps/alpha/api/v1/daemon", {
            method: "POST",
            headers: { origin: "http://evil.test" },
          })
        ).status,
        403,
      );
      assertEquals(
        (await request("/scheduler/apps/alpha/api/v1/daemon", { method: "POST" })).status,
        403,
      );
      assertEquals(
        (
          await request("/scheduler/apps/alpha/api/v1/daemon", {
            method: "POST",
            headers: { origin: access.origin, "sec-fetch-site": "cross-site" },
          })
        ).status,
        403,
      );
      assertEquals(
        (
          await request("/scheduler/apps/alpha/api/v1/daemon", {
            method: "POST",
            headers: { origin: access.origin, "content-length": "1048577" },
          })
        ).status,
        413,
      );
      assertEquals(
        (await proxyMinicrond(new Request("http://127.0.0.3:12345/"), state, access, root)).status,
        403,
      );
      assertEquals(
        (
          await proxyMinicrond(
            new Request(`${access.origin}/scheduler/apps/alpha/`),
            state,
            { ...access, app: "beta", basePath: "/scheduler/apps/beta/" },
            root,
          )
        ).status,
        403,
      );
      assertEquals((await request("/scheduler/apps/beta/api/v1/daemon")).status, 403);
      assertEquals((await request("/apps/alpha/api/v1/daemon")).status, 403);
      assertEquals((await request("/scheduler/apps/alpha/%2fapi/v1/daemon")).status, 400);
      const redirect = await request("/scheduler/apps/alpha/redirect");
      assertEquals(redirect.status, 302);
      assertEquals(redirect.headers.get("location"), null);
      const events = await request("/scheduler/apps/alpha/events");
      assertEquals(events.headers.get("content-type"), "text/event-stream");
      assertEquals(await events.text(), "data: first\n\ndata: second\n\n");
      const download = await request("/scheduler/apps/alpha/download");
      assertEquals(download.headers.get("content-disposition"), 'attachment; filename="jobs.toml"');
      assertEquals(await download.text(), "[[job]]\n");
    } finally {
      if (server) await new Promise<void>((resolve) => server!.close(() => resolve()));
      await rm(root, { recursive: true, force: true });
    }
  },
);
