import { assertEquals } from "@std/assert";
import { addPostgresVersion, execPostgresAppSql } from "../../src/services/postgres.ts";
import { applyAppDataPlane, materializeAppHome, provisionApp } from "../../src/services/app.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { composeArgs } from "../../src/services/compose.ts";
import { RenderService } from "../../src/services/render.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { buildCliExec, cliRunComposeCommand } from "../../src/services/php.ts";
import { isComposeAvailable } from "./helpers.ts";

Deno.test("PG-03/PG-05 PostgreSQL PHP connectivity and two-app isolation", async () => {
  if (!(await isComposeAvailable())) {
    console.log("  [skip] Docker Compose unavailable — PostgreSQL app live check skipped");
    return;
  }

  const root = await Deno.makeTempDir({ prefix: "bento-pg4-live-" });
  const project = `bentopg4${crypto.randomUUID().replaceAll("-", "").slice(0, 10)}`;
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
    state = addPostgresVersion(await store.load(), "17");
    state = provisionApp(platform, state, {
      slug: "alpha",
      domain: "alpha.test",
      postgresVersion: "17",
      createDatabase: true,
    }).state;
    state = provisionApp(platform, state, {
      slug: "beta",
      domain: "beta.test",
      postgresVersion: "17",
      createDatabase: true,
    }).state;
    await new RenderService(platform).apply(state, { renderOnly: true, skipValidate: true });

    const up = await platform.process.run(
      await composeArgs(platform, state, ["up", "-d", "postgres17"]),
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
          "postgres17",
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

    for (const slug of ["alpha", "beta"] as const) {
      const app = state.apps[slug]!;
      const plane = await applyAppDataPlane(platform, app, { explicitDatabase: true });
      assertEquals(plane.databaseApplied, true);
      await materializeAppHome(platform, app, {
        recursivePerms: false,
        redisSharedPassword: "",
      });
    }
    await store.save(state);

    const alpha = state.apps.alpha!;
    const own = await execPostgresAppSql(
      platform,
      "postgres17",
      alpha.database.user,
      "alpha",
      "CREATE TABLE IF NOT EXISTS isolation_probe(id integer); SELECT 1;",
      alpha.database.password,
    );
    assertEquals(own.code, 0);
    const cross = await execPostgresAppSql(
      platform,
      "postgres17",
      alpha.database.user,
      "beta",
      "SELECT 1;",
      alpha.database.password,
    );
    assertEquals(cross.code !== 0, true);

    // The CLI image reads the protected app credential file; no password is placed on host argv.
    const build = await platform.process.run(
      await composeArgs(platform, state, ["build", "php85"]),
      { cwd: root, timeoutMs: 600_000 },
    );
    if (build.code !== 0) {
      console.log(`  [soft-skip] PHP image build unavailable: ${build.stderr.slice(-240)}`);
      return;
    }
    const phpPlan = buildCliExec(platform, state, "alpha", [
      "php",
      "-r",
      "$e=parse_ini_file('/home/alpha/credentials/app.env'); $p=new PDO('pgsql:host='.$e['PGHOST'].';port='.$e['PGPORT'].';dbname='.$e['PGDATABASE'],$e['PGUSER'],$e['PGPASSWORD']); echo $p->query('SELECT 1')->fetchColumn();",
    ]);
    const php = await platform.process.run(
      await composeArgs(platform, state, cliRunComposeCommand(phpPlan, { tty: false })),
      { cwd: root, timeoutMs: 600_000 },
    );
    assertEquals(php.code, 0, `pdo_pgsql failed: ${php.stdout}\n${php.stderr}`);
    assertEquals(php.stdout.trim(), "1");
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
