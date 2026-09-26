import { implement } from "@orpc/server";
import { jobsContract } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { isPhpApp } from "../../../domain/state.ts";

const os = implement(jobsContract);

type SchedulerWebAccess = {
  enabled: boolean;
  reason?: string;
  pathFor(app: string): string;
};

export function createJobsRouter(ctx: CliContext, schedulerWebAccess?: SchedulerWebAccess) {
  return os.router({
    schedulerAccess: os.schedulerAccess.handler(async () => {
      if (!schedulerWebAccess?.enabled) {
        return {
          enabled: false,
          reason:
            schedulerWebAccess?.reason ??
            "Start Bento with WEB_BASIC_AUTH on a fixed local port to enable browser schedulers.",
          schedulers: [],
        };
      }
      const state = (await ctx.store.exists()) ? await ctx.store.load() : undefined;
      return {
        enabled: true,
        schedulers: Object.values(state?.apps ?? {})
          .filter((app) => app.enabled && isPhpApp(app))
          .sort((a, b) => a.slug.localeCompare(b.slug))
          .map((app) => ({ app: app.slug, path: schedulerWebAccess.pathFor(app.slug) })),
      };
    }),
  });
}
