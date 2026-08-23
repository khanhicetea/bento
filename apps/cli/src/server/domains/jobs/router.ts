import { basename } from "node:path";
import { implement } from "@orpc/server";
import { jobsContract, type JobsOverview } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(jobsContract);

export function createJobsRouter(ctx: CliContext) {
  return os.router({ overview: os.overview.handler(async () => await jobsOverview(ctx)) });
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
          command: commandSummary(job.command, job.commandMode),
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
          command: commandSummary(worker.command, "argv"),
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
          command: commandSummary(app.deploy.argv, "argv"),
        })),
    };
  } catch (error) {
    return empty(ctx.stackRoot, redact(error instanceof Error ? error.message : String(error)));
  }
}

function commandSummary(command: string[], mode: "argv" | "shell"): string {
  if (mode === "shell") return "Shell command (content hidden)";
  const executable = command[0] ? basename(command[0]) : "command";
  const argumentCount = Math.max(0, command.length - 1);
  return `${executable}${argumentCount ? ` (+${argumentCount} args)` : ""}`;
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
