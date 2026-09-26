import { basename, relative } from "node:path";
import { implement } from "@orpc/server";
import { dataContract, type DatabaseActivity, type DatabaseRuntime, type DataOverview } from "@bento/shared";
import type { CliContext } from "#/commands/context.ts";
import type { AppDatabaseBinding } from "#/domain/state.ts";
import { validationError } from "#/domain/errors.ts";
import { queryDatabaseSizes, queryProcesslist } from "#/services/mysql.ts";
import { queryPostgresActivity, queryPostgresDatabaseSizes } from "#/services/postgres.ts";
import { requireMysqlRootPassword, requirePostgresRootPassword } from "#/services/stack_env.ts";
import { listWebBackupRuns, startWebBackup } from "#/services/web_backup.ts";
import { redact } from "#/ui/output.ts";

const os = implement(dataContract);

export function createDataRouter(ctx: CliContext) {
  return os.router({
    overview: os.overview.handler(async () => await dataOverview(ctx)),
    backupRuns: os.backupRuns.handler(async () => ({
      runs: (await listWebBackupRuns(ctx.platform)).map((run) => ({
        id: run.id,
        status: run.status,
        startedAt: run.startedAt,
        finishedAt: run.finishedAt,
        progress: run.progress,
        steps: run.steps.map((step) => ({ name: step.name, status: step.status, error: step.error })),
      })),
    })),
    startBackup: os.startBackup.handler(async ({ input }) => {
      const state = await ctx.store.load();
      const operation = await startWebBackup(ctx.platform, state, input.app);
      return { id: operation.id };
    }),
    runtime: os.runtime.handler(async ({ input }) => await databaseRuntime(ctx, input)),
    activity: os.activity.handler(async ({ input }) => await databaseActivity(ctx, input)),
    backup: os.backup.handler(async ({ input }) => {
      const state = await ctx.store.load();
      const database =
        input.engine === "sqlite"
          ? state.apps[input.app]?.databases.find(
              (binding): binding is Extract<AppDatabaseBinding, { engine: "sqlite" }> =>
                binding.engine === "sqlite" && binding.file.path === input.database,
            )?.file.id
          : input.database;
      if (!database) throw new Error("database binding was not found");
      const result = await ctx.data.backup({
        scope: "database",
        slug: input.app,
        database,
        engine: input.engine,
        compress: "zstd",
      });
      return {
        artifacts: result.artifacts.map((item) => ({
          name: relative(ctx.platform.paths.paths.backupsDir, item.path),
          database: item.database,
          bytes: item.bytes,
        })),
      };
    }),
    restore: os.restore.handler(async ({ input }) => {
      if (input.confirmation !== input.targetDatabase)
        throw new Error("confirmation must exactly match the target database");
      const state = await ctx.store.load();
      const binding = state.apps[input.app]?.databases.find(
        (item) =>
          item.engine === input.engine &&
          input.artifact.startsWith(`${item.service}/`) &&
          item.databases.some((database) => input.artifact.startsWith(`${item.service}/${database.name}/`)),
      );
      if (!binding) {
        throw validationError("backup source must belong to a recorded database binding of this app and engine");
      }
      if (binding.databases.some((database) => database.name === input.targetDatabase)) {
        throw validationError(
          "verification restore requires an unused target; production replacement is not available in the web UI",
        );
      }
      const file = await ctx.data.resolveBackupArtifact(input.artifact);
      await ctx.data.restore({
        file,
        slug: input.app,
        targetDatabase: input.targetDatabase,
        engine: input.engine,
      });
      return { message: `Restored ${basename(file)} into ${input.targetDatabase}` };
    }),
  });
}

async function dataOverview(ctx: CliContext): Promise<DataOverview> {
  if (!(await ctx.store.exists())) return empty(ctx.stackRoot);
  try {
    const state = await ctx.store.load();
    const bindings = Object.values(state.apps)
      .sort((left, right) => left.slug.localeCompare(right.slug))
      .flatMap((app) =>
        app.databases.map((binding, index) => ({
          app: String(app.slug),
          primary: index === 0,
          engine: binding.engine,
          service: binding.engine === "sqlite" ? "local file" : String(binding.service),
          resources:
            binding.engine === "sqlite"
              ? [binding.file.path]
              : binding.databases.map((database) => String(database.name)),
        })),
      );
    return {
      initialized: true,
      stackRoot: ctx.stackRoot,
      services: state.databaseServices.map((service) => ({
        engine: service.engine,
        version: String(service.version),
        service: String(service.service),
        image: service.image,
        volume: service.volume,
        appCount: new Set(
          bindings
            .filter((binding) => binding.engine === service.engine && binding.service === service.service)
            .map((binding) => binding.app),
        ).size,
      })),
      bindings,
      backups: await listBackups(ctx),
    };
  } catch (error) {
    return empty(ctx.stackRoot, redact(error instanceof Error ? error.message : String(error)));
  }
}

async function databaseRuntime(
  ctx: CliContext,
  input: { service: string; engine: "mysql" | "postgres" },
): Promise<DatabaseRuntime> {
  const state = await ctx.store.load();
  const managed = state.databaseServices.find((item) => item.service === input.service && item.engine === input.engine);
  if (!managed) {
    return {
      service: input.service,
      engine: input.engine,
      serverVersion: "Unknown",
      databases: [],
      processes: [],
      error: "This database service is not managed by Bento.",
    };
  }
  try {
    return await liveDatabaseRuntime(ctx, input, String(managed.version));
  } catch (error) {
    return {
      service: input.service,
      engine: input.engine,
      serverVersion: String(managed.version),
      databases: [],
      processes: [],
      error: redact(error instanceof Error ? error.message : String(error)).slice(0, 2_000),
    };
  }
}

async function databaseActivity(
  ctx: CliContext,
  input: { service: string; engine: "mysql" | "postgres" },
): Promise<DatabaseActivity> {
  const state = await ctx.store.load();
  const managed = state.databaseServices.find((item) => item.service === input.service && item.engine === input.engine);
  if (!managed) {
    return {
      service: input.service,
      engine: input.engine,
      processes: [],
      error: "This database service is not managed by Bento.",
    };
  }
  try {
    const password =
      input.engine === "mysql"
        ? await requireMysqlRootPassword(ctx.platform)
        : await requirePostgresRootPassword(ctx.platform);
    return {
      service: input.service,
      engine: input.engine,
      processes: await queryDatabaseActivity(ctx, input, password),
    };
  } catch (error) {
    return {
      service: input.service,
      engine: input.engine,
      processes: [],
      error: redact(error instanceof Error ? error.message : String(error)).slice(0, 2_000),
    };
  }
}

async function liveDatabaseRuntime(
  ctx: CliContext,
  input: { service: string; engine: "mysql" | "postgres" },
  configuredVersion: string,
): Promise<DatabaseRuntime> {
  const password =
    input.engine === "mysql"
      ? await requireMysqlRootPassword(ctx.platform)
      : await requirePostgresRootPassword(ctx.platform);
  const databases =
    input.engine === "mysql"
      ? (await queryDatabaseSizes(ctx.platform, input.service, password)).rows.map((row) => ({
          name: row.database,
          bytes: Math.round(Number(row.totalSize) * 1024 * 1024) || 0,
        }))
      : (await queryPostgresDatabaseSizes(ctx.platform, input.service, password)).map((row) => ({
          name: row.database,
          bytes: Number(row.bytes) || 0,
        }));
  let processes: DatabaseRuntime["processes"] = [];
  let error: string | undefined;
  try {
    processes = await queryDatabaseActivity(ctx, input, password);
  } catch (cause) {
    error = `Database sizes loaded, but process activity is unavailable: ${redact(messageOf(cause)).slice(0, 1_000)}`;
  }
  return {
    service: input.service,
    engine: input.engine,
    serverVersion: configuredVersion,
    databases,
    processes,
    ...(error ? { error } : {}),
  };
}

async function queryDatabaseActivity(
  ctx: CliContext,
  input: { service: string; engine: "mysql" | "postgres" },
  password: string,
): Promise<DatabaseRuntime["processes"]> {
  if (input.engine === "mysql") {
    const activity = await queryProcesslist(ctx.platform, input.service, password);
    return activity.stdout
      .split("\n")
      .filter((line) => line.trim())
      .map((line) => {
        const fields = line.split("\t");
        return {
          id: fields[0] ?? "",
          user: fields[1] ?? "",
          database: fields[3] ?? "",
          state: fields[6] || fields[4] || "unknown",
          query: fields[4] || "—",
        };
      });
  }

  return (await queryPostgresActivity(ctx.platform, input.service, password)).map((row) => ({
    id: row.pid,
    user: row.user,
    database: row.database,
    state: row.state,
    query: row.queryStart ? `Active since ${row.queryStart}` : "—",
  }));
}

async function listBackups(ctx: CliContext): Promise<DataOverview["backups"]> {
  return await ctx.data.listBackupArtifacts();
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

function empty(stackRoot: string, error?: string): DataOverview {
  return {
    initialized: false,
    stackRoot,
    services: [],
    bindings: [],
    backups: [],
    ...(error ? { error } : {}),
  };
}
