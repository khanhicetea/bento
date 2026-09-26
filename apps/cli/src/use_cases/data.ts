import { join, relative, resolve } from "node:path";
import { conflictError, notFoundError } from "#/domain/errors.ts";
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
      const release = await platform.lock.tryExclusive(join(platform.paths.paths.lockDir, "database-backup.lock"));
      if (!release)
        throw conflictError("a logical backup is running", "Wait for the backup batch to finish before restoring.");
      try {
        const next = await runDatabaseRestore(platform, state, request);
        if (next !== state) await store.save(next);
      } finally {
        await release();
      }
    });
  }

  async function resolveBackupArtifact(artifact: string): Promise<string> {
    const root = resolve(platform.paths.paths.backupsDir);
    const file = resolve(root, artifact);
    const rel = relative(root, file);
    if (
      !rel ||
      rel === ".." ||
      rel.startsWith("../") ||
      rel.startsWith("..\\") ||
      !/\.sql(?:\.gz|\.zst|\.zstd)$/i.test(file)
    ) {
      throw notFoundError("finalized relational backup artifact was not found");
    }
    let current = root;
    for (const part of rel.split(/[\\/]/)) {
      const info = await platform.fs.lstat(current);
      if (info.isSymlink || !info.isDirectory) throw notFoundError("backup artifact was not found");
      current = resolve(current, part);
    }
    const info = await platform.fs.lstat(file);
    if (info.isSymlink || !info.isFile || info.size === 0) throw notFoundError("backup artifact was not found");
    return file;
  }

  async function listBackupArtifacts(): Promise<Array<{ name: string; bytes: number; modifiedAt?: string }>> {
    const root = resolve(platform.paths.paths.backupsDir);
    if (!(await platform.fs.exists(root))) return [];
    const rootInfo = await platform.fs.lstat(root);
    if (rootInfo.isSymlink || !rootInfo.isDirectory) return [];
    const found: Array<{ name: string; bytes: number; modifiedAt?: string }> = [];
    const pending = [root];
    while (pending.length) {
      const directory = pending.pop()!;
      for (const name of await platform.fs.readDir(directory)) {
        const path = resolve(directory, name);
        const rel = relative(root, path);
        if (!rel || rel === ".." || rel.startsWith("../") || rel.startsWith("..\\")) continue;
        const info = await platform.fs.lstat(path);
        if (info.isSymlink) continue;
        if (info.isDirectory) pending.push(path);
        if (info.isFile && info.size > 0 && /\.(?:sql|sqlite)(?:\.gz|\.zst|\.zstd)?$/i.test(name)) {
          const stat = await platform.fs.stat(path);
          found.push({
            name: rel,
            bytes: info.size,
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
