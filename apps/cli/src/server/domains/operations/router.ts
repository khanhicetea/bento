import { basename } from "node:path";
import { implement } from "@orpc/server";
import { operationsContract, type OperationsOverview } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { composeArgs } from "../../../services/compose.ts";
import { runDatabaseBackup } from "../../../services/database_backup.ts";
import { drainDeploy } from "../../../services/deploy.ts";
import { runDoctor } from "../../../services/doctor.ts";
import { runStackMaintenance } from "../../../services/maintenance.ts";
import { buildStatus } from "../../../services/status.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(operationsContract);

export function createOperationsRouter(ctx: CliContext) {
  return os.router({
    overview: os.overview.handler(async () => await operationsOverview(ctx)),
    stackAction: os.stackAction.handler(async ({ input }) => {
      const state = await ctx.store.load();
      const status = await buildStatus(ctx.platform, state);
      if (input.action !== "start" && input.confirmation !== status.stackName) {
        throw new Error(`confirmation must exactly match stack name: ${status.stackName}`);
      }
      const subcommand =
        input.action === "start" ? ["up", "-d"] : input.action === "stop" ? ["stop"] : ["restart"];
      const result = await ctx.platform.process.run(
        await composeArgs(ctx.platform, state, subcommand),
        {
          cwd: ctx.stackRoot,
          timeoutMs: 120_000,
        },
      );
      if (result.code !== 0) throw new Error(safeDiagnostic(result.stderr || result.stdout));
      return completed(`Stack ${input.action} completed`, ctx);
    }),
    restartService: os.restartService.handler(async ({ input }) => {
      if (input.confirmation !== input.service) {
        throw new Error("confirmation must exactly match the service name");
      }
      const state = await ctx.store.load();
      const servicesResult = await ctx.platform.process.run(
        await composeArgs(ctx.platform, state, ["config", "--services"]),
        { cwd: ctx.stackRoot, timeoutMs: 15_000 },
      );
      if (servicesResult.code !== 0) {
        throw new Error(safeDiagnostic(servicesResult.stderr || servicesResult.stdout));
      }
      const services = servicesResult.stdout.split("\n").map((service) => service.trim());
      if (!services.includes(input.service)) throw new Error(`unknown service: ${input.service}`);
      const result = await ctx.platform.process.run(
        await composeArgs(ctx.platform, state, ["restart", input.service]),
        { cwd: ctx.stackRoot, timeoutMs: 120_000 },
      );
      if (result.code !== 0) throw new Error(safeDiagnostic(result.stderr || result.stdout));
      return completed(`Service ${input.service} restarted`, ctx);
    }),
    apply: os.apply.handler(async () => {
      const state = await ctx.store.load();
      await ctx.render.apply(state);
      return completed("Configuration rendered, validated, and applied", ctx);
    }),
    backup: os.backup.handler(async () => {
      const state = await ctx.store.load();
      const artifacts = await runDatabaseBackup(ctx.platform, state, {
        scope: "all",
        compress: "zstd",
      });
      return {
        ...completed(`Created ${artifacts.length} backup artifact(s)`, ctx),
        artifacts: artifacts.map((artifact) => ({
          engine: artifact.engine,
          database: artifact.database,
          bytes: artifact.bytes,
          path: basename(artifact.path),
        })),
      };
    }),
    logs: os.logs.handler(async ({ input }) => {
      const state = await ctx.store.load();
      const logArgs = ["logs", "--no-color", "--tail", String(input.tail)];
      if (input.service) {
        const servicesResult = await ctx.platform.process.run(
          await composeArgs(ctx.platform, state, ["config", "--services"]),
          { cwd: ctx.stackRoot, timeoutMs: 15_000 },
        );
        if (servicesResult.code !== 0) {
          throw new Error(safeDiagnostic(servicesResult.stderr || servicesResult.stdout));
        }
        const services = servicesResult.stdout.split("\n").map((service) => service.trim());
        if (!services.includes(input.service)) throw new Error(`unknown service: ${input.service}`);
        logArgs.push(input.service);
      }
      const result = await ctx.platform.process.run(
        await composeArgs(ctx.platform, state, logArgs),
        { cwd: ctx.stackRoot, timeoutMs: 15_000 },
      );
      if (result.code !== 0)
        throw new Error(safeDiagnostic(result.stderr || "Unable to read logs"));
      const allLines = redact(result.stdout)
        .split("\n")
        .filter(Boolean)
        .map((line) => line.slice(0, 2_000));
      return { lines: allLines.slice(-input.tail), truncated: allLines.length > input.tail };
    }),
    doctor: os.doctor.handler(async () => {
      const state = await ctx.store.load();
      return await runDoctor(ctx.platform, state);
    }),
    maintenance: os.maintenance.handler(async ({ input }) => {
      const result = await runStackMaintenance(ctx.platform, { retainDays: input.retainDays });
      return {
        ...completed(`Maintenance removed ${result.removed.length} file(s)`, ctx),
        removed: result.removed.length,
        notes: result.notes.map(redact),
      };
    }),
    drainDeploy: os.drainDeploy.handler(async ({ input }) => {
      if (input.confirmation !== input.app) {
        throw new Error("confirmation must exactly match the application slug");
      }
      const state = await ctx.store.load();
      const app = state.apps[input.app];
      if (!app) throw new Error(`app not found: ${input.app}`);
      const job = await drainDeploy(ctx.platform, app, ctx.platform.paths.appHome(input.app));
      return completed(
        job ? `Deploy ${job.id} finished with status ${job.status}` : "No deploy queued",
        ctx,
      );
    }),
  });
}

function completed(message: string, ctx: CliContext) {
  return { message, completedAt: ctx.platform.clock.nowIso() };
}

function safeDiagnostic(value: string) {
  return redact(value).slice(0, 4_000) || "Operation failed";
}

async function operationsOverview(ctx: CliContext): Promise<OperationsOverview> {
  if (!(await ctx.store.exists())) return empty(ctx.stackRoot);
  try {
    const state = await ctx.store.load();
    const status = await buildStatus(ctx.platform, state);
    return {
      initialized: true,
      stackRoot: ctx.stackRoot,
      stackName: status.stackName,
      roles: status.roles,
      runtimes: status.phpVersions,
      ...(status.generation ? { generation: status.generation } : {}),
      counts: {
        applications: status.apps.length,
        cronJobs: status.cronJobs,
        workers: status.workers,
        proxies: status.proxies.length,
      },
      warnings: status.warnings,
      notes: status.notes,
    };
  } catch (error) {
    return empty(ctx.stackRoot, redact(error instanceof Error ? error.message : String(error)));
  }
}

function empty(stackRoot: string, error?: string): OperationsOverview {
  return {
    initialized: false,
    stackRoot,
    roles: [],
    runtimes: [],
    counts: { applications: 0, cronJobs: 0, workers: 0, proxies: 0 },
    warnings: [],
    notes: [],
    ...(error ? { error } : {}),
  };
}
