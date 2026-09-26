/** One in-process web backup at a time. The journal survives server exit; execution does not. */
import { join } from "node:path";
import { conflictError, notFoundError, validationError } from "#/domain/errors.ts";
import type { DesiredState } from "#/domain/state.ts";
import type { Platform } from "#/platform/mod.ts";
import { runDatabaseBackup } from "#/services/database_backup.ts";
import {
  finishOperation,
  finishOperationStep,
  interruptOperation,
  listBackupOperations,
  setOperationProgress,
  startBackupOperation,
  startOperationStep,
  type OperationRecord,
} from "#/services/operation_journal.ts";

function lockPath(platform: Platform): string {
  return join(platform.paths.paths.lockDir, "web-backup.lock");
}

export async function listWebBackupRuns(platform: Platform): Promise<OperationRecord[]> {
  const release = await platform.lock.tryExclusive(lockPath(platform));
  try {
    const records = await listBackupOperations(platform);
    if (release) {
      for (const record of records) {
        if (record.kind === "web-backup" && record.status === "running") await interruptOperation(platform, record);
      }
    }
    return records.filter((record) => record.kind === "web-backup");
  } finally {
    if (release) await release();
  }
}

/** Returns immediately after committing a private record. No automatic resume or retries. */
export async function startWebBackup(platform: Platform, state: DesiredState, app?: string): Promise<OperationRecord> {
  if (app && !Object.hasOwn(state.apps, app)) throw notFoundError(`app not found: ${app}`);
  const release = await platform.lock.tryExclusive(lockPath(platform));
  if (!release) throw conflictError("a web backup is already running", "Wait for it to finish and refresh Backups.");
  let operation: OperationRecord;
  try {
    for (const old of await listBackupOperations(platform)) {
      if (old.kind === "web-backup" && old.status === "running") await interruptOperation(platform, old);
    }
    operation = await startBackupOperation(platform, "web-backup");
    await startOperationStep(platform, operation, "backup");
  } catch (error) {
    await release();
    throw error;
  }

  // The lock remains held until the task settles. An abrupt server exit releases the OS
  // lock, allowing the next status read to mark the record interrupted, not successful.
  void (async () => {
    try {
      await runDatabaseBackup(
        platform,
        state,
        { scope: app ? "app" : "all", ...(app ? { slug: app } : {}), compress: "zstd" },
        async (completed, total) => {
          if (total === 0) throw validationError("no logical backup targets are configured");
          await setOperationProgress(platform, operation, completed, total);
        },
      );
      await finishOperationStep(platform, operation);
      await finishOperation(platform, operation);
    } catch (error) {
      if (operation.status === "running") await finishOperation(platform, operation, error);
    } finally {
      await release();
    }
  })().catch(() => {
    // A journal write/release error leaves an interrupted record; never claim success.
  });
  return operation;
}
