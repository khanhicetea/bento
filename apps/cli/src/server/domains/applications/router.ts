import { implement, ORPCError } from "@orpc/server";
import { applicationsContract, type Application, type ApplicationList } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { isBentoError, type BentoError } from "../../../domain/errors.ts";
import type { AppState } from "../../../domain/state.ts";
import { setAppEnabled } from "../../../services/app.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(applicationsContract);

export function createApplicationsRouter(ctx: CliContext) {
  return os.router({
    list: os.list.handler(async () => await listApplications(ctx)),
    setEnabled: os.setEnabled.handler(async ({ input }) => {
      try {
        const changed = await ctx.store.withExclusive(async (state) => {
          const result = setAppEnabled(
            state,
            input.slug,
            input.enabled,
            ctx.platform.clock.nowIso(),
          );
          await ctx.store.save(result.state);
          await ctx.render.apply(result.state, {
            reloadPlan: result.reloadPlan,
            skipValidate: false,
            alreadyLocked: true,
          });
          return result.app;
        });
        return toApplication(changed);
      } catch (error) {
        throw asORPCError(error);
      }
    }),
  });
}

export async function listApplications(ctx: CliContext): Promise<ApplicationList> {
  if (!(await ctx.store.exists())) {
    return {
      initialized: false,
      stateExists: false,
      stackRoot: ctx.stackRoot,
      applications: [],
      phpVersions: [],
    };
  }

  try {
    const state = await ctx.store.load();
    return {
      initialized: true,
      stateExists: true,
      stackRoot: ctx.stackRoot,
      applications: Object.values(state.apps)
        .sort((a, b) => a.slug.localeCompare(b.slug))
        .map(toApplication),
      phpVersions: state.phpVersions.map((item) => item.version),
    };
  } catch (error) {
    return {
      initialized: false,
      stateExists: true,
      error: redact(error instanceof Error ? error.message : String(error)),
      stackRoot: ctx.stackRoot,
      applications: [],
      phpVersions: [],
    };
  }
}

export function toApplication(app: AppState): Application {
  return {
    slug: app.slug,
    enabled: app.enabled,
    domain: app.mainDomain,
    aliases: app.aliases,
    phpVersion: app.phpVersion,
    fpmProfile: app.fpmProfile,
    tls: app.tls.kind,
    accessLog: app.accessLog,
    deployEnabled: app.deploy.enabled,
    databases: app.databases.map((database) => ({
      engine: database.engine,
      service:
        database.engine === "mysql" || database.engine === "postgres"
          ? database.service
          : undefined,
      names:
        database.engine === "mysql" || database.engine === "postgres"
          ? database.databases.map((item) => item.name)
          : [],
      file:
        database.engine === "sqlite" || database.engine === "litestream"
          ? database.file.path
          : undefined,
    })),
  };
}

function asORPCError(error: unknown): ORPCError<string, unknown> {
  if (!isBentoError(error)) {
    return new ORPCError("INTERNAL_SERVER_ERROR", {
      message: "Application operation failed",
    });
  }

  const code =
    error.code === "NOT_FOUND"
      ? "NOT_FOUND"
      : error.code === "CONFLICT"
        ? "CONFLICT"
        : error.code === "VALIDATION"
          ? "BAD_REQUEST"
          : "INTERNAL_SERVER_ERROR";
  return new ORPCError(code, {
    message:
      code === "INTERNAL_SERVER_ERROR" ? "Application operation failed" : errorMessage(error),
  });
}

function errorMessage(error: BentoError): string {
  return error.recovery ? `${error.message} ${error.recovery}` : error.message;
}
