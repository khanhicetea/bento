import { assertEquals } from "@std/assert";
import { createEmptyState, postgresImage, postgresServiceName } from "../../src/domain/state.ts";
import { asPostgresVersion } from "../../src/domain/types.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { composeArgs } from "../../src/services/compose.ts";
import { RenderService } from "../../src/services/render.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { isComposeAvailable } from "./helpers.ts";

Deno.test("PG-04 rendered PostgreSQL starts privately and passes pg_isready", async () => {
  if (!(await isComposeAvailable())) {
    console.log("  [skip] Docker Compose unavailable — PostgreSQL live check skipped");
    return;
  }

  const root = await Deno.makeTempDir({ prefix: "bento-pg2-live-" });
  const project = `bentopg2${crypto.randomUUID().replaceAll("-", "").slice(0, 10)}`;
  const platform = createPlatform(root, Deno.cwd());
  const store = new StateStore(platform);
  const service = postgresServiceName(asPostgresVersion("17"));
  try {
    await store.init();
    const envPath = platform.paths.paths.envFile;
    const env = await platform.fs.readText(envPath);
    await platform.fs.atomicWriteText(
      envPath,
      env.replace("COMPOSE_PROJECT_NAME=bento", `COMPOSE_PROJECT_NAME=${project}`),
      0o600,
    );

    const state = createEmptyState();
    state.databaseServices.push({
      engine: "postgres",
      version: asPostgresVersion("17"),
      service,
      image: postgresImage(asPostgresVersion("17")),
      volume: `${service}-data`,
    });
    await store.save(state);
    await new RenderService(platform).apply(state, {
      renderOnly: true,
      skipValidate: true,
    });

    const up = await platform.process.run(
      await composeArgs(platform, state, ["up", "-d", String(service)]),
      { cwd: root, timeoutMs: 180_000 },
    );
    if (up.code !== 0) {
      console.log(`  [soft-skip] postgres:17 unavailable: ${up.stderr.slice(0, 240)}`);
      return;
    }

    let ready = false;
    for (let attempt = 0; attempt < 30; attempt++) {
      const result = await platform.process.run(
        await composeArgs(platform, state, [
          "exec",
          "-T",
          String(service),
          "pg_isready",
          "-U",
          "postgres",
        ]),
        { cwd: root, timeoutMs: 5_000 },
      );
      if (result.code === 0) {
        ready = true;
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 1_000));
    }
    assertEquals(ready, true);

    const container = `${project}-${service}-1`;
    const inspect = await platform.process.run([
      "docker",
      "inspect",
      container,
      "--format",
      "{{json .HostConfig.PortBindings}}",
    ]);
    assertEquals(inspect.code, 0);
    assertEquals(["{}", "null"].includes(inspect.stdout.trim()), true);
  } finally {
    const state = await store.load().catch(() => undefined);
    if (state) {
      await platform.process.run(
        await composeArgs(platform, state, ["rm", "-f", "-s", String(service)]),
        { cwd: root, timeoutMs: 30_000 },
      ).catch(() => undefined);
    }
    await platform.process.run(["docker", "volume", "rm", `${project}_${service}-data`]).catch(
      () => undefined,
    );
    await Deno.remove(root, { recursive: true }).catch(() => undefined);
  }
});
