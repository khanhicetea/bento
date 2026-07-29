/** Runtime validation and explicit migrations to the current state schema. */
import { z } from "zod";
import {
  type AppDatabase,
  type AppDeployConfig,
  type AppRedisIdentity,
  type AppState,
  createEmptyState,
  type CronJob,
  type DesiredState,
  type DomainOwner,
  type EntrypointMode,
  type ManagedDatabaseService,
  type ManagedPhpVersion,
  type ProxySite,
  type QueuePolicy,
  type RedisMode,
  type StackDefaults,
  type TemplateProvenance,
  type TlsMode,
  type Worker,
} from "../domain/state.ts";
import {
  asAbsoluteAppPath,
  asAppSlug,
  asCronJobName,
  asDatabaseName,
  asDatabaseService,
  asDomainName,
  asFpmProfile,
  asGid,
  asMysqlVersion,
  asPhpVersion,
  asPostgresVersion,
  asProxySiteName,
  asUid,
  asWorkerName,
} from "../domain/types.ts";
import { STATE_SCHEMA_VERSION } from "../version.ts";
import { stateError, validationError } from "../domain/errors.ts";
import {
  absolutePathSchema,
  appSlugSchema,
  cronScheduleSchema,
  databaseServiceSchema,
  domainNameSchema,
  err,
  fpmProfileSchema,
  fromZod,
  isoDateSchema,
  mysqlVersionSchema,
  nonEmptyStringSchema,
  ok,
  type ParseResult,
  phpVersionSchema,
  positiveIntSchema,
  postgresVersionSchema,
  safeRelativePathSchema,
  stringArraySchema,
  uidGidSchema,
  unwrap,
} from "./validators.ts";

const strict = <T extends z.ZodRawShape>(shape: T) => z.object(shape).strict();
const tlsModeSchema: z.ZodType<TlsMode> = z.discriminatedUnion("kind", [
  strict({ kind: z.literal("self-ca") }),
  strict({ kind: z.literal("shared") }),
  strict({ kind: z.literal("acme") }),
  strict({
    kind: z.literal("external"),
    certPath: nonEmptyStringSchema,
    keyPath: nonEmptyStringSchema,
  }),
]);
const templateProvenanceSchema: z.ZodType<TemplateProvenance> = z.union([
  strict({ kind: z.literal("upstream") }),
  strict({
    kind: z.literal("custom"),
    sourcePath: nonEmptyStringSchema,
    activatedAt: isoDateSchema,
    copiedFromVersion: nonEmptyStringSchema.optional(),
  }),
]);
const deploySchema = strict({
  enabled: z.boolean(),
  queuePolicy: z.enum(["latest", "fifo"]).default("latest"),
  timeoutSec: positiveIntSchema.default(900),
  workdir: nonEmptyStringSchema,
  argv: stringArraySchema.min(1),
  hmacSecret: nonEmptyStringSchema.optional(),
});
const redisSchema = strict({
  mode: z.enum(["shared", "acl"]),
  prefix: nonEmptyStringSchema,
  password: z.string().optional(),
  aclUsername: nonEmptyStringSchema.optional(),
  aclPassword: z.string().optional(),
});
const databaseSchema = strict({
  name: nonEmptyStringSchema.regex(/^[a-zA-Z0-9_]+$/, "must be alphanumeric/underscore"),
  createdAt: isoDateSchema,
});
const sqliteBackupSchema = strict({
  provider: z.literal("litestream"),
  destination: nonEmptyStringSchema,
  syncInterval: z.enum(["1s", "10s", "60s"]),
  snapshotInterval: nonEmptyStringSchema,
  snapshotRetention: nonEmptyStringSchema,
  l0Retention: nonEmptyStringSchema,
  enabled: z.boolean(),
});
const bindingSchema = z.discriminatedUnion("engine", [
  strict({
    engine: z.literal("mysql"),
    service: databaseServiceSchema.regex(/^mysql\d+$/),
    user: nonEmptyStringSchema,
    password: z.string(),
    databases: z.array(databaseSchema).default([]),
  }),
  strict({
    engine: z.literal("postgres"),
    service: databaseServiceSchema.regex(/^postgres\d+$/),
    user: nonEmptyStringSchema,
    password: z.string(),
    databases: z.array(databaseSchema).default([]),
  }),
  strict({
    engine: z.literal("sqlite"),
    file: strict({
      id: nonEmptyStringSchema,
      path: safeRelativePathSchema.refine(
        (path) =>
          path === "data/sqlite/database.sqlite" ||
          /^sqlite\/[a-z0-9_]+\/database\.sqlite$/.test(path),
        { message: "SQLite path must be the managed sqlite/<file-id>/database.sqlite path" },
      ),
      createdAt: isoDateSchema,
    }),
    backupVerifiedAt: isoDateSchema.optional(),
  }),
]);
const appBase = {
  slug: appSlugSchema,
  enabled: z.boolean().default(true),
  uid: uidGidSchema,
  gid: uidGidSchema,
  home: absolutePathSchema,
  mainDomain: domainNameSchema,
  aliases: z.array(domainNameSchema).default([]),
  documentRoot: safeRelativePathSchema.default(""),
  entrypointMode: z.enum(["front-controller", "legacy"]),
  phpVersion: phpVersionSchema,
  phpService: nonEmptyStringSchema,
  fpmProfile: fpmProfileSchema,
  tls: tlsModeSchema,
  accessLog: z.boolean().default(false),
  redis: redisSchema,
  deploy: deploySchema,
  vhostTemplate: templateProvenanceSchema.default({ kind: "upstream" }),
  poolTemplate: templateProvenanceSchema.default({ kind: "upstream" }),
  createdAt: isoDateSchema,
  updatedAt: isoDateSchema,
};
const appSchema = strict({ ...appBase, database: bindingSchema });
const v1AppSchema = strict({
  ...appBase,
  mysqlService: nonEmptyStringSchema,
  mysqlUser: nonEmptyStringSchema,
  mysqlPassword: z.string(),
  databases: z.array(databaseSchema).default([]),
});
const proxySchema = strict({
  name: appSlugSchema,
  mainDomain: domainNameSchema,
  aliases: z.array(domainNameSchema).default([]),
  upstreams: z.array(nonEmptyStringSchema).min(1),
  tls: tlsModeSchema,
  accessLog: z.boolean().default(false),
  createdAt: isoDateSchema,
  updatedAt: isoDateSchema,
});
const domainOwnerSchema = z.discriminatedUnion("kind", [
  strict({ kind: z.literal("app"), slug: appSlugSchema }),
  strict({ kind: z.literal("proxy"), name: appSlugSchema }),
]);
const cronJobSchema = strict({
  name: nonEmptyStringSchema,
  app: appSlugSchema,
  schedule: cronScheduleSchema,
  timezone: nonEmptyStringSchema.default("UTC"),
  workdir: nonEmptyStringSchema,
  command: stringArraySchema.min(1),
  commandMode: z.enum(["argv", "shell"]).default("argv"),
  output: z.enum(["log", "null", "inherit"]).default("log"),
  enabled: z.boolean().default(true),
  timeoutSec: positiveIntSchema.optional(),
  lock: nonEmptyStringSchema.optional(),
});
const workerSchema = strict({
  name: nonEmptyStringSchema,
  app: appSlugSchema,
  command: stringArraySchema.min(1),
  workdir: nonEmptyStringSchema,
  enabled: z.boolean().default(true),
  autorestart: z.boolean().default(true),
  stopsignal: nonEmptyStringSchema.default("TERM"),
  stopwaitsecs: positiveIntSchema.default(10),
});
const defaultsSchema = strict({
  phpVersion: phpVersionSchema,
  database: z.discriminatedUnion("engine", [
    strict({
      engine: z.literal("mysql"),
      version: mysqlVersionSchema,
      service: databaseServiceSchema.regex(/^mysql\d+$/),
    }),
    strict({
      engine: z.literal("postgres"),
      version: postgresVersionSchema,
      service: databaseServiceSchema.regex(/^postgres\d+$/),
    }),
  ]),
  fpmProfile: fpmProfileSchema,
  redisMode: z.enum(["shared", "acl"]).default("shared"),
});
const managedPhpSchema = strict({
  version: phpVersionSchema,
  service: nonEmptyStringSchema,
  image: nonEmptyStringSchema,
  processCap: positiveIntSchema.default(200),
});
const managedDatabaseSchema = z.discriminatedUnion("engine", [
  strict({
    engine: z.literal("mysql"),
    version: mysqlVersionSchema,
    service: databaseServiceSchema.regex(/^mysql\d+$/),
    image: nonEmptyStringSchema,
    volume: nonEmptyStringSchema,
  }),
  strict({
    engine: z.literal("postgres"),
    version: postgresVersionSchema,
    service: databaseServiceSchema.regex(/^postgres\d+$/),
    image: nonEmptyStringSchema,
    volume: nonEmptyStringSchema,
  }),
]);
const commonState = {
  proxies: z.record(z.string(), proxySchema),
  domains: z.record(z.string(), domainOwnerSchema),
  cronJobs: z.array(cronJobSchema),
  workers: z.array(workerSchema),
  createdAt: isoDateSchema,
  updatedAt: isoDateSchema,
};
const desiredStateRawSchema = strict({
  schemaVersion: z.literal(STATE_SCHEMA_VERSION),
  defaults: defaultsSchema,
  phpVersions: z.array(managedPhpSchema).min(1),
  databaseServices: z.array(managedDatabaseSchema).min(1),
  sqliteBackup: sqliteBackupSchema.optional(),
  apps: z.record(z.string(), appSchema),
  ...commonState,
}).superRefine((state, ctx) => {
  const services = new Set(state.databaseServices.map((s) => s.service));
  const defaultManaged = state.databaseServices.find((s) =>
    s.service === state.defaults.database.service
  );
  if (!defaultManaged) {
    ctx.addIssue({
      code: "custom",
      path: ["defaults", "database", "service"],
      message: "must reference a managed service",
    });
  } else if (
    defaultManaged.engine !== state.defaults.database.engine ||
    defaultManaged.version !== state.defaults.database.version
  ) {
    ctx.addIssue({
      code: "custom",
      path: ["defaults", "database"],
      message: "engine and version must match the managed service",
    });
  }
  for (const [slug, app] of Object.entries(state.apps)) {
    if (app.database.engine === "sqlite") continue;
    if (!services.has(app.database.service)) {
      ctx.addIssue({
        code: "custom",
        path: ["apps", slug, "database", "service"],
        message: "must reference a managed service",
      });
    }
    const relational = app.database as Exclude<typeof app.database, { engine: "sqlite" }>;
    const managed = state.databaseServices.find((s) => s.service === relational.service);
    if (managed && managed.engine !== app.database.engine) {
      ctx.addIssue({
        code: "custom",
        path: ["apps", slug, "database", "engine"],
        message: "must match managed service engine",
      });
    }
  }
});
const v1StateSchema = strict({
  schemaVersion: z.literal(1),
  defaults: strict({
    phpVersion: phpVersionSchema,
    mysqlVersion: mysqlVersionSchema,
    fpmProfile: fpmProfileSchema,
    redisMode: z.enum(["shared", "acl"]).default("shared"),
  }),
  phpVersions: z.array(managedPhpSchema).min(1),
  mysqlVersions: z.array(
    strict({
      version: mysqlVersionSchema,
      service: nonEmptyStringSchema,
      image: nonEmptyStringSchema,
      volume: nonEmptyStringSchema,
    }),
  ).min(1),
  apps: z.record(z.string(), v1AppSchema),
  ...commonState,
});

function brandDatabase(db: z.infer<typeof databaseSchema>): AppDatabase {
  return { name: asDatabaseName(db.name), createdAt: db.createdAt };
}
function brandDeploy(d: z.infer<typeof deploySchema>): AppDeployConfig {
  return {
    enabled: d.enabled,
    queuePolicy: d.queuePolicy as QueuePolicy,
    timeoutSec: d.timeoutSec,
    workdir: d.workdir,
    argv: d.argv,
    ...(d.hmacSecret ? { hmacSecret: d.hmacSecret } : {}),
  };
}
function brandRedis(r: z.infer<typeof redisSchema>): AppRedisIdentity {
  return {
    mode: r.mode as RedisMode,
    prefix: r.prefix,
    ...(r.password !== undefined ? { password: r.password } : {}),
    ...(r.aclUsername ? { aclUsername: r.aclUsername } : {}),
    ...(r.aclPassword !== undefined ? { aclPassword: r.aclPassword } : {}),
  };
}
function brandApp(app: z.infer<typeof appSchema>): AppState {
  const database: AppState["database"] = app.database.engine === "sqlite"
    ? ({
      engine: "sqlite" as const,
      file: app.database.file,
      ...(app.database.backupVerifiedAt ? { backupVerifiedAt: app.database.backupVerifiedAt } : {}),
    } as AppState["database"])
    : {
      engine: app.database.engine,
      service: asDatabaseService(app.database.service),
      user: app.database.user,
      password: app.database.password,
      databases: app.database.databases.map(brandDatabase),
    };
  return {
    slug: asAppSlug(app.slug),
    enabled: app.enabled,
    uid: asUid(app.uid),
    gid: asGid(app.gid),
    home: asAbsoluteAppPath(app.home),
    mainDomain: asDomainName(app.mainDomain),
    aliases: app.aliases.map(asDomainName),
    documentRoot: app.documentRoot,
    entrypointMode: app.entrypointMode as EntrypointMode,
    phpVersion: asPhpVersion(app.phpVersion),
    phpService: app.phpService,
    fpmProfile: asFpmProfile(app.fpmProfile),
    tls: app.tls,
    accessLog: app.accessLog,
    database,
    redis: brandRedis(app.redis),
    deploy: brandDeploy(app.deploy),
    vhostTemplate: app.vhostTemplate,
    poolTemplate: app.poolTemplate,
    createdAt: app.createdAt,
    updatedAt: app.updatedAt,
  };
}
function brandProxy(p: z.infer<typeof proxySchema>): ProxySite {
  return {
    name: asProxySiteName(p.name),
    mainDomain: asDomainName(p.mainDomain),
    aliases: p.aliases.map(asDomainName),
    upstreams: p.upstreams,
    tls: p.tls,
    accessLog: p.accessLog,
    createdAt: p.createdAt,
    updatedAt: p.updatedAt,
  };
}
function brandOwner(o: z.infer<typeof domainOwnerSchema>): DomainOwner {
  return o.kind === "app"
    ? { kind: "app", slug: asAppSlug(o.slug) }
    : { kind: "proxy", name: asProxySiteName(o.name) };
}
function brandCron(j: z.infer<typeof cronJobSchema>): CronJob {
  return {
    name: asCronJobName(j.name),
    app: asAppSlug(j.app),
    schedule: j.schedule,
    timezone: j.timezone,
    workdir: j.workdir,
    command: j.command,
    commandMode: j.commandMode,
    output: j.output,
    enabled: j.enabled,
    ...(j.timeoutSec !== undefined ? { timeoutSec: j.timeoutSec } : {}),
    ...(j.lock ? { lock: j.lock } : {}),
  };
}
function brandWorker(w: z.infer<typeof workerSchema>): Worker {
  return {
    name: asWorkerName(w.name),
    app: asAppSlug(w.app),
    command: w.command,
    workdir: w.workdir,
    enabled: w.enabled,
    autorestart: w.autorestart,
    stopsignal: w.stopsignal,
    stopwaitsecs: w.stopwaitsecs,
  };
}
function brandDefaults(d: z.infer<typeof defaultsSchema>): StackDefaults {
  return {
    phpVersion: asPhpVersion(d.phpVersion),
    database: d.database.engine === "mysql"
      ? {
        engine: "mysql",
        version: asMysqlVersion(d.database.version),
        service: asDatabaseService(d.database.service),
      }
      : {
        engine: "postgres",
        version: asPostgresVersion(d.database.version),
        service: asDatabaseService(d.database.service),
      },
    fpmProfile: asFpmProfile(d.fpmProfile),
    redisMode: d.redisMode as RedisMode,
  };
}
function brandManaged(v: z.infer<typeof managedDatabaseSchema>): ManagedDatabaseService {
  return v.engine === "mysql"
    ? { ...v, version: asMysqlVersion(v.version), service: asDatabaseService(v.service) }
    : { ...v, version: asPostgresVersion(v.version), service: asDatabaseService(v.service) };
}

export function parseDesiredState(value: unknown): ParseResult<DesiredState> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return err("state must be a JSON object");
  }
  const version = (value as Record<string, unknown>).schemaVersion;
  if (typeof version !== "number" || !Number.isInteger(version)) {
    return err("schemaVersion must be an integer");
  }
  if (version !== STATE_SCHEMA_VERSION) {
    return err(`unsupported state schemaVersion ${version}; expected ${STATE_SCHEMA_VERSION}`);
  }
  const parsed = fromZod(desiredStateRawSchema.safeParse(value));
  if (!parsed.ok) return parsed;
  const raw = parsed.value;
  const apps: Record<string, AppState> = {};
  for (const [key, app] of Object.entries(raw.apps)) {
    if (app.slug !== key) return err(`apps.${key}: key must match slug ${app.slug}`);
    apps[key] = brandApp(app);
  }
  const proxies: Record<string, ProxySite> = {};
  for (const [key, p] of Object.entries(raw.proxies)) {
    if (p.name !== key) return err(`proxies.${key}: key must match name ${p.name}`);
    proxies[key] = brandProxy(p);
  }
  const domains: Record<string, DomainOwner> = {};
  for (const [key, o] of Object.entries(raw.domains)) domains[key.toLowerCase()] = brandOwner(o);
  return ok({
    schemaVersion: STATE_SCHEMA_VERSION,
    defaults: brandDefaults(raw.defaults),
    phpVersions: raw.phpVersions.map((v): ManagedPhpVersion => ({
      version: asPhpVersion(v.version),
      service: v.service,
      image: v.image,
      processCap: v.processCap,
    })),
    databaseServices: raw.databaseServices.map(brandManaged),
    ...(raw.sqliteBackup ? { sqliteBackup: raw.sqliteBackup } : {}),
    apps,
    proxies,
    domains,
    cronJobs: raw.cronJobs.map(brandCron),
    workers: raw.workers.map(brandWorker),
    createdAt: raw.createdAt,
    updatedAt: raw.updatedAt,
  });
}

/** Pure migration: validates v1, transforms it, then validates the complete v2 result. */
export function migrateV1ToV2(value: unknown): ParseResult<DesiredState> {
  const parsed = fromZod(v1StateSchema.safeParse(value));
  if (!parsed.ok) return parsed;
  const old = parsed.value;
  for (const [key, app] of Object.entries(old.apps)) {
    if (app.slug !== key) return err(`apps.${key}: key must match slug ${app.slug}`);
  }
  const defaultService = old.mysqlVersions.find((v) => v.version === old.defaults.mysqlVersion)
    ?.service;
  if (!defaultService) return err("defaults.mysqlVersion must reference a managed MySQL version");
  const apps = Object.fromEntries(
    Object.entries(old.apps).map(([key, app]) => [key, {
      slug: app.slug,
      enabled: app.enabled,
      uid: app.uid,
      gid: app.gid,
      home: app.home,
      mainDomain: app.mainDomain,
      aliases: app.aliases,
      documentRoot: app.documentRoot,
      entrypointMode: app.entrypointMode,
      phpVersion: app.phpVersion,
      phpService: app.phpService,
      fpmProfile: app.fpmProfile,
      tls: app.tls,
      accessLog: app.accessLog,
      database: {
        engine: "mysql",
        service: app.mysqlService,
        user: app.mysqlUser,
        password: app.mysqlPassword,
        databases: app.databases,
      },
      redis: app.redis,
      deploy: app.deploy,
      vhostTemplate: app.vhostTemplate,
      poolTemplate: app.poolTemplate,
      createdAt: app.createdAt,
      updatedAt: app.updatedAt,
    }]),
  );
  return parseDesiredState({
    schemaVersion: STATE_SCHEMA_VERSION,
    defaults: {
      phpVersion: old.defaults.phpVersion,
      database: {
        engine: "mysql",
        version: old.defaults.mysqlVersion,
        service: defaultService,
      },
      fpmProfile: old.defaults.fpmProfile,
      redisMode: old.defaults.redisMode,
    },
    phpVersions: old.phpVersions,
    databaseServices: old.mysqlVersions.map((v) => ({ engine: "mysql", ...v })),
    apps,
    proxies: old.proxies,
    domains: old.domains,
    cronJobs: old.cronJobs,
    workers: old.workers,
    createdAt: old.createdAt,
    updatedAt: old.updatedAt,
  });
}
/** Pure v2 -> v3 migration. Relational bindings are preserved byte-for-value. */
export function migrateV2ToV3(value: unknown): ParseResult<DesiredState> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return err("state must be a JSON object");
  }
  const raw = value as Record<string, unknown>;
  if (raw.schemaVersion !== 2) return err("expected schemaVersion 2");
  return parseDesiredState({ ...raw, schemaVersion: STATE_SCHEMA_VERSION });
}

export function loadV1StateFromJson(text: string): DesiredState {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (cause) {
    throw validationError("state.json is not valid JSON", { cause: String(cause) });
  }
  const result = migrateV1ToV2(raw);
  if (!result.ok) throw stateError(`invalid schema v1 desired state: ${result.errors.join("; ")}`);
  return result.value;
}
export function loadStateFromJson(text: string): DesiredState {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (cause) {
    throw validationError("state.json is not valid JSON", { cause: String(cause) });
  }
  const result = parseDesiredState(raw);
  if (!result.ok) {
    throw stateError(`invalid desired state: ${result.errors.join("; ")}`, {
      recovery: "Fix state.json or restore from backup. Bento will not overwrite invalid state.",
    });
  }
  return result.value;
}
export function stateToJson(state: DesiredState): string {
  return `${JSON.stringify(state, null, 2)}\n`;
}
export function emptyStateJson(now?: string): string {
  return stateToJson(createEmptyState(now));
}
export { createEmptyState, unwrap };
