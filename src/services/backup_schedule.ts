/** Safe host-crontab management and scheduled logical-backup result persistence. */

import { basename, isAbsolute, join } from "@std/path";
import { z } from "zod";
import type { DesiredState } from "../domain/state.ts";
import { isBentoError, platformError, stateError, validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import { parseCronSchedule } from "../schemas/validators.ts";
import { redact } from "../ui/output.ts";
import { type DatabaseBackupArtifact, runDatabaseBackup } from "./database_backup.ts";

const MARKER_PREFIX = "BENTO BACKUP SCHEDULE";
const RESULT_VERSION = 1;
const RESULT_MAX_BYTES = 64 * 1024;
const ERROR_MAX_BYTES = 2 * 1024;
const PATH_MAX_BYTES = 4096;

export type BackupScheduleArtifactSummary = {
  engine: "mysql" | "postgres" | "sqlite";
  service: string;
  database: string;
  basename: string;
  bytes: number;
};

export type BackupScheduleLastRun = {
  version: 1;
  status: "running" | "succeeded" | "failed";
  startedAt: string;
  finishedAt: string | null;
  exitCode: number | null;
  artifactCount: number;
  artifactBytes: number;
  artifacts: BackupScheduleArtifactSummary[];
  omittedCount: number;
  error?: string;
};

export type BackupScheduleStatus = {
  installed: boolean;
  schedule: string | null;
  lastRun: BackupScheduleLastRun | null;
};

export type CrontabMergeResult = {
  crontab: string;
  changed: boolean;
  action: "installed" | "removed" | "unchanged";
};

type ParsedBlock = {
  root: string;
  start: number;
  end: number;
  lines: string[];
};

const artifactSchema = z.object({
  engine: z.enum(["mysql", "postgres", "sqlite"]),
  service: z.string().max(128),
  database: z.string().max(128),
  basename: z.string().max(255),
  bytes: z.number().int().nonnegative(),
}).strict();

const lastRunSchema = z.object({
  version: z.literal(RESULT_VERSION),
  status: z.enum(["running", "succeeded", "failed"]),
  startedAt: z.string().datetime(),
  finishedAt: z.string().datetime().nullable(),
  exitCode: z.number().int().min(0).max(255).nullable(),
  artifactCount: z.number().int().nonnegative(),
  artifactBytes: z.number().int().nonnegative(),
  artifacts: z.array(artifactSchema).max(100),
  omittedCount: z.number().int().nonnegative(),
  error: z.string().max(ERROR_MAX_BYTES).optional(),
}).strict().superRefine((record, ctx) => {
  if (record.status === "running" && (record.finishedAt !== null || record.exitCode !== null)) {
    ctx.addIssue({ code: z.ZodIssueCode.custom, message: "running result is terminal" });
  }
  if (record.status !== "running" && (record.finishedAt === null || record.exitCode === null)) {
    ctx.addIssue({ code: z.ZodIssueCode.custom, message: "terminal result is incomplete" });
  }
});

export function backupScheduleMarkers(stackRoot: string): { begin: string; end: string } {
  validateManagedPath(stackRoot, "stack root");
  return {
    begin: `# BEGIN ${MARKER_PREFIX} ${stackRoot}`,
    end: `# END ${MARKER_PREFIX} ${stackRoot}`,
  };
}

/** POSIX-shell single quoting. Cron invokes this line through its shell. */
export function shellSingleQuote(value: string): string {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}

export function backupScheduleCronFragment(opts: {
  schedule: string;
  bentoBin: string;
  stackRoot: string;
}): string {
  const schedule = requireCronSchedule(opts.schedule);
  validateManagedPath(opts.bentoBin, "Bento executable");
  validateManagedPath(opts.stackRoot, "stack root");
  const markers = backupScheduleMarkers(opts.stackRoot);
  const command = `${schedule} ${shellSingleQuote(opts.bentoBin)} --stack ${
    shellSingleQuote(opts.stackRoot)
  } backup schedule run >/dev/null 2>&1`;
  return `${markers.begin}\n${command}\n${markers.end}\n`;
}

/** Merge only one stack-qualified managed block, preserving every other byte. */
export function mergeBackupScheduleCrontab(
  existing: string,
  opts:
    | { action: "install"; stackRoot: string; fragment: string }
    | { action: "remove"; stackRoot: string },
): CrontabMergeResult {
  validateManagedPath(opts.stackRoot, "stack root");
  const blocks = parseManagedBlocks(existing);
  const target = blocks.find((block) => block.root === opts.stackRoot);

  if (opts.action === "remove") {
    if (!target) return { crontab: existing, changed: false, action: "unchanged" };
    return {
      crontab: existing.slice(0, target.start) + existing.slice(target.end),
      changed: true,
      action: "removed",
    };
  }

  // Validate the supplied fragment as exactly one block for this root.
  const fragmentBlocks = parseManagedBlocks(opts.fragment);
  if (
    fragmentBlocks.length !== 1 || fragmentBlocks[0]!.root !== opts.stackRoot ||
    fragmentBlocks[0]!.start !== 0 || fragmentBlocks[0]!.end !== opts.fragment.length
  ) {
    throw validationError("backup schedule fragment is malformed");
  }
  const next = target
    ? existing.slice(0, target.start) + opts.fragment + existing.slice(target.end)
    : `${existing}${existing !== "" && !existing.endsWith("\n") ? "\n" : ""}${opts.fragment}`;
  return {
    crontab: next,
    changed: next !== existing,
    action: next === existing ? "unchanged" : "installed",
  };
}

/** Read crontab, recognizing only the explicit platform no-crontab diagnostic. */
export async function readBackupHostCrontab(platform: Platform): Promise<string> {
  const result = await platform.process.run(["crontab", "-l"], { timeoutMs: 5_000 });
  if (result.code === 0) return result.stdout;
  if (
    result.code === 1 && result.stdout === "" &&
    /^no crontab for [^\r\n]+\s*$/i.test(result.stderr)
  ) {
    return "";
  }
  throw platformError(
    `failed to read host crontab (exit ${result.code}): ${
      boundedUtf8(redact(result.stderr || result.stdout || "unknown error"), 512)
    }`,
  );
}

export async function registerBackupSchedule(
  platform: Platform,
  opts: { schedule: string; bentoBin: string },
): Promise<CrontabMergeResult> {
  const schedule = requireCronSchedule(opts.schedule);
  await validateExecutable(platform, opts.bentoBin);
  const stackRoot = platform.paths.paths.root;
  const fragment = backupScheduleCronFragment({ schedule, bentoBin: opts.bentoBin, stackRoot });
  return await mutateHostCrontab(platform, { action: "install", stackRoot, fragment });
}

export async function unregisterBackupSchedule(
  platform: Platform,
): Promise<CrontabMergeResult> {
  return await mutateHostCrontab(platform, {
    action: "remove",
    stackRoot: platform.paths.paths.root,
  });
}

export async function getBackupScheduleStatus(
  platform: Platform,
): Promise<BackupScheduleStatus> {
  const crontab = await readBackupHostCrontab(platform);
  const blocks = parseManagedBlocks(crontab);
  const target = blocks.find((block) => block.root === platform.paths.paths.root);
  return {
    installed: target !== undefined,
    schedule: target ? scheduleFromBlock(target) : null,
    lastRun: await readBackupScheduleLastRun(platform),
  };
}

export function backupScheduleLastRunPath(platform: Platform): string {
  return join(platform.paths.paths.backupsDir, ".schedule", "last-run.json");
}

export async function readBackupScheduleLastRun(
  platform: Platform,
): Promise<BackupScheduleLastRun | null> {
  const path = backupScheduleLastRunPath(platform);
  if (!(await platform.fs.exists(path))) return null;
  const info = await platform.fs.lstat(path);
  if (info.isSymlink || !info.isFile) {
    throw stateError("backup schedule last-run record must be a regular non-symlink file");
  }
  if (info.size > RESULT_MAX_BYTES) {
    throw stateError("backup schedule last-run record exceeds 64 KiB");
  }
  let raw: unknown;
  try {
    raw = JSON.parse(await platform.fs.readText(path));
  } catch (cause) {
    throw stateError(`backup schedule last-run record is malformed: ${String(cause)}`);
  }
  const parsed = lastRunSchema.safeParse(raw);
  if (!parsed.success) {
    throw stateError(
      `backup schedule last-run record is invalid: ${
        parsed.error.issues.map((i) => i.message).join("; ")
      }`,
    );
  }
  return parsed.data;
}

export async function runScheduledBackup(
  platform: Platform,
  state: DesiredState,
): Promise<DatabaseBackupArtifact[]> {
  const startedAt = platform.clock.nowIso();
  await writeLastRun(platform, {
    version: RESULT_VERSION,
    status: "running",
    startedAt,
    finishedAt: null,
    exitCode: null,
    artifactCount: 0,
    artifactBytes: 0,
    artifacts: [],
    omittedCount: 0,
  });
  try {
    const artifacts = await runDatabaseBackup(platform, state, { scope: "all" });
    await writeLastRun(platform, terminalRecord(startedAt, platform.clock.nowIso(), artifacts));
    return artifacts;
  } catch (cause) {
    const exitCode = isBentoError(cause) ? cause.exitCode : 1;
    const message = boundedUtf8(
      redact(cause instanceof Error ? cause.message : String(cause)),
      ERROR_MAX_BYTES,
    );
    await writeLastRun(platform, {
      version: RESULT_VERSION,
      status: "failed",
      startedAt,
      finishedAt: platform.clock.nowIso(),
      exitCode,
      artifactCount: 0,
      artifactBytes: 0,
      artifacts: [],
      omittedCount: 0,
      error: message,
    });
    throw cause;
  }
}

async function mutateHostCrontab(
  platform: Platform,
  opts:
    | { action: "install"; stackRoot: string; fragment: string }
    | { action: "remove"; stackRoot: string },
): Promise<CrontabMergeResult> {
  // Crontab belongs to the host user, not to one stack. Use a user-scoped lock so
  // concurrent registrations from different stack roots cannot overwrite each other.
  const release = await platform.lock.exclusive(hostCrontabLockPath());
  try {
    const existing = await readBackupHostCrontab(platform);
    const merged = mergeBackupScheduleCrontab(existing, opts);
    if (!merged.changed) return merged;
    const result = await platform.process.run(["crontab", "-"], {
      stdin: merged.crontab,
      timeoutMs: 5_000,
    });
    if (result.code !== 0) {
      throw platformError(
        `failed to install host crontab (exit ${result.code}): ${
          boundedUtf8(redact(result.stderr || result.stdout || "unknown error"), 512)
        }`,
      );
    }
    return merged;
  } finally {
    await release();
  }
}

function hostCrontabLockPath(): string {
  const base = Deno.env.get("XDG_RUNTIME_DIR") ?? Deno.env.get("HOME") ?? "/tmp";
  return join(base, ".bento", "locks", "host-crontab.lock");
}

async function validateExecutable(platform: Platform, path: string): Promise<void> {
  validateManagedPath(path, "Bento executable");
  if (!(await platform.fs.exists(path))) {
    throw validationError("Bento executable does not exist");
  }
  const info = await platform.fs.lstat(path);
  if (info.isSymlink || !info.isFile) {
    throw validationError("Bento executable must be a regular non-symlink file");
  }
  if ((info.mode & 0o111) === 0) {
    throw validationError("Bento executable is not executable");
  }
}

function validateManagedPath(path: string, label: string): void {
  if (!isAbsolute(path)) throw validationError(`${label} must be absolute`);
  if (
    path.includes("%") ||
    [...path].some((char) => {
      const code = char.charCodeAt(0);
      return code <= 0x1f || code === 0x7f;
    })
  ) {
    throw validationError(`${label} contains a control character or cron-special %`);
  }
  if (new TextEncoder().encode(path).length > PATH_MAX_BYTES) {
    throw validationError(`${label} is too long`);
  }
}

function requireCronSchedule(schedule: string): string {
  const parsed = parseCronSchedule(schedule);
  if (!parsed.ok) throw validationError(parsed.errors.join("; "));
  return parsed.value;
}

function parseManagedBlocks(crontab: string): ParsedBlock[] {
  const lines: Array<{ text: string; start: number; end: number }> = [];
  let offset = 0;
  while (offset < crontab.length) {
    const newline = crontab.indexOf("\n", offset);
    const end = newline < 0 ? crontab.length : newline + 1;
    lines.push({
      text: crontab.slice(offset, newline < 0 ? crontab.length : newline),
      start: offset,
      end,
    });
    offset = end;
  }

  const blocks: ParsedBlock[] = [];
  let open: { root: string; line: number } | undefined;
  const seen = new Set<string>();
  const beginPrefix = `# BEGIN ${MARKER_PREFIX} `;
  const endPrefix = `# END ${MARKER_PREFIX} `;
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]!;
    const isBegin = line.text.startsWith(beginPrefix);
    const isEnd = line.text.startsWith(endPrefix);
    if (!isBegin && !isEnd) {
      if (
        line.text.startsWith(`# BEGIN ${MARKER_PREFIX}`) ||
        line.text.startsWith(`# END ${MARKER_PREFIX}`)
      ) {
        throw validationError("malformed Bento backup schedule marker");
      }
      continue;
    }
    const root = line.text.slice((isBegin ? beginPrefix : endPrefix).length);
    if (!root || root.includes("\r")) {
      throw validationError("malformed Bento backup schedule marker");
    }
    validateManagedPath(root, "backup schedule marker stack root");
    if (isBegin) {
      if (open || seen.has(root)) {
        throw validationError("duplicate or nested Bento backup schedule marker");
      }
      open = { root, line: index };
      continue;
    }
    if (!open || open.root !== root) {
      throw validationError("reversed or unpaired Bento backup schedule marker");
    }
    const first = lines[open.line]!;
    blocks.push({
      root,
      start: first.start,
      end: line.end,
      lines: lines.slice(open.line, index + 1).map((entry) => entry.text),
    });
    seen.add(root);
    open = undefined;
  }
  if (open) throw validationError("unpaired Bento backup schedule marker");
  return blocks;
}

function scheduleFromBlock(block: ParsedBlock): string {
  if (block.lines.length !== 3) {
    throw stateError("installed backup schedule block is malformed");
  }
  const fields = block.lines[1]!.trimStart().split(/[\t ]+/, 6);
  if (fields.length < 6) throw stateError("installed backup schedule command is malformed");
  return requireCronSchedule(fields.slice(0, 5).join(" "));
}

function terminalRecord(
  startedAt: string,
  finishedAt: string,
  source: DatabaseBackupArtifact[],
): BackupScheduleLastRun {
  const summaries = source.slice(0, 100).map((artifact) => ({
    engine: artifact.engine,
    service: boundedUtf8(redact(artifact.service), 128),
    database: boundedUtf8(redact(artifact.database), 128),
    basename: boundedUtf8(redact(basename(artifact.path)), 255),
    bytes: artifact.bytes,
  }));
  return {
    version: RESULT_VERSION,
    status: "succeeded",
    startedAt,
    finishedAt,
    exitCode: 0,
    artifactCount: source.length,
    artifactBytes: source.reduce((total, artifact) => total + artifact.bytes, 0),
    artifacts: summaries,
    omittedCount: source.length - summaries.length,
  };
}

async function writeLastRun(
  platform: Platform,
  input: BackupScheduleLastRun,
): Promise<void> {
  const dir = join(platform.paths.paths.backupsDir, ".schedule");
  if (await platform.fs.exists(dir)) {
    const info = await platform.fs.lstat(dir);
    if (info.isSymlink || !info.isDirectory) {
      throw stateError("backup schedule result directory must be a non-symlink directory");
    }
  } else {
    await platform.fs.mkdirp(dir, 0o700);
  }
  await platform.fs.chmod(dir, 0o700);

  const record = structuredClone(input);
  let serialized = `${JSON.stringify(record, null, 2)}\n`;
  while (
    new TextEncoder().encode(serialized).length > RESULT_MAX_BYTES && record.artifacts.length
  ) {
    record.artifacts.pop();
    record.omittedCount++;
    serialized = `${JSON.stringify(record, null, 2)}\n`;
  }
  if (new TextEncoder().encode(serialized).length > RESULT_MAX_BYTES) {
    throw stateError("backup schedule result exceeds 64 KiB bound");
  }
  await platform.fs.atomicWriteText(backupScheduleLastRunPath(platform), serialized, 0o600);
}

function boundedUtf8(value: string, maxBytes: number): string {
  const encoder = new TextEncoder();
  if (encoder.encode(value).length <= maxBytes) return value;
  let low = 0;
  let high = value.length;
  while (low < high) {
    const middle = Math.ceil((low + high) / 2);
    if (encoder.encode(value.slice(0, middle)).length <= maxBytes - 3) low = middle;
    else high = middle - 1;
  }
  return `${value.slice(0, low)}...`;
}
