import { join } from "@std/path";
import type { AppDatabaseBinding, DesiredState, SqliteBackupPolicy } from "../domain/state.ts";
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

type SqliteBinding = Extract<AppDatabaseBinding, { engine: "sqlite" }>;

export type WatchedSqliteDatabase = {
  path: string;
  status: string;
  last_sync_at?: string;
};

export function requireSqliteApp(state: DesiredState, slug: string) {
  const app = state.apps[slug];
  if (!app) throw notFoundError(`app not found: ${slug}`);
  if (app.database.engine !== "sqlite") {
    throw validationError(`app ${slug} uses ${app.database.engine}, not SQLite`);
  }
  return { app, database: app.database as SqliteBinding };
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
    if (app.database.engine !== "sqlite") continue;
    const sqliteDir = sqliteHostDir(platform, app.database.file.id);
    const hostPath = sqliteHostPath(platform, app.database.file.id);
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

  const secretDir = join(platform.paths.paths.root, "secrets", "litestream");
  await platform.fs.mkdirp(secretDir, 0o700);
  await platform.fs.atomicWriteText(
    join(secretDir, "stack-s3.env"),
    [
      `S3_BUCKET_NAME=${env.S3_BUCKET_NAME}`,
      `S3_REGION=${env.S3_REGION}`,
      `S3_ENDPOINT=${env.S3_ENDPOINT ?? ""}`,
      `AWS_ACCESS_KEY_ID=${env.S3_ACCESS_KEY_ID}`,
      `AWS_SECRET_ACCESS_KEY=${env.S3_SECRET_ACCESS_KEY}`,
      `AWS_REGION=${env.S3_REGION}`,
      "",
    ].join("\n"),
    0o600,
  );

  // The watcher keeps transaction metadata outside app-owned directories. Both
  // mounts are writable by the constrained-root Litestream process without ACLs.
  await platform.fs.mkdirp(join(platform.paths.paths.root, "litestream-meta"), 0o700);
  await platform.fs.mkdirp(join(platform.paths.paths.root, "runtime", "litestream"), 0o700);

  const next = structuredClone(state);
  next.sqliteBackup = {
    provider: "litestream",
    destination: "primary-s3",
    syncInterval: rpo,
    snapshotInterval: "1h",
    snapshotRetention: retention,
    l0Retention: "24h",
    enabled: true,
  };
  const nextDb = requireSqliteApp(next, slug).database;
  nextDb.file.path = sqliteRelativePath(nextDb.file.id);
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
    sqliteContainerPath(database.file.id),
  ]);
  return result.stdout.trim();
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
  const replicaUrl = await sqliteReplicaUrl(platform, database.file.id);
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

async function sqliteReplicaUrl(platform: Platform, fileId: string): Promise<string> {
  const env = await loadStackEnv(platform);
  const composeEnvironment = await loadStackComposeEnvironment(platform);
  const params = new URLSearchParams();
  if (env.S3_ENDPOINT) params.set("endpoint", env.S3_ENDPOINT);
  if (env.S3_REGION) params.set("region", env.S3_REGION);
  const query = params.size > 0 ? `?${params.toString()}` : "";
  return `s3://${env.S3_BUCKET_NAME}/bento/${composeEnvironment.projectName}/${fileId}/database.sqlite${query}`;
}
