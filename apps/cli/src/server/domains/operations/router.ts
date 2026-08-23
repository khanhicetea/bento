import { implement } from "@orpc/server";
import { operationsContract, type OperationsOverview } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { buildStatus } from "../../../services/status.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(operationsContract);

export function createOperationsRouter(ctx: CliContext) {
  return os.router({ overview: os.overview.handler(async () => await operationsOverview(ctx)) });
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
