import { basename } from "node:path";
import { implement } from "@orpc/server";
import { operationsContract, type OperationsOverview } from "@bento/shared";
import type { CliContext } from "#/commands/context.ts";
import { runDatabaseBackup } from "#/services/database_backup.ts";
import { getBackupScheduleRunStatus } from "#/services/backup_schedule.ts";
import { drainDeploy } from "#/services/deploy.ts";
import { runDoctor } from "#/services/doctor.ts";
import { runStackMaintenance } from "#/services/maintenance.ts";
import { buildStatus } from "#/services/status.ts";
import { redact } from "#/ui/output.ts";

const os = implement(operationsContract);

export function createOperationsRouter(ctx: CliContext) {
  return os.router({
    overview: os.overview.handler(async () => await operationsOverview(ctx)),
    backupRunStatus: os.backupRunStatus.handler(async () => {
      const { lastRun, lastOperation } = await getBackupScheduleRunStatus(ctx.platform);
      return {
        lastRun: lastRun && {
          operationId: lastRun.operationId,
          status: lastRun.status,
          startedAt: lastRun.startedAt,
          finishedAt: lastRun.finishedAt,
          artifactCount: lastRun.artifactCount,
          artifactBytes: lastRun.artifactBytes,
          error: lastRun.error,
        },
        lastOperation: lastOperation && {
          id: lastOperation.id,
          status: lastOperation.status,
          steps: lastOperation.steps.map((step) => ({ name: step.name, status: step.status, error: step.error })),
        },
      };
    }),
    stackAction: os.stackAction.handler(async ({ input }) => {
      await ctx.operations.stackAction(input.action, input.confirmation);
      return completed(`Stack ${input.action} completed`, ctx);
    }),
    restartService: os.restartService.handler(async ({ input }) => {
      await ctx.operations.restartService(input.service, input.confirmation);
      return completed(`Service ${input.service} restarted`, ctx);
    }),
    apply: os.apply.handler(async () => {
      await ctx.operations.apply();
      return completed("Configuration rendered, validated, and applied", ctx);
    }),
    setupCloudflareTunnel: os.setupCloudflareTunnel.handler(async ({ input }) => {
      await ctx.operations.configureCloudflareTunnel(input.token);
      return completed("Cloudflare tunnel configured and started", ctx);
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
      const result = await ctx.operations.logs(input);
      return { ...result, lines: result.lines.map(redact) };
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
      return completed(job ? `Deploy ${job.id} finished with status ${job.status}` : "No deploy queued", ctx);
    }),
  });
}

function completed(message: string, ctx: CliContext) {
  return { message, completedAt: ctx.platform.clock.nowIso() };
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
      cloudflareTunnel: status.cloudflareTunnel,
      roles: status.roles,
      runtimes: status.phpVersions,
      ...(status.generation ? { generation: status.generation } : {}),
      counts: {
        applications: status.apps.length,
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
    cloudflareTunnel: { configured: false },
    roles: [],
    runtimes: [],
    counts: { applications: 0, proxies: 0 },
    warnings: [],
    notes: [],
    ...(error ? { error } : {}),
  };
}
