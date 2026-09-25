import { runtime as bunRuntime, assert, assertEquals, assertStringIncludes } from "../runtime.ts";
import { dirname, join } from "node:path";
import { createEmptyState } from "../../src/domain/state.ts";
import { createPlatform, createRecordingProcessRunner } from "../../src/platform/mod.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { parseDesiredState, stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { assembleComposeDocuments } from "../../src/services/compose.ts";
import { generateAll } from "../../src/services/generate.ts";
import { runSqliteBackup } from "../../src/services/sqlite_local.ts";

bunRuntime.test("one app can persist independent SQLite and Litestream bindings", () => {
  const platform = createPlatform("/tmp/bento-sqlite-multiple", bunRuntime.cwd());
  const result = provisionApp(platform, createEmptyState("2026-08-01T00:00:00.000Z"), {
    slug: "files",
    domain: "files.test",
    databaseEngine: "sqlite",
  });
  const mixed = provisionApp(platform, result.state, {
    slug: "files",
    domain: "files.test",
    databaseEngine: "litestream",
  });
  const withSecondLocal = provisionApp(platform, mixed.state, {
    slug: "files",
    domain: "files.test",
    databaseEngine: "sqlite",
    createDatabase: true,
  });
  const raw = JSON.parse(stateToJson(withSecondLocal.state));
  const parsed = parseDesiredState(raw);
  assert(parsed.ok);
  if (parsed.ok) {
    assertEquals(
      parsed.value.apps.files?.databases.map((database) => database.engine),
      ["sqlite", "litestream", "sqlite"],
    );
    const sqliteIds = parsed.value.apps.files?.databases
      .filter((database) => database.engine === "sqlite")
      .map((database) => database.file.id);
    assertEquals(new Set(sqliteIds).size, 2);
  }
});

bunRuntime.test("plain SQLite backup uses .backup and gzip in the runner", async () => {
  const root = await bunRuntime.makeTempDir({
    prefix: "bento-sqlite-backup-",
  });
  try {
    const platform = createPlatform(root, bunRuntime.cwd());
    platform.clock = createFixedClock("2026-08-03T04:05:06.000Z");
    const result = provisionApp(platform, createEmptyState(), {
      slug: "local",
      domain: "local.test",
      databaseEngine: "sqlite",
    });
    const scripts: string[] = [];
    platform.process = createRecordingProcessRunner(async (command) => {
      const script = command.at(-1) ?? "";
      scripts.push(script);
      assertStringIncludes(script, ".backup");
      const match = script.match(/FINAL='\/var\/backups\/bento\/([^']+)'/);
      assert(match);
      const output = join(root, "backups", match[1]!);
      await platform.fs.mkdirp(dirname(output));
      await platform.fs.writeText(output, "sqlite backup");
      return { code: 0, stdout: "", stderr: "" };
    });

    const gzipArtifact = await runSqliteBackup(platform, result.state, "local", "gzip");
    const zstdArtifact = await runSqliteBackup(platform, result.state, "local", "zstd");
    assertStringIncludes(scripts[0]!, 'gzip -c "$RAW" > "$PARTIAL"');
    assertStringIncludes(scripts[1]!, 'zstd -3 -q -c "$RAW" > "$PARTIAL"');
    assertEquals(scripts[0]!.includes("pipefail"), false);
    assertEquals(scripts[1]!.includes("pipefail"), false);
    assertEquals(gzipArtifact.engine, "sqlite");
    const fileId = gzipArtifact.database;
    assertEquals(
      gzipArtifact.path.endsWith(
        `/backups/sqlite/local/${fileId}_2026-08-03T04-05-06-000Z.sqlite.gz`,
      ),
      true,
    );
    assertEquals(zstdArtifact.path.endsWith(".sqlite.zst"), true);
    assertEquals(gzipArtifact.bytes > 0, true);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test(
  "plain SQLite is distinct from Litestream and gets weekly runner maintenance",
  async () => {
    const root = await bunRuntime.makeTempDir({
      prefix: "bento-sqlite-local-",
    });
    try {
      const platform = createPlatform(root, bunRuntime.cwd());
      const result = provisionApp(platform, createEmptyState("2026-08-01T00:00:00.000Z"), {
        slug: "local",
        domain: "local.test",
        databaseEngine: "sqlite",
      });
      assert(result.app.database.engine === "sqlite");
      assertEquals(result.app.database.file.path.endsWith("/local.db"), true);
      assert(parseDesiredState(JSON.parse(stateToJson(result.state))).ok);

      const files = await generateAll(platform, result.state, "digest");
      const seed = files.find((file) => file.relPath.endsWith("minicrond/local/seed.toml"));
      const scheduler = files.find((file) => file.relPath.endsWith("services/minicrond-local/run"));
      assert(seed && typeof seed.content === "string");
      const schedule = seed.content.match(/schedule = "(\d+) (\d+) \* \* (\d+)"/);
      assert(schedule);
      assertEquals(Number(schedule[1]), result.app.database.vacuumSchedule?.minute);
      assertEquals(Number(schedule[2]), result.app.database.vacuumSchedule?.hour);
      assertEquals(Number(schedule[3]), result.app.database.vacuumSchedule?.dayOfWeek);
      assert(Number(schedule[1]) >= 0 && Number(schedule[1]) <= 59);
      assert(Number(schedule[2]) >= 0 && Number(schedule[2]) <= 4);
      assert(Number(schedule[3]) >= 0 && Number(schedule[3]) <= 6);
      assertStringIncludes(seed.content, "/usr/bin/sqlite3");
      assertStringIncludes(seed.content, "VACUUM;");
      assert(scheduler, "plain SQLite must start its own minicrond daemon");

      const rerendered = await generateAll(platform, result.state, "digest");
      const rerenderedSeed = rerendered.find((file) =>
        file.relPath.endsWith("minicrond/local/seed.toml"),
      );
      assert(rerenderedSeed && typeof rerenderedSeed.content === "string");
      assertEquals(rerenderedSeed.content, seed.content);

      const compose = assembleComposeDocuments(platform, result.state).find((file) =>
        file.relPath.includes("php-php85"),
      );
      assert(compose && typeof compose.content === "string");
      assertStringIncludes(compose.content, "./backups/sqlite:/var/backups/bento/sqlite");
    } finally {
      await bunRuntime.remove(root, { recursive: true });
    }
  },
);

bunRuntime.test("local SQLite VACUUM slots do not overlap when files are added", async () => {
  const root = await bunRuntime.makeTempDir({
    prefix: "bento-sqlite-schedule-",
  });
  try {
    const platform = createPlatform(root, bunRuntime.cwd());
    const first = provisionApp(platform, createEmptyState(), {
      slug: "first",
      domain: "first.test",
      databaseEngine: "sqlite",
    });
    const second = provisionApp(platform, first.state, {
      slug: "second",
      domain: "second.test",
      databaseEngine: "sqlite",
    });
    const files = await generateAll(platform, second.state, "digest");
    const schedules = files
      .filter((file) => file.relPath.endsWith("/seed.toml"))
      .flatMap((file) => {
        if (typeof file.content !== "string") return [];
        return file.content
          .split("\n")
          .filter((line) => line.startsWith("schedule = "))
          .map((line) => line.match(/^schedule = "(\d+ \d+ \* \* \d+)"/)?.[1])
          .filter((schedule): schedule is string => schedule !== undefined);
      });
    assertEquals(schedules.length, 2);
    assertEquals(new Set(schedules).size, schedules.length);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});
