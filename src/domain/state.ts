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

export type DatabaseEngine = "mysql" | "postgres";
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
  | { kind: "app"; slug: AppSlug }
  | { kind: "proxy"; name: ProxySiteName };

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
  };

export type AppState = {
  slug: AppSlug;
  enabled: boolean;
  uid: Uid;
  gid: Gid;
  home: AbsoluteAppPath;
  mainDomain: DomainName;
  aliases: DomainName[];
  documentRoot: string;
  entrypointMode: EntrypointMode;
  phpVersion: PhpVersion;
  phpService: string;
  fpmProfile: FpmProfile;
  tls: TlsMode;
  accessLog: boolean;
  database: AppDatabaseBinding;
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
  if (app.database.engine !== "mysql") throw new Error(`app ${app.slug} is not MySQL-backed`);
  return app.database;
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
