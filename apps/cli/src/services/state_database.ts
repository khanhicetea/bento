import { Database, type SQLQueryBindings } from "bun:sqlite";
import type {
  AppDatabaseBinding,
  AppState,
  DesiredState,
  DomainOwner,
  ProxySite,
  TemplateProvenance,
  TlsMode,
} from "#/domain/state.ts";
import { isPhpApp } from "#/domain/state.ts";
import { isBentoError, migrationError, stateError } from "#/domain/errors.ts";
import type { Platform } from "#/platform/mod.ts";
import { parseDesiredState, stateToJson } from "#/schemas/state.ts";

export const STATE_DATABASE_SCHEMA_VERSION = 1;

type Migration = {
  version: number;
  name: string;
  sql: string;
};

export type MigrationResult = {
  fromVersion: number;
  toVersion: number;
  applied: number[];
};

type StackConfigRow = {
  state_schema_version: number;
  default_php_version: string;
  default_database_engine: "mysql" | "postgres";
  default_database_version: string;
  default_database_service: string;
  default_fpm_profile: string;
  default_redis_mode: "shared" | "acl";
  sqlite_backup_provider: "litestream" | null;
  sqlite_backup_destination: string | null;
  sqlite_backup_sync_interval: "1s" | "10s" | "60s" | null;
  sqlite_backup_snapshot_interval: string | null;
  sqlite_backup_snapshot_retention: string | null;
  sqlite_backup_l0_retention: string | null;
  sqlite_backup_enabled: number | null;
  created_at: string;
  updated_at: string;
};

type PhpVersionRow = {
  version: string;
  service: string;
  image: string;
  process_cap: number;
};

type DatabaseServiceRow = {
  engine: "mysql" | "postgres";
  version: string;
  service: string;
  image: string;
  volume: string;
};

type AppRow = {
  slug: string;
  runtime_kind: "php" | "process";
  process_language: "node" | "bun" | "python" | null;
  process_version: string | null;
  process_image: string | null;
  process_service: string | null;
  process_internal_port: number | null;
  process_command_json: string | null;
  process_workdir: string | null;
  process_health_path: string | null;
  enabled: number;
  uid: number;
  gid: number;
  home: string;
  document_root: string;
  entrypoint_mode: "front-controller" | "legacy";
  php_version: string;
  php_service: string;
  fpm_profile: string;
  tls_kind: TlsMode["kind"];
  tls_cert_path: string | null;
  tls_key_path: string | null;
  access_log: number;
  redis_mode: "shared" | "acl";
  redis_prefix: string;
  redis_password: string | null;
  redis_acl_username: string | null;
  redis_acl_password: string | null;
  deploy_enabled: number;
  deploy_hmac_secret: string | null;
  deploy_queue_policy: "latest" | "fifo";
  deploy_timeout_sec: number;
  deploy_workdir: string;
  deploy_argv_json: string;
  vhost_template_kind: TemplateProvenance["kind"];
  vhost_template_source_path: string | null;
  vhost_template_copied_from_version: string | null;
  vhost_template_activated_at: string | null;
  pool_template_kind: TemplateProvenance["kind"];
  pool_template_source_path: string | null;
  pool_template_copied_from_version: string | null;
  pool_template_activated_at: string | null;
  created_at: string;
  updated_at: string;
};

type BindingRow = {
  id: number;
  app_slug: string;
  position: number;
  engine: AppDatabaseBinding["engine"];
  service: string | null;
  username: string | null;
  password: string | null;
  file_id: string | null;
  file_path: string | null;
  file_created_at: string | null;
  backup_verified_at: string | null;
  vacuum_day_of_week: number | null;
  vacuum_hour: number | null;
  vacuum_minute: number | null;
};

type AppDatabaseRow = {
  binding_id: number;
  position: number;
  name: string;
  created_at: string;
};

type DomainRow = {
  domain: string;
  owner_kind: DomainOwner["kind"];
  app_slug: string | null;
  proxy_name: string | null;
  primary_domain: number;
  owner_position: number;
};

type ProxyRow = {
  name: string;
  enabled: number;
  tls_kind: TlsMode["kind"];
  tls_cert_path: string | null;
  tls_key_path: string | null;
  access_log: number;
  created_at: string;
  updated_at: string;
};

type PositionValueRow = {
  owner: string;
  position: number;
  value: string;
};

const migrations: Migration[] = [
  {
    version: 1,
    name: "minicrond-owned-user-jobs",
    sql: `
CREATE TABLE stack_config (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  state_schema_version INTEGER NOT NULL CHECK (state_schema_version > 0),
  default_php_version TEXT NOT NULL,
  default_database_engine TEXT NOT NULL CHECK (default_database_engine IN ('mysql', 'postgres')),
  default_database_version TEXT NOT NULL,
  default_database_service TEXT NOT NULL,
  default_fpm_profile TEXT NOT NULL,
  default_redis_mode TEXT NOT NULL CHECK (default_redis_mode IN ('shared', 'acl')),
  sqlite_backup_provider TEXT CHECK (sqlite_backup_provider IS NULL OR sqlite_backup_provider = 'litestream'),
  sqlite_backup_destination TEXT,
  sqlite_backup_sync_interval TEXT CHECK (sqlite_backup_sync_interval IS NULL OR sqlite_backup_sync_interval IN ('1s', '10s', '60s')),
  sqlite_backup_snapshot_interval TEXT,
  sqlite_backup_snapshot_retention TEXT,
  sqlite_backup_l0_retention TEXT,
  sqlite_backup_enabled INTEGER CHECK (sqlite_backup_enabled IS NULL OR sqlite_backup_enabled IN (0, 1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (default_php_version) REFERENCES managed_php_versions(version),
  FOREIGN KEY (default_database_service, default_database_engine, default_database_version)
    REFERENCES managed_database_services(service, engine, version),
  CHECK (
    (sqlite_backup_provider IS NULL AND sqlite_backup_destination IS NULL AND sqlite_backup_sync_interval IS NULL AND
      sqlite_backup_snapshot_interval IS NULL AND sqlite_backup_snapshot_retention IS NULL AND
      sqlite_backup_l0_retention IS NULL AND sqlite_backup_enabled IS NULL)
    OR
    (sqlite_backup_provider = 'litestream' AND sqlite_backup_destination IS NOT NULL AND
      sqlite_backup_sync_interval IS NOT NULL AND sqlite_backup_snapshot_interval IS NOT NULL AND
      sqlite_backup_snapshot_retention IS NOT NULL AND sqlite_backup_l0_retention IS NOT NULL AND
      sqlite_backup_enabled IS NOT NULL)
  )
);

CREATE TABLE managed_php_versions (
  version TEXT PRIMARY KEY,
  service TEXT NOT NULL UNIQUE,
  image TEXT NOT NULL,
  process_cap INTEGER NOT NULL CHECK (process_cap > 0),
  UNIQUE (version, service)
);

CREATE TABLE managed_database_services (
  service TEXT PRIMARY KEY,
  engine TEXT NOT NULL CHECK (engine IN ('mysql', 'postgres')),
  version TEXT NOT NULL,
  image TEXT NOT NULL,
  volume TEXT NOT NULL UNIQUE,
  UNIQUE (engine, version),
  UNIQUE (service, engine),
  UNIQUE (service, engine, version)
);

CREATE TABLE applications (
  slug TEXT PRIMARY KEY,
  runtime_kind TEXT NOT NULL DEFAULT 'php' CHECK (runtime_kind IN ('php', 'process')),
  process_language TEXT CHECK (process_language IS NULL OR process_language IN ('node', 'bun', 'python')),
  process_version TEXT,
  process_image TEXT,
  process_service TEXT,
  process_internal_port INTEGER CHECK (process_internal_port IS NULL OR process_internal_port BETWEEN 1024 AND 65535),
  process_command_json TEXT CHECK (process_command_json IS NULL OR (json_valid(process_command_json) AND json_type(process_command_json) = 'array')),
  process_workdir TEXT,
  process_health_path TEXT,
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  uid INTEGER NOT NULL,
  gid INTEGER NOT NULL,
  home TEXT NOT NULL,
  document_root TEXT NOT NULL,
  entrypoint_mode TEXT NOT NULL CHECK (entrypoint_mode IN ('front-controller', 'legacy')),
  php_version TEXT NOT NULL REFERENCES managed_php_versions(version),
  php_service TEXT NOT NULL REFERENCES managed_php_versions(service),
  fpm_profile TEXT NOT NULL,
  tls_kind TEXT NOT NULL CHECK (tls_kind IN ('self-ca', 'shared', 'acme', 'external')),
  tls_cert_path TEXT,
  tls_key_path TEXT,
  access_log INTEGER NOT NULL CHECK (access_log IN (0, 1)),
  redis_mode TEXT NOT NULL CHECK (redis_mode IN ('shared', 'acl')),
  redis_prefix TEXT NOT NULL,
  redis_password TEXT,
  redis_acl_username TEXT,
  redis_acl_password TEXT,
  deploy_enabled INTEGER NOT NULL CHECK (deploy_enabled IN (0, 1)),
  deploy_hmac_secret TEXT,
  deploy_queue_policy TEXT NOT NULL CHECK (deploy_queue_policy IN ('latest', 'fifo')),
  deploy_timeout_sec INTEGER NOT NULL CHECK (deploy_timeout_sec > 0),
  deploy_workdir TEXT NOT NULL,
  deploy_argv_json TEXT NOT NULL CHECK (json_valid(deploy_argv_json) AND json_type(deploy_argv_json) = 'array'),
  vhost_template_kind TEXT NOT NULL CHECK (vhost_template_kind IN ('upstream', 'custom')),
  vhost_template_source_path TEXT,
  vhost_template_copied_from_version TEXT,
  vhost_template_activated_at TEXT,
  pool_template_kind TEXT NOT NULL CHECK (pool_template_kind IN ('upstream', 'custom')),
  pool_template_source_path TEXT,
  pool_template_copied_from_version TEXT,
  pool_template_activated_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (php_version, php_service) REFERENCES managed_php_versions(version, service),
  CHECK ((tls_kind = 'external' AND tls_cert_path IS NOT NULL AND tls_key_path IS NOT NULL) OR
    (tls_kind <> 'external' AND tls_cert_path IS NULL AND tls_key_path IS NULL)),
  CHECK ((vhost_template_kind = 'custom' AND vhost_template_source_path IS NOT NULL AND vhost_template_activated_at IS NOT NULL) OR
    (vhost_template_kind = 'upstream' AND vhost_template_source_path IS NULL AND vhost_template_copied_from_version IS NULL AND vhost_template_activated_at IS NULL)),
  CHECK ((pool_template_kind = 'custom' AND pool_template_source_path IS NOT NULL AND pool_template_activated_at IS NOT NULL) OR
    (pool_template_kind = 'upstream' AND pool_template_source_path IS NULL AND pool_template_copied_from_version IS NULL AND pool_template_activated_at IS NULL))
);

CREATE TABLE app_database_bindings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_slug TEXT NOT NULL REFERENCES applications(slug) ON DELETE CASCADE,
  position INTEGER NOT NULL CHECK (position >= 0),
  engine TEXT NOT NULL CHECK (engine IN ('mysql', 'postgres', 'sqlite', 'litestream')),
  service TEXT,
  username TEXT,
  password TEXT,
  file_id TEXT UNIQUE,
  file_path TEXT,
  file_created_at TEXT,
  backup_verified_at TEXT,
  vacuum_day_of_week INTEGER CHECK (vacuum_day_of_week IS NULL OR vacuum_day_of_week BETWEEN 0 AND 6),
  vacuum_hour INTEGER CHECK (vacuum_hour IS NULL OR vacuum_hour BETWEEN 0 AND 4),
  vacuum_minute INTEGER CHECK (vacuum_minute IS NULL OR vacuum_minute BETWEEN 0 AND 59),
  UNIQUE (app_slug, position),
  FOREIGN KEY (service, engine) REFERENCES managed_database_services(service, engine),
  CHECK (
    (engine IN ('mysql', 'postgres') AND service IS NOT NULL AND username IS NOT NULL AND password IS NOT NULL AND
      file_id IS NULL AND file_path IS NULL AND file_created_at IS NULL AND backup_verified_at IS NULL AND
      vacuum_day_of_week IS NULL AND vacuum_hour IS NULL AND vacuum_minute IS NULL)
    OR
    (engine = 'litestream' AND service IS NULL AND username IS NULL AND password IS NULL AND
      file_id IS NOT NULL AND file_path IS NOT NULL AND file_created_at IS NOT NULL AND
      vacuum_day_of_week IS NULL AND vacuum_hour IS NULL AND vacuum_minute IS NULL)
    OR
    (engine = 'sqlite' AND service IS NULL AND username IS NULL AND password IS NULL AND
      file_id IS NOT NULL AND file_path IS NOT NULL AND file_created_at IS NOT NULL AND backup_verified_at IS NULL AND
      ((vacuum_day_of_week IS NULL AND vacuum_hour IS NULL AND vacuum_minute IS NULL) OR
       (vacuum_day_of_week IS NOT NULL AND vacuum_hour IS NOT NULL AND vacuum_minute IS NOT NULL)))
  )
);

CREATE TABLE app_databases (
  binding_id INTEGER NOT NULL REFERENCES app_database_bindings(id) ON DELETE CASCADE,
  position INTEGER NOT NULL CHECK (position >= 0),
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (binding_id, position),
  UNIQUE (binding_id, name)
);

CREATE TABLE proxy_sites (
  name TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  tls_kind TEXT NOT NULL CHECK (tls_kind IN ('self-ca', 'shared', 'acme', 'external')),
  tls_cert_path TEXT,
  tls_key_path TEXT,
  access_log INTEGER NOT NULL CHECK (access_log IN (0, 1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK ((tls_kind = 'external' AND tls_cert_path IS NOT NULL AND tls_key_path IS NOT NULL) OR
    (tls_kind <> 'external' AND tls_cert_path IS NULL AND tls_key_path IS NULL))
);

CREATE TABLE proxy_upstreams (
  proxy_name TEXT NOT NULL REFERENCES proxy_sites(name) ON DELETE CASCADE,
  position INTEGER NOT NULL CHECK (position >= 0),
  value TEXT NOT NULL,
  PRIMARY KEY (proxy_name, position)
);

CREATE TABLE domains (
  domain TEXT PRIMARY KEY,
  owner_kind TEXT NOT NULL CHECK (owner_kind IN ('app', 'proxy')),
  app_slug TEXT REFERENCES applications(slug) ON DELETE CASCADE,
  proxy_name TEXT REFERENCES proxy_sites(name) ON DELETE CASCADE,
  primary_domain INTEGER NOT NULL CHECK (primary_domain IN (0, 1)),
  owner_position INTEGER NOT NULL CHECK (owner_position >= 0),
  CHECK ((owner_kind = 'app' AND app_slug IS NOT NULL AND proxy_name IS NULL) OR
    (owner_kind = 'proxy' AND proxy_name IS NOT NULL AND app_slug IS NULL))
);
CREATE UNIQUE INDEX one_primary_domain_per_app ON domains(app_slug) WHERE owner_kind = 'app' AND primary_domain = 1;
CREATE UNIQUE INDEX one_primary_domain_per_proxy ON domains(proxy_name) WHERE owner_kind = 'proxy' AND primary_domain = 1;
CREATE UNIQUE INDEX app_domain_position ON domains(app_slug, owner_position) WHERE owner_kind = 'app';
CREATE UNIQUE INDEX proxy_domain_position ON domains(proxy_name, owner_position) WHERE owner_kind = 'proxy';

`,
  },
];

export async function migrateStateDatabase(platform: Platform): Promise<MigrationResult> {
  const path = platform.paths.paths.stateDb;
  await platform.fs.mkdirp(platform.paths.paths.root, 0o700);
  let database: Database | undefined;
  try {
    database = openDatabase(path, "create");
    await platform.fs.chmod(path, 0o600);
    database.exec(`
      CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY CHECK (version > 0),
        name TEXT NOT NULL UNIQUE,
        applied_at TEXT NOT NULL
      )
    `);
    const appliedRows = database
      .query<
        { version: number; name: string },
        []
      >("SELECT version, name FROM schema_migrations ORDER BY version")
      .all();
    validateAppliedMigrations(appliedRows);
    const fromVersion = appliedRows.at(-1)?.version ?? 0;
    const applied: number[] = [];
    for (const migration of migrations) {
      if (migration.version <= fromVersion) continue;
      database
        .transaction(() => {
          database!.exec(migration.sql);
          database!.run(
            "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
            [migration.version, migration.name, platform.clock.nowIso()],
          );
        })
        .immediate();
      applied.push(migration.version);
    }
    return {
      fromVersion,
      toVersion: STATE_DATABASE_SCHEMA_VERSION,
      applied,
    };
  } catch (cause) {
    if (isBentoError(cause)) throw cause;
    throw migrationError(`failed to migrate desired state database at ${path}`, cause);
  } finally {
    database?.close();
  }
}

export async function stateDatabaseInitialized(platform: Platform): Promise<boolean> {
  const path = platform.paths.paths.stateDb;
  if (!(await platform.fs.exists(path))) return false;
  let database: Database | undefined;
  try {
    database = openDatabase(path, "read");
    assertCurrentMigrations(database);
    return (
      database
        .query<
          { initialized: number },
          []
        >("SELECT 1 AS initialized FROM stack_config WHERE id = 1")
        .get() !== null
    );
  } catch (cause) {
    if (isBentoError(cause)) throw cause;
    throw stateError(`cannot inspect desired state database at ${path}`, {
      recovery: "Run `bento migrate`; if it still fails, restore a known-good state.db backup.",
    });
  } finally {
    database?.close();
  }
}

export async function loadStateDatabase(platform: Platform): Promise<DesiredState> {
  const path = platform.paths.paths.stateDb;
  if (!(await platform.fs.exists(path))) {
    throw stateError(`no desired state database at ${path}`, {
      recovery: "Run `bento init` to create a new stack, or restore a known-good state.db.",
    });
  }
  let database: Database | undefined;
  try {
    database = openDatabase(path, "read");
    assertCurrentMigrations(database);
    return readState(database, path);
  } catch (cause) {
    if (isBentoError(cause)) throw cause;
    throw stateError(`cannot read desired state database at ${path}`, {
      recovery: "Run `bento migrate`; if it still fails, restore a known-good state.db backup.",
    });
  } finally {
    database?.close();
  }
}

export async function saveStateDatabase(
  platform: Platform,
  state: DesiredState,
): Promise<DesiredState> {
  const path = platform.paths.paths.stateDb;
  if (!(await platform.fs.exists(path))) {
    throw stateError(`no desired state database at ${path}`, {
      recovery: "Run `bento init` before saving desired state.",
    });
  }
  const validated = validateState(state);
  let database: Database | undefined;
  try {
    await platform.fs.chmod(path, 0o600);
    database = openDatabase(path, "write");
    assertCurrentMigrations(database);
    database.transaction(() => replaceState(database!, validated)).immediate();
    return validated;
  } catch (cause) {
    if (isBentoError(cause)) throw cause;
    throw stateError(`failed to save desired state database at ${path}`, {
      recovery:
        "The transaction was rolled back. Check state.db permissions and integrity, then retry.",
    });
  } finally {
    database?.close();
  }
}

function openDatabase(path: string, mode: "read" | "write" | "create"): Database {
  const database = new Database(path, {
    readonly: mode === "read",
    readwrite: mode !== "read",
    create: mode === "create",
    strict: true,
  });
  database.exec("PRAGMA foreign_keys = ON");
  database.exec("PRAGMA busy_timeout = 5000");
  return database;
}

function validateAppliedMigrations(rows: { version: number; name: string }[]): void {
  for (const [index, row] of rows.entries()) {
    const expected = migrations[index];
    if (!expected || row.version !== expected.version || row.name !== expected.name) {
      throw migrationError(
        `unsupported desired state database migration ${row.version} (${row.name}); this development schema cannot migrate an old state.db. Back up state.db and its WAL while the stack is stopped, move the old database aside, then run bento init to create a new stack. Recreate app scheduler definitions in each app's minicrond registry.`,
      );
    }
  }
}

function assertCurrentMigrations(database: Database): void {
  const table = database
    .query<
      { present: number },
      []
    >("SELECT 1 AS present FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations'")
    .get();
  if (!table) {
    throw stateError("desired state database schema is not initialized", {
      recovery: "Run `bento migrate` before using this stack.",
    });
  }
  const rows = database
    .query<
      { version: number; name: string },
      []
    >("SELECT version, name FROM schema_migrations ORDER BY version")
    .all();
  validateAppliedMigrations(rows);
  const current = rows.at(-1)?.version ?? 0;
  if (current !== STATE_DATABASE_SCHEMA_VERSION) {
    throw stateError(
      `desired state database schema is version ${current}; expected ${STATE_DATABASE_SCHEMA_VERSION}`,
      { recovery: "Run `bento migrate` with this Bento binary before retrying." },
    );
  }
}

function validateState(state: DesiredState): DesiredState {
  const raw = JSON.parse(stateToJson(state)) as unknown;
  const parsed = parseDesiredState(raw);
  if (!parsed.ok) {
    throw stateError(`invalid desired state: ${parsed.errors.join("; ")}`, {
      recovery: "Correct the state mutation; Bento did not modify state.db.",
    });
  }
  return parsed.value;
}

function readState(database: Database, path: string): DesiredState {
  const config = database
    .query<StackConfigRow, []>("SELECT * FROM stack_config WHERE id = 1")
    .get();
  if (!config) {
    throw stateError(`no desired state in ${path}`, {
      recovery: "Run `bento init` to initialize this migrated database.",
    });
  }

  const phpVersions = database
    .query<PhpVersionRow, []>(
      "SELECT version, service, image, process_cap FROM managed_php_versions ORDER BY rowid",
    )
    .all()
    .map((row) => ({
      version: row.version,
      service: row.service,
      image: row.image,
      processCap: row.process_cap,
    }));
  const databaseServices = database
    .query<
      DatabaseServiceRow,
      []
    >("SELECT engine, version, service, image, volume FROM managed_database_services ORDER BY rowid")
    .all();
  const domains = readDomains(database);
  const bindings = database
    .query<BindingRow, []>("SELECT * FROM app_database_bindings ORDER BY app_slug, position")
    .all();
  const databaseEntries = database
    .query<
      AppDatabaseRow,
      []
    >("SELECT binding_id, position, name, created_at FROM app_databases ORDER BY binding_id, position")
    .all();
  const apps = Object.fromEntries(
    database
      .query<AppRow, []>("SELECT * FROM applications ORDER BY slug")
      .all()
      .map((row) => {
        const appBindings = bindings
          .filter((binding) => binding.app_slug === row.slug)
          .map((binding) => bindingFromRow(binding, databaseEntries));
        const common = {
          slug: row.slug,
          enabled: asBoolean(row.enabled),
          uid: row.uid,
          gid: row.gid,
          home: row.home,
          tls: tlsFromColumns(row),
          accessLog: asBoolean(row.access_log),
          databases: appBindings,
          redis: {
            mode: row.redis_mode,
            prefix: row.redis_prefix,
            ...(row.redis_password !== null ? { password: row.redis_password } : {}),
            ...(row.redis_acl_username !== null ? { aclUsername: row.redis_acl_username } : {}),
            ...(row.redis_acl_password !== null ? { aclPassword: row.redis_acl_password } : {}),
          },
          deploy: {
            enabled: asBoolean(row.deploy_enabled),
            queuePolicy: row.deploy_queue_policy,
            timeoutSec: row.deploy_timeout_sec,
            workdir: row.deploy_workdir,
            argv: parseJsonArray(row.deploy_argv_json),
            ...(row.deploy_hmac_secret !== null ? { hmacSecret: row.deploy_hmac_secret } : {}),
          },
          vhostTemplate: templateFromColumns(row, "vhost"),
          createdAt: row.created_at,
          updatedAt: row.updated_at,
        };
        return [
          row.slug,
          row.runtime_kind === "php"
            ? {
                ...common,
                kind: "php",
                documentRoot: row.document_root,
                entrypointMode: row.entrypoint_mode,
                phpVersion: row.php_version,
                phpService: row.php_service,
                fpmProfile: row.fpm_profile,
                poolTemplate: templateFromColumns(row, "pool"),
              }
            : {
                ...common,
                kind: "process",
                runtime: {
                  language: row.process_language,
                  version: row.process_version,
                  image: row.process_image,
                  service: row.process_service,
                  internalPort: row.process_internal_port,
                  command:
                    row.process_command_json === null
                      ? []
                      : parseJsonArray(row.process_command_json),
                  workdir: row.process_workdir,
                  ...(row.process_health_path !== null
                    ? { healthPath: row.process_health_path }
                    : {}),
                },
              },
        ];
      }),
  );

  const upstreams = groupedValues(
    database
      .query<
        PositionValueRow,
        []
      >("SELECT proxy_name AS owner, position, value FROM proxy_upstreams ORDER BY proxy_name, position")
      .all(),
  );
  const domainRows = database
    .query<
      DomainRow,
      []
    >("SELECT domain, owner_kind, app_slug, proxy_name, primary_domain, owner_position FROM domains ORDER BY owner_kind, COALESCE(app_slug, proxy_name), owner_position, domain")
    .all();
  const proxies = Object.fromEntries(
    database
      .query<ProxyRow, []>("SELECT * FROM proxy_sites ORDER BY name")
      .all()
      .map((row) => {
        const linked = domainRows.filter(
          (domain) => domain.owner_kind === "proxy" && domain.proxy_name === row.name,
        );
        const primary = linked.find((domain) => asBoolean(domain.primary_domain));
        return [
          row.name,
          {
            name: row.name,
            enabled: asBoolean(row.enabled),
            mainDomain: primary?.domain ?? "unlinked.invalid",
            aliases: linked
              .filter((domain) => !asBoolean(domain.primary_domain))
              .map((domain) => domain.domain),
            upstreams: upstreams.get(row.name) ?? [],
            tls: tlsFromColumns(row),
            accessLog: asBoolean(row.access_log),
            createdAt: row.created_at,
            updatedAt: row.updated_at,
          },
        ];
      }),
  );

  const raw = {
    schemaVersion: config.state_schema_version,
    defaults: {
      phpVersion: config.default_php_version,
      database: {
        engine: config.default_database_engine,
        version: config.default_database_version,
        service: config.default_database_service,
      },
      fpmProfile: config.default_fpm_profile,
      redisMode: config.default_redis_mode,
    },
    phpVersions,
    databaseServices,
    ...(config.sqlite_backup_provider !== null
      ? {
          sqliteBackup: {
            provider: config.sqlite_backup_provider,
            destination: config.sqlite_backup_destination,
            syncInterval: config.sqlite_backup_sync_interval,
            snapshotInterval: config.sqlite_backup_snapshot_interval,
            snapshotRetention: config.sqlite_backup_snapshot_retention,
            l0Retention: config.sqlite_backup_l0_retention,
            enabled: asBoolean(config.sqlite_backup_enabled!),
          },
        }
      : {}),
    apps,
    proxies,
    domains,
    createdAt: config.created_at,
    updatedAt: config.updated_at,
  };
  const parsed = parseDesiredState(raw);
  if (!parsed.ok) {
    throw stateError(`invalid desired state database: ${parsed.errors.join("; ")}`, {
      recovery: "Restore a known-good state.db. Bento will not rewrite invalid desired state.",
    });
  }
  return parsed.value;
}

function readDomains(database: Database): Record<string, unknown> {
  return Object.fromEntries(
    database
      .query<DomainRow, []>(
        "SELECT domain, owner_kind, app_slug, proxy_name, primary_domain, owner_position FROM domains ORDER BY owner_kind, COALESCE(app_slug, proxy_name), owner_position, domain",
      )
      .all()
      .map((row) => [
        row.domain,
        row.owner_kind === "app"
          ? { kind: "app", slug: row.app_slug, primary: asBoolean(row.primary_domain) }
          : { kind: "proxy", name: row.proxy_name, primary: asBoolean(row.primary_domain) },
      ]),
  );
}

function bindingFromRow(
  row: BindingRow,
  databaseEntries: AppDatabaseRow[],
): Record<string, unknown> {
  if (row.engine === "mysql" || row.engine === "postgres") {
    return {
      engine: row.engine,
      service: row.service,
      user: row.username,
      password: row.password,
      databases: databaseEntries
        .filter((database) => database.binding_id === row.id)
        .map((database) => ({ name: database.name, createdAt: database.created_at })),
    };
  }
  return {
    engine: row.engine,
    file: {
      id: row.file_id,
      path: row.file_path,
      createdAt: row.file_created_at,
    },
    ...(row.engine === "litestream" && row.backup_verified_at !== null
      ? { backupVerifiedAt: row.backup_verified_at }
      : {}),
    ...(row.engine === "sqlite" && row.vacuum_day_of_week !== null
      ? {
          vacuumSchedule: {
            dayOfWeek: row.vacuum_day_of_week,
            hour: row.vacuum_hour,
            minute: row.vacuum_minute,
          },
        }
      : {}),
  };
}

function replaceState(database: Database, state: DesiredState): void {
  database.exec(`
    DELETE FROM domains;
    DELETE FROM applications;
    DELETE FROM proxy_sites;
    DELETE FROM stack_config;
    DELETE FROM managed_php_versions;
    DELETE FROM managed_database_services;
  `);

  for (const php of state.phpVersions) {
    run(
      database,
      "INSERT INTO managed_php_versions (version, service, image, process_cap) VALUES (?, ?, ?, ?)",
      [php.version, php.service, php.image, php.processCap],
    );
  }
  for (const service of state.databaseServices) {
    run(
      database,
      "INSERT INTO managed_database_services (service, engine, version, image, volume) VALUES (?, ?, ?, ?, ?)",
      [service.service, service.engine, service.version, service.image, service.volume],
    );
  }
  const backup = state.sqliteBackup;
  run(
    database,
    `INSERT INTO stack_config (
      id, state_schema_version, default_php_version, default_database_engine,
      default_database_version, default_database_service, default_fpm_profile, default_redis_mode,
      sqlite_backup_provider, sqlite_backup_destination, sqlite_backup_sync_interval,
      sqlite_backup_snapshot_interval, sqlite_backup_snapshot_retention, sqlite_backup_l0_retention,
      sqlite_backup_enabled, created_at, updated_at
    ) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    [
      state.schemaVersion,
      state.defaults.phpVersion,
      state.defaults.database.engine,
      state.defaults.database.version,
      state.defaults.database.service,
      state.defaults.fpmProfile,
      state.defaults.redisMode,
      backup?.provider ?? null,
      backup?.destination ?? null,
      backup?.syncInterval ?? null,
      backup?.snapshotInterval ?? null,
      backup?.snapshotRetention ?? null,
      backup?.l0Retention ?? null,
      backup?.enabled ?? null,
      state.createdAt,
      state.updatedAt,
    ],
  );

  for (const app of Object.values(state.apps).sort((a, b) => a.slug.localeCompare(b.slug))) {
    insertApp(database, app, state);
  }
  for (const proxy of Object.values(state.proxies).sort((a, b) => a.name.localeCompare(b.name))) {
    insertProxy(database, proxy);
  }
  insertDomains(database, state);
}

function insertApp(database: Database, app: AppState, state: DesiredState): void {
  const tls = tlsColumns(app.tls);
  const vhost = templateColumns(app.vhostTemplate);
  const pool = templateColumns(isPhpApp(app) ? app.poolTemplate : { kind: "upstream" });
  const compatibilityPhp = state.phpVersions.find(
    (entry) => entry.version === state.defaults.phpVersion,
  )!;
  run(
    database,
    `INSERT INTO applications (
      slug, runtime_kind, process_language, process_version, process_image, process_service,
      process_internal_port, process_command_json, process_workdir, process_health_path,
      enabled, uid, gid, home, document_root, entrypoint_mode, php_version, php_service,
      fpm_profile, tls_kind, tls_cert_path, tls_key_path, access_log,
      redis_mode, redis_prefix, redis_password, redis_acl_username, redis_acl_password,
      deploy_enabled, deploy_hmac_secret, deploy_queue_policy, deploy_timeout_sec, deploy_workdir,
      deploy_argv_json, vhost_template_kind, vhost_template_source_path,
      vhost_template_copied_from_version, vhost_template_activated_at, pool_template_kind,
      pool_template_source_path, pool_template_copied_from_version, pool_template_activated_at,
      created_at, updated_at
    ) VALUES (${placeholders(44)})`,
    [
      app.slug,
      app.kind,
      isPhpApp(app) ? null : app.runtime.language,
      isPhpApp(app) ? null : app.runtime.version,
      isPhpApp(app) ? null : app.runtime.image,
      isPhpApp(app) ? null : app.runtime.service,
      isPhpApp(app) ? null : app.runtime.internalPort,
      isPhpApp(app) ? null : JSON.stringify(app.runtime.command),
      isPhpApp(app) ? null : app.runtime.workdir,
      isPhpApp(app) ? null : (app.runtime.healthPath ?? null),
      app.enabled,
      app.uid,
      app.gid,
      app.home,
      isPhpApp(app) ? app.documentRoot : ".",
      isPhpApp(app) ? app.entrypointMode : "front-controller",
      isPhpApp(app) ? app.phpVersion : state.defaults.phpVersion,
      isPhpApp(app) ? app.phpService : compatibilityPhp.service,
      isPhpApp(app) ? app.fpmProfile : state.defaults.fpmProfile,
      ...tls,
      app.accessLog,
      app.redis.mode,
      app.redis.prefix,
      app.redis.password ?? null,
      app.redis.aclUsername ?? null,
      app.redis.aclPassword ?? null,
      app.deploy.enabled,
      app.deploy.hmacSecret ?? null,
      app.deploy.queuePolicy,
      app.deploy.timeoutSec,
      app.deploy.workdir,
      JSON.stringify(app.deploy.argv),
      ...vhost,
      ...pool,
      app.createdAt,
      app.updatedAt,
    ],
  );
  for (const [position, binding] of app.databases.entries()) {
    const changes =
      binding.engine === "mysql" || binding.engine === "postgres"
        ? run(
            database,
            `INSERT INTO app_database_bindings (
              app_slug, position, engine, service, username, password
            ) VALUES (?, ?, ?, ?, ?, ?)`,
            [app.slug, position, binding.engine, binding.service, binding.user, binding.password],
          )
        : run(
            database,
            `INSERT INTO app_database_bindings (
              app_slug, position, engine, file_id, file_path, file_created_at,
              backup_verified_at, vacuum_day_of_week, vacuum_hour, vacuum_minute
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
            [
              app.slug,
              position,
              binding.engine,
              binding.file.id,
              binding.file.path,
              binding.file.createdAt,
              binding.engine === "litestream" ? (binding.backupVerifiedAt ?? null) : null,
              binding.engine === "sqlite" ? (binding.vacuumSchedule?.dayOfWeek ?? null) : null,
              binding.engine === "sqlite" ? (binding.vacuumSchedule?.hour ?? null) : null,
              binding.engine === "sqlite" ? (binding.vacuumSchedule?.minute ?? null) : null,
            ],
          );
    if (binding.engine === "mysql" || binding.engine === "postgres") {
      const bindingId = Number(changes.lastInsertRowid);
      for (const [databasePosition, appDatabase] of binding.databases.entries()) {
        run(
          database,
          "INSERT INTO app_databases (binding_id, position, name, created_at) VALUES (?, ?, ?, ?)",
          [bindingId, databasePosition, appDatabase.name, appDatabase.createdAt],
        );
      }
    }
  }
}

function insertProxy(database: Database, proxy: ProxySite): void {
  const tls = tlsColumns(proxy.tls);
  run(
    database,
    `INSERT INTO proxy_sites (
      name, enabled, tls_kind, tls_cert_path, tls_key_path, access_log, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
    [proxy.name, proxy.enabled, ...tls, proxy.accessLog, proxy.createdAt, proxy.updatedAt],
  );
  insertValues(database, "proxy_upstreams", ["proxy_name"], [proxy.name], proxy.upstreams);
}

function insertDomains(database: Database, state: DesiredState): void {
  const byOwner = new Map<string, [string, DomainOwner][]>();
  for (const entry of Object.entries(state.domains)) {
    const owner = entry[1];
    const ownerId = owner.kind === "app" ? `app:${owner.slug}` : `proxy:${owner.name}`;
    const entries = byOwner.get(ownerId) ?? [];
    entries.push(entry);
    byOwner.set(ownerId, entries);
  }

  for (const [ownerId, entries] of [...byOwner.entries()].sort(([left], [right]) =>
    left.localeCompare(right),
  )) {
    const proxy = ownerId.startsWith("proxy:")
      ? state.proxies[ownerId.slice("proxy:".length)]
      : undefined;
    const aliasOrder = new Map(proxy?.aliases.map((domain, index) => [String(domain), index]));
    entries.sort((left, right) => {
      const primary = Number(right[1].primary) - Number(left[1].primary);
      if (primary !== 0) return primary;
      const leftPosition = aliasOrder.get(left[0]);
      const rightPosition = aliasOrder.get(right[0]);
      if (leftPosition !== undefined || rightPosition !== undefined) {
        return (
          (leftPosition ?? Number.MAX_SAFE_INTEGER) - (rightPosition ?? Number.MAX_SAFE_INTEGER)
        );
      }
      return left[0].localeCompare(right[0]);
    });

    for (const [position, [domain, owner]] of entries.entries()) {
      run(
        database,
        `INSERT INTO domains (
          domain, owner_kind, app_slug, proxy_name, primary_domain, owner_position
        ) VALUES (?, ?, ?, ?, ?, ?)`,
        [
          domain,
          owner.kind,
          owner.kind === "app" ? owner.slug : null,
          owner.kind === "proxy" ? owner.name : null,
          owner.primary,
          position,
        ],
      );
    }
  }
}

function tlsColumns(tls: TlsMode): SQLQueryBindings[] {
  return tls.kind === "external" ? [tls.kind, tls.certPath, tls.keyPath] : [tls.kind, null, null];
}

function tlsFromColumns(row: {
  tls_kind: TlsMode["kind"];
  tls_cert_path: string | null;
  tls_key_path: string | null;
}): Record<string, unknown> {
  return row.tls_kind === "external"
    ? { kind: "external", certPath: row.tls_cert_path, keyPath: row.tls_key_path }
    : { kind: row.tls_kind };
}

function templateColumns(template: TemplateProvenance): SQLQueryBindings[] {
  return template.kind === "custom"
    ? [template.kind, template.sourcePath, template.copiedFromVersion ?? null, template.activatedAt]
    : [template.kind, null, null, null];
}

function templateFromColumns(row: AppRow, prefix: "vhost" | "pool"): Record<string, unknown> {
  const kind = row[`${prefix}_template_kind`];
  if (kind === "upstream") return { kind };
  const sourcePath = row[`${prefix}_template_source_path`];
  const copiedFromVersion = row[`${prefix}_template_copied_from_version`];
  const activatedAt = row[`${prefix}_template_activated_at`];
  return {
    kind,
    sourcePath,
    ...(copiedFromVersion !== null ? { copiedFromVersion } : {}),
    activatedAt,
  };
}

function insertValues(
  database: Database,
  table: "proxy_upstreams",
  ownerColumns: string[],
  owners: SQLQueryBindings[],
  values: string[],
): void {
  const columns = [...ownerColumns, "position", "value"];
  const sql = `INSERT INTO ${table} (${columns.join(", ")}) VALUES (${placeholders(columns.length)})`;
  for (const [position, value] of values.entries()) {
    run(database, sql, [...owners, position, value]);
  }
}

function groupedValues(rows: PositionValueRow[]): Map<string, string[]> {
  const grouped = new Map<string, string[]>();
  for (const row of rows) {
    const values = grouped.get(row.owner) ?? [];
    values.push(row.value);
    grouped.set(row.owner, values);
  }
  return grouped;
}

function parseJsonArray(value: string): unknown {
  return JSON.parse(value) as unknown;
}

function placeholders(count: number): string {
  return Array.from({ length: count }, () => "?").join(", ");
}

function run(database: Database, sql: string, bindings: SQLQueryBindings[]) {
  return database.run(sql, bindings);
}

function asBoolean(value: number): boolean {
  return value === 1;
}
