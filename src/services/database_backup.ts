/** Engine-neutral logical backup/restore dispatch. */

import { join, relative, resolve } from "@std/path";
import type { AppDatabaseBinding, DesiredState } from "../domain/state.ts";
import { assertNever } from "../domain/state.ts";
import { asDatabaseName } from "../domain/types.ts";
import { conflictError, notFoundError, validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import {
  applyBackupRetention,
  type BackupRequest,
  runBackup as runMysqlBackup,
  runRestore as runMysqlRestore,
} from "./mysql.ts";
import { runPostgresBackup, runPostgresRestore } from "./postgres.ts";
import { runSqliteBackup } from "./sqlite_local.ts";

export type DatabaseBackupRequest = BackupRequest;
export type DatabaseBackupArtifact = {
  engine: "mysql" | "postgres" | "sqlite";
  path: string;
  database: string;
  service: string;
  bytes: number;
};
export type DatabaseRestoreRequest = {
  file: string;
  slug: string;
  targetDatabase: string;
  replaceOriginal?: string;
  engine?: "mysql" | "postgres";
};

/** Back up an app, one recorded database, or every database across local engines. */
export async function runDatabaseBackup(
  platform: Platform,
  state: DesiredState,
  req: DatabaseBackupRequest,
): Promise<DatabaseBackupArtifact[]> {
  const release = await platform.lock.tryExclusive(
    join(platform.paths.paths.lockDir, "database-backup.lock"),
  );
  if (!release) {
    throw conflictError(
      "another logical backup batch is already running",
      "Wait for the current logical backup batch to finish, then retry.",
    );
  }
  try {
    const targets = resolveTargets(state, req);
    const artifacts: DatabaseBackupArtifact[] = [];
    const compress = req.compress ?? "zstd";

    // Retention is deliberately deferred until every engine adapter succeeds.
    // Earlier valid artifacts remain if a later target fails.
    for (const target of targets) {
      switch (target.engine) {
        case "mysql": {
          const result = await runMysqlBackup(
            platform,
            state,
            { scope: "database", slug: target.slug, database: target.database, compress },
            { skipRetention: true },
          );
          artifacts.push(...result);
          break;
        }
        case "postgres":
          artifacts.push(await runPostgresBackup(platform, target, compress));
          break;
        case "sqlite":
          artifacts.push(
            await runSqliteBackup(platform, state, target.slug, compress, target.database),
          );
          break;
        default:
          assertNever(target.engine);
      }
    }
    await applyBackupRetention(platform, artifacts, 10);
    return artifacts;
  } finally {
    await release();
  }
}

/** Restore through the selected app's engine and return state with a new target recorded. */
export async function runDatabaseRestore(
  platform: Platform,
  state: DesiredState,
  req: DatabaseRestoreRequest,
): Promise<DesiredState> {
  const app = state.apps[req.slug];
  if (!app) throw notFoundError(`app not found: ${req.slug}`);
  const database = resolveRestoreBinding(platform, app.databases, req);
  validateDumpPathForEngine(platform, state, req.file, database.engine);
  const scopedApp = { ...app, database, databases: [database] };

  switch (database.engine) {
    case "mysql":
      await runMysqlRestore(platform, {
        ...state,
        apps: { ...state.apps, [req.slug]: scopedApp },
      }, req);
      break;
    case "postgres":
      await runPostgresRestore(platform, {
        file: req.file,
        app: scopedApp,
        targetDatabase: req.targetDatabase,
        replaceOriginal: req.replaceOriginal,
      });
      break;
    default:
      assertNever(database);
  }

  if (database.databases.some((entry) => entry.name === req.targetDatabase)) {
    return state;
  }
  const next = structuredClone(state);
  const nextBinding = next.apps[req.slug]!.databases.find((entry) =>
    entry.engine === database.engine && entry.service === database.service
  );
  if (!nextBinding || nextBinding.engine === "sqlite" || nextBinding.engine === "litestream") {
    throw validationError(`database binding disappeared for app ${req.slug}`);
  }
  nextBinding.databases.push({
    name: asDatabaseName(req.targetDatabase),
    createdAt: platform.clock.nowIso(),
  });
  next.apps[req.slug]!.database = nextBinding;
  next.apps[req.slug]!.updatedAt = platform.clock.nowIso();
  next.updatedAt = platform.clock.nowIso();
  return next;
}

function resolveTargets(state: DesiredState, req: DatabaseBackupRequest): Array<{
  engine: "mysql" | "postgres" | "sqlite";
  service: string;
  database: string;
  slug: string;
}> {
  if (req.scope !== "all" && !req.slug) {
    throw validationError(`${req.scope} backup requires --app`);
  }
  const apps = req.scope === "all"
    ? Object.values(state.apps).sort((a, b) => a.slug.localeCompare(b.slug))
    : [
      state.apps[req.slug!] ?? (() => {
        throw notFoundError(`app not found: ${req.slug}`);
      })(),
    ];
  const targets: Array<{
    engine: "mysql" | "postgres" | "sqlite";
    service: string;
    database: string;
    slug: string;
  }> = [];
  for (const app of apps) {
    let matched = false;
    for (const binding of app.databases) {
      if (binding.engine === "litestream") continue;
      if (binding.engine === "sqlite") {
        if (req.scope !== "database" || req.database === binding.file.id) {
          targets.push({
            engine: "sqlite",
            service: "sqlite",
            database: binding.file.id,
            slug: app.slug,
          });
          matched = true;
        }
        continue;
      }
      const databases = req.scope === "database"
        ? binding.databases.filter((database) => database.name === req.database)
        : binding.databases;
      for (const database of databases) {
        targets.push({
          engine: binding.engine,
          service: binding.service,
          database: database.name,
          slug: app.slug,
        });
        matched = true;
      }
    }
    if (req.scope === "database" && (!req.database || !matched)) {
      throw notFoundError(`database ${req.database ?? ""} not recorded for app ${app.slug}`);
    }
  }
  return targets;
}

function resolveRestoreBinding(
  platform: Platform,
  bindings: AppDatabaseBinding[],
  req: DatabaseRestoreRequest,
): Extract<AppDatabaseBinding, { engine: "mysql" | "postgres" }> {
  const relational = bindings.filter(
    (binding): binding is Extract<AppDatabaseBinding, { engine: "mysql" | "postgres" }> =>
      binding.engine === "mysql" || binding.engine === "postgres",
  );
  const requested = req.engine
    ? relational.filter((binding) => binding.engine === req.engine)
    : relational;
  const named = requested.filter((binding) =>
    binding.databases.some((database) =>
      database.name === req.replaceOriginal || database.name === req.targetDatabase
    )
  );
  if (named.length === 1) return named[0]!;

  const rel = relative(resolve(platform.paths.paths.backupsDir), resolve(req.file));
  const service = rel && rel !== ".." && !rel.startsWith("../") && !rel.startsWith("..\\")
    ? rel.split(/[\\/]/)[0]
    : undefined;
  const byService = requested.filter((binding) => binding.service === service);
  if (byService.length === 1) return byService[0]!;
  if (requested.length === 1) return requested[0]!;
  if (requested.length === 0) {
    throw validationError(`app ${req.slug} has no ${req.engine ?? "relational"} database binding`);
  }
  throw validationError(
    `restore target is ambiguous for app ${req.slug}; specify --engine mysql or --engine postgres`,
  );
}

function validateDumpPathForEngine(
  platform: Platform,
  state: DesiredState,
  file: string,
  targetEngine: "mysql" | "postgres",
): void {
  if (!/\.sql(?:\.gz|\.zst|\.zstd)?$/i.test(file)) {
    throw validationError("restore source must be a .sql, .sql.gz, .sql.zst, or .sql.zstd file");
  }
  const rel = relative(resolve(platform.paths.paths.backupsDir), resolve(file));
  if (!rel || rel === ".." || rel.startsWith("../") || rel.startsWith("..\\")) return;
  const service = rel.split(/[\\/]/)[0];
  const managed = state.databaseServices.find((entry) => entry.service === service);
  if (managed && managed.engine !== targetEngine) {
    throw validationError(
      `backup from ${managed.engine} service ${service} cannot be restored into a ${targetEngine} app`,
    );
  }
}
