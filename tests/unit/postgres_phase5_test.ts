import { assertEquals, assertRejects, assertStringIncludes, assertThrows } from "@std/assert";
import { createEmptyState } from "../../src/domain/state.ts";
import type { Platform, RunOptions, RunResult } from "../../src/platform/mod.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { provisionApp } from "../../src/services/app.ts";
import {
  addPostgresVersion,
  assertPostgresShellSecretsOffArgv,
  buildPostgresShellPlan,
  createPostgresAppDatabase,
  createPostgresAppDatabaseLive,
  executePostgresShell,
  postgresActivitySql,
  postgresDatabaseSizeSql,
  queryPostgresActivity,
  queryPostgresDatabaseSizes,
  resolvePostgresServices,
} from "../../src/services/postgres.ts";

function testPlatform(
  root: string,
  handler?: (command: string[], options?: RunOptions) => Promise<RunResult> | RunResult,
): Platform & { process: ReturnType<typeof createRecordingProcessRunner> } {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-26T12:00:00.000Z"),
    random: createSeededRandom("facade5000000000"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(handler),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

function postgresState(platform: Platform) {
  return provisionApp(platform, addPostgresVersion(createEmptyState(), "17"), {
    slug: "demo",
    domain: "demo.test",
    postgresVersion: "17",
  }).state;
}

Deno.test("PostgreSQL database state enforces namespace, engine, and duplicates", () => {
  const platform = testPlatform("/tmp/bento-pg5");
  const state = postgresState(platform);
  const next = createPostgresAppDatabase(state, "demo", "demo_archive", "2026-07-26T12:00:00Z");
  assertEquals(next.apps.demo?.database.databases[0]?.name, "demo_archive");
  assertThrows(
    () => createPostgresAppDatabase(next, "demo", "demo_archive", "2026-07-26T12:00:00Z"),
    Error,
    "already recorded",
  );
  assertThrows(
    () => createPostgresAppDatabase(state, "demo", "other", "2026-07-26T12:00:00Z"),
    Error,
    "outside app namespace",
  );
  const mysql = provisionApp(platform, createEmptyState(), {
    slug: "mysqlapp",
    domain: "mysql.test",
  }).state;
  assertThrows(
    () => createPostgresAppDatabase(mysql, "mysqlapp", "mysqlapp", "2026-07-26T12:00:00Z"),
    Error,
    "no PostgreSQL database binding",
  );
});

Deno.test("PostgreSQL operations target a selected binding without changing the primary", () => {
  const platform = testPlatform("/tmp/bento-pg5-multi");
  let state = addPostgresVersion(addPostgresVersion(createEmptyState(), "16"), "17");
  state = provisionApp(platform, state, {
    slug: "demo",
    domain: "demo.test",
    databaseEngine: "postgres",
    postgresVersion: "16",
  }).state;
  state = provisionApp(platform, state, {
    slug: "demo",
    domain: "demo.test",
    databaseEngine: "postgres",
    postgresVersion: "17",
  }).state;

  const next = createPostgresAppDatabase(
    state,
    "demo",
    "demo_events",
    "2026-07-30T00:00:00Z",
    "postgres17",
  );
  const app = next.apps.demo!;
  const postgres16 = app.databases.find((binding) =>
    binding.engine === "postgres" && binding.service === "postgres16"
  );
  const postgres17 = app.databases.find((binding) =>
    binding.engine === "postgres" && binding.service === "postgres17"
  );

  assertEquals(postgres16?.engine === "postgres" ? postgres16.databases.length : -1, 0);
  assertEquals(
    postgres17?.engine === "postgres" ? postgres17.databases.map((database) => database.name) : [],
    ["demo_events"],
  );
  assertEquals(app.database.engine === "postgres" ? app.database.service : "", "postgres16");
  assertEquals(
    buildPostgresShellPlan(platform, { kind: "app", app }, { service: "postgres17" }).service,
    "postgres17",
  );
});

Deno.test("PostgreSQL database failure occurs before state can be recorded", async () => {
  const platform = testPlatform("/tmp/bento-pg5", (command) => ({
    code: command.includes("pg_isready") ? 0 : 1,
    stdout: "",
    stderr: "grant failed",
  }));
  const state = postgresState(platform);
  await assertRejects(
    () => createPostgresAppDatabaseLive(platform, state, "demo", "demo_archive", "root-secret"),
    Error,
    "role setup failed",
  );
  assertEquals(state.apps.demo?.database.databases.length, 0);
  for (const call of platform.process.calls) {
    assertEquals(call.command.join(" ").includes("root-secret"), false);
  }
});

Deno.test("PostgreSQL app shell stages password on stdin and always cleans up", async () => {
  const platform = testPlatform("/tmp/bento-pg5", () => ({ code: 0, stdout: "", stderr: "" }));
  const app = postgresState(platform).apps.demo!;
  const plan = buildPostgresShellPlan(platform, { kind: "app", app }, { interactive: false });
  assertPostgresShellSecretsOffArgv(plan, [app.database.password]);
  assertEquals(plan.stage?.stdin.includes(app.database.password), true);
  assertEquals(plan.open.command.join(" ").includes(app.database.password), false);
  let actualOpenArgv: string[] = [];
  await assertRejects(
    () =>
      executePostgresShell(platform, plan, (command) => {
        actualOpenArgv = command;
        return Promise.reject(new Error("shell failed"));
      }),
    Error,
    "shell failed",
  );
  assertEquals(actualOpenArgv.join(" ").includes(app.database.password), false);
  assertEquals(platform.process.calls.at(-1)?.command.includes("rm"), true);
  assertEquals(platform.process.calls.at(-1)?.command.includes(plan.credentialPath), true);
});

Deno.test("PostgreSQL root shell uses mounted pgpass and service resolution is engine-safe", () => {
  const platform = testPlatform("/tmp/bento-pg5");
  const state = postgresState(platform);
  const plan = buildPostgresShellPlan(platform, { kind: "root", service: "postgres17" });
  assertEquals(plan.stage, undefined);
  assertStringIncludes(plan.open.command.join(" "), "PGPASSFILE=/etc/bento/postgres/root.pgpass");
  assertEquals(resolvePostgresServices(state, { service: "17" }), ["postgres17"]);
  const mysqlState = provisionApp(platform, state, { slug: "myapp", domain: "my.test" }).state;
  assertThrows(
    () => resolvePostgresServices(mysqlState, { app: "myapp" }),
    Error,
    "not PostgreSQL-backed",
  );
});

Deno.test("PostgreSQL size/activity parsing skips empty and malformed output", async () => {
  let call = 0;
  const platform = testPlatform("/tmp/bento-pg5", () => ({
    code: 0,
    stdout: call++ === 0
      ? "demo\t8192\t8192 bytes\nmalformed\n\n"
      : "42\tdemo\tdemo\tactive\t10.0.0.2\t2026-01-01\t2026-01-02\nbad\n",
    stderr: "",
  }));
  assertEquals(await queryPostgresDatabaseSizes(platform, "postgres17", "secret"), [{
    database: "demo",
    bytes: "8192",
    size: "8192 bytes",
  }]);
  assertEquals((await queryPostgresActivity(platform, "postgres17", "secret"))[0]?.pid, "42");
  for (const recorded of platform.process.calls) {
    assertEquals(recorded.command.join(" ").includes("secret"), false);
  }
  assertStringIncludes(postgresDatabaseSizeSql(["demo"]), "pg_database_size");
  assertEquals(postgresActivitySql().includes("query,"), false);
  assertEquals(postgresActivitySql().includes("query_start"), true);
});
