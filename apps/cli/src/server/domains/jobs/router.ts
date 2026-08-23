import { basename, join } from "node:path";
import { implement, ORPCError } from "@orpc/server";
import { jobsContract, type JobsOverview } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { isBentoError, notFoundError, type BentoError } from "../../../domain/errors.ts";
import type { ReloadPlan } from "../../../domain/reload.ts";
import type { DesiredState } from "../../../domain/state.ts";
import { addCronJob, removeCronJob } from "../../../services/cron.ts";
import { addWorker, removeWorker } from "../../../services/worker.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(jobsContract);

export function createJobsRouter(ctx: CliContext) {
  return os.router({
    overview: os.overview.handler(async () => await jobsOverview(ctx)),
    logs: os.logs.handler(async ({ input }) => {
      try {
        const state = await ctx.store.load();
        if (!state.apps[input.app]) throw notFoundError(`app not found: ${input.app}`);

        const job =
          input.kind === "cron"
            ? state.cronJobs.find(
                (candidate) => candidate.app === input.app && candidate.name === input.name,
              )
            : state.workers.find(
                (candidate) => candidate.app === input.app && candidate.name === input.name,
              );
        if (!job) {
          throw notFoundError(`${input.kind} job ${input.name} not found for app ${input.app}`);
        }

        const jobName = String(job.name);
        if (!/^[a-zA-Z0-9_-]+$/.test(jobName)) {
          throw notFoundError(`logs unavailable for ${input.kind} job ${input.name}`);
        }
        const appHome = ctx.platform.paths.appHome(input.app);
        const logFiles =
          input.kind === "cron"
            ? [{ label: "output", path: join(appHome, "logs", "cron", `${jobName}.log`) }]
            : [
                { label: "output", path: join(appHome, "logs", "worker", `${jobName}.log`) },
                { label: "error", path: join(appHome, "logs", "worker", `${jobName}.err`) },
              ];
        const allLines: string[] = [];
        for (const logFile of logFiles) {
          const content = await readJobLog(ctx, logFile.path);
          if (content === null) continue;
          const lines = logLines(content);
          if (input.kind === "worker" && lines.length > 0) {
            allLines.push(`[${logFile.label}]`, ...lines);
          } else {
            allLines.push(...lines);
          }
        }
        return {
          lines: allLines.slice(-1000),
          truncated: allLines.length > 1000,
        };
      } catch (error) {
        throw asORPCError(error);
      }
    }),
    addCron: os.addCron.handler(async ({ input }) => {
      try {
        await ctx.store.withExclusive(async (state) => {
          const result = addCronJob(state, input, ctx.platform);
          await saveAndApply(ctx, result.state, result.reloadPlan);
        });
        return await jobsOverview(ctx);
      } catch (error) {
        throw asORPCError(error);
      }
    }),
    removeCron: os.removeCron.handler(async ({ input }) => {
      try {
        await ctx.store.withExclusive(async (state) => {
          const result = removeCronJob(state, input.app, input.name, ctx.platform.clock.nowIso());
          await saveAndApply(ctx, result.state, result.reloadPlan);
        });
        return await jobsOverview(ctx);
      } catch (error) {
        throw asORPCError(error);
      }
    }),
    addWorker: os.addWorker.handler(async ({ input }) => {
      try {
        await ctx.store.withExclusive(async (state) => {
          const result = addWorker(state, input, ctx.platform);
          await saveAndApply(ctx, result.state, result.reloadPlan);
        });
        return await jobsOverview(ctx);
      } catch (error) {
        throw asORPCError(error);
      }
    }),
    removeWorker: os.removeWorker.handler(async ({ input }) => {
      try {
        await ctx.store.withExclusive(async (state) => {
          const result = removeWorker(state, input.app, input.name, ctx.platform.clock.nowIso());
          await saveAndApply(ctx, result.state, result.reloadPlan);
        });
        return await jobsOverview(ctx);
      } catch (error) {
        throw asORPCError(error);
      }
    }),
  });
}

async function jobsOverview(ctx: CliContext): Promise<JobsOverview> {
  if (!(await ctx.store.exists())) return empty(ctx.stackRoot);
  try {
    const state = await ctx.store.load();
    return {
      initialized: true,
      stackRoot: ctx.stackRoot,
      cronJobs: [...state.cronJobs]
        .sort((a, b) => `${a.app}:${a.name}`.localeCompare(`${b.app}:${b.name}`))
        .map((job) => ({
          name: String(job.name),
          app: String(job.app),
          schedule: job.schedule,
          timezone: job.timezone,
          command: commandDisplay(job.command, job.commandMode),
          commandMode: job.commandMode,
          output: job.output,
          enabled: job.enabled,
          ...(job.timeoutSec !== undefined ? { timeoutSec: job.timeoutSec } : {}),
        })),
      workers: [...state.workers]
        .sort((a, b) => `${a.app}:${a.name}`.localeCompare(`${b.app}:${b.name}`))
        .map((worker) => ({
          name: String(worker.name),
          app: String(worker.app),
          command: commandDisplay(worker.command, "argv"),
          enabled: worker.enabled,
          autorestart: worker.autorestart,
          stopsignal: worker.stopsignal,
          stopwaitsecs: worker.stopwaitsecs,
        })),
      deploys: Object.values(state.apps)
        .filter((app) => app.deploy.enabled)
        .sort((a, b) => a.slug.localeCompare(b.slug))
        .map((app) => ({
          app: String(app.slug),
          enabled: app.deploy.enabled,
          queuePolicy: app.deploy.queuePolicy,
          timeoutSec: app.deploy.timeoutSec,
          command: commandSummary(app.deploy.argv),
        })),
    };
  } catch (error) {
    return empty(ctx.stackRoot, redact(error instanceof Error ? error.message : String(error)));
  }
}

async function readJobLog(ctx: CliContext, path: string): Promise<string | null> {
  const stat = await ctx.platform.fs.lstat(path).catch(() => null);
  if (!stat?.isFile || stat.isSymlink) return null;
  return await ctx.platform.fs.readText(path).catch(() => null);
}

function logLines(content: string): string[] {
  return redact(content)
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => line.slice(0, 2_000));
}

function commandSummary(command: string[]): string {
  const executable = command[0] ? basename(command[0]) : "command";
  const argumentCount = Math.max(0, command.length - 1);
  return `${executable}${argumentCount ? ` (+${argumentCount} args)` : ""}`;
}

export function commandDisplay(command: string[], mode: "argv" | "shell"): string {
  if (mode === "shell") return redactCommand(command[0] ?? "");
  let redactNext = false;
  return command
    .map((argument) => {
      if (redactNext) {
        redactNext = false;
        return "***";
      }
      if (
        /^--?(?:password|passwd|secret|token|api[-_]?key|private[-_]?key|credential)$/i.test(
          argument,
        )
      ) {
        redactNext = true;
        return argument;
      }
      return redactCommand(argument);
    })
    .map(quoteArgument)
    .join(" ");
}

function quoteArgument(argument: string): string {
  if (argument === "***") return argument;
  return /^[A-Za-z0-9_./:@%+=,-]+$/.test(argument)
    ? argument
    : `'${argument.replaceAll("'", `'\\''`)}'`;
}

function redactCommand(command: string): string {
  return redact(command)
    .replace(
      /((?:--?|\/)(?:password|passwd|secret|token|api[-_]?key|private[-_]?key|credential)(?:=|\s+))("[^"]*"|'[^']*'|[^\s]+)/gi,
      "$1***",
    )
    .replace(
      /((?:PASSWORD|PASSWD|SECRET|TOKEN|API_KEY|PRIVATE_KEY|CREDENTIAL)=)([^\s]+)/gi,
      "$1***",
    );
}

async function saveAndApply(ctx: CliContext, state: DesiredState, reloadPlan: ReloadPlan) {
  await ctx.store.save(state);
  await ctx.render.apply(state, {
    reloadPlan,
    skipValidate: false,
    alreadyLocked: true,
  });
}

function asORPCError(error: unknown): ORPCError<string, unknown> {
  if (!isBentoError(error)) {
    return new ORPCError("INTERNAL_SERVER_ERROR", { message: "Job operation failed" });
  }
  const code =
    error.code === "NOT_FOUND"
      ? "NOT_FOUND"
      : error.code === "CONFLICT"
        ? "CONFLICT"
        : error.code === "VALIDATION" || error.code === "SAFETY"
          ? "BAD_REQUEST"
          : "INTERNAL_SERVER_ERROR";
  return new ORPCError(code, {
    message: code === "INTERNAL_SERVER_ERROR" ? "Job operation failed" : errorMessage(error),
  });
}

function errorMessage(error: BentoError): string {
  return error.recovery ? `${error.message} ${error.recovery}` : error.message;
}

function empty(stackRoot: string, error?: string): JobsOverview {
  return {
    initialized: false,
    stackRoot,
    cronJobs: [],
    workers: [],
    deploys: [],
    ...(error ? { error } : {}),
  };
}
