import { assertEquals, assertRejects, assertStringIncludes } from "@std/assert";
import { join } from "@std/path";
import { isBentoError } from "../../src/domain/errors.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createFileLock, createMemoryLock } from "../../src/platform/lock.ts";
import type { Platform, RunOptions, RunResult } from "../../src/platform/mod.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import {
  backupScheduleCronFragment,
  backupScheduleLastRunPath,
  getBackupScheduleStatus,
  mergeBackupScheduleCrontab,
  readBackupScheduleLastRun,
  registerBackupSchedule,
  runScheduledBackup,
  shellSingleQuote,
  unregisterBackupSchedule,
} from "../../src/services/backup_schedule.ts";
import { runDatabaseBackup } from "../../src/services/database_backup.ts";
import { applyBackupRetention } from "../../src/services/mysql.ts";
import { addPostgresVersion } from "../../src/services/postgres.ts";
import { provisionApp } from "../../src/services/app.ts";

function testPlatform(
  root: string,
  handler?: (command: string[], options?: RunOptions) => Promise<RunResult> | RunResult,
): Platform & { process: ReturnType<typeof createRecordingProcessRunner> } {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-08-01T03:15:00.000Z"),
    random: createSeededRandom("aabbccddeeff0088"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(handler),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

Deno.test("backup schedule helpers quote commands and preserve unrelated crontab bytes", () => {
  assertEquals(shellSingleQuote("/opt/Bento's bin"), "'/opt/Bento'\\''s bin'");
  const root = "/srv/bento stack";
  const fragment = backupScheduleCronFragment({
    schedule: "  15   3 * * *  ",
    bentoBin: "/opt/Bento's bin",
    stackRoot: root,
  });
  assertStringIncludes(fragment, "15 3 * * * '/opt/Bento'\\''s bin' --stack '/srv/bento stack'");
  assertStringIncludes(fragment, "backup schedule run >/dev/null 2>&1");

  const other = backupScheduleCronFragment({
    schedule: "0 4 * * *",
    bentoBin: "/usr/local/bin/bento",
    stackRoot: "/srv/other",
  });
  const existing = `MAILTO=ops@example.test\n${other}5 5 * * * echo keep`;
  const installed = mergeBackupScheduleCrontab(existing, {
    action: "install",
    stackRoot: root,
    fragment,
  });
  assertEquals(installed.changed, true);
  assertEquals(installed.crontab.startsWith(existing + "\n"), true);
  assertStringIncludes(installed.crontab, other);

  const idempotent = mergeBackupScheduleCrontab(installed.crontab, {
    action: "install",
    stackRoot: root,
    fragment,
  });
  assertEquals(idempotent.changed, false);
  const removed = mergeBackupScheduleCrontab(installed.crontab, {
    action: "remove",
    stackRoot: root,
  });
  assertEquals(removed.crontab, existing + "\n");
  assertEquals(removed.crontab.includes("/srv/other"), true);
});

Deno.test("backup schedule service registers, reports, and unregisters through injected crontab", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-backup-schedule-service-" });
  try {
    let crontab = "MAILTO=ops@example.test\n";
    const platform = testPlatform(root, (command, options) => {
      assertEquals(command[0], "crontab");
      if (command[1] === "-l") return { code: 0, stdout: crontab, stderr: "" };
      assertEquals(command, ["crontab", "-"]);
      crontab = String(options?.stdin ?? "");
      return { code: 0, stdout: "", stderr: "" };
    });
    const bin = join(root, "bento executable");
    await platform.fs.writeText(bin, "#!/bin/sh\n", 0o700);

    const installed = await registerBackupSchedule(platform, {
      schedule: "15 3 * * *",
      bentoBin: bin,
    });
    assertEquals(installed.changed, true);
    assertEquals(
      (await registerBackupSchedule(platform, {
        schedule: "15 3 * * *",
        bentoBin: bin,
      })).changed,
      false,
    );
    const status = await getBackupScheduleStatus(platform);
    assertEquals(status.installed, true);
    assertEquals(status.schedule, "15 3 * * *");
    assertEquals(status.lastRun, null);

    assertEquals((await unregisterBackupSchedule(platform)).changed, true);
    assertEquals(crontab, "MAILTO=ops@example.test\n");
    assertEquals((await unregisterBackupSchedule(platform)).changed, false);
    assertEquals(
      platform.process.calls.every((call) => call.command[0] === "crontab"),
      true,
    );
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("backup schedule serializes crontab updates across stack roots", async () => {
  const rootA = await Deno.makeTempDir({ prefix: "bento-backup-stack-a-" });
  const rootB = await Deno.makeTempDir({ prefix: "bento-backup-stack-b-" });
  try {
    let crontab = "";
    const runner = createRecordingProcessRunner((command, options) => {
      if (command[1] === "-l") return { code: 0, stdout: crontab, stderr: "" };
      crontab = String(options?.stdin ?? "");
      return { code: 0, stdout: "", stderr: "" };
    });
    const sharedLock = createMemoryLock();
    const platformA = testPlatform(rootA);
    const platformB = testPlatform(rootB);
    platformA.process = runner;
    platformB.process = runner;
    platformA.lock = sharedLock;
    platformB.lock = sharedLock;
    const binA = join(rootA, "bento");
    const binB = join(rootB, "bento");
    await platformA.fs.writeText(binA, "#!/bin/sh\n", 0o700);
    await platformB.fs.writeText(binB, "#!/bin/sh\n", 0o700);

    await Promise.all([
      registerBackupSchedule(platformA, { schedule: "1 1 * * *", bentoBin: binA }),
      registerBackupSchedule(platformB, { schedule: "2 2 * * *", bentoBin: binB }),
    ]);

    assertStringIncludes(crontab, rootA);
    assertStringIncludes(crontab, rootB);
  } finally {
    await Deno.remove(rootA, { recursive: true });
    await Deno.remove(rootB, { recursive: true });
  }
});

Deno.test("memory and file tryExclusive locks reject immediately while held", async () => {
  const memory = createMemoryLock();
  const releaseMemory = await memory.tryExclusive("backup");
  assertEquals(typeof releaseMemory, "function");
  assertEquals(await memory.tryExclusive("backup"), null);
  await releaseMemory!();
  const reacquiredMemory = await memory.tryExclusive("backup");
  assertEquals(typeof reacquiredMemory, "function");
  await reacquiredMemory!();

  const root = await Deno.makeTempDir({ prefix: "bento-backup-lock-" });
  try {
    const lock = createFileLock();
    const path = join(root, "backup.lock");
    const releaseFile = await lock.tryExclusive(path);
    assertEquals(typeof releaseFile, "function");
    assertEquals(await lock.tryExclusive(path), null);
    await releaseFile!();
    const reacquired = await lock.tryExclusive(path);
    assertEquals(typeof reacquired, "function");
    await reacquired!();
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("database backup rejects overlapping batches without waiting", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-backup-overlap-" });
  try {
    const platform = testPlatform(root);
    const lockPath = join(platform.paths.paths.lockDir, "database-backup.lock");
    const release = await platform.lock.tryExclusive(lockPath);
    const conflict = await runDatabaseBackup(platform, createEmptyState(), { scope: "all" })
      .then(() => null)
      .catch((error: unknown) => error);
    assertEquals(isBentoError(conflict), true);
    if (isBentoError(conflict)) {
      assertEquals(conflict.code, "CONFLICT");
      assertEquals(conflict.exitCode, 4);
      assertStringIncludes(conflict.message, "another logical backup batch is already running");
    }
    await release!();
    assertEquals(
      await runDatabaseBackup(platform, createEmptyState(), { scope: "all" }),
      [],
    );
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("scheduled backup persists bounded redacted last-run records with private modes", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-backup-last-run-" });
  try {
    const secret = "schedule-super-secret";
    const platform = testPlatform(root, () => ({
      code: 1,
      stdout: "",
      stderr: `password=${secret}`,
    }));
    let state = addPostgresVersion(createEmptyState(), "17");
    state = provisionApp(platform, state, {
      slug: "reports",
      domain: "reports.test",
      databaseEngine: "postgres",
      postgresVersion: "17",
      createDatabase: true,
    }).state;

    await assertRejects(() => runScheduledBackup(platform, state), Error, "dump failed");
    const failed = await readBackupScheduleLastRun(platform);
    assertEquals(failed?.status, "failed");
    assertEquals(failed?.exitCode, 1);
    assertEquals(failed?.error?.includes(secret), false);
    assertStringIncludes(failed?.error ?? "", "password=***");

    const recordPath = backupScheduleLastRunPath(platform);
    assertEquals((await platform.fs.stat(recordPath)).mode & 0o777, 0o600);
    assertEquals((await platform.fs.stat(join(recordPath, ".."))).mode & 0o777, 0o700);

    assertEquals(await runScheduledBackup(platform, createEmptyState()), []);
    const succeeded = await readBackupScheduleLastRun(platform);
    assertEquals(succeeded?.status, "succeeded");
    assertEquals(succeeded?.exitCode, 0);
    assertEquals(succeeded?.artifactCount, 0);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("backup retention removes only allowlisted regular dump files", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-backup-retention-" });
  try {
    const platform = testPlatform(root);
    const dir = join(platform.paths.paths.backupsDir, "postgres17", "reports");
    await platform.fs.mkdirp(dir);
    const generated = Array.from(
      { length: 12 },
      (_, index) =>
        `postgres17_reports_2026-08-${String(index + 1).padStart(2, "0")}T03-15-00-000Z.sql.gz`,
    );
    for (const name of generated) {
      await platform.fs.writeText(join(dir, name), "dump\n");
    }
    await platform.fs.writeText(join(dir, "aaa-manual.sql"), "keep\n");
    await platform.fs.writeText(
      join(dir, "postgres17_reports_2026-08-01T03-15-00-000Z.sql.zstd"),
      "keep\n",
    );
    await platform.fs.writeText(
      join(dir, "postgres17_reports_2026-99-99T99-99-99-999Z.sql.gz"),
      "keep\n",
    );
    await platform.fs.writeText(join(dir, "notes.txt"), "keep\n");
    await platform.fs.writeText(join(dir, `${generated[11]}.partial`), "keep\n");
    await platform.fs.mkdirp(join(dir, "directory.sql"));
    await Deno.symlink(join(dir, generated[11]!), join(dir, "symlink.sql"));

    await applyBackupRetention(
      platform,
      [{ service: "postgres17", database: "reports" }],
      10,
    );

    assertEquals(await platform.fs.exists(join(dir, generated[0]!)), false);
    assertEquals(await platform.fs.exists(join(dir, generated[1]!)), false);
    for (
      const name of [
        generated[2]!,
        generated[11]!,
        "aaa-manual.sql",
        "postgres17_reports_2026-08-01T03-15-00-000Z.sql.zstd",
        "postgres17_reports_2026-99-99T99-99-99-999Z.sql.gz",
        "notes.txt",
        `${generated[11]}.partial`,
        "directory.sql",
        "symlink.sql",
      ]
    ) {
      assertEquals(await platform.fs.exists(join(dir, name)), true, name);
    }
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
