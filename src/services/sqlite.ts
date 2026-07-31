import { join, resolve } from "@std/path";
import type { AppDatabaseBinding, DesiredState, SqliteBackupPolicy } from "../domain/state.ts";
import { databaseBindings } from "../domain/state.ts";
import { conflictError, notFoundError, serviceError, validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import { composeArgs } from "./compose.ts";
import { loadStackComposeEnvironment, loadStackEnv } from "./stack_env.ts";
import {
  sqliteContainerPath,
  sqliteHostDir,
  sqliteHostPath,
  sqliteRelativePath,
} from "./sqlite_paths.ts";

type LitestreamBinding = Extract<AppDatabaseBinding, { engine: "litestream" }>;

export type WatchedSqliteDatabase = {
  path: string;
  status: string;
  last_sync_at?: string;
};

export type SqliteBackupStatus = {
  app: string;
  file: string;
  configured: boolean;
  containerRunning: boolean;
  replicationStatus: string;
  lastSyncAt?: string;
  expectedRpo?: string;
  verifiedAt?: string;
};

export function requireSqliteApp(state: DesiredState, slug: string) {
  const app = state.apps[slug];
  if (!app) throw notFoundError(`app not found: ${slug}`);
  const database = databaseBindings(app, "litestream")[0];
  if (!database) throw validationError(`app ${slug} has no Litestream database`);
  return { app, database: database as LitestreamBinding };
}

export function requireSqliteBackupPolicy(state: DesiredState): SqliteBackupPolicy {
  if (!state.sqliteBackup?.enabled) {
    throw conflictError(
      "stack-wide SQLite backup is not enabled",
      "Run `bento sqlite backup enable <app>` first.",
    );
  }
  return state.sqliteBackup;
}

export async function enableSqliteBackup(
  platform: Platform,
  state: DesiredState,
  slug: string,
  rpo = "60s",
  retention = "168h",
): Promise<DesiredState> {
  const environment = await loadStackComposeEnvironment(platform);
  if (!environment.litestreamEnabled) {
    throw conflictError(
      "BENTO_LITESTREAM_ENABLED is false",
      "Set BENTO_LITESTREAM_ENABLED=true in the stack .env and retry.",
    );
  }
  if (!["1s", "10s", "60s"].includes(rpo)) {
    throw validationError("--rpo must be one of 1s, 10s, or 60s");
  }

  const dockerInfo = await platform.process.run([
    "docker",
    "info",
    "--format",
    "{{json .SecurityOptions}}",
  ]);
  const securityOptions = dockerInfo.stdout.toLowerCase();
  if (
    dockerInfo.code === 0 &&
    (securityOptions.includes("rootless") || securityOptions.includes("userns"))
  ) {
    throw conflictError(
      "SQLite directory watching requires rootful Docker without user-namespace remapping",
      "Use a supported rootful Docker Engine host or keep stack-wide Litestream backup disabled.",
    );
  }

  requireSqliteApp(state, slug);
  for (const app of Object.values(state.apps)) {
    for (const database of databaseBindings(app, "litestream")) {
      const sqliteDir = sqliteHostDir(platform, database.file.id);
      const hostPath = sqliteHostPath(platform, database.file.id, app.slug, "litestream");
      const dirStat = await platform.fs.lstat(sqliteDir);
      if (!dirStat.isDirectory || dirStat.isSymlink) {
        throw validationError(`SQLite directory is not a real local directory: ${sqliteDir}`);
      }
      if (await platform.fs.exists(hostPath)) {
        const stat = await platform.fs.lstat(hostPath);
        if (!stat.isFile || stat.isSymlink) {
          throw validationError(`SQLite path must be a non-symlink regular file: ${hostPath}`);
        }
      }
    }
  }

  const env = await loadStackEnv(platform);
  for (
    const key of [
      "S3_BUCKET_NAME",
      "S3_REGION",
      "S3_ACCESS_KEY_ID",
      "S3_SECRET_ACCESS_KEY",
    ]
  ) {
    if (!env[key]) throw validationError(`${key} is required in the stack .env`);
  }

  // The watcher keeps transaction metadata outside app-owned directories. Both
  // mounts are writable by the constrained-root Litestream process without ACLs.
  await platform.fs.mkdirp(join(platform.paths.paths.root, "litestream-meta"), 0o700);
  await platform.fs.mkdirp(join(platform.paths.paths.root, "runtime", "litestream"), 0o700);

  const next = structuredClone(state);
  next.sqliteBackup = {
    provider: "litestream",
    destination: "primary-s3",
    syncInterval: rpo,
    snapshotInterval: "6h",
    snapshotRetention: retention,
    l0Retention: "24h",
    enabled: true,
  };
  const nextDb = requireSqliteApp(next, slug).database;
  nextDb.file.path = sqliteRelativePath(nextDb.file.id, slug, "litestream");
  next.apps[slug]!.updatedAt = platform.clock.nowIso();
  next.updatedAt = platform.clock.nowIso();
  return next;
}

export async function sqliteCompose(
  platform: Platform,
  state: DesiredState,
  command: string[],
): Promise<{ code: number; stdout: string; stderr: string }> {
  return await platform.process.run(await composeArgs(platform, state, command), {
    cwd: platform.paths.paths.root,
    timeoutMs: 180_000,
  });
}

export async function listSqliteBackups(
  platform: Platform,
  state: DesiredState,
): Promise<WatchedSqliteDatabase[]> {
  requireSqliteBackupPolicy(state);
  const result = await runSocketCommand(platform, state, ["list", "-json"]);
  try {
    const parsed = JSON.parse(result.stdout) as { databases?: WatchedSqliteDatabase[] };
    return Array.isArray(parsed.databases) ? parsed.databases : [];
  } catch (cause) {
    throw serviceError(`invalid Litestream database list: ${String(cause)}`);
  }
}

export async function getSqliteBackupStatus(
  platform: Platform,
  state: DesiredState,
  slug: string,
): Promise<SqliteBackupStatus> {
  const { database } = requireSqliteApp(state, slug);
  const configured = Boolean(state.sqliteBackup?.enabled);
  const ps = configured
    ? await sqliteCompose(platform, state, ["ps", "--status", "running", "litestream"])
    : { code: 0, stdout: "", stderr: "" };
  const containerRunning = ps.stdout.includes("litestream");
  const watched = containerRunning
    ? (await listSqliteBackups(platform, state)).find((entry) =>
      entry.path === sqliteContainerPath(database.file.id, slug)
    )
    : undefined;
  return {
    app: slug,
    file: sqliteContainerPath(database.file.id, slug),
    configured,
    containerRunning,
    replicationStatus: watched?.status ?? "not discovered",
    lastSyncAt: watched?.last_sync_at,
    expectedRpo: state.sqliteBackup?.syncInterval,
    verifiedAt: database.backupVerifiedAt,
  };
}

export async function syncSqliteBackup(
  platform: Platform,
  state: DesiredState,
  slug: string,
): Promise<string> {
  const { database } = requireSqliteApp(state, slug);
  requireSqliteBackupPolicy(state);
  const result = await runSocketCommand(platform, state, [
    "sync",
    "-wait",
    "-timeout",
    "120",
    sqliteContainerPath(database.file.id, slug),
  ]);
  return result.stdout.trim();
}

export async function exportSqliteBackup(
  platform: Platform,
  state: DesiredState,
  slug: string,
  requestedOutput: string,
): Promise<string> {
  const { database } = requireSqliteApp(state, slug);
  requireSqliteBackupPolicy(state);
  const output = resolve(requestedOutput);
  if (await platform.fs.exists(output)) {
    throw conflictError(`refusing to overwrite existing file: ${output}`);
  }

  const exportId = platform.random.id("sqlite-export");
  const restoredName = `${exportId}.sqlite`;
  const containerRestorePath = `/var/lib/litestream/${restoredName}`;
  const hostRestorePath = join(platform.paths.paths.root, "litestream-meta", restoredName);
  const partialOutput = `${output}.${exportId}.partial`;
  const replicaUrl = await sqliteReplicaUrl(platform, database.file.id, slug);

  try {
    const result = await sqliteCompose(platform, state, [
      "exec",
      "-T",
      "litestream",
      "litestream",
      "restore",
      "-o",
      containerRestorePath,
      "-integrity-check",
      "full",
      replicaUrl,
    ]);
    if (result.code !== 0) {
      throw serviceError(`Litestream export failed: ${result.stderr.trim()}`);
    }
    const restored = await platform.fs.lstat(hostRestorePath);
    if (!restored.isFile || restored.isSymlink || restored.size === 0) {
      throw serviceError("Litestream export did not produce a non-empty regular database file");
    }
    await platform.fs.copyFile(hostRestorePath, partialOutput);
    await platform.fs.chmod(partialOutput, 0o600);
    if (await platform.fs.exists(output)) {
      throw conflictError(`refusing to overwrite existing file: ${output}`);
    }
    await platform.fs.rename(partialOutput, output);
    return output;
  } finally {
    await platform.fs.remove(hostRestorePath);
    await platform.fs.remove(partialOutput);
    for (const suffix of ["-wal", "-shm", "-journal"]) {
      await platform.fs.remove(`${hostRestorePath}${suffix}`);
    }
  }
}

export async function verifySqliteBackup(
  platform: Platform,
  state: DesiredState,
  slug: string,
): Promise<string> {
  const { database } = requireSqliteApp(state, slug);
  requireSqliteBackupPolicy(state);
  await syncSqliteBackup(platform, state, slug);

  const proofName = `verify-${database.file.id}-${Date.now()}.sqlite`;
  const containerProofPath = `/var/lib/litestream/${proofName}`;
  const hostProofPath = join(platform.paths.paths.root, "litestream-meta", proofName);
  const replicaUrl = await sqliteReplicaUrl(platform, database.file.id, slug);
  const result = await sqliteCompose(platform, state, [
    "exec",
    "-T",
    "litestream",
    "litestream",
    "restore",
    "-o",
    containerProofPath,
    "-integrity-check",
    "full",
    replicaUrl,
  ]);
  if (await platform.fs.exists(hostProofPath)) await platform.fs.remove(hostProofPath);
  for (const suffix of ["-wal", "-shm", "-journal"]) {
    const sidecar = `${hostProofPath}${suffix}`;
    if (await platform.fs.exists(sidecar)) await platform.fs.remove(sidecar);
  }
  if (result.code !== 0) {
    throw serviceError(`Litestream restore verification failed: ${result.stderr.trim()}`);
  }
  return result.stdout.trim();
}

async function runSocketCommand(
  platform: Platform,
  state: DesiredState,
  args: string[],
): Promise<{ code: number; stdout: string; stderr: string }> {
  let result = { code: 1, stdout: "", stderr: "Litestream control socket not ready" };
  const [command, ...commandArgs] = args;
  if (!command) throw new Error("Litestream command is required");
  for (let attempt = 0; attempt < 20; attempt++) {
    result = await sqliteCompose(platform, state, [
      "exec",
      "-T",
      "litestream",
      "litestream",
      command,
      "-socket",
      "/run/litestream/control.sock",
      ...commandArgs,
    ]);
    if (result.code === 0) return result;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw serviceError(`Litestream command failed: ${result.stderr.trim()}`);
}

async function sqliteReplicaUrl(
  platform: Platform,
  fileId: string,
  slug: string,
): Promise<string> {
  const env = await loadStackEnv(platform);
  const composeEnvironment = await loadStackComposeEnvironment(platform);
  const params = new URLSearchParams();
  if (env.S3_ENDPOINT) params.set("endpoint", env.S3_ENDPOINT);
  if (env.S3_REGION) params.set("region", env.S3_REGION);
  const query = params.size > 0 ? `?${params.toString()}` : "";
  return `s3://${env.S3_BUCKET_NAME}/bento/${composeEnvironment.projectName}/${fileId}/${slug}.sqlite${query}`;
}
