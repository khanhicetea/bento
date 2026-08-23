import {
  addCronJobInputSchema,
  addWorkerInputSchema,
  type AddCronJobInput,
  type AddWorkerInput,
} from "@bento/shared";
import { load } from "js-yaml";

const cronFields = new Set([
  "schedule",
  "command",
  "timezone",
  "mode",
  "commandMode",
  "output",
  "timeout",
  "timeoutSec",
]);
const workerFields = new Set(["command", "autorestart", "stopsignal", "stopwaitsecs"]);

export function parseCronJobsYaml(source: string, app: string): AddCronJobInput[] {
  const entries = parseNamedEntries(source, "cron job", cronFields);
  return entries.map(([name, value]) => {
    const mode = value.mode ?? value.commandMode ?? "argv";
    const timeout = value.timeout ?? value.timeoutSec;
    return parseInput(
      addCronJobInputSchema,
      {
        app,
        name,
        schedule: value.schedule,
        timezone: value.timezone ?? "UTC",
        command: normalizeCommand(value.command, mode, name),
        commandMode: mode,
        output: value.output ?? "log",
        ...(timeout === undefined || timeout === null ? {} : { timeoutSec: timeout }),
      },
      name,
    );
  });
}

export function parseWorkersYaml(source: string, app: string): AddWorkerInput[] {
  const entries = parseNamedEntries(source, "worker", workerFields);
  return entries.map(([name, value]) =>
    parseInput(
      addWorkerInputSchema,
      {
        app,
        name,
        command: value.command,
        autorestart: value.autorestart ?? true,
        stopsignal: value.stopsignal ?? "TERM",
        stopwaitsecs: value.stopwaitsecs ?? 10,
      },
      name,
    ),
  );
}

function parseNamedEntries(
  source: string,
  kind: string,
  allowedFields: Set<string>,
): Array<[string, Record<string, unknown>]> {
  if (!source.trim()) throw new Error("Enter YAML to import.");

  let document: unknown;
  try {
    document = load(source);
  } catch (error) {
    throw new Error(`Invalid YAML: ${messageOf(error)}`);
  }
  if (!isRecord(document) || Object.keys(document).length === 0) {
    throw new Error(`YAML must be a mapping with each ${kind} name as a key.`);
  }

  return Object.entries(document).map(([name, value]) => {
    if (!isRecord(value)) throw new Error(`${name} must contain a mapping of fields.`);
    const unknownField = Object.keys(value).find((field) => !allowedFields.has(field));
    if (unknownField) throw new Error(`${name}.${unknownField} is not a supported field.`);
    return [name, value];
  });
}

function normalizeCommand(command: unknown, mode: unknown, name: string): unknown {
  if (mode === "shell" && typeof command === "string") return [command];
  if (mode === "argv" && !Array.isArray(command)) {
    throw new Error(`${name}.command must be a YAML list when mode is argv.`);
  }
  return command;
}

function parseInput<T>(
  schema: {
    safeParse: (
      value: unknown,
    ) =>
      | { success: true; data: T }
      | { success: false; error: { issues: Array<{ path: PropertyKey[]; message: string }> } };
  },
  value: unknown,
  name: string,
): T {
  const result = schema.safeParse(value);
  if (result.success) return result.data;
  const issue = result.error.issues[0];
  const path = issue?.path.filter((part) => part !== "app" && part !== "name").join(".");
  throw new Error(`${name}${path ? `.${path}` : ""}: ${issue?.message ?? "invalid value"}`);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
