/** Authoritative in-memory desired-state domain model. */

import type {
  AbsoluteAppPath,
  AppSlug,
  CronJobName,
  DatabaseName,
  DatabaseService,
  DomainName,
  FpmProfile,
  Gid,
  MysqlVersion,
  PhpVersion,
  PostgresVersion,
  ProxySiteName,
  Uid,
  WorkerName,
} from "./types.ts";
import {
  asDatabaseService,
  asFpmProfile,
  asMysqlVersion,
  asPhpVersion,
  DEFAULT_FPM_PROFILE,
  DEFAULT_MYSQL_VERSION,
  DEFAULT_PHP_VERSION,
} from "./types.ts";
import { STATE_SCHEMA_VERSION } from "../version.ts";

export type DatabaseEngine = "mysql" | "postgres" | "sqlite" | "litestream";
export type SqliteBackupPolicy = {
  provider: "litestream";
  destination: string;
  syncInterval: string;
  snapshotInterval: string;
  snapshotRetention: string;
  l0Retention: string;
  enabled: boolean;
};
export type TlsMode =
  | { kind: "self-ca" }
  | { kind: "shared" }
  | { kind: "acme" }
  | { kind: "external"; certPath: string; keyPath: string };
export type EntrypointMode = "front-controller" | "legacy";
export type RedisMode = "shared" | "acl";
export type QueuePolicy = "latest" | "fifo";
export type DeployStatus = "queued" | "running" | "success" | "failed" | "skipped";
export type TemplateProvenance =
  | { kind: "upstream" }
  | { kind: "custom"; sourcePath: string; copiedFromVersion?: string; activatedAt: string };
export type DomainOwner =
  | { kind: "app"; slug: AppSlug; primary: boolean }
  | { kind: "proxy"; name: ProxySiteName; primary: boolean };

export type AppDeployConfig = {
  enabled: boolean;
  hmacSecret?: string;
  queuePolicy: QueuePolicy;
  timeoutSec: number;
  workdir: string;
  argv: string[];
};
export type AppRedisIdentity = {
  mode: RedisMode;
  prefix: string;
  password?: string;
  aclUsername?: string;
  aclPassword?: string;
};
export type AppDatabase = { name: DatabaseName; createdAt: string };
export type SqliteVacuumSchedule = {
  dayOfWeek: number;
  hour: number;
  minute: number;
};

export type AppDatabaseBinding =
  | {
    engine: "mysql";
    service: DatabaseService;
    user: string;
    password: string;
    databases: AppDatabase[];
  }
  | {
    engine: "postgres";
    service: DatabaseService;
    user: string;
    password: string;
    databases: AppDatabase[];
  }
  | {
    /** Local SQLite file maintained weekly by the app runner. */
    engine: "sqlite";
    file: { id: string; path: string; createdAt: string };
    /** Stable weekly local-time slot selected when the file is created. */
    vacuumSchedule?: SqliteVacuumSchedule;
    /** Type-only compatibility members; file bindings never persist these. */
    service: DatabaseService;
    user: string;
    password: string;
    databases: AppDatabase[];
  }
  | {
    /** SQLite file continuously replicated by the stack Litestream service. */
    engine: "litestream";
    file: { id: string; path: string; createdAt: string };
    backupVerifiedAt?: string;
    /** Type-only compatibility members; file bindings never persist these. */
    service: DatabaseService;
    user: string;
    password: string;
    databases: AppDatabase[];
  };

export type AppState = {
  slug: AppSlug;
  enabled: boolean;
  uid: Uid;
  gid: Gid;
  home: AbsoluteAppPath;
  documentRoot: string;
  entrypointMode: EntrypointMode;
  phpVersion: PhpVersion;
  phpService: string;
  fpmProfile: FpmProfile;
  tls: TlsMode;
  accessLog: boolean;
  databases: AppDatabaseBinding[];
  /**
   * Derived in-memory primary binding. This is not persisted; new code should
   * use `databases` or `databaseBindings`.
   */
  database: AppDatabaseBinding;
  /** Derived in-memory primary domain; domain links remain authoritative. */
  mainDomain: DomainName;
  /** Derived in-memory non-primary domains; domain links remain authoritative. */
  aliases: DomainName[];
  redis: AppRedisIdentity;
  deploy: AppDeployConfig;
  vhostTemplate: TemplateProvenance;
  poolTemplate: TemplateProvenance;
  createdAt: string;
  updatedAt: string;
};
export type ProxySite = {
  name: ProxySiteName;
  mainDomain: DomainName;
  aliases: DomainName[];
  upstreams: string[];
  tls: TlsMode;
  accessLog: boolean;
  createdAt: string;
  updatedAt: string;
};
export type CronJob = {
  name: CronJobName;
  app: AppSlug;
  schedule: string;
  timezone: string;
  workdir: string;
  command: string[];
  commandMode: "argv" | "shell";
  output: "log" | "null" | "inherit";
  timeoutSec?: number;
  lock?: string;
  enabled: boolean;
};
export type Worker = {
  name: WorkerName;
  app: AppSlug;
  command: string[];
  workdir: string;
  enabled: boolean;
  autorestart: boolean;
  stopsignal: string;
  stopwaitsecs: number;
};

export type DatabaseDefault =
  | { engine: "mysql"; version: MysqlVersion; service: DatabaseService }
  | { engine: "postgres"; version: PostgresVersion; service: DatabaseService };
export type StackDefaults = {
  phpVersion: PhpVersion;
  database: DatabaseDefault;
  fpmProfile: FpmProfile;
  redisMode: RedisMode;
};
export type ManagedPhpVersion = {
  version: PhpVersion;
  service: string;
  image: string;
  processCap: number;
};
export type ManagedDatabaseService =
  | {
    engine: "mysql";
    version: MysqlVersion;
    service: DatabaseService;
    image: string;
    volume: string;
  }
  | {
    engine: "postgres";
    version: PostgresVersion;
    service: DatabaseService;
    image: string;
    volume: string;
  };
export type ManagedMysqlVersion = Extract<ManagedDatabaseService, { engine: "mysql" }>;
export type ManagedPostgresVersion = Extract<ManagedDatabaseService, { engine: "postgres" }>;

export type DesiredState = {
  schemaVersion: typeof STATE_SCHEMA_VERSION;
  defaults: StackDefaults;
  phpVersions: ManagedPhpVersion[];
  databaseServices: ManagedDatabaseService[];
  sqliteBackup?: SqliteBackupPolicy;
  apps: Record<string, AppState>;
  proxies: Record<string, ProxySite>;
  domains: Record<string, DomainOwner>;
  cronJobs: CronJob[];
  workers: Worker[];
  createdAt: string;
  updatedAt: string;
};

export function phpServiceName(version: PhpVersion): string {
  return `php${String(version).replace(".", "")}`;
}
export function mysqlServiceName(version: MysqlVersion): DatabaseService {
  return asDatabaseService(`mysql${String(version).replace(".", "")}`);
}
export function postgresServiceName(version: PostgresVersion): DatabaseService {
  return asDatabaseService(`postgres${version}`);
}
export function phpImage(version: PhpVersion): string {
  return `bento/php:${version}`;
}
export function mysqlImage(version: MysqlVersion): string {
  return `mysql:${version}`;
}
export function postgresImage(version: PostgresVersion): string {
  return `postgres:${version}`;
}

export function createEmptyState(now: string = new Date().toISOString()): DesiredState {
  const php = DEFAULT_PHP_VERSION;
  const mysql = DEFAULT_MYSQL_VERSION;
  const mysqlService = mysqlServiceName(mysql);
  return {
    schemaVersion: STATE_SCHEMA_VERSION,
    defaults: {
      phpVersion: php,
      database: { engine: "mysql", version: mysql, service: mysqlService },
      fpmProfile: DEFAULT_FPM_PROFILE,
      redisMode: "shared",
    },
    phpVersions: [{
      version: php,
      service: phpServiceName(php),
      image: phpImage(php),
      processCap: 200,
    }],
    databaseServices: [{
      engine: "mysql",
      version: mysql,
      service: mysqlService,
      image: mysqlImage(mysql),
      volume: `${mysqlService}-data`,
    }],
    apps: {},
    proxies: {},
    domains: {},
    cronJobs: [],
    workers: [],
    createdAt: now,
    updatedAt: now,
  };
}

export function managedMysqlServices(state: DesiredState): ManagedMysqlVersion[] {
  return state.databaseServices.filter((v): v is ManagedMysqlVersion => v.engine === "mysql");
}
export function managedPostgresServices(state: DesiredState): ManagedPostgresVersion[] {
  return state.databaseServices.filter((v): v is ManagedPostgresVersion => v.engine === "postgres");
}
export function requireMysqlBinding(
  app: AppState,
): Extract<AppDatabaseBinding, { engine: "mysql" }> {
  const database = databaseBindings(app, "mysql")[0];
  if (!database) throw new Error(`app ${app.slug} has no MySQL database binding`);
  return database;
}
export function assertNever(value: never): never {
  throw new Error(`unsupported database engine: ${JSON.stringify(value)}`);
}
export function listApps(state: DesiredState): AppState[] {
  return Object.values(state.apps).sort((a, b) => a.slug.localeCompare(b.slug));
}
export function getApp(state: DesiredState, slug: string): AppState | undefined {
  return state.apps[slug];
}
export function findDomainOwner(state: DesiredState, domain: string): DomainOwner | undefined {
  return state.domains[domain.toLowerCase()];
}
export function listAppDomains(state: DesiredState, slug: string): DomainName[] {
  return Object.entries(state.domains)
    .filter(([, owner]) => owner.kind === "app" && owner.slug === slug)
    .sort((a, b) => Number(b[1].primary) - Number(a[1].primary) || a[0].localeCompare(b[0]))
    .map(([domain]) => domain as DomainName);
}
export function getAppPrimaryDomain(state: DesiredState, slug: string): DomainName | undefined {
  const primary = Object.entries(state.domains).find(([, owner]) =>
    owner.kind === "app" && owner.slug === slug && owner.primary
  );
  return primary?.[0] as DomainName | undefined;
}
export function withAppRelations(app: AppState, state: DesiredState): AppState {
  const domains = listAppDomains(state, app.slug);
  const mainDomain = getAppPrimaryDomain(state, app.slug) ?? domains[0];
  if (!mainDomain) throw new Error(`app ${app.slug} has no linked domain`);
  return {
    ...app,
    database: primaryDatabase(app),
    mainDomain,
    aliases: domains.filter((domain) => domain !== mainDomain),
  };
}
export function primaryDatabase(app: AppState): AppDatabaseBinding {
  const database = app.databases[0];
  if (!database) throw new Error(`app ${app.slug} has no database binding`);
  return database;
}
export function databaseBindings<E extends DatabaseEngine>(
  app: AppState,
  engine: E,
): Extract<AppDatabaseBinding, { engine: E }>[] {
  return app.databases.filter(
    (database): database is Extract<AppDatabaseBinding, { engine: E }> =>
      database.engine === engine,
  );
}
export function assertKnownPhpVersion(state: DesiredState, version: PhpVersion): ManagedPhpVersion {
  const found = state.phpVersions.find((v) => v.version === version);
  if (!found) throw new Error(`PHP version ${version} is not managed`);
  return found;
}
export function assertKnownMysqlService(
  state: DesiredState,
  service: DatabaseService,
): ManagedMysqlVersion {
  const found = managedMysqlServices(state).find((v) => v.service === service);
  if (!found) throw new Error(`MySQL service ${service} is not managed`);
  return found;
}
export function cloneState(state: DesiredState): DesiredState {
  return structuredClone(state);
}
export function defaultDeployConfig(appHome: string): AppDeployConfig {
  return {
    enabled: false,
    queuePolicy: "latest",
    timeoutSec: 900,
    workdir: appHome,
    argv: ["sh", `${appHome}/.bento/deploy.sh`],
  };
}
export function defaultRedisIdentity(slug: string, mode: RedisMode): AppRedisIdentity {
  return mode === "acl"
    ? { mode: "acl", prefix: `${slug}:`, aclUsername: `app_${slug}` }
    : { mode: "shared", prefix: `${slug}:` };
}
export { asFpmProfile, asMysqlVersion, asPhpVersion };
