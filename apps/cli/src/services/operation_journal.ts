/** Private, bounded evidence for long-running stack operations; not a work queue. */
import { join } from "node:path";
import { z } from "zod";
import { stateError, validationError } from "#/domain/errors.ts";
import type { Platform } from "#/platform/mod.ts";
import { redact } from "#/ui/output.ts";

const MAX_BYTES = 8192;
const KEEP_RECORDS = 20;
const idSchema = z.string().regex(/^op_[a-f0-9]{16}$/);
const stepSchema = z.object({
  name: z.enum(["backup", "upload"]),
  status: z.enum(["running", "succeeded", "failed", "interrupted"]),
  startedAt: z.string().datetime(),
  finishedAt: z.string().datetime().nullable(),
  error: z.string().max(1024).optional(),
});
const recordSchema = z.object({
  version: z.literal(1),
  id: idSchema,
  kind: z.enum(["scheduled-backup", "web-backup"]),
  status: z.enum(["running", "succeeded", "failed", "interrupted"]),
  progress: z.object({ completed: z.number().int().nonnegative(), total: z.number().int().nonnegative() }).optional(),
  startedAt: z.string().datetime(),
  finishedAt: z.string().datetime().nullable(),
  steps: z.array(stepSchema).max(2),
});

export type OperationRecord = z.infer<typeof recordSchema>;
export type OperationStep = OperationRecord["steps"][number]["name"];

function directory(platform: Platform): string {
  return join(platform.paths.paths.backupsDir, ".schedule", "operations");
}

export function operationRecordPath(platform: Platform, id: string): string {
  if (!idSchema.safeParse(id).success) throw validationError("invalid operation ID");
  return join(directory(platform), `${id}.json`);
}

export async function readOperationRecord(platform: Platform, id: string): Promise<OperationRecord> {
  await assertJournalDirectories(platform);
  const path = operationRecordPath(platform, id);
  const info = await platform.fs.lstat(path);
  if (info.isSymlink || !info.isFile || info.size > MAX_BYTES) throw stateError("invalid operation record file");
  let raw: unknown;
  try {
    raw = JSON.parse(await platform.fs.readText(path));
  } catch {
    throw stateError("malformed operation record");
  }
  const parsed = recordSchema.safeParse(raw);
  if (!parsed.success || parsed.data.id !== id) throw stateError("invalid operation record");
  return parsed.data;
}

async function assertJournalDirectories(platform: Platform): Promise<void> {
  for (const dir of [join(platform.paths.paths.backupsDir, ".schedule"), directory(platform)]) {
    const info = await platform.fs.lstat(dir);
    if (info.isSymlink || !info.isDirectory) throw stateError("operation directory must be a non-symlink directory");
  }
}

async function save(platform: Platform, record: OperationRecord): Promise<void> {
  const parent = join(platform.paths.paths.backupsDir, ".schedule");
  if (await platform.fs.exists(parent)) {
    const info = await platform.fs.lstat(parent);
    if (info.isSymlink || !info.isDirectory)
      throw stateError("backup schedule directory must be a non-symlink directory");
  } else {
    await platform.fs.mkdirp(parent, 0o700);
  }
  await platform.fs.chmod(parent, 0o700);
  const dir = directory(platform);
  if (await platform.fs.exists(dir)) {
    const info = await platform.fs.lstat(dir);
    if (info.isSymlink || !info.isDirectory) throw stateError("operation directory must be a non-symlink directory");
  } else {
    await platform.fs.mkdirp(dir, 0o700);
  }
  await platform.fs.chmod(dir, 0o700);
  const serialized = `${JSON.stringify(record, null, 2)}\n`;
  if (!recordSchema.safeParse(record).success || new TextEncoder().encode(serialized).length > MAX_BYTES) {
    throw stateError("invalid or oversized operation record");
  }
  await platform.fs.atomicWriteText(operationRecordPath(platform, record.id), serialized, 0o600);
}

/** Caller holds the relevant operation lock before creating a record. */
export async function startBackupOperation(
  platform: Platform,
  kind: OperationRecord["kind"],
): Promise<OperationRecord> {
  const record: OperationRecord = {
    version: 1,
    id: platform.random.id("op"),
    kind,
    status: "running",
    startedAt: platform.clock.nowIso(),
    finishedAt: null,
    steps: [],
  };
  if (await platform.fs.exists(operationRecordPath(platform, record.id))) throw stateError("operation ID collision");
  await save(platform, record);
  await prune(platform, record.id);
  return record;
}

export async function setOperationProgress(
  platform: Platform,
  record: OperationRecord,
  completed: number,
  total: number,
): Promise<void> {
  if (record.status !== "running" || completed < 0 || completed > total) throw stateError("invalid backup progress");
  record.progress = { completed, total };
  await save(platform, record);
}

export async function listBackupOperations(platform: Platform): Promise<OperationRecord[]> {
  const dir = directory(platform);
  if (!(await platform.fs.exists(dir))) return [];
  await assertJournalDirectories(platform);
  const names = (await platform.fs.readDir(dir)).filter((name) => /^op_[a-f0-9]{16}\.json$/.test(name));
  if (names.length > 100) throw stateError("too many operation records");
  const records = await Promise.all(names.map((name) => readOperationRecord(platform, name.slice(0, -5))));
  return records.sort((a, b) => b.startedAt.localeCompare(a.startedAt) || b.id.localeCompare(a.id));
}

export async function startOperationStep(
  platform: Platform,
  record: OperationRecord,
  name: OperationStep,
): Promise<void> {
  if (record.status !== "running" || record.steps.some((step) => step.name === name)) {
    throw stateError("operation step already started or operation is terminal");
  }
  record.steps.push({ name, status: "running", startedAt: platform.clock.nowIso(), finishedAt: null });
  await save(platform, record);
}

export async function finishOperationStep(platform: Platform, record: OperationRecord, error?: unknown): Promise<void> {
  const step = record.steps.at(-1);
  if (!step || step.status !== "running") throw stateError("no running operation step");
  step.status = error === undefined ? "succeeded" : "failed";
  step.finishedAt = platform.clock.nowIso();
  if (error !== undefined) step.error = safeError(error);
  await save(platform, record);
}

export async function finishOperation(platform: Platform, record: OperationRecord, error?: unknown): Promise<void> {
  if (record.status !== "running") throw stateError("operation already finished");
  if (error !== undefined && record.steps.at(-1)?.status === "running") {
    await finishOperationStep(platform, record, error);
  }
  record.status = error === undefined ? "succeeded" : "failed";
  record.finishedAt = platform.clock.nowIso();
  await save(platform, record);
}

/** Reconcile only after the lock proves the former runner is no longer active. Never replay steps. */
export async function interruptOperation(platform: Platform, record: OperationRecord): Promise<void> {
  if (record.status !== "running") return;
  const step = record.steps.at(-1);
  if (step?.status === "running") {
    step.status = "interrupted";
    step.finishedAt = platform.clock.nowIso();
  }
  record.status = "interrupted";
  record.finishedAt = platform.clock.nowIso();
  await save(platform, record);
}

function safeError(error: unknown): string {
  const message = redact(error instanceof Error ? error.message : String(error));
  return [...message].slice(0, 1024).join("");
}

async function prune(platform: Platform, activeId: string): Promise<void> {
  const dir = directory(platform);
  const names = (await platform.fs.readDir(dir)).filter((name) => /^op_[a-f0-9]{16}\.json$/.test(name));
  // IDs are random, not chronological. Sort by modification time and never prune the active record.
  // The schedule status references its latest operation ID; never prune that record.
  let scheduledId: string | undefined;
  const statusPath = join(platform.paths.paths.backupsDir, ".schedule", "last-run.json");
  if (await platform.fs.exists(statusPath)) {
    const info = await platform.fs.lstat(statusPath);
    if (info.isSymlink || !info.isFile || info.size > 64 * 1024)
      throw stateError("invalid backup schedule result file");
    const last: unknown = JSON.parse(await platform.fs.readText(statusPath));
    if (last && typeof last === "object" && "operationId" in last && idSchema.safeParse(last.operationId).success) {
      scheduledId = last.operationId as string;
    }
  }
  const files = await Promise.all(
    names
      .filter((name) => name !== `${activeId}.json` && name !== `${scheduledId}.json`)
      .map(async (name) => ({ name, info: await platform.fs.lstat(join(dir, name)) })),
  );
  const old = await Promise.all(
    files
      .filter((file) => file.info.isFile && !file.info.isSymlink)
      .map(async (file) => ({
        name: file.name,
        modifiedAt: (await platform.fs.stat(join(dir, file.name))).modifiedAt?.getTime() ?? 0,
      })),
  );
  old.sort((a, b) => a.modifiedAt - b.modifiedAt || a.name.localeCompare(b.name));
  for (const file of old.slice(0, Math.max(0, old.length - (KEEP_RECORDS - 1)))) {
    await platform.fs.remove(join(dir, file.name));
  }
}
