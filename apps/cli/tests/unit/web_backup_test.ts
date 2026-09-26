import { runtime as bunRuntime, assertEquals, assertRejects } from "../runtime.ts";
import { join } from "node:path";
import { createEmptyState } from "../../src/domain/state.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import type { Platform } from "../../src/platform/mod.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { provisionApp } from "../../src/services/app.ts";
import { startBackupOperation, startOperationStep, operationRecordPath } from "../../src/services/operation_journal.ts";
import { addPostgresVersion } from "../../src/services/postgres.ts";
import { listWebBackupRuns, startWebBackup } from "../../src/services/web_backup.ts";
import { createDataUseCases } from "../../src/use_cases/data.ts";
import { StateStore } from "../../src/services/state_store.ts";

bunRuntime.test(
  "web backups return a durable ID, reject overlap, and record failed progress without secrets",
  async () => {
    const root = await bunRuntime.makeTempDir({ prefix: "bento-web-backup-" });
    try {
      let unblock!: () => void;
      const blocked = new Promise<void>((resolve) => {
        unblock = resolve;
      });
      const fs = createFileSystem();
      const platform: Platform = {
        fs,
        clock: createFixedClock("2026-08-01T03:15:00.000Z"),
        random: createSeededRandom("aabbccddeeff0088"),
        lock: createMemoryLock(),
        process: createRecordingProcessRunner(async () => {
          await blocked;
          return { code: 1, stdout: "", stderr: "password=private-value" };
        }),
        assets: createAssetResolver(fs),
        paths: createPathPolicy(root),
      };
      let state = addPostgresVersion(createEmptyState(), "17");
      state = provisionApp(platform, state, {
        slug: "reports",
        domain: "reports.test",
        databaseEngine: "postgres",
        postgresVersion: "17",
        createDatabase: true,
      }).state;
      const started = await startWebBackup(platform, state, "reports");
      assertEquals(started.kind, "web-backup");
      assertEquals((await listWebBackupRuns(platform))[0]?.status, "running");
      await assertRejects(() => startWebBackup(platform, state), Error, "already running");
      unblock();
      let latest = (await listWebBackupRuns(platform))[0];
      for (let n = 0; n < 30 && latest?.status === "running"; n++) {
        await Bun.sleep(5);
        latest = (await listWebBackupRuns(platform))[0];
      }
      assertEquals(latest?.status, "failed");
      assertEquals(latest?.progress, { completed: 0, total: 1 });
      assertEquals(latest?.steps[0]?.error?.includes("private-value"), false);
      assertEquals((await fs.stat(operationRecordPath(platform, started.id))).mode & 0o777, 0o600);
    } finally {
      await bunRuntime.remove(root, { recursive: true });
    }
  },
);

bunRuntime.test("successful web backup records completed targets and a local artifact", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-web-backup-success-" });
  try {
    const fs = createFileSystem();
    const platform: Platform = {
      fs,
      clock: createFixedClock("2026-08-01T03:15:00.000Z"),
      random: createSeededRandom("aabbccddeeff0088"),
      lock: createMemoryLock(),
      process: createRecordingProcessRunner(async () => {
        const file = join(
          root,
          "backups",
          "postgres17",
          "reports",
          "postgres17_reports_2026-08-01T03-15-00-000Z.sql.zst",
        );
        await fs.writeText(file, "dump", 0o600);
        return { code: 0, stdout: "", stderr: "" };
      }),
      assets: createAssetResolver(fs),
      paths: createPathPolicy(root),
    };
    let state = addPostgresVersion(createEmptyState(), "17");
    state = provisionApp(platform, state, {
      slug: "reports",
      domain: "reports.test",
      databaseEngine: "postgres",
      postgresVersion: "17",
      createDatabase: true,
    }).state;
    await startWebBackup(platform, state, "reports");
    let latest = (await listWebBackupRuns(platform))[0];
    for (let n = 0; n < 30 && latest?.status === "running"; n++) {
      await Bun.sleep(5);
      latest = (await listWebBackupRuns(platform))[0];
    }
    assertEquals(latest?.status, "succeeded");
    assertEquals(latest?.progress, { completed: 1, total: 1 });
    assertEquals(latest?.steps[0]?.status, "succeeded");
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("web backup status reconciles an abandoned run only after its lock is released", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-web-backup-interrupted-" });
  try {
    const fs = createFileSystem();
    const platform: Platform = {
      fs,
      clock: createFixedClock("2026-08-01T03:15:00.000Z"),
      random: createSeededRandom("aabbccddeeff0088"),
      lock: createMemoryLock(),
      process: createRecordingProcessRunner(),
      assets: createAssetResolver(fs),
      paths: createPathPolicy(root),
    };
    const record = await startBackupOperation(platform, "web-backup");
    await startOperationStep(platform, record, "backup");
    const release = await platform.lock.tryExclusive(join(platform.paths.paths.lockDir, "web-backup.lock"));
    assertEquals((await listWebBackupRuns(platform))[0]?.status, "running");
    await release!();
    assertEquals((await listWebBackupRuns(platform))[0]?.status, "interrupted");
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("artifact picker excludes symlinks and resolves only regular finalized relational files", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-backup-picker-safe-" });
  try {
    const fs = createFileSystem();
    const platform: Platform = {
      fs,
      clock: createFixedClock("2026-08-01T03:15:00.000Z"),
      random: createSeededRandom("aabbccddeeff0088"),
      lock: createMemoryLock(),
      process: createRecordingProcessRunner(),
      assets: createAssetResolver(fs),
      paths: createPathPolicy(root),
    };
    const data = createDataUseCases({ platform, store: new StateStore(platform) });
    const dir = join(platform.paths.paths.backupsDir, "mysql84", "demo");
    await fs.mkdirp(dir);
    await fs.writeText(join(dir, "good.sql.zst"), "dump", 0o600);
    await fs.writeText(join(dir, "good.sql.zst.partial"), "partial", 0o600);
    await bunRuntime.symlink(join(dir, "good.sql.zst"), join(dir, "linked.sql.zst"));
    await bunRuntime.symlink(dir, join(platform.paths.paths.backupsDir, "linked-dir"));
    assertEquals(
      (await data.listBackupArtifacts()).map((item) => item.name),
      ["mysql84/demo/good.sql.zst"],
    );
    assertEquals(await data.resolveBackupArtifact("mysql84/demo/good.sql.zst"), join(dir, "good.sql.zst"));
    await assertRejects(() => data.resolveBackupArtifact("mysql84/demo/linked.sql.zst"), Error);
    await assertRejects(() => data.resolveBackupArtifact("linked-dir/good.sql.zst"), Error);
    await assertRejects(() => data.resolveBackupArtifact("mysql84/demo/good.sql.zst.partial"), Error);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});
