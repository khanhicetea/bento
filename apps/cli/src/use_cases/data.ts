import { relative, resolve } from "node:path";
import { notFoundError } from "#/domain/errors.ts";
import type { Platform } from "#/platform/mod.ts";
import {
  runDatabaseBackup,
  runDatabaseRestore,
  type DatabaseBackupArtifact,
  type DatabaseBackupRequest,
  type DatabaseRestoreRequest,
} from "#/services/database_backup.ts";
import { StateStore } from "#/services/state_store.ts";

export type BackupDatabasesResult = {
  artifacts: DatabaseBackupArtifact[];
};

export function createDataUseCases(deps: { platform: Platform; store: StateStore }) {
  const { platform, store } = deps;

  async function backup(request: DatabaseBackupRequest): Promise<BackupDatabasesResult> {
    const state = await store.load();
    const artifacts = await runDatabaseBackup(platform, state, request);
    return { artifacts };
  }

  async function restore(request: DatabaseRestoreRequest): Promise<void> {
    await store.withExclusive(async (state) => {
      const next = await runDatabaseRestore(platform, state, request);
      if (next !== state) await store.save(next);
    });
  }

  async function resolveBackupArtifact(artifact: string): Promise<string> {
    const root = resolve(platform.paths.paths.backupsDir);
    const file = resolve(root, artifact);
    const rel = relative(root, file);
    if (!rel || rel === ".." || rel.startsWith("../") || rel.startsWith("..\\") || !(await platform.fs.exists(file))) {
      throw notFoundError("backup artifact was not found");
    }
    return file;
  }

  async function listBackupArtifacts(): Promise<Array<{ name: string; bytes: number; modifiedAt?: string }>> {
    const root = resolve(platform.paths.paths.backupsDir);
    if (!(await platform.fs.exists(root))) return [];
    const found: Array<{ name: string; bytes: number; modifiedAt?: string }> = [];
    const pending = [root];
    while (pending.length) {
      const directory = pending.pop()!;
      for (const name of await platform.fs.readDir(directory)) {
        const path = resolve(directory, name);
        const rel = relative(root, path);
        if (!rel || rel === ".." || rel.startsWith("../") || rel.startsWith("..\\")) continue;
        const stat = await platform.fs.stat(path);
        if (stat.isDirectory) pending.push(path);
        if (stat.isFile && /\.(?:sql|sqlite)(?:\.gz|\.zst|\.zstd)?$/i.test(name)) {
          found.push({
            name: rel,
            bytes: stat.size,
            ...(stat.modifiedAt ? { modifiedAt: stat.modifiedAt.toISOString() } : {}),
          });
        }
      }
    }
    return found.sort((left, right) => right.name.localeCompare(left.name));
  }

  return { backup, restore, resolveBackupArtifact, listBackupArtifacts };
}

export type DataUseCases = ReturnType<typeof createDataUseCases>;
