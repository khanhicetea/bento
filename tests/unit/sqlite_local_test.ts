import { assert, assertEquals, assertStringIncludes } from "@std/assert";
import { dirname, join } from "@std/path";
import { createEmptyState } from "../../src/domain/state.ts";
import { createPlatform, createRecordingProcessRunner } from "../../src/platform/mod.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { parseDesiredState, stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { assembleComposeDocuments } from "../../src/services/compose.ts";
import { generateAll } from "../../src/services/generate.ts";
import { runSqliteBackup } from "../../src/services/sqlite_local.ts";

Deno.test("one app can persist independent SQLite and Litestream bindings", () => {
  const platform = createPlatform("/tmp/bento-sqlite-multiple", Deno.cwd());
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

Deno.test("plain SQLite backup uses .backup and gzip in the runner", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-sqlite-backup-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
    platform.clock = createFixedClock("2026-08-03T04:05:06.000Z");
    const result = provisionApp(platform, createEmptyState(), {
      slug: "local",
      domain: "local.test",
      databaseEngine: "sqlite",
    });
    platform.process = createRecordingProcessRunner(async (command) => {
      const script = command.at(-1) ?? "";
      assertStringIncludes(script, ".backup");
      assertStringIncludes(script, "gzip -c");
      const match = script.match(/FINAL='\/var\/backups\/bento\/([^']+)'/);
      assert(match);
      const output = join(root, "backups", match[1]!);
      await platform.fs.mkdirp(dirname(output));
      await platform.fs.writeText(output, "sqlite backup");
      return { code: 0, stdout: "", stderr: "" };
    });

    const artifact = await runSqliteBackup(platform, result.state, "local", "gzip");
    assertEquals(artifact.engine, "sqlite");
    assertEquals(artifact.path.endsWith(".sqlite.gz"), true);
    assertEquals(artifact.bytes > 0, true);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("plain SQLite is distinct from Litestream and gets weekly runner maintenance", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-sqlite-local-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
    const result = provisionApp(platform, createEmptyState("2026-08-01T00:00:00.000Z"), {
      slug: "local",
      domain: "local.test",
      databaseEngine: "sqlite",
    });
    assert(result.app.database.engine === "sqlite");
    assertEquals(result.app.database.file.path.endsWith("/local.db"), true);
    assert(parseDesiredState(JSON.parse(stateToJson(result.state))).ok);

    const files = await generateAll(platform, result.state, "digest");
    const crontab = files.find((file) => file.relPath.endsWith("cron/local.crontab"));
    const scheduler = files.find((file) => file.relPath.endsWith("services/scheduler-local/run"));
    assert(crontab && typeof crontab.content === "string");
    const vacuumLine = crontab.content.split("\n").find((line) => line.includes("VACUUM;"));
    assert(vacuumLine);
    const schedule = vacuumLine.match(/^(\d+) (\d+) \* \* (\d+) /);
    assert(schedule);
    assertEquals(Number(schedule[1]), result.app.database.vacuumSchedule?.minute);
    assertEquals(Number(schedule[2]), result.app.database.vacuumSchedule?.hour);
    assertEquals(Number(schedule[3]), result.app.database.vacuumSchedule?.dayOfWeek);
    assert(Number(schedule[1]) >= 0 && Number(schedule[1]) <= 59);
    assert(Number(schedule[2]) >= 0 && Number(schedule[2]) <= 4);
    assert(Number(schedule[3]) >= 0 && Number(schedule[3]) <= 6);
    assertStringIncludes(crontab.content, "sqlite3");
    assertStringIncludes(crontab.content, "VACUUM;");
    assert(scheduler, "plain SQLite must start a Supercronic scheduler");

    const rerendered = await generateAll(platform, result.state, "digest");
    const rerenderedCrontab = rerendered.find((file) =>
      file.relPath.endsWith("cron/local.crontab")
    );
    assert(rerenderedCrontab && typeof rerenderedCrontab.content === "string");
    assertEquals(rerenderedCrontab.content, crontab.content);

    const compose = assembleComposeDocuments(platform, result.state)
      .find((file) => file.relPath.includes("php-php85"));
    assert(compose && typeof compose.content === "string");
    assertStringIncludes(compose.content, "./backups/sqlite:/var/backups/bento/sqlite");
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("local SQLite VACUUM slots do not overlap when files are added", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-sqlite-schedule-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
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
      .filter((file) => file.relPath.endsWith(".crontab"))
      .flatMap((file) => {
        if (typeof file.content !== "string") return [];
        return file.content
          .split("\n")
          .filter((line) => line.includes("VACUUM;"))
          .map((line) => line.match(/^(\d+ \d+ \* \* \d+) /)?.[1])
          .filter((schedule): schedule is string => schedule !== undefined);
      });
    assertEquals(schedules.length, 2);
    assertEquals(new Set(schedules).size, schedules.length);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
