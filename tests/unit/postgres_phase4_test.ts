import { assertEquals, assertRejects, assertStringIncludes, assertThrows } from "@std/assert";
import { join } from "@std/path";
import { redactAppForOutput } from "../../src/commands/subcommands/app.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import type { Platform, RunOptions, RunResult } from "../../src/platform/mod.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { applyAppDataPlane, materializeAppHome, provisionApp } from "../../src/services/app.ts";
import {
  addPostgresVersion,
  postgresDatabaseSql,
  postgresRoleSql,
  postgresSchemaSql,
} from "../../src/services/postgres.ts";
import { StateStore } from "../../src/services/state_store.ts";

function testPlatform(
  root: string,
  handler?: (command: string[], options?: RunOptions) => Promise<RunResult> | RunResult,
): Platform & { process: ReturnType<typeof createRecordingProcessRunner> } {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-27T12:00:00.000Z"),
    random: createSeededRandom("aabbccddeeff0044"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(handler),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

Deno.test("PostgreSQL app selection persists one engine binding and preserves password", () => {
  const platform = testPlatform("/tmp/bento-pg4");
  let state = addPostgresVersion(createEmptyState(), "17");
  const first = provisionApp(platform, state, {
    slug: "demo",
    domain: "demo.test",
    databaseEngine: "postgres",
    postgresVersion: "postgres17",
    createDatabase: true,
  });
  assertEquals(first.app.database.engine, "postgres");
  assertEquals(first.app.database.service, "postgres17");
  assertEquals(first.app.database.databases[0]?.name, "demo");
  state = first.state;

  const reconciled = provisionApp(platform, state, {
    slug: "demo",
    domain: "new.demo.test",
  });
  assertEquals(reconciled.app.database, {
    ...first.app.database,
    databases: first.app.database.databases,
  });
  assertEquals(reconciled.app.database.password, first.app.database.password);
});

Deno.test("database selection rejects contradictory flags and adds another engine binding", () => {
  const platform = testPlatform("/tmp/bento-pg4");
  const state = addPostgresVersion(createEmptyState(), "17");
  const base = { slug: "demo", domain: "demo.test" };
  assertThrows(
    () => provisionApp(platform, state, { ...base, mysqlVersion: "8.4", postgresVersion: "17" }),
    Error,
    "cannot be used together",
  );
  assertThrows(
    () =>
      provisionApp(platform, state, {
        ...base,
        databaseEngine: "postgres",
        mysqlVersion: "8.4",
      }),
    Error,
    "contradicts",
  );
  const existing = provisionApp(platform, state, base).state;
  const mixed = provisionApp(platform, existing, {
    ...base,
    databaseEngine: "postgres",
    postgresVersion: "17",
  });
  assertEquals(mixed.app.databases.map((database) => database.engine), ["mysql", "postgres"]);
});

Deno.test("PostgreSQL policy SQL preserves password and isolates database/schema", () => {
  const platform = testPlatform("/tmp/bento-pg4");
  const app = provisionApp(platform, addPostgresVersion(createEmptyState(), "17"), {
    slug: "my-app",
    domain: "my-app.test",
    postgresVersion: "17",
  }).app;
  const role = postgresRoleSql(app);
  assertStringIncludes(role, "CREATE ROLE %I LOGIN PASSWORD %L");
  assertStringIncludes(role, "NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION");
  assertStringIncludes(role, "WHERE NOT EXISTS");
  assertEquals(role.includes('ALTER ROLE "my-app" WITH LOGIN'), true);
  const database = postgresDatabaseSql(app, "my_app_archive");
  assertStringIncludes(database, "CREATE DATABASE %I OWNER %I");
  assertStringIncludes(database, 'REVOKE ALL PRIVILEGES ON DATABASE "my_app_archive" FROM PUBLIC');
  assertStringIncludes(database, "GRANT CONNECT, TEMPORARY");
  const schema = postgresSchemaSql(app);
  assertStringIncludes(schema, "REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC");
  assertStringIncludes(schema, 'ALTER SCHEMA public OWNER TO "my-app"');
});

Deno.test("PostgreSQL provisioning applies role/database/schema without secrets on argv", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg4-" });
  const platform = testPlatform(root, (command) => ({
    code: 0,
    stdout: command.includes("pg_isready") ? "ready" : "ok",
    stderr: "",
  }));
  await platform.fs.atomicWriteText(
    platform.paths.paths.envFile,
    "POSTGRES_PASSWORD=root-secret\nREDIS_PASSWORD=redis-secret\n",
    0o600,
  );
  const app = provisionApp(platform, addPostgresVersion(createEmptyState(), "17"), {
    slug: "demo",
    domain: "demo.test",
    postgresVersion: "17",
    createDatabase: true,
  }).app;
  const plane = await applyAppDataPlane(platform, app, { explicitDatabase: true });
  assertEquals(plane.databaseApplied, true);
  assertEquals(plane.mysqlApplied, false);
  assertEquals(platform.process.calls.length, 4); // readiness, role, database, schema
  for (const call of platform.process.calls) {
    const argv = call.command.join(" ");
    assertEquals(argv.includes(app.database.password), false);
    assertEquals(argv.includes("CREATE ROLE"), false);
  }
  const streamed = platform.process.calls.map((call) => String(call.options?.stdin ?? "")).join(
    "\n",
  );
  assertStringIncludes(streamed, app.database.password);
  assertStringIncludes(streamed, "CREATE ROLE");
  assertStringIncludes(streamed, "REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC");

  const safe = redactAppForOutput(app);
  assertEquals(safe.database.password, "***");
  assertEquals(JSON.stringify(safe).includes(app.database.password), false);
  await Deno.remove(root, { recursive: true });
});

Deno.test("explicit unavailable PostgreSQL fails before state save and keeps secrets off argv", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg4-" });
  try {
    const platform = testPlatform(root, () => ({ code: 1, stdout: "", stderr: "down" }));
    const store = new StateStore(platform);
    await store.init();
    const original = addPostgresVersion(await store.load(), "17");
    await store.save(original);
    const bytes = await platform.fs.readText(platform.paths.paths.stateFile);
    const provisioned = provisionApp(platform, original, {
      slug: "demo",
      domain: "demo.test",
      postgresVersion: "17",
      createDatabase: true,
    });
    await assertRejects(
      () => applyAppDataPlane(platform, provisioned.app, { explicitDatabase: true }),
      Error,
      "unavailable",
    );
    assertEquals(await platform.fs.readText(platform.paths.paths.stateFile), bytes);
    for (const call of platform.process.calls) {
      assertEquals(call.command.join(" ").includes(provisioned.app.database.password), false);
    }
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("PostgreSQL app credentials are engine-correct, private, and retain Redis", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg4-" });
  try {
    const platform = testPlatform(root);
    const app = provisionApp(platform, addPostgresVersion(createEmptyState(), "17"), {
      slug: "demo",
      domain: "demo.test",
      postgresVersion: "17",
      createDatabase: true,
    }).app;
    await materializeAppHome(platform, app, {
      recursivePerms: false,
      redisSharedPassword: "redis-secret",
    });
    const path = join(platform.paths.appHome("demo"), "credentials", "app.env");
    const text = await platform.fs.readText(path);
    assertStringIncludes(text, "DB_CONNECTION=pgsql\n");
    assertStringIncludes(text, "PGHOST=postgres17\nPGPORT=5432\nPGUSER=demo\n");
    assertStringIncludes(text, `PGPASSWORD=${app.database.password}\nPGDATABASE=demo\n`);
    assertStringIncludes(text, "REDIS_PASSWORD=redis-secret");
    assertEquals(text.includes("MYSQL_"), false);
    assertEquals(((await platform.fs.stat(path)).mode ?? 0) & 0o777, 0o600);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
