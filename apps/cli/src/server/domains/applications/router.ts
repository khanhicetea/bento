import { basename, join } from "node:path";
import { implement, ORPCError } from "@orpc/server";
import {
  applicationsContract,
  type AddApplicationDatabaseInput,
  type Application,
  type ApplicationList,
  type SaveApplicationInput,
} from "@bento/shared";
import type { CliContext } from "#/commands/context.ts";
import { isBentoError, type BentoError } from "#/domain/errors.ts";
import type { AppDatabaseBinding, AppState, TlsMode } from "#/domain/state.ts";
import { FPM_PROFILES } from "#/domain/types.ts";
import { redact } from "#/ui/output.ts";
import { ApplicationApplyError } from "#/use_cases/applications.ts";

const os = implement(applicationsContract);

export function createApplicationsRouter(ctx: CliContext) {
  return os.router({
    list: os.list.handler(async () => await listApplications(ctx)),
    publicKey: os.publicKey.handler(async ({ input }) => {
      const state = await ctx.store.load();
      if (!state.apps[input.slug]) throw new ORPCError("NOT_FOUND", { message: "application was not found" });
      const sshDir = join(ctx.platform.paths.appHome(input.slug), ".ssh");
      const path = join(sshDir, "id_ed25519.pub");
      try {
        const dir = await ctx.platform.fs.lstat(sshDir);
        const file = await ctx.platform.fs.lstat(path);
        if (!dir.isDirectory || dir.isSymlink || !file.isFile || file.isSymlink || file.size > 1024) {
          throw new Error("invalid public key file");
        }
        const publicKey = (await ctx.platform.fs.readText(path)).trim();
        if (!/^ssh-ed25519 [A-Za-z0-9+/]+={0,2}(?: [^\r\n]*)?$/.test(publicKey)) {
          throw new Error("invalid public key format");
        }
        return { publicKey };
      } catch {
        throw new ORPCError("NOT_FOUND", { message: "application public key is unavailable" });
      }
    }),
    databaseCredentials: os.databaseCredentials.handler(async ({ input }) => {
      const state = await ctx.store.load();
      const app = state.apps[input.slug];
      if (!app) throw new ORPCError("NOT_FOUND", { message: "application was not found" });
      const binding = app.databases.find(
        (database): database is Extract<AppDatabaseBinding, { engine: "mysql" | "postgres" }> =>
          database.engine === input.engine && String(database.service) === input.service,
      );
      if (!binding) {
        throw new ORPCError("NOT_FOUND", { message: "managed database binding was not found" });
      }
      return {
        engine: binding.engine,
        host: String(binding.service),
        port: binding.engine === "mysql" ? 3306 : 5432,
        user: binding.user,
        password: binding.password,
        databases: binding.databases.map((database) => String(database.name)),
      };
    }),
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
        const changed = await ctx.applications.setEnabled(input.slug, input.enabled);
        if (changed.stopWarning) ctx.log.warn(changed.stopWarning);
        return toApplication(changed.app);
      } catch (error) {
        logApplicationError(ctx, "set enabled", error);
        throw asORPCError(error);
      }
    }),
    setRunning: os.setRunning.handler(async ({ input }) => {
      try {
        return toApplication(await ctx.applications.setRunning(input.slug, input.action));
      } catch (error) {
        logApplicationError(ctx, "set running", error);
        throw asORPCError(error);
      }
    }),
    remove: os.remove.handler(async ({ input }) => {
      try {
        const removed = await ctx.applications.remove(input.slug, input.confirmation);
        return toApplication(removed.app);
      } catch (error) {
        logApplicationError(ctx, "remove", error);
        throw asORPCError(error);
      }
    }),
  });
}

async function saveApplication(ctx: CliContext, input: SaveApplicationInput): Promise<Application> {
  const result = await ctx.applications.provision({
    slug: input.slug,
    domain: input.domain,
    aliases: input.aliases,
    kind: input.kind,
    documentRoot: input.documentRoot,
    entrypointMode: input.entrypointMode,
    phpVersion: input.phpVersion,
    fpmProfile: input.fpmProfile,
    processLanguage: input.processLanguage,
    processVersion: input.processVersion,
    processCommand: input.processCommand,
    processWorkdir: input.processWorkdir,
    processPort: input.processPort,
    processHealthPath: input.processHealthPath,
    databaseEngine: input.databaseEngine,
    mysqlVersion: input.databaseEngine === "mysql" ? input.databaseService : undefined,
    postgresVersion: input.databaseEngine === "postgres" ? input.databaseService : undefined,
    createDatabase: input.createDatabase,
    databaseName: input.databaseName,
    tls: tlsMode(input),
    accessLog: input.accessLog,
  });
  return toApplication(result.app);
}

async function addApplicationDatabase(ctx: CliContext, input: AddApplicationDatabaseInput): Promise<Application> {
  const result = await ctx.applications.addDatabase({
    slug: input.slug,
    engine: input.engine,
    service: input.service,
    databaseName: input.databaseName,
  });
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
    kind: app.kind,
    enabled: app.enabled,
    domain: app.mainDomain,
    aliases: app.aliases,
    ...(app.kind === "php"
      ? {
          documentRoot: app.documentRoot,
          entrypointMode: app.entrypointMode,
          phpVersion: String(app.phpVersion),
          fpmProfile: String(app.fpmProfile),
        }
      : { processRuntime: app.runtime }),
    tls: app.tls.kind,
    tlsCertificatePath: app.tls.kind === "external" ? app.tls.certPath : undefined,
    tlsKeyPath: app.tls.kind === "external" ? app.tls.keyPath : undefined,
    accessLog: app.accessLog,
    deployEnabled: app.deploy.enabled,
    ...(app.deploy.enabled
      ? {
          deploySummary: {
            queuePolicy: app.deploy.queuePolicy,
            timeoutSec: app.deploy.timeoutSec,
            command: `${basename(app.deploy.argv[0] ?? "command")}${app.deploy.argv.length > 1 ? ` (+${app.deploy.argv.length - 1} args)` : ""}`,
          },
        }
      : {}),
    databases: app.databases.map((database) => ({
      engine: database.engine,
      service: database.engine === "mysql" || database.engine === "postgres" ? database.service : undefined,
      names:
        database.engine === "mysql" || database.engine === "postgres"
          ? database.databases.map((item) => item.name)
          : [],
      file: database.engine === "sqlite" ? database.file.path : undefined,
    })),
  };
}

function logApplicationError(ctx: CliContext, operation: string, error: unknown): void {
  const failure = error instanceof ApplicationApplyError ? error.cause : error;
  const detail = failure instanceof Error ? (failure.stack ?? failure.message) : String(failure);
  ctx.log.error(`application ${operation} failed: ${redact(detail)}`);
}

function asORPCError(error: unknown): ORPCError<string, unknown> {
  if (error instanceof ApplicationApplyError) {
    return new ORPCError("INTERNAL_SERVER_ERROR", {
      message:
        "Application settings were saved, but applying them failed. Check the server log, fix the cause, then run bento apply.",
    });
  }
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
    message: code === "INTERNAL_SERVER_ERROR" ? "Application operation failed" : errorMessage(error),
  });
}

function errorMessage(error: BentoError): string {
  return error.recovery ? `${error.message} ${error.recovery}` : error.message;
}
