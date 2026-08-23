import { implement, ORPCError } from "@orpc/server";
import {
  applicationsContract,
  type AddApplicationDatabaseInput,
  type Application,
  type ApplicationList,
  type SaveApplicationInput,
} from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { isBentoError, type BentoError } from "../../../domain/errors.ts";
import type { AppState, TlsMode } from "../../../domain/state.ts";
import { FPM_PROFILES } from "../../../domain/types.ts";
import {
  applyAppDataPlane,
  deleteApp,
  getAppOrThrow,
  materializeAppHome,
  provisionApp,
  setAppEnabled,
} from "../../../services/app.ts";
import { writeAppPruneManifest } from "../../../services/app_prune.ts";
import { enableSqliteBackup, sqliteCompose } from "../../../services/sqlite.ts";
import { loadRedisPassword } from "../../../services/stack_env.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(applicationsContract);

export function createApplicationsRouter(ctx: CliContext) {
  return os.router({
    list: os.list.handler(async () => await listApplications(ctx)),
    save: os.save.handler(async ({ input }) => {
      try {
        return await saveApplication(ctx, input);
      } catch (error) {
        logApplicationError(ctx, "save", error);
        throw asORPCError(error);
      }
    }),
    addDatabase: os.addDatabase.handler(async ({ input }) => {
      try {
        return await addApplicationDatabase(ctx, input);
      } catch (error) {
        logApplicationError(ctx, "add database", error);
        throw asORPCError(error);
      }
    }),
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
        logApplicationError(ctx, "set enabled", error);
        throw asORPCError(error);
      }
    }),
    remove: os.remove.handler(async ({ input }) => {
      try {
        const removed = await ctx.store.withExclusive(async (state) => {
          const result = deleteApp(
            state,
            input.slug,
            input.confirmation,
            ctx.platform.clock.nowIso(),
          );
          await writeAppPruneManifest(ctx.platform, result.app);
          await ctx.store.save(result.state);
          await ctx.render.apply(result.state, {
            reloadPlan: result.reloadPlan,
            skipValidate: false,
            alreadyLocked: true,
          });
          return result.app;
        });
        return toApplication(removed);
      } catch (error) {
        logApplicationError(ctx, "remove", error);
        throw asORPCError(error);
      }
    }),
  });
}

async function saveApplication(ctx: CliContext, input: SaveApplicationInput): Promise<Application> {
  const result = await ctx.store.withExclusive(async (state) => {
    const provisioned = provisionApp(ctx.platform, state, {
      slug: input.slug,
      domain: input.domain,
      aliases: input.aliases,
      documentRoot: input.documentRoot,
      entrypointMode: input.entrypointMode,
      phpVersion: input.phpVersion,
      fpmProfile: input.fpmProfile,
      databaseEngine: input.databaseEngine,
      mysqlVersion: input.databaseEngine === "mysql" ? input.databaseService : undefined,
      postgresVersion: input.databaseEngine === "postgres" ? input.databaseService : undefined,
      createDatabase: input.createDatabase,
      databaseName: input.databaseName,
      tls: tlsMode(input),
      accessLog: input.accessLog,
    });
    const selectedService = state.databaseServices.find(
      (service) =>
        service.engine === input.databaseEngine &&
        (service.service === input.databaseService || service.version === input.databaseService),
    );
    await applyAppDataPlane(ctx.platform, provisioned.app, {
      explicitDatabase: input.createDatabase,
      databaseEngine: input.databaseEngine,
      databaseService: selectedService?.service,
      databaseName: input.databaseName,
    });
    const redisSharedPassword = await loadRedisPassword(ctx.platform);
    await materializeAppHome(ctx.platform, provisioned.app, {
      recursivePerms: true,
      redisSharedPassword,
    });

    let nextState = provisioned.state;
    let startLitestream = false;
    if (input.databaseEngine === "litestream" && !nextState.sqliteBackup?.enabled) {
      nextState = await enableSqliteBackup(ctx.platform, nextState, input.slug);
      startLitestream = true;
    }

    await ctx.store.save(nextState);
    await ctx.render.apply(nextState, {
      reloadPlan: provisioned.reloadPlan,
      skipValidate: false,
      alreadyLocked: true,
    });
    return { app: provisioned.app, state: nextState, startLitestream };
  });

  if (result.startLitestream) {
    const up = await sqliteCompose(ctx.platform, result.state, [
      "up",
      "-d",
      "--force-recreate",
      "litestream",
    ]);
    if (up.code !== 0) {
      throw new Error(`Litestream container failed to start: ${up.stderr.trim()}`);
    }
  }

  return toApplication(result.app);
}

async function addApplicationDatabase(
  ctx: CliContext,
  input: AddApplicationDatabaseInput,
): Promise<Application> {
  const result = await ctx.store.withExclusive(async (state) => {
    const current = getAppOrThrow(state, input.slug);
    const provisioned = provisionApp(ctx.platform, state, {
      slug: current.slug,
      domain: current.mainDomain,
      aliases: current.aliases,
      databaseEngine: input.engine,
      mysqlVersion: input.engine === "mysql" ? input.service : undefined,
      postgresVersion: input.engine === "postgres" ? input.service : undefined,
      createDatabase: true,
      databaseName: input.databaseName,
    });
    const selectedService = state.databaseServices.find(
      (service) =>
        service.engine === input.engine &&
        (service.service === input.service || service.version === input.service),
    );
    await applyAppDataPlane(ctx.platform, provisioned.app, {
      explicitDatabase: true,
      databaseEngine: input.engine,
      databaseService: selectedService?.service,
      databaseName: input.databaseName,
    });
    const redisSharedPassword = await loadRedisPassword(ctx.platform);
    await materializeAppHome(ctx.platform, provisioned.app, {
      recursivePerms: true,
      redisSharedPassword,
    });

    let nextState = provisioned.state;
    let startLitestream = false;
    if (input.engine === "litestream" && !nextState.sqliteBackup?.enabled) {
      nextState = await enableSqliteBackup(ctx.platform, nextState, input.slug);
      startLitestream = true;
    }
    await ctx.store.save(nextState);
    await ctx.render.apply(nextState, {
      reloadPlan: provisioned.reloadPlan,
      skipValidate: false,
      alreadyLocked: true,
    });
    return { app: provisioned.app, state: nextState, startLitestream };
  });

  if (result.startLitestream) {
    const up = await sqliteCompose(ctx.platform, result.state, [
      "up",
      "-d",
      "--force-recreate",
      "litestream",
    ]);
    if (up.code !== 0) {
      throw new Error(`Litestream container failed to start: ${up.stderr.trim()}`);
    }
  }
  return toApplication(result.app);
}

function tlsMode(input: SaveApplicationInput): TlsMode {
  if (input.tls !== "external") return { kind: input.tls };
  return {
    kind: "external",
    certPath: input.tlsCertificatePath!,
    keyPath: input.tlsKeyPath!,
  };
}

export async function listApplications(ctx: CliContext): Promise<ApplicationList> {
  const fpmProfiles = Object.keys(FPM_PROFILES);
  if (!(await ctx.store.exists())) {
    return {
      initialized: false,
      stateExists: false,
      stackRoot: ctx.stackRoot,
      applications: [],
      phpVersions: [],
      fpmProfiles,
      databaseServices: [],
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
      fpmProfiles,
      databaseServices: state.databaseServices.map((service) => ({
        engine: service.engine,
        version: service.version,
        service: service.service,
      })),
      defaults: {
        phpVersion: state.defaults.phpVersion,
        fpmProfile: state.defaults.fpmProfile,
        databaseEngine: state.defaults.database.engine,
        databaseService: state.defaults.database.service,
      },
    };
  } catch (error) {
    return {
      initialized: false,
      stateExists: true,
      error: redact(error instanceof Error ? error.message : String(error)),
      stackRoot: ctx.stackRoot,
      applications: [],
      phpVersions: [],
      fpmProfiles,
      databaseServices: [],
    };
  }
}

export function toApplication(app: AppState): Application {
  return {
    slug: app.slug,
    enabled: app.enabled,
    domain: app.mainDomain,
    aliases: app.aliases,
    documentRoot: app.documentRoot,
    entrypointMode: app.entrypointMode,
    phpVersion: app.phpVersion,
    fpmProfile: app.fpmProfile,
    tls: app.tls.kind,
    tlsCertificatePath: app.tls.kind === "external" ? app.tls.certPath : undefined,
    tlsKeyPath: app.tls.kind === "external" ? app.tls.keyPath : undefined,
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

function logApplicationError(ctx: CliContext, operation: string, error: unknown): void {
  const detail = error instanceof Error ? (error.stack ?? error.message) : String(error);
  ctx.log.error(`application ${operation} failed: ${redact(detail)}`);
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
        : error.code === "VALIDATION" || error.code === "SAFETY"
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
