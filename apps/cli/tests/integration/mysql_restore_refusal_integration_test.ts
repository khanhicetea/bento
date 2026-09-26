import { runtime as bunRuntime, assertEquals, assertRejects } from "../runtime.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { provisionApp, applyAppDataPlane } from "../../src/services/app.ts";
import { composeArgs } from "../../src/services/compose.ts";
import { runDatabaseBackup, runDatabaseRestore } from "../../src/services/database_backup.ts";
import { execMysqlSql } from "../../src/services/mysql.ts";
import { RenderService } from "../../src/services/render.ts";
import { requireMysqlRootPassword } from "../../src/services/stack_env.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { isComposeAvailable } from "./helpers.ts";

bunRuntime.test("MySQL verification restore refuses an existing database without modifying its contents", async () => {
  if (!(await isComposeAvailable())) {
    console.log("  [skip] Docker Compose unavailable — MySQL restore refusal skipped");
    return;
  }
  const root = await bunRuntime.makeTempDir({ prefix: "bento-mysql-restore-refusal-" });
  const project = `bentomyr${crypto.randomUUID().replaceAll("-", "").slice(0, 10)}`;
  const platform = createPlatform(root, bunRuntime.cwd());
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
    state = provisionApp(platform, await store.load(), {
      slug: "alpha",
      domain: "alpha.test",
      createDatabase: true,
    }).state;
    await new RenderService(platform).apply(state, { renderOnly: true, skipValidate: true });
    const up = await platform.process.run(await composeArgs(platform, state, ["up", "-d", "mysql84"]), {
      cwd: root,
      timeoutMs: 180_000,
    });
    if (up.code !== 0) {
      console.log(`  [soft-skip] mysql:8.4 unavailable: ${up.stderr.slice(0, 240)}`);
      return;
    }
    const password = await requireMysqlRootPassword(platform);
    for (let attempt = 0; attempt < 30; attempt++) {
      if ((await execMysqlSql(platform, "mysql84", "SELECT 1;", password)).code === 0) break;
      await Bun.sleep(1_000);
    }
    await applyAppDataPlane(platform, state.apps.alpha!, { explicitDatabase: true });
    const seed = await execMysqlSql(
      platform,
      "mysql84",
      "CREATE TABLE alpha.proof(marker VARCHAR(32)); INSERT INTO alpha.proof VALUES ('original');",
      password,
    );
    assertEquals(seed.code, 0, seed.stderr);
    const [artifact] = await runDatabaseBackup(platform, state, {
      scope: "database",
      slug: "alpha",
      database: "alpha",
      compress: "gzip",
    });
    assertEquals(artifact?.engine, "mysql");
    await assertRejects(
      () => runDatabaseRestore(platform, state!, { file: artifact!.path, slug: "alpha", targetDatabase: "alpha" }),
      Error,
      "restore failed",
    );
    const verify = await execMysqlSql(platform, "mysql84", "SELECT marker FROM alpha.proof;", password);
    assertEquals(verify.code, 0, verify.stderr);
    assertEquals(verify.stdout.includes("original"), true);
  } finally {
    if (state)
      await platform.process
        .run(await composeArgs(platform, state, ["down", "--remove-orphans"]), { cwd: root, timeoutMs: 30_000 })
        .catch(() => undefined);
    await platform.process.run(["docker", "volume", "rm", `${project}_mysql84-data`]).catch(() => undefined);
    await bunRuntime.remove(root, { recursive: true }).catch(() => undefined);
  }
});
