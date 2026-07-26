import { assertEquals } from "@std/assert";
import { createPlatform } from "../../src/platform/mod.ts";
import { applyAppDataPlane, provisionApp } from "../../src/services/app.ts";
import { composeArgs } from "../../src/services/compose.ts";
import { runDatabaseBackup, runDatabaseRestore } from "../../src/services/database_backup.ts";
import { addPostgresVersion, execPostgresAppSql } from "../../src/services/postgres.ts";
import { RenderService } from "../../src/services/render.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { isComposeAvailable } from "./helpers.ts";

Deno.test("PG-09/PG-10 PostgreSQL logical backup restores to a new isolated database", async () => {
  if (!(await isComposeAvailable())) {
    console.log("  [skip] Docker Compose unavailable — PostgreSQL backup chain skipped");
    return;
  }

  const root = await Deno.makeTempDir({ prefix: "bento-pg6-live-" });
  const project = `bentopg6${crypto.randomUUID().replaceAll("-", "").slice(0, 10)}`;
  const platform = createPlatform(root, Deno.cwd());
  const store = new StateStore(platform);
  let state;
  try {
    await store.init();
    const env = await platform.fs.readText(platform.paths.paths.envFile);
    await platform.fs.atomicWriteText(
      platform.paths.paths.envFile,
      env.replace("COMPOSE_PROJECT_NAME=bento", `COMPOSE_PROJECT_NAME=${project}`),
      0o600,
    );
    state = provisionApp(
      platform,
      addPostgresVersion(await store.load(), "17"),
      {
        slug: "alpha",
        domain: "alpha.test",
        postgresVersion: "17",
        createDatabase: true,
      },
    ).state;
    await new RenderService(platform).apply(state, { renderOnly: true, skipValidate: true });
    const up = await platform.process.run(
      await composeArgs(platform, state, ["up", "-d", "postgres17"]),
      { cwd: root, timeoutMs: 180_000 },
    );
    if (up.code !== 0) {
      console.log(`  [soft-skip] postgres:17 unavailable: ${up.stderr.slice(0, 240)}`);
      return;
    }
    for (let attempt = 0; attempt < 30; attempt++) {
      const ready = await platform.process.run(
        await composeArgs(platform, state, [
          "exec",
          "-T",
          "postgres17",
          "pg_isready",
          "-U",
          "postgres",
        ]),
        { cwd: root, timeoutMs: 5_000 },
      );
      if (ready.code === 0) break;
      await new Promise((resolve) => setTimeout(resolve, 1_000));
    }
    await applyAppDataPlane(platform, state.apps.alpha!, { explicitDatabase: true });
    const app = state.apps.alpha!;
    const seed = await execPostgresAppSql(
      platform,
      "postgres17",
      app.database.user,
      "alpha",
      "CREATE TABLE proof(value text); INSERT INTO proof VALUES ('restored');",
      app.database.password,
    );
    assertEquals(seed.code, 0, seed.stderr);

    const [artifact] = await runDatabaseBackup(platform, state, {
      scope: "database",
      slug: "alpha",
      database: "alpha",
      compress: "gzip",
    });
    assertEquals(artifact?.engine, "postgres");
    state = await runDatabaseRestore(platform, state, {
      file: artifact!.path,
      slug: "alpha",
      targetDatabase: "alpha_verify",
    });
    assertEquals(state.apps.alpha!.database.databases.at(-1)?.name, "alpha_verify");
    const verify = await execPostgresAppSql(
      platform,
      "postgres17",
      app.database.user,
      "alpha_verify",
      "SELECT value FROM proof;",
      app.database.password,
    );
    assertEquals(verify.code, 0, verify.stderr);
    assertEquals(verify.stdout.includes("restored"), true);
  } finally {
    if (state) {
      await platform.process.run(
        await composeArgs(platform, state, ["rm", "-f", "-s", "postgres17"]),
        { cwd: root, timeoutMs: 30_000 },
      ).catch(() => undefined);
    }
    await platform.process.run(["docker", "volume", "rm", `${project}_postgres17-data`]).catch(
      () => undefined,
    );
    await Deno.remove(root, { recursive: true }).catch(() => undefined);
  }
});
