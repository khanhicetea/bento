import { assertEquals, assertRejects, assertStringIncludes } from "@std/assert";
import { join } from "@std/path";
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
import { runDatabaseBackup, runDatabaseRestore } from "../../src/services/database_backup.ts";
import { addPostgresVersion } from "../../src/services/postgres.ts";

function testPlatform(
  root: string,
  handler?: (command: string[], options?: RunOptions) => Promise<RunResult> | RunResult,
): Platform & { process: ReturnType<typeof createRecordingProcessRunner> } {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-27T14:00:00.000Z"),
    random: createSeededRandom("feedface00000000"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(handler),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

function mixedState(platform: Platform) {
  let state = addPostgresVersion(createEmptyState(), "17");
  state = provisionApp(platform, state, {
    slug: "myapp",
    domain: "my.test",
    createDatabase: true,
  }).state;
  state = provisionApp(platform, state, {
    slug: "pgapp",
    domain: "pg.test",
    postgresVersion: "17",
    createDatabase: true,
  }).state;
  return state;
}

Deno.test("Phase 6 PostgreSQL dump is portable, protected, and atomically finalized", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg6-backup-" });
  try {
    const fs = createFileSystem();
    const final = join(
      root,
      "backups/postgres17/pgapp/postgres17_pgapp_2026-07-27T14-00-00-000Z.sql.zst",
    );
    const process = createRecordingProcessRunner(async () => {
      await fs.writeBytes(final, new Uint8Array([1, 2, 3]), 0o600);
      return { code: 0, stdout: "", stderr: "" };
    });
    const platform: Platform = {
      ...testPlatform(root),
      fs,
      process,
      assets: createAssetResolver(fs),
    };
    const state = mixedState(platform);
    const secret = state.apps.pgapp!.database.password;
    const artifacts = await runDatabaseBackup(platform, state, {
      scope: "app",
      slug: "pgapp",
    });

    assertEquals(artifacts, [{
      engine: "postgres",
      path: final,
      database: "pgapp",
      service: "postgres17",
      bytes: 3,
    }]);
    const call = process.calls[0]!;
    const script = call.command.at(-1) ?? "";
    assertStringIncludes(script, "pg_dump");
    assertStringIncludes(script, "--no-owner --no-acl");
    assertStringIncludes(script, "PGPASSFILE=/etc/bento/postgres/root.pgpass");
    assertStringIncludes(script, "zstd -3 -q -c");
    assertStringIncludes(script, ".partial");
    assertEquals(call.command.join(" ").includes(secret), false);
    assertEquals(call.options?.stdin, undefined);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 6 failed or empty PostgreSQL dump publishes no artifact", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg6-empty-" });
  try {
    const platform = testPlatform(root, () => ({ code: 0, stdout: "", stderr: "" }));
    const state = mixedState(platform);
    await assertRejects(
      () => runDatabaseBackup(platform, state, { scope: "app", slug: "pgapp" }),
      Error,
      "empty",
    );
    const dir = join(root, "backups/postgres17/pgapp");
    assertEquals((await platform.fs.readDir(dir)).filter((name) => name.includes(".sql")), []);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 6 mixed-engine all dispatches correctly and defers retention", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg6-mixed-" });
  try {
    const fs = createFileSystem();
    const platform = testPlatform(root, async (command) => {
      const service = command[4]!;
      const database = service === "mysql84" ? "myapp" : "pgapp";
      const path = join(
        root,
        `backups/${service}/${database}/${service}_${database}_2026-07-27T14-00-00-000Z.sql.zst`,
      );
      await fs.mkdirp(join(root, `backups/${service}/${database}`));
      await fs.writeBytes(path, new Uint8Array([1]), 0o600);
      return { code: 0, stdout: "", stderr: "" };
    });
    platform.fs = fs;
    platform.assets = createAssetResolver(fs);
    const state = mixedState(platform);
    const artifacts = await runDatabaseBackup(platform, state, { scope: "all" });
    assertEquals(artifacts.map((artifact) => artifact.engine).sort(), ["mysql", "postgres"]);
    assertStringIncludes(platform.process.calls[0]!.command.at(-1)!, "mysqldump");
    assertStringIncludes(platform.process.calls[1]!.command.at(-1)!, "pg_dump");
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 6 mid-batch failure preserves artifacts and skips retention", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg6-batch-" });
  try {
    const fs = createFileSystem();
    const mysqlDir = join(root, "backups/mysql84/myapp");
    await fs.mkdirp(mysqlDir);
    for (let index = 0; index < 11; index++) {
      await fs.writeText(join(mysqlDir, `old-${String(index).padStart(2, "0")}.sql`), "old");
    }
    let call = 0;
    const platform = testPlatform(root, async () => {
      if (call++ === 0) {
        await fs.writeText(
          join(mysqlDir, "mysql84_myapp_2026-07-27T14-00-00-000Z.sql.zst"),
          "new",
          0o600,
        );
        return { code: 0, stdout: "", stderr: "" };
      }
      return { code: 1, stdout: "", stderr: "pg_dump failed" };
    });
    platform.fs = fs;
    platform.assets = createAssetResolver(fs);
    await assertRejects(
      () => runDatabaseBackup(platform, mixedState(platform), { scope: "all" }),
      Error,
      "dump failed",
    );
    assertEquals((await fs.readDir(mysqlDir)).length, 12);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 6 PostgreSQL restore uses app credentials off argv and records only success", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg6-restore-" });
  try {
    const platform = testPlatform(root, () => ({ code: 0, stdout: "", stderr: "" }));
    const state = mixedState(platform);
    const dump = join(root, "portable.sql.gz");
    await platform.fs.writeBytes(dump, new Uint8Array([1, 2]), 0o600);
    const next = await runDatabaseRestore(platform, state, {
      file: dump,
      slug: "pgapp",
      targetDatabase: "pgapp_verify",
    });

    assertEquals(state.apps.pgapp!.database.databases.length, 1);
    assertEquals(next.apps.pgapp!.database.databases.at(-1)?.name, "pgapp_verify");
    const call = platform.process.calls[0]!;
    const script = call.command.at(-1) ?? "";
    const secret = state.apps.pgapp!.database.password;
    assertStringIncludes(script, "CREATE DATABASE");
    assertStringIncludes(script, "gzip -dc");
    assertStringIncludes(script, "ALTER SCHEMA public OWNER TO");
    assertEquals(call.command.join(" ").includes(secret), false);
    assertEquals(String(call.options?.stdin).includes(secret), true);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 6 replacement confirmation and cross-engine source fail before side effects", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg6-safety-" });
  try {
    const platform = testPlatform(root);
    const state = mixedState(platform);
    const pgDump = join(root, "pg.sql");
    await platform.fs.writeText(pgDump, "SELECT 1", 0o600);
    await assertRejects(
      () =>
        runDatabaseRestore(platform, state, {
          file: pgDump,
          slug: "pgapp",
          targetDatabase: "pgapp",
          replaceOriginal: "wrong",
        }),
      Error,
      "exactly match",
    );

    const mysqlDump = join(root, "backups/mysql84/myapp/source.sql");
    await platform.fs.mkdirp(join(root, "backups/mysql84/myapp"));
    await platform.fs.writeText(mysqlDump, "SELECT 1", 0o600);
    await assertRejects(
      () =>
        runDatabaseRestore(platform, state, {
          file: mysqlDump,
          slug: "pgapp",
          targetDatabase: "pgapp_verify",
        }),
      Error,
      "cannot be restored",
    );
    assertEquals(platform.process.calls.length, 0);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
