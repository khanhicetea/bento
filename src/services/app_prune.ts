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

export type AppPruneDatabase = {
  engine: DatabaseEngine;
  databaseService: string;
  databaseUser: string;
  databases: string[];
};

export type AppPruneManifest = {
  version: 3;
  slug: string;
  bindings: AppPruneDatabase[];
};

export type AppPrunePlan = AppPruneManifest & {
  home: string;
  manifestFound: boolean;
};

/** Save non-secret, engine-aware cleanup metadata in the retained home. */
export async function writeAppPruneManifest(platform: Platform, app: AppState): Promise<void> {
  const home = platform.paths.appHome(app.slug);
  await platform.fs.mkdirp(join(home, ".bento"), 0o700);
  const manifest: AppPruneManifest = {
    version: 3,
    slug: app.slug,
    bindings: app.databases.map((database): AppPruneDatabase =>
      database.engine === "sqlite" || database.engine === "litestream"
        ? {
          engine: database.engine,
          databaseService: "local-file",
          databaseUser: app.slug,
          databases: [database.file.id],
        }
        : {
          engine: database.engine,
          databaseService: database.service,
          databaseUser: database.user,
          databases: database.databases.map((entry) => entry.name),
        }
    ),
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
      version: 3,
      slug,
      bindings: [],
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
  for (const binding of manifest.bindings) {
    const managed = binding.engine === "sqlite" || binding.engine === "litestream" ||
      state.databaseServices.some((service) =>
        service.engine === binding.engine && service.service === binding.databaseService
      );
    if (!managed) throw validationError(`unsafe app prune manifest: ${manifestPath}`);
  }

  return { ...manifest, home, manifestFound: true };
}

function normalizeManifest(raw: unknown, slug: string): AppPruneManifest {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    throw validationError("invalid app prune manifest");
  }
  const value = raw as Record<string, unknown>;
  if (value.version !== 3 || value.slug !== slug || !Array.isArray(value.bindings)) {
    throw validationError("invalid app prune manifest");
  }
  const bindings = value.bindings.map((rawBinding): AppPruneDatabase => {
    if (!rawBinding || typeof rawBinding !== "object" || Array.isArray(rawBinding)) {
      throw validationError("invalid app prune database binding");
    }
    const binding = rawBinding as Record<string, unknown>;
    const engine = binding.engine;
    if (
      engine !== "mysql" && engine !== "postgres" && engine !== "sqlite" &&
      engine !== "litestream"
    ) {
      throw validationError("invalid app prune database engine");
    }
    const databaseService = typeof binding.databaseService === "string"
      ? binding.databaseService
      : "";
    const databaseUser = typeof binding.databaseUser === "string" ? binding.databaseUser : "";
    const databases = Array.isArray(binding.databases) ? binding.databases.filter(isString) : [];
    const validNames = engine === "sqlite" || engine === "litestream"
      ? databaseService === "local-file" && databases.length === 1 &&
        databases.every((name) =>
          name.startsWith(`${slug}_`) && /^[a-f0-9]{10}$/.test(name.slice(slug.length + 1))
        )
      : /^[a-zA-Z0-9_-]+$/.test(databaseService) &&
        databases.every((name) =>
          /^[a-zA-Z0-9_]+$/.test(name) && (name === slug || name.startsWith(`${slug}_`))
        );
    if (databaseUser !== slug || !validNames) {
      throw validationError("unsafe app prune database binding");
    }
    return { engine, databaseService, databaseUser, databases };
  });
  return { version: 3, slug, bindings };
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
    for (const binding of plan.bindings) {
      if (binding.engine === "mysql") await pruneMysql(platform, plan.slug, binding);
      else if (binding.engine === "postgres") await prunePostgres(platform, plan.slug, binding);
      else {
        const sqliteDir = join(platform.paths.paths.root, "sqlite", binding.databases[0]!);
        await platform.fs.remove(sqliteDir, { recursive: true });
        cleaned.push(`SQLite directory ${sqliteDir}`);
      }
      if (binding.engine !== "sqlite" && binding.engine !== "litestream") {
        for (const database of binding.databases) cleaned.push(`database ${database}`);
        cleaned.push(
          `${
            binding.engine === "mysql" ? "MySQL account" : "PostgreSQL role"
          } ${binding.databaseUser}`,
        );
      }
    }
  }

  await platform.fs.remove(plan.home, { recursive: true });
  cleaned.push(`home ${plan.home}`);
  return { cleaned };
}

async function pruneMysql(
  platform: Platform,
  slug: string,
  plan: AppPruneDatabase,
): Promise<void> {
  const password = await requireMysqlRootPassword(platform);
  const sql = [
    ...plan.databases.map((database) => `DROP DATABASE IF EXISTS ${mysqlIdent(database)};`),
    `DROP USER IF EXISTS ${mysqlIdent(plan.databaseUser)}@'%';`,
    "FLUSH PRIVILEGES;",
  ].join("\n");
  const result = await execMysqlSql(platform, plan.databaseService, sql, password);
  if (result.code !== 0) {
    throwPruneServiceError("MySQL", slug, result.stderr, result.code);
  }
}

async function prunePostgres(
  platform: Platform,
  slug: string,
  plan: AppPruneDatabase,
): Promise<void> {
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
    throwPruneServiceError("PostgreSQL", slug, result.stderr, result.code);
  }
}

function throwPruneServiceError(
  engine: "MySQL" | "PostgreSQL",
  slug: string,
  stderr: string,
  code: number,
): never {
  throw serviceError(
    `failed to prune ${engine} data for ${slug}: ${stderr.trim() || `exit ${code}`}`,
    `Start the recorded ${engine} service and retry; the retained home was not removed.`,
  );
}
