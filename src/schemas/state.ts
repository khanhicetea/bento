/** Runtime validation for the current state schema. */
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
  type SqliteVacuumSchedule,
  type StackDefaults,
  type TemplateProvenance,
  type TlsMode,
  withAppRelations,
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
const sqliteVacuumScheduleSchema: z.ZodType<SqliteVacuumSchedule> = strict({
  dayOfWeek: z.number().int().min(0).max(6),
  hour: z.number().int().min(0).max(4),
  minute: z.number().int().min(0).max(59),
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
    engine: z.literal("litestream"),
    file: strict({
      id: nonEmptyStringSchema.regex(/^[a-z][a-z0-9-]{1,31}_[a-f0-9]{10}$/),
      path: safeRelativePathSchema,
      createdAt: isoDateSchema,
    }).refine(
      (file) => {
        const slug = file.id.slice(0, -11);
        return file.path === `sqlite/${file.id}/${slug}.sqlite`;
      },
      {
        message:
          "Litestream path must be sqlite/<app-slug>_<10-random-hex-chars>/<app-slug>.sqlite",
      },
    ),
    backupVerifiedAt: isoDateSchema.optional(),
  }),
  strict({
    engine: z.literal("sqlite"),
    file: strict({
      id: nonEmptyStringSchema.regex(/^[a-z][a-z0-9-]{1,31}_[a-f0-9]{10}$/),
      path: safeRelativePathSchema,
      createdAt: isoDateSchema,
    }).refine(
      (file) => {
        const slug = file.id.slice(0, -11);
        // Legacy schema-v3 SQLite files are now identified as Litestream.
        // New plain SQLite files use .db to stay outside the *.sqlite watcher.
        return file.path === `sqlite/${file.id}/${slug}.db` ||
          file.path === `sqlite/${file.id}/${slug}.sqlite`;
      },
      {
        message:
          "SQLite path must be sqlite/<app-slug>_<10-random-hex-chars>/<app-slug>.(db|sqlite)",
      },
    ),
    vacuumSchedule: sqliteVacuumScheduleSchema.optional(),
    backupVerifiedAt: isoDateSchema.optional(),
  }),
]);
const appBase = {
  slug: appSlugSchema,
  enabled: z.boolean().default(true),
  uid: uidGidSchema,
  gid: uidGidSchema,
  home: absolutePathSchema,
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
const appSchema = strict({
  ...appBase,
  databases: z.array(bindingSchema).min(1),
}).superRefine((app, ctx) => {
  const identities = new Set<string>();
  for (const [index, database] of app.databases.entries()) {
    if (
      (database.engine === "sqlite" || database.engine === "litestream") &&
      !database.file.id.startsWith(`${app.slug}_`)
    ) {
      ctx.addIssue({
        code: "custom",
        path: ["databases", index, "file", "id"],
        message: "SQLite file identity must belong to the app slug",
      });
    }
    const identity = database.engine === "sqlite" || database.engine === "litestream"
      ? database.file.id
      : `${database.engine}:${database.service}`;
    if (identities.has(identity)) {
      ctx.addIssue({
        code: "custom",
        path: ["databases", index],
        message: `duplicate database binding ${identity}`,
      });
    }
    identities.add(identity);
  }
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
  strict({ kind: z.literal("app"), slug: appSlugSchema, primary: z.boolean() }),
  strict({ kind: z.literal("proxy"), name: appSlugSchema, primary: z.boolean() }),
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
    for (const [index, database] of app.databases.entries()) {
      if (database.engine === "sqlite" || database.engine === "litestream") continue;
      if (!services.has(database.service)) {
        ctx.addIssue({
          code: "custom",
          path: ["apps", slug, "databases", index, "service"],
          message: "must reference a managed service",
        });
      }
      const managed = state.databaseServices.find((s) => s.service === database.service);
      if (managed && managed.engine !== database.engine) {
        ctx.addIssue({
          code: "custom",
          path: ["apps", slug, "databases", index, "engine"],
          message: "must match managed service engine",
        });
      }
      if (
        new Set(database.databases.map((entry) => entry.name)).size !==
          database.databases.length
      ) {
        ctx.addIssue({
          code: "custom",
          path: ["apps", slug, "databases", index, "databases"],
          message: "database names must be unique within a binding",
        });
      }
    }
    const links = Object.values(state.domains).filter((owner) =>
      owner.kind === "app" && owner.slug === slug
    );
    if (links.filter((owner) => owner.primary).length !== 1) {
      ctx.addIssue({
        code: "custom",
        path: ["domains"],
        message: `app ${slug} must have exactly one primary domain link`,
      });
    }
  }
  for (const [domain, owner] of Object.entries(state.domains)) {
    const parsedDomain = domainNameSchema.safeParse(domain);
    if (!parsedDomain.success || parsedDomain.data !== domain) {
      ctx.addIssue({
        code: "custom",
        path: ["domains", domain],
        message: "key must be a normalized domain name",
      });
    }
    if (owner.kind === "app" && !state.apps[owner.slug]) {
      ctx.addIssue({
        code: "custom",
        path: ["domains", domain],
        message: `references unknown app ${owner.slug}`,
      });
    }
    if (owner.kind === "proxy" && !state.proxies[owner.name]) {
      ctx.addIssue({
        code: "custom",
        path: ["domains", domain],
        message: `references unknown proxy ${owner.name}`,
      });
    }
  }
  for (const [name] of Object.entries(state.proxies)) {
    const links = Object.values(state.domains).filter((owner) =>
      owner.kind === "proxy" && owner.name === name
    );
    if (links.filter((owner) => owner.primary).length !== 1) {
      ctx.addIssue({
        code: "custom",
        path: ["domains"],
        message: `proxy ${name} must have exactly one primary domain link`,
      });
    }
  }
  for (
    const [collection, records] of [
      ["cronJobs", state.cronJobs],
      ["workers", state.workers],
    ] as const
  ) {
    const identities = new Set<string>();
    for (const [index, record] of records.entries()) {
      const identity = `${record.app}:${record.name}`;
      if (identities.has(identity)) {
        ctx.addIssue({
          code: "custom",
          path: [collection, index, "name"],
          message: `duplicate linked record ${identity}`,
        });
      }
      identities.add(identity);
    }
  }
  for (
    const [collection, records] of [
      ["cronJobs", state.cronJobs],
      ["workers", state.workers],
    ] as const
  ) {
    for (const [index, record] of records.entries()) {
      if (!state.apps[record.app]) {
        ctx.addIssue({
          code: "custom",
          path: [collection, index, "app"],
          message: `references unknown app ${record.app}`,
        });
      }
    }
  }
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
  const databases: AppState["databases"] = app.databases.map((database) =>
    database.engine === "sqlite" || database.engine === "litestream"
      ? {
        engine: database.engine,
        file: database.file,
        ...(database.engine === "sqlite" && database.vacuumSchedule
          ? { vacuumSchedule: database.vacuumSchedule }
          : {}),
        ...(database.engine === "litestream" && database.backupVerifiedAt
          ? { backupVerifiedAt: database.backupVerifiedAt }
          : {}),
      } as AppState["databases"][number]
      : {
        engine: database.engine,
        service: asDatabaseService(database.service),
        user: database.user,
        password: database.password,
        databases: database.databases.map(brandDatabase),
      }
  );
  return {
    slug: asAppSlug(app.slug),
    enabled: app.enabled,
    uid: asUid(app.uid),
    gid: asGid(app.gid),
    home: asAbsoluteAppPath(app.home),
    documentRoot: app.documentRoot,
    entrypointMode: app.entrypointMode as EntrypointMode,
    phpVersion: asPhpVersion(app.phpVersion),
    phpService: app.phpService,
    fpmProfile: asFpmProfile(app.fpmProfile),
    tls: app.tls,
    accessLog: app.accessLog,
    databases,
    database: databases[0]!,
    mainDomain: asDomainName("unlinked.invalid"),
    aliases: [],
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
    ? { kind: "app", slug: asAppSlug(o.slug), primary: o.primary }
    : { kind: "proxy", name: asProxySiteName(o.name), primary: o.primary };
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
    apps: Object.fromEntries(
      Object.entries(apps).map(([slug, app]) => [
        slug,
        withAppRelations(app, { apps, domains } as DesiredState),
      ]),
    ),
    proxies,
    domains,
    cronJobs: raw.cronJobs.map(brandCron),
    workers: raw.workers.map(brandWorker),
    createdAt: raw.createdAt,
    updatedAt: raw.updatedAt,
  });
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
  const apps = Object.fromEntries(
    Object.entries(state.apps).map(([slug, app]) => {
      const { database: _database, mainDomain: _mainDomain, aliases: _aliases, ...persisted } = app;
      return [slug, persisted];
    }),
  );
  return `${JSON.stringify({ ...state, apps }, null, 2)}\n`;
}
export function emptyStateJson(now?: string): string {
  return stateToJson(createEmptyState(now));
}
export { createEmptyState, unwrap };
