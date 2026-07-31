/** rclone sidecar configuration and logical-backup upload support. */

import { join, relative, resolve } from "@std/path";
import { z } from "zod";
import { platformError, stateError, validationError } from "../domain/errors.ts";
import type { DesiredState } from "../domain/state.ts";
import type { Platform } from "../platform/mod.ts";
import { redact } from "../ui/output.ts";
import { composeArgs } from "./compose.ts";
import type { DatabaseBackupArtifact } from "./database_backup.ts";

const BACKUP_TARGET_VERSION = 1;
const BACKUP_TARGET_FILE = "rclone.json";

export type RcloneBackupTarget = {
  version: 1;
  remote: string;
  prefix: string;
};

const backupTargetSchema = z.object({
  version: z.literal(BACKUP_TARGET_VERSION),
  remote: z.string().min(1).max(128),
  prefix: z.string().max(1024),
}).strict();

/** Create the operator-owned rclone config once, without ever replacing credentials. */
export async function initializeRcloneConfig(platform: Platform): Promise<void> {
  const dir = platform.paths.paths.rcloneDir;
  const config = platform.paths.paths.rcloneConfigFile;
  if (await platform.fs.exists(dir)) {
    const info = await platform.fs.lstat(dir);
    if (info.isSymlink || !info.isDirectory) {
      throw stateError("rclone configuration directory must be a non-symlink directory");
    }
  } else {
    await platform.fs.mkdirp(dir, 0o700);
  }
  await platform.fs.chmod(dir, 0o700);

  if (await platform.fs.exists(config)) {
    const info = await platform.fs.lstat(config);
    if (info.isSymlink || !info.isFile) {
      throw stateError("rclone configuration must be a regular non-symlink file");
    }
    await platform.fs.chmod(config, 0o600);
    return;
  }
  await platform.fs.atomicWriteText(
    config,
    "# Bento rclone configuration. Configure with: bento rclone -- config\n",
    0o600,
  );
}

export function rcloneBackupTargetPath(platform: Platform): string {
  return join(platform.paths.paths.backupsDir, ".schedule", BACKUP_TARGET_FILE);
}

export function validateRcloneBackupTarget(remote: string, prefix: string): RcloneBackupTarget {
  const normalizedRemote = remote.trim();
  // Keep the scheduled destination unambiguous: a remote is the config section name,
  // not an already-composed `remote:path` expression.
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(normalizedRemote)) {
    throw validationError(
      "rclone remote must be a configured remote name containing only letters, digits, _ or -",
    );
  }
  const normalizedPrefix = normalizeRclonePrefix(prefix);
  return { version: BACKUP_TARGET_VERSION, remote: normalizedRemote, prefix: normalizedPrefix };
}

export async function saveRcloneBackupTarget(
  platform: Platform,
  remote: string,
  prefix: string,
): Promise<RcloneBackupTarget> {
  await initializeRcloneConfig(platform);
  const target = validateRcloneBackupTarget(remote, prefix);
  const dir = join(platform.paths.paths.backupsDir, ".schedule");
  await platform.fs.mkdirp(dir, 0o700);
  await platform.fs.chmod(dir, 0o700);
  await platform.fs.atomicWriteText(
    rcloneBackupTargetPath(platform),
    `${JSON.stringify(target, null, 2)}\n`,
    0o600,
  );
  return target;
}

export async function readRcloneBackupTarget(
  platform: Platform,
): Promise<RcloneBackupTarget | null> {
  const path = rcloneBackupTargetPath(platform);
  if (!(await platform.fs.exists(path))) return null;
  const info = await platform.fs.lstat(path);
  if (info.isSymlink || !info.isFile) {
    throw stateError("rclone backup target must be a regular non-symlink file");
  }
  if (info.size > 16 * 1024) throw stateError("rclone backup target exceeds 16 KiB");
  let raw: unknown;
  try {
    raw = JSON.parse(await platform.fs.readText(path));
  } catch (cause) {
    throw stateError(`rclone backup target is malformed: ${String(cause)}`);
  }
  const parsed = backupTargetSchema.safeParse(raw);
  if (!parsed.success) {
    throw stateError(
      `rclone backup target is invalid: ${
        parsed.error.issues.map((issue) => issue.message).join("; ")
      }`,
    );
  }
  return validateRcloneBackupTarget(parsed.data.remote, parsed.data.prefix);
}

/** Compose command for the dedicated, ephemeral rclone sidecar. */
export async function rcloneComposeCommand(
  platform: Platform,
  state: DesiredState,
  args: string[],
): Promise<string[]> {
  if (args.length === 0) throw validationError("usage: bento rclone -- <rclone arguments>");
  await initializeRcloneConfig(platform);
  return await composeArgs(platform, state, [
    "--profile",
    "rclone",
    "run",
    "--rm",
    "--no-deps",
    "rclone",
    ...args,
  ]);
}

/** Upload exactly the artifacts from one successful backup batch, preserving their backup-relative path. */
export async function uploadBackupArtifacts(
  platform: Platform,
  state: DesiredState,
  artifacts: DatabaseBackupArtifact[],
  target: RcloneBackupTarget,
): Promise<void> {
  if (artifacts.length === 0) return;
  await initializeRcloneConfig(platform);
  for (const artifact of artifacts) {
    const artifactPath = backupContainerPath(platform, artifact.path);
    const destination = rcloneDestination(target, artifactPath);
    const command = await composeArgs(platform, state, [
      "--profile",
      "rclone",
      "run",
      "--rm",
      "--no-deps",
      "rclone",
      "copyto",
      artifactPath,
      destination,
    ]);
    const result = await platform.process.run(command, {
      cwd: platform.paths.paths.root,
      timeoutMs: 15 * 60_000,
    });
    if (result.code !== 0) {
      const detail = redact((result.stderr || result.stdout || "unknown rclone error").trim());
      throw platformError(`rclone upload failed for ${artifact.path}: ${detail}`);
    }
  }
}

function normalizeRclonePrefix(prefix: string): string {
  const value = prefix.trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
  if (value === "") return "";
  if (
    [...value].some((char) => {
      const code = char.charCodeAt(0);
      return code <= 0x1f || code === 0x7f;
    }) || value.split("/").some((part) => part === ".." || part === ".")
  ) {
    throw validationError("rclone prefix must be a relative path without . or .. segments");
  }
  return value.replace(/\/{2,}/g, "/");
}

function backupContainerPath(platform: Platform, artifactPath: string): string {
  const rel = relative(resolve(platform.paths.paths.backupsDir), resolve(artifactPath));
  if (!rel || rel === ".." || rel.startsWith("../") || rel.startsWith("..\\")) {
    throw stateError(`backup artifact is outside the stack backups directory: ${artifactPath}`);
  }
  return `/backups/${rel.replaceAll("\\", "/")}`;
}

function rcloneDestination(target: RcloneBackupTarget, containerPath: string): string {
  const rel = containerPath.slice("/backups/".length);
  return `${target.remote}:${target.prefix ? `${target.prefix}/` : ""}${rel}`;
}
