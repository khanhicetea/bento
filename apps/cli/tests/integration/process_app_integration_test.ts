import { basename, join } from "node:path";
import { runtime as bunRuntime, assertEquals } from "../runtime.ts";
import { chownDockerFixture, isComposeAvailable, loadState, withStack } from "./helpers.ts";
import { composeArgs } from "../../src/services/compose.ts";
import { createPlatform } from "../../src/platform/mod.ts";

bunRuntime.test(
  "managed Node.js process app starts privately and serves through its Unix socket",
  async () => {
    if (!(await isComposeAvailable())) {
      console.warn("[soft-skip] process app integration: docker compose unavailable");
      return;
    }

    await withStack(async (h) => {
      const projectName = `proc-${basename(h.stack)
        .toLowerCase()
        .replace(/[^a-z0-9-]/g, "-")}`;
      assertEquals(await h.run("init", "--name", projectName), 0);
      assertEquals(
        await h.run(
          "app",
          "create",
          "nodeapi",
          "--domain",
          "nodeapi.test",
          "--runtime",
          "node",
          "--runtime-version",
          "24",
          "--start",
          "node",
          "--start",
          "server.js",
          "--health-path",
          "/health",
          "--no-apply",
        ),
        0,
      );

      const codeDir = join(h.stack, "homes", "nodeapi", "code");
      await bunRuntime.writeTextFile(
        join(codeDir, "server.js"),
        `const http = require("node:http");
const host = process.env.HOST || "127.0.0.1";
const port = Number(process.env.PORT || 8080);
http.createServer((request, response) => {
  response.writeHead(request.url === "/health" ? 200 : 404, { "content-type": "text/plain" });
  response.end(request.url === "/health" ? "healthy\\n" : "not found\\n");
}).listen(port, host);
`,
      );

      const app = (await loadState(h.stack)).apps.nodeapi!;
      const appHome = join(h.stack, "homes", "nodeapi");
      await chownDockerFixture(appHome, app.uid, app.gid);
      try {
        assertEquals(await h.run("app", "start", "nodeapi"), 0);
        const state = await loadState(h.stack);
        const command = await composeArgs(createPlatform(h.stack, bunRuntime.cwd()), state, [
          "exec",
          "-T",
          "app-nodeapi",
          "curl",
          "--fail",
          "--silent",
          "--unix-socket",
          "/run/bento-http/http.sock",
          "http://localhost/health",
        ]);
        const curl = await new bunRuntime.Command(command[0]!, {
          args: command.slice(1),
          stdout: "piped",
          stderr: "piped",
        }).output();
        assertEquals(curl.code, 0, new TextDecoder().decode(curl.stderr));
        assertEquals(new TextDecoder().decode(curl.stdout).trim(), "healthy");

        // Enablement performs an independent Docker health inspection before
        // exposing the stable Unix socket through stack Nginx.
        assertEquals(await h.run("app", "enable", "nodeapi"), 0);
        const vhost = await bunRuntime.readTextFile(
          join(h.stack, "generated", "nginx", "sites", "nodeapi.conf"),
        );
        assertEquals(vhost.includes("unix:/run/bento-apps/nodeapi/http.sock"), true);
        assertEquals(vhost.includes("fastcgi_pass"), false);
      } finally {
        await h.run("app", "disable", "nodeapi").catch(() => 1);
        await h.run("compose", "--", "down", "--remove-orphans").catch(() => 1);
        await chownDockerFixture(appHome, process.getuid?.() ?? 0, process.getgid?.() ?? 0);
      }
    });
  },
);
