/** Engine-neutral logical backup/restore dispatch. */

import { relative, resolve } from "@std/path";
import type { DesiredState } from "../domain/state.ts";
import { assertNever } from "../domain/state.ts";
import { asDatabaseName } from "../domain/types.ts";
import { notFoundError, validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import {
  applyBackupRetention,
  type BackupRequest,
  runBackup as runMysqlBackup,
  runRestore as runMysqlRestore,
} from "./mysql.ts";
import { runPostgresBackup, runPostgresRestore } from "./postgres.ts";

export type DatabaseBackupRequest = BackupRequest;
export type DatabaseBackupArtifact = {
  engine: "mysql" | "postgres";
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
};

/** Back up an app, one recorded database, or every database across both engines. */
export async function runDatabaseBackup(
  platform: Platform,
  state: DesiredState,
  req: DatabaseBackupRequest,
): Promise<DatabaseBackupArtifact[]> {
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
      default:
        assertNever(target.engine);
    }
  }
  await applyBackupRetention(platform, artifacts, 10);
  return artifacts;
}

/** Restore through the selected app's engine and return state with a new target recorded. */
export async function runDatabaseRestore(
  platform: Platform,
  state: DesiredState,
  req: DatabaseRestoreRequest,
): Promise<DesiredState> {
  const app = state.apps[req.slug];
  if (!app) throw notFoundError(`app not found: ${req.slug}`);
  validateDumpPathForEngine(platform, state, req.file, app.database.engine);

  switch (app.database.engine) {
    case "mysql":
      await runMysqlRestore(platform, state, req);
      break;
    case "postgres":
      await runPostgresRestore(platform, {
        file: req.file,
        app,
        targetDatabase: req.targetDatabase,
        replaceOriginal: req.replaceOriginal,
      });
      break;
    default:
      assertNever(app.database);
  }

  if (app.database.databases.some((database) => database.name === req.targetDatabase)) {
    return state;
  }
  const next = structuredClone(state);
  next.apps[req.slug]!.database.databases.push({
    name: asDatabaseName(req.targetDatabase),
    createdAt: platform.clock.nowIso(),
  });
  next.apps[req.slug]!.updatedAt = platform.clock.nowIso();
  next.updatedAt = platform.clock.nowIso();
  return next;
}

function resolveTargets(state: DesiredState, req: DatabaseBackupRequest): Array<{
  engine: "mysql" | "postgres";
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
    engine: "mysql" | "postgres";
    service: string;
    database: string;
    slug: string;
  }> = [];
  for (const app of apps) {
    const databases = req.scope === "database"
      ? app.database.databases.filter((database) => database.name === req.database)
      : app.database.databases;
    if (req.scope === "database" && (!req.database || databases.length === 0)) {
      throw notFoundError(`database ${req.database ?? ""} not recorded for app ${app.slug}`);
    }
    for (const database of databases) {
      targets.push({
        engine: app.database.engine,
        service: app.database.service,
        database: database.name,
        slug: app.slug,
      });
    }
  }
  return targets;
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
