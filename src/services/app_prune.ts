/** Destructive cleanup for durable data retained after app removal. */

import { join } from "@std/path";
import type { AppState, DatabaseEngine, DesiredState } from "../domain/state.ts";
import { conflictError, safetyError, serviceError, validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import { parseAppSlug, unwrap } from "../schemas/validators.ts";
import { execMysqlSql } from "./mysql.ts";
import { execPostgresSql, postgresIdentifier, postgresLiteral } from "./postgres.ts";
import { requireMysqlRootPassword, requirePostgresRootPassword } from "./stack_env.ts";
import { mysqlIdent } from "./template.ts";

const MANIFEST = ".bento/prune.json";

export type AppPruneManifest = {
  version: 2;
  slug: string;
  engine: DatabaseEngine;
  databaseService: string;
  databaseUser: string;
  databases: string[];
};

export type AppPrunePlan = AppPruneManifest & {
  home: string;
  manifestFound: boolean;
};

type LegacyMysqlManifest = {
  version: 1;
  slug: string;
  mysqlService: string;
  mysqlUser: string;
  databases: string[];
};

/** Save non-secret, engine-aware cleanup metadata in the retained home. */
export async function writeAppPruneManifest(platform: Platform, app: AppState): Promise<void> {
  const home = platform.paths.appHome(app.slug);
  await platform.fs.mkdirp(join(home, ".bento"), 0o700);
  const manifest: AppPruneManifest = app.database.engine === "sqlite"
    ? {
      version: 2,
      slug: app.slug,
      engine: "sqlite",
      databaseService: "local-file",
      databaseUser: app.slug,
      databases: [app.database.file.id],
    }
    : {
      version: 2,
      slug: app.slug,
      engine: app.database.engine,
      databaseService: app.database.service,
      databaseUser: app.database.user,
      databases: app.database.databases.map((database) => database.name),
    };
  await platform.fs.writeText(
    join(home, MANIFEST),
    `${JSON.stringify(manifest, null, 2)}\n`,
    0o600,
  );
}

/** Build the exact cleanup list. Active apps can never be pruned. */
export async function planAppPrune(
  platform: Platform,
  state: DesiredState,
  slugInput: string,
): Promise<AppPrunePlan> {
  const slug = unwrap(parseAppSlug(slugInput), "slug");
  if (state.apps[slug]) {
    throw conflictError(
      `refusing to prune active app ${slug}`,
      `Remove it first with bento app remove ${slug} --confirm 'delete ${slug}'.`,
    );
  }

  const home = platform.paths.appHome(slug);
  const manifestPath = join(home, MANIFEST);
  if (!(await platform.fs.exists(home))) {
    throw validationError(`no retained data found for app ${slug}`);
  }
  if (!(await platform.fs.exists(manifestPath))) {
    return {
      version: 2,
      slug,
      engine: "mysql",
      databaseService: "",
      databaseUser: "",
      databases: [],
      home,
      manifestFound: false,
    };
  }

  let raw: unknown;
  try {
    raw = JSON.parse(await platform.fs.readText(manifestPath));
  } catch {
    throw validationError(`invalid app prune manifest: ${manifestPath}`);
  }
  const manifest = normalizeManifest(raw, slug);
  const managed = manifest.engine === "sqlite" ||
    state.databaseServices.some((service) =>
      service.engine === manifest.engine && service.service === manifest.databaseService
    );
  if (!managed) throw validationError(`unsafe app prune manifest: ${manifestPath}`);

  return { ...manifest, home, manifestFound: true };
}

function normalizeManifest(raw: unknown, slug: string): AppPruneManifest {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    throw validationError("invalid app prune manifest");
  }
  const value = raw as Record<string, unknown>;
  let candidate: AppPruneManifest;
  if (value.version === 1) {
    const legacy = value as Partial<LegacyMysqlManifest>;
    candidate = {
      version: 2,
      slug: typeof legacy.slug === "string" ? legacy.slug : "",
      engine: "mysql",
      databaseService: typeof legacy.mysqlService === "string" ? legacy.mysqlService : "",
      databaseUser: typeof legacy.mysqlUser === "string" ? legacy.mysqlUser : "",
      databases: Array.isArray(legacy.databases) ? legacy.databases.filter(isString) : [],
    };
  } else {
    candidate = {
      version: 2,
      slug: typeof value.slug === "string" ? value.slug : "",
      engine: value.engine === "postgres"
        ? "postgres"
        : value.engine === "sqlite"
        ? "sqlite"
        : "mysql",
      databaseService: typeof value.databaseService === "string" ? value.databaseService : "",
      databaseUser: typeof value.databaseUser === "string" ? value.databaseUser : "",
      databases: Array.isArray(value.databases) ? value.databases.filter(isString) : [],
    };
    if (
      value.version !== 2 ||
      (value.engine !== "mysql" && value.engine !== "postgres" && value.engine !== "sqlite")
    ) {
      throw validationError("invalid app prune manifest");
    }
  }

  const validDatabaseNames = candidate.engine === "sqlite"
    ? candidate.databaseService === "local-file" && candidate.databases.length === 1 &&
      candidate.databases.every((name) =>
        name.startsWith(`${slug}_`) && /^[a-f0-9]{10}$/.test(name.slice(slug.length + 1))
      )
    : /^[a-zA-Z0-9_-]+$/.test(candidate.databaseService) &&
      candidate.databases.every((name) =>
        /^[a-zA-Z0-9_]+$/.test(name) && (name === slug || name.startsWith(`${slug}_`))
      );
  const valid = candidate.slug === slug && candidate.databaseUser === slug &&
    Array.isArray(value.databases) && candidate.databases.length === value.databases.length &&
    validDatabaseNames;
  if (!valid) throw validationError("unsafe app prune manifest");
  return candidate;
}

function isString(value: unknown): value is string {
  return typeof value === "string";
}

export type AppPruneResult = { cleaned: string[] };

/** Execute a pre-listed plan only after the literal confirmation `delete`. */
export async function executeAppPrune(
  platform: Platform,
  plan: AppPrunePlan,
  confirmation: string | null,
): Promise<AppPruneResult> {
  if (confirmation !== "delete") {
    throw safetyError(
      `refusing to prune app ${plan.slug}: confirmation must be exactly 'delete'`,
      "Run the command again and type delete at the prompt.",
    );
  }

  const cleaned: string[] = [];
  if (plan.manifestFound) {
    if (plan.engine === "mysql") await pruneMysql(platform, plan);
    else if (plan.engine === "postgres") await prunePostgres(platform, plan);
    else {
      const sqliteDir = join(platform.paths.paths.root, "sqlite", plan.databases[0]!);
      await platform.fs.remove(sqliteDir, { recursive: true });
      cleaned.push(`SQLite directory ${sqliteDir}`);
    }
    if (plan.engine !== "sqlite") {
      for (const database of plan.databases) cleaned.push(`database ${database}`);
      cleaned.push(
        `${plan.engine === "mysql" ? "MySQL account" : "PostgreSQL role"} ${plan.databaseUser}`,
      );
    }
  }

  await platform.fs.remove(plan.home, { recursive: true });
  cleaned.push(`home ${plan.home}`);
  return { cleaned };
}

async function pruneMysql(platform: Platform, plan: AppPrunePlan): Promise<void> {
  const password = await requireMysqlRootPassword(platform);
  const sql = [
    ...plan.databases.map((database) => `DROP DATABASE IF EXISTS ${mysqlIdent(database)};`),
    `DROP USER IF EXISTS ${mysqlIdent(plan.databaseUser)}@'%';`,
    "FLUSH PRIVILEGES;",
  ].join("\n");
  const result = await execMysqlSql(platform, plan.databaseService, sql, password);
  if (result.code !== 0) {
    throwPruneServiceError("MySQL", plan, result.stderr, result.code);
  }
}

async function prunePostgres(platform: Platform, plan: AppPrunePlan): Promise<void> {
  const password = await requirePostgresRootPassword(platform);
  const sql = [
    ...plan.databases.flatMap((database) => [
      `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ${
        postgresLiteral(database)
      } AND pid <> pg_backend_pid();`,
      `DROP DATABASE IF EXISTS ${postgresIdentifier(database)};`,
    ]),
    `DROP ROLE IF EXISTS ${postgresIdentifier(plan.databaseUser)};`,
  ].join("\n");
  const result = await execPostgresSql(platform, plan.databaseService, sql, password);
  if (result.code !== 0) {
    throwPruneServiceError("PostgreSQL", plan, result.stderr, result.code);
  }
}

function throwPruneServiceError(
  engine: "MySQL" | "PostgreSQL",
  plan: AppPrunePlan,
  stderr: string,
  code: number,
): never {
  throw serviceError(
    `failed to prune ${engine} data for ${plan.slug}: ${stderr.trim() || `exit ${code}`}`,
    `Start the recorded ${engine} service and retry; the retained home was not removed.`,
  );
}
