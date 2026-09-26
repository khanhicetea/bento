import { basename, relative, resolve } from "node:path";
import { implement } from "@orpc/server";
import { dataContract, type DatabaseActivity, type DatabaseRuntime, type DataOverview } from "@bento/shared";
import type { CliContext } from "#/commands/context.ts";
import type { AppDatabaseBinding } from "#/domain/state.ts";
import { runDatabaseBackup, runDatabaseRestore } from "#/services/database_backup.ts";
import { queryDatabaseSizes, queryProcesslist } from "#/services/mysql.ts";
import { queryPostgresActivity, queryPostgresDatabaseSizes } from "#/services/postgres.ts";
import { requireMysqlRootPassword, requirePostgresRootPassword } from "#/services/stack_env.ts";
import { redact } from "#/ui/output.ts";

const os = implement(dataContract);

export function createDataRouter(ctx: CliContext) {
  return os.router({
    overview: os.overview.handler(async () => await dataOverview(ctx)),
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
      const artifacts = await runDatabaseBackup(ctx.platform, state, {
        scope: "database",
        slug: input.app,
        database,
        engine: input.engine,
        compress: "zstd",
      });
      return {
        artifacts: artifacts.map((item) => ({
          name: relative(ctx.platform.paths.paths.backupsDir, item.path),
          database: item.database,
          bytes: item.bytes,
        })),
      };
    }),
    restore: os.restore.handler(async ({ input }) => {
      if (input.confirmation !== input.targetDatabase)
        throw new Error("confirmation must exactly match the target database");
      const root = resolve(ctx.platform.paths.paths.backupsDir);
      const file = resolve(root, input.artifact);
      const rel = relative(root, file);
      if (
        !rel ||
        rel === ".." ||
        rel.startsWith("../") ||
        rel.startsWith("..\\") ||
        !(await ctx.platform.fs.exists(file))
      ) {
        throw new Error("backup artifact was not found");
      }
      await ctx.store.withExclusive(async (state) => {
        const next = await runDatabaseRestore(ctx.platform, state, {
          file,
          slug: input.app,
          targetDatabase: input.targetDatabase,
          engine: input.engine,
        });
        await ctx.store.save(next);
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
          service:
            binding.engine === "sqlite"
              ? "local file"
              : binding.engine === "litestream"
                ? "Litestream"
                : String(binding.service),
          resources:
            binding.engine === "sqlite" || binding.engine === "litestream"
              ? [binding.file.path]
              : binding.databases.map((database) => String(database.name)),
          ...(binding.engine === "litestream" && binding.backupVerifiedAt
            ? { backupVerifiedAt: binding.backupVerifiedAt }
            : {}),
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
      ...(state.sqliteBackup
        ? {
            sqliteBackup: {
              enabled: state.sqliteBackup.enabled,
              provider: state.sqliteBackup.provider,
              destination: state.sqliteBackup.destination,
              syncInterval: state.sqliteBackup.syncInterval,
              snapshotInterval: state.sqliteBackup.snapshotInterval,
              snapshotRetention: state.sqliteBackup.snapshotRetention,
            },
          }
        : {}),
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
  const root = resolve(ctx.platform.paths.paths.backupsDir);
  if (!(await ctx.platform.fs.exists(root))) return [];
  const found: DataOverview["backups"] = [];
  const pending = [root];
  while (pending.length) {
    const directory = pending.pop()!;
    for (const name of await ctx.platform.fs.readDir(directory)) {
      const path = resolve(directory, name);
      const rel = relative(root, path);
      if (!rel || rel === ".." || rel.startsWith("../") || rel.startsWith("..\\")) continue;
      const stat = await ctx.platform.fs.stat(path);
      if (stat.isDirectory) pending.push(path);
      if (stat.isFile && /\.(?:sql|sqlite)(?:\.gz|\.zst|\.zstd)?$/i.test(name))
        found.push({
          name: rel,
          bytes: stat.size,
          ...(stat.modifiedAt ? { modifiedAt: stat.modifiedAt.toISOString() } : {}),
        });
    }
  }
  return found.sort((a, b) => b.name.localeCompare(a.name));
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
