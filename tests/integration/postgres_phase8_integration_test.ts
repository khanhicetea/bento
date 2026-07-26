import { assertEquals } from "@std/assert";
import { join } from "@std/path";
import { createPlatform } from "../../src/platform/mod.ts";
import { addPostgresVersion, execPostgresSql } from "../../src/services/postgres.ts";
import { composeArgs } from "../../src/services/compose.ts";
import { RenderService } from "../../src/services/render.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { loadPostgresRootPassword } from "../../src/services/stack_env.ts";
import { exportStack, importStack } from "../../src/services/stack_transfer.ts";
import { isComposeAvailable } from "./helpers.ts";

Deno.test("PG-12 full stack transfer round-trips a compatible PostgreSQL raw volume", async () => {
  if (!(await isComposeAvailable())) {
    console.log("  [skip] Docker Compose unavailable — PostgreSQL stack transfer skipped");
    return;
  }

  const parent = await Deno.makeTempDir({ prefix: "bento-pg8-live-" });
  const sourceRoot = join(parent, "source-stack");
  const importRoot = join(parent, "import-stack");
  const transfer = join(parent, "transfer");
  const project = `bentopg8${crypto.randomUUID().replaceAll("-", "").slice(0, 10)}`;
  const source = createPlatform(sourceRoot, Deno.cwd());
  const destination = createPlatform(importRoot, Deno.cwd());
  let state = await new StateStore(source).init();
  state = addPostgresVersion(state, "17");
  await new StateStore(source).save(state);
  const env = await source.fs.readText(source.paths.paths.envFile);
  await source.fs.atomicWriteText(
    source.paths.paths.envFile,
    env.replace("COMPOSE_PROJECT_NAME=bento", `COMPOSE_PROJECT_NAME=${project}`),
    0o600,
  );
  const volumeNames = [
    `${project}_mysql84-data`,
    `${project}_postgres17-data`,
    `${project}_redis-data`,
  ];

  try {
    await new RenderService(source).apply(state, { renderOnly: true, skipValidate: true });
    // Export requires every durable volume, even when only PostgreSQL is running.
    for (const volume of [volumeNames[0]!, volumeNames[2]!]) {
      await source.process.run(["docker", "volume", "create", volume]);
    }
    const up = await source.process.run(
      await composeArgs(source, state, ["up", "-d", "postgres17"]),
      { cwd: sourceRoot, timeoutMs: 180_000 },
    );
    if (up.code !== 0) {
      console.log(`  [soft-skip] postgres:17 unavailable: ${up.stderr.slice(0, 240)}`);
      return;
    }
    for (let attempt = 0; attempt < 30; attempt++) {
      const ready = await source.process.run(
        await composeArgs(source, state, [
          "exec",
          "-T",
          "postgres17",
          "pg_isready",
          "-U",
          "postgres",
        ]),
        { cwd: sourceRoot, timeoutMs: 5_000 },
      );
      if (ready.code === 0) break;
      await new Promise((resolve) => setTimeout(resolve, 1_000));
    }
    const password = await loadPostgresRootPassword(source);
    const seed = await execPostgresSql(
      source,
      "postgres17",
      "CREATE TABLE bento_transfer_proof(value text); INSERT INTO bento_transfer_proof VALUES ('round-trip');",
      password!,
    );
    assertEquals(seed.code, 0, seed.stderr);

    const exported = await exportStack(source, state, transfer);
    assertEquals(exported.files.some((path) => path.endsWith("postgres17-data.tar.gz")), true);

    await source.process.run(await composeArgs(source, state, ["rm", "-f", "-s", "postgres17"]), {
      cwd: sourceRoot,
      timeoutMs: 30_000,
    });
    for (const volume of volumeNames) {
      const removed = await source.process.run(["docker", "volume", "rm", volume]);
      assertEquals(removed.code, 0, removed.stderr);
    }

    const imported = await importStack(destination, transfer);
    assertEquals(imported.volumes.includes(`${project}_postgres17-data`), true);
    const importedState = await new StateStore(destination).load();
    for (let attempt = 0; attempt < 30; attempt++) {
      const ready = await destination.process.run(
        await composeArgs(destination, importedState, [
          "exec",
          "-T",
          "postgres17",
          "pg_isready",
          "-U",
          "postgres",
        ]),
        { cwd: importRoot, timeoutMs: 5_000 },
      );
      if (ready.code === 0) break;
      await new Promise((resolve) => setTimeout(resolve, 1_000));
    }
    const verify = await execPostgresSql(
      destination,
      "postgres17",
      "SELECT value FROM bento_transfer_proof;",
      password!,
    );
    assertEquals(verify.code, 0, verify.stderr);
    assertEquals(verify.stdout.includes("round-trip"), true);
  } finally {
    // Use direct Docker cleanup because the test intentionally exercises raw volumes.
    await source.process.run(["docker", "rm", "-f", `${project}-postgres17-1`]).catch(() =>
      undefined
    );
    await destination.process.run([
      "docker",
      "ps",
      "-aq",
      "--filter",
      `label=com.docker.compose.project=${project}`,
    ])
      .then(async (result) => {
        const ids = result.stdout.trim().split(/\s+/).filter(Boolean);
        if (ids.length > 0) await destination.process.run(["docker", "rm", "-f", ...ids]);
      }).catch(() => undefined);
    for (const volume of volumeNames) {
      await destination.process.run(["docker", "volume", "rm", volume]).catch(() => undefined);
    }
    await Deno.remove(parent, { recursive: true }).catch(() => undefined);
  }
});
