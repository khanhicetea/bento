import { implement } from "@orpc/server";
import { webContract } from "@bento/shared";
import type { CliContext } from "#/commands/context.ts";
import { createApplicationsRouter } from "#/server/domains/applications/router.ts";
import { createDataRouter } from "#/server/domains/data/router.ts";
import { createJobsRouter } from "#/server/domains/jobs/router.ts";
import { createOperationsRouter } from "#/server/domains/operations/router.ts";
import { createRoutingRouter } from "#/server/domains/routing/router.ts";
import { createSystemRouter } from "#/server/domains/system/router.ts";

const os = implement(webContract);

type WebRouterOptions = {
  schedulerWebAccess?: {
    enabled: boolean;
    reason?: string;
    pathFor(app: string): string;
  };
};

/** Compose independently contracted domain routers into the public web API. */
export function createWebRouter(ctx: CliContext, options: WebRouterOptions = {}) {
  return os.router({
    system: createSystemRouter(),
    applications: createApplicationsRouter(ctx),
    data: createDataRouter(ctx),
    routing: createRoutingRouter(ctx),
    jobs: createJobsRouter(ctx, options.schedulerWebAccess),
    operations: createOperationsRouter(ctx),
  });
}

export type AppRouter = ReturnType<typeof createWebRouter>;
