import { join } from "node:path";
import { runtime as bunRuntime, assertEquals } from "../runtime.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { provisionApp, materializeAppHome, deleteApp } from "../../src/services/app.ts";
import { composeArgs } from "../../src/services/compose.ts";
import { RenderService } from "../../src/services/render.ts";
import { loadStackComposeEnvironment } from "../../src/services/stack_env.ts";
import { enableDeploy } from "../../src/services/deploy.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { chownDockerFixture, isComposeAvailable } from "./helpers.ts";
import { proxyMinicrond } from "../../src/server/minicrond_proxy.ts";

bunRuntime.test(
  "two PHP apps and root have isolated live minicrond sockets and registries",
  async () => {
    if (!(await isComposeAvailable())) {
      console.warn("[soft-skip] Docker Compose unavailable");
      return;
    }
    const root = await bunRuntime.makeTempDir({ prefix: "bento-minicrond-live-" });
    const platform = createPlatform(root, bunRuntime.cwd());
    const store = new StateStore(platform);
    let state = createEmptyState();
    let ownsProject = false;
    try {
      const projectName = `bentomc${platform.random.hex(4)}`;
      await store.init({ projectName });
      assertEquals((await loadStackComposeEnvironment(platform)).projectName, projectName);
      ownsProject = true;
      state = await store.load();
      const first = provisionApp(platform, state, { slug: "alpha", domain: "alpha.test" });
      const second = provisionApp(platform, first.state, { slug: "beta", domain: "beta.test" });
      state = enableDeploy(second.state, { slug: "alpha" }, platform).state;
      for (const app of [state.apps.alpha!, state.apps.beta!]) {
        await materializeAppHome(platform, app, false);
      }
      // Stage app-owned import input before dropping the fixture's host ownership.
      await platform.fs.atomicWriteText(
        join(root, "homes", "alpha", "scheduler-fixture.toml"),
        '[[job]]\nname = "fixture-alpha"\nschedule = "0 0 * * *"\nargv = ["/bin/true"]\n',
        0o600,
      );
      await store.save(state);
      await new RenderService(platform).apply(state, { renderOnly: true, skipValidate: true });
      for (const app of [state.apps.alpha!, state.apps.beta!]) {
        await chownDockerFixture(join(root, "homes", app.slug), app.uid, app.gid);
      }

      async function compose(args: string[]) {
        return await platform.process.run(await composeArgs(platform, state, args), {
          cwd: root,
          timeoutMs: 600_000,
        });
      }
      // The runner shares the FPM image tag but has no build stanza of its own.
      const built = await compose(["build", "php85"]);
      assertEquals(built.code, 0, built.stderr);
      const started = await compose(["up", "-d", "--no-deps", "php85-runner"]);
      assertEquals(started.code, 0, started.stderr);
      const retiredBinary = await compose([
        "exec",
        "-T",
        "php85-runner",
        "test",
        "-e",
        "/usr/local/bin/supercronic",
      ]);
      assertEquals(
        retiredBinary.code,
        1,
        "retired scheduler binary must not ship in the PHP image",
      );

      async function cli(slug: "alpha" | "beta" | "gamma", args: string[]) {
        const app = state.apps[slug]!;
        return await compose([
          "exec",
          "-T",
          "--user",
          `${app.uid}:${app.gid}`,
          "-e",
          `MINICRON_DATA=${app.home}/.local/share/minicron`,
          "-e",
          `BASE_PATH=/scheduler/apps/${slug}/`,
          "-e",
          `HOME=${app.home}`,
          "-e",
          `USER=${slug}`,
          "php85-runner",
          "minicrond",
          ...args,
        ]);
      }
      let ready = false;
      for (let attempt = 0; attempt < 30; attempt++) {
        const a = await cli("alpha", ["status"]);
        const b = await cli("beta", ["status"]);
        if (a.code === 0 && b.code === 0) {
          ready = true;
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 500));
      }
      assertEquals(ready, true, (await compose(["logs", "--tail", "80", "php85-runner"])).stdout);
      const prefixedApi = await compose([
        "exec",
        "-T",
        "--user",
        `${state.apps.alpha!.uid}:${state.apps.alpha!.gid}`,
        "php85-runner",
        "curl",
        "--noproxy",
        "*",
        "--fail",
        "--show-error",
        "--silent",
        "--unix-socket",
        "/home/alpha/.local/share/minicron/minicron.sock",
        "http://minicron/scheduler/apps/alpha/api/v1/jobs",
      ]);
      assertEquals(prefixedApi.code, 0, `${prefixedApi.stdout} ${prefixedApi.stderr}`);
      const rootApi = await compose([
        "exec",
        "-T",
        "--user",
        `${state.apps.alpha!.uid}:${state.apps.alpha!.gid}`,
        "php85-runner",
        "curl",
        "--noproxy",
        "*",
        "--silent",
        "--output",
        "/dev/null",
        "--write-out",
        "%{http_code}",
        "--unix-socket",
        "/home/alpha/.local/share/minicron/minicron.sock",
        "http://minicron/api/v1/jobs",
      ]);
      assertEquals(rootApi.stdout, "404");
      const imported = await cli("alpha", ["import", "/home/alpha/scheduler-fixture.toml"]);
      assertEquals(imported.code, 0, imported.stderr);
      const alpha = await cli("alpha", ["list"]);
      const beta = await cli("beta", ["list"]);
      assertEquals(alpha.code, 0, alpha.stderr);
      assertEquals(beta.code, 0, beta.stderr);
      assertEquals(alpha.stdout.includes("fixture-alpha"), true);
      assertEquals(alpha.stdout.includes("bento-internal-deploy-drain"), true);
      // Config-owned tasks cannot be taken over through the app registry API.
      const readOnly = await compose([
        "exec",
        "-T",
        "--user",
        `${state.apps.alpha!.uid}:${state.apps.alpha!.gid}`,
        "php85-runner",
        "curl",
        "--noproxy",
        "*",
        "--silent",
        "--output",
        "/dev/null",
        "--write-out",
        "%{http_code}",
        "--request",
        "DELETE",
        "--unix-socket",
        "/home/alpha/.local/share/minicron/minicron.sock",
        "http://minicron/scheduler/apps/alpha/api/v1/jobs/bento-internal-deploy-drain",
      ]);
      assertEquals(readOnly.stdout, "403");
      assertEquals(beta.stdout.includes("fixture-alpha"), false);
      assertEquals(beta.stdout.includes("bento-internal-deploy-drain"), false);
      const rootJobs = await compose([
        "exec",
        "-T",
        "-e",
        "MINICRON_DATA=/var/lib/bento/minicron",
        "php85-runner",
        "minicrond",
        "list",
      ]);
      assertEquals(rootJobs.code, 0, rootJobs.stderr);
      assertEquals(rootJobs.stdout.includes("bento-internal-logrotate-alpha"), true);
      assertEquals(rootJobs.stdout.includes("bento-internal-logrotate-beta"), true);
      assertEquals(rootJobs.stdout.includes("fixture-alpha"), false);
      async function rootPid() {
        return await compose([
          "exec",
          "-T",
          "php85-runner",
          "/command/s6-svstat",
          "-o",
          "pid",
          "/run/bento-s6/services/minicrond-root",
        ]);
      }
      const beforeAdd = await rootPid();
      assertEquals(beforeAdd.code, 0, beforeAdd.stderr);
      assertEquals(Number(beforeAdd.stdout.trim()) > 0, true);

      // Add an app while the runner is already up: apply must reconcile its new
      // s6 service, not wait for a whole-stack restart before the UI is usable.
      const added = provisionApp(platform, state, { slug: "gamma", domain: "gamma.test" });
      await materializeAppHome(platform, added.app, false);
      await chownDockerFixture(join(root, "homes", "gamma"), added.app.uid, added.app.gid);
      state = added.state;
      await store.save(state);
      await new RenderService(platform).apply(state, {
        reloadPlan: added.reloadPlan,
        skipValidate: true,
      });
      let gammaReady = false;
      for (let attempt = 0; attempt < 30; attempt++) {
        if ((await cli("gamma", ["status"])).code === 0) {
          gammaReady = true;
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 500));
      }
      assertEquals(
        gammaReady,
        true,
        (await compose(["logs", "--tail", "80", "php85-runner"])).stdout,
      );
      const proxied = await proxyMinicrond(
        new Request("http://127.0.0.1/scheduler/apps/gamma/"),
        state,
        { app: "gamma", origin: "http://127.0.0.1", basePath: "/scheduler/apps/gamma/" },
        root,
      );
      // Unix peer auth permits the Bento gateway only when the control plane
      // runs as root (non-root installations need a UID-matched relay).
      assertEquals(proxied.status, process.getuid?.() === 0 ? 200 : 503);
      await proxied.body?.cancel();
      let alphaAfterAdd = await cli("alpha", ["status"]);
      for (let attempt = 0; attempt < 30 && alphaAfterAdd.code !== 0; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 500));
        alphaAfterAdd = await cli("alpha", ["status"]);
      }
      assertEquals(alphaAfterAdd.code, 0, alphaAfterAdd.stderr);
      assertEquals(
        (await rootPid()).stdout,
        beforeAdd.stdout,
        (await compose(["logs", "--tail", "100", "php85-runner"])).stdout,
      );
      const rootAfterAdd = await compose([
        "exec",
        "-T",
        "-e",
        "MINICRON_DATA=/var/lib/bento/minicron",
        "php85-runner",
        "minicrond",
        "list",
      ]);
      assertEquals(rootAfterAdd.code, 0, rootAfterAdd.stderr);
      assertEquals(rootAfterAdd.stdout.includes("bento-internal-logrotate-gamma"), true);

      const deleted = deleteApp(state, "gamma", "delete gamma");
      state = deleted.state;
      await store.save(state);
      await new RenderService(platform).apply(state, {
        reloadPlan: deleted.reloadPlan,
        skipValidate: true,
      });
      assertEquals((await rootPid()).stdout, beforeAdd.stdout);
      const rootAfterDelete = await compose([
        "exec",
        "-T",
        "-e",
        "MINICRON_DATA=/var/lib/bento/minicron",
        "php85-runner",
        "minicrond",
        "list",
      ]);
      assertEquals(rootAfterDelete.code, 0, rootAfterDelete.stderr);
      assertEquals(rootAfterDelete.stdout.includes("bento-internal-logrotate-gamma"), false);
    } finally {
      if (ownsProject) {
        await platform.process
          .run(await composeArgs(platform, state, ["down", "--remove-orphans"]), {
            cwd: root,
            timeoutMs: 60_000,
          })
          .catch(() => undefined);
      }
      for (const slug of ["alpha", "beta", "gamma"]) {
        const home = join(root, "homes", slug);
        if (await platform.fs.exists(home)) {
          await chownDockerFixture(home, process.getuid?.() ?? 0, process.getgid?.() ?? 0);
        }
      }
      const maintenance = join(root, "maintenance");
      if (await platform.fs.exists(maintenance)) {
        await chownDockerFixture(maintenance, process.getuid?.() ?? 0, process.getgid?.() ?? 0);
      }
      await bunRuntime.remove(root, { recursive: true }).catch(() => undefined);
    }
  },
);
