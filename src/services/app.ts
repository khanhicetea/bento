/**
 * Application provisioning and lifecycle.
 */

import { join } from "@std/path";
import type {
  AppDatabase,
  AppDatabaseBinding,
  AppState,
  DatabaseEngine,
  DesiredState,
  EntrypointMode,
  ManagedDatabaseService,
  TlsMode,
} from "../domain/state.ts";
import { defaultDeployConfig, defaultRedisIdentity } from "../domain/state.ts";
import { primaryDatabase } from "../domain/state.ts";
import {
  asAbsoluteAppPath,
  asAppSlug,
  asDatabaseService,
  asDomainName,
  asFpmProfile,
  asGid,
  asPhpVersion,
  asUid,
  DEFAULT_FPM_PROFILE,
  DEFAULT_UID_BASE,
  FPM_PROFILES,
  type FpmProfile,
  type PhpVersion,
} from "../domain/types.ts";
import { conflictError, notFoundError, safetyError, validationError } from "../domain/errors.ts";
import {
  parseAppSlug,
  parseDomainName,
  parsePhpVersion,
  parseSafeRelativePath,
  unwrap,
} from "../schemas/validators.ts";
import type { Platform } from "../platform/mod.ts";
import { containerAppHome } from "../platform/paths.ts";
import {
  mergeReloadPlans,
  type ReloadPlan,
  reloadPlanForPoolChange,
  reloadPlanForRunnerChange,
} from "../domain/reload.ts";
import { applyAppPermissionPolicy } from "./permissions.ts";
import { applyAppMysqlGrants, isMysqlReachable, tryBestEffortMysqlAccount } from "./mysql.ts";
import {
  applyAppPostgresDatabase,
  isPostgresReachable,
  tryBestEffortPostgresRole,
} from "./postgres.ts";
import { tryApplyAppRedisAcl } from "./redis.ts";
import {
  sqliteContainerPath,
  sqliteHostDir,
  sqliteHostPath,
  sqliteRelativePath,
} from "./sqlite_paths.ts";
import {
  randomSqliteVacuumSchedule,
  resolveSqliteVacuumSchedules,
  sqliteVacuumScheduleSlot,
} from "./sqlite_schedule.ts";
import {
  loadMysqlRootPassword,
  loadPostgresRootPassword,
  loadRedisPassword,
  requireMysqlRootPassword,
  requirePostgresRootPassword,
} from "./stack_env.ts";
import { serviceError } from "../domain/errors.ts";

export type ProvisionAppInput = {
  slug: string;
  domain: string;
  aliases?: string[];
  documentRoot?: string;
  entrypointMode?: EntrypointMode;
  phpVersion?: string;
  fpmProfile?: string;
  databaseEngine?: string;
  /** Managed MySQL version or service; retained as the compatibility shorthand. */
  mysqlVersion?: string;
  /** Managed PostgreSQL major version or service. */
  postgresVersion?: string;
  createDatabase?: boolean;
  databaseName?: string;
  tls?: TlsMode;
  accessLog?: boolean;
};

export type ProvisionAppResult = {
  state: DesiredState;
  app: AppState;
  reloadPlan: ReloadPlan;
  created: boolean;
};

export function provisionApp(
  platform: Platform,
  state: DesiredState,
  input: ProvisionAppInput,
): ProvisionAppResult {
  const slug = unwrap(parseAppSlug(input.slug), "slug");
  const domain = unwrap(parseDomainName(input.domain), "domain");
  const aliases = (input.aliases ?? []).map((a, i) => unwrap(parseDomainName(a), `aliases[${i}]`));

  const existing = state.apps[slug];
  const created = !existing;

  // Domain uniqueness
  const claimed = [domain, ...aliases];
  if (new Set(claimed).size !== claimed.length) {
    throw validationError("primary domain and aliases must not contain duplicates");
  }
  for (const d of claimed) {
    const owner = state.domains[d];
    if (!owner) continue;
    if (owner.kind === "app" && owner.slug === slug) continue;
    throw conflictError(
      `domain ${d} is already owned by ${
        owner.kind === "app" ? `app ${owner.slug}` : `proxy ${owner.name}`
      }`,
    );
  }

  // Runtime selection: omitted choices preserve existing recorded runtime
  const phpVersionStr = input.phpVersion
    ? unwrap(parsePhpVersion(input.phpVersion), "phpVersion")
    : existing?.phpVersion ?? state.defaults.phpVersion;
  const phpVersion = asPhpVersion(String(phpVersionStr));
  const managedPhp = state.phpVersions.find((v) => v.version === phpVersion);
  if (!managedPhp) {
    throw validationError(
      `PHP version ${phpVersion} is not managed. Add it first.`,
    );
  }

  const fpmProfileStr = input.fpmProfile ??
    existing?.fpmProfile ??
    state.defaults.fpmProfile;
  if (!(String(fpmProfileStr) in FPM_PROFILES)) {
    throw validationError(
      `unknown FPM profile ${fpmProfileStr}; choose one of: ${
        Object.keys(FPM_PROFILES).join(", ")
      }`,
    );
  }
  const fpmProfile = asFpmProfile(String(fpmProfileStr));

  const currentPrimary = existing ? primaryDatabase(existing) : undefined;
  const databaseEngine = (input.databaseEngine ?? currentPrimary?.engine ??
    state.defaults.database.engine) as DatabaseEngine;
  const fileDatabase = databaseEngine === "sqlite" || databaseEngine === "litestream";
  const managedDatabase = fileDatabase
    ? undefined
    : resolveAppDatabaseService(state, existing, input);

  // Explicit database requests require a live engine adapter in applyAppDataPlane before save.

  const documentRoot = unwrap(
    parseSafeRelativePath(input.documentRoot ?? existing?.documentRoot ?? "public"),
    "documentRoot",
  );
  const entrypointMode: EntrypointMode = input.entrypointMode ??
    existing?.entrypointMode ??
    "front-controller";
  const tls: TlsMode = input.tls ?? existing?.tls ?? { kind: "shared" };
  const accessLog = input.accessLog ?? existing?.accessLog ?? false;

  // Stable UID/GID
  const { uid, gid } = existing
    ? { uid: existing.uid, gid: existing.gid }
    : allocateIdentity(state);

  const homeContainer = containerAppHome(slug);
  const now = platform.clock.nowIso();

  // Generate once for a new app; all later reconciliation preserves it.
  const existingBinding = fileDatabase
    ? existing?.databases.find((database) => database.engine === databaseEngine)
    : existing?.databases.find((database) =>
      database.engine === managedDatabase?.engine &&
      database.service === managedDatabase.service
    );
  const databasePassword = existingBinding &&
      existingBinding.engine !== "sqlite" && existingBinding.engine !== "litestream"
    ? existingBinding.password
    : platform.random.hex(18);
  const redisPassword = existing?.redis.password ??
    (state.defaults.redisMode === "shared" ? undefined : platform.random.hex(18));

  let redis = existing?.redis ??
    defaultRedisIdentity(slug, state.defaults.redisMode);
  if (!existing) {
    redis = {
      ...redis,
      ...(redisPassword ? { password: redisPassword } : {}),
      ...(redis.mode === "acl"
        ? {
          aclPassword: platform.random.hex(18),
          aclUsername: `app_${slug}`,
        }
        : {}),
    };
  }

  const databases = existingBinding &&
      existingBinding.engine !== "sqlite" && existingBinding.engine !== "litestream"
    ? [...existingBinding.databases]
    : [];
  if (input.createDatabase && !fileDatabase) {
    const dbName = input.databaseName ?? slug;
    if (!/^[a-zA-Z0-9_]+$/.test(dbName)) {
      throw validationError(`invalid database name ${dbName}`);
    }
    // Namespace: app databases must start with slug_ or equal slug
    if (dbName !== slug && !dbName.startsWith(`${slug}_`)) {
      throw validationError(
        `database ${dbName} is outside app namespace; use ${slug} or ${slug}_*`,
      );
    }
    if (!databases.some((d) => d.name === dbName)) {
      databases.push({ name: asDatabaseName(dbName), createdAt: now });
    }
  }

  const selectedBinding = fileDatabase
    ? existingBinding && !input.createDatabase ? existingBinding : createSqliteBinding(
      platform,
      state,
      slug,
      now,
      databaseEngine as "sqlite" | "litestream",
    )
    : databaseBinding(
      managedDatabase!.engine,
      String(managedDatabase!.service),
      slug,
      databasePassword,
      databases,
    );
  const appDatabases = existing ? [...existing.databases] : [];
  const selectedIndex = existingBinding && !(fileDatabase && input.createDatabase)
    ? appDatabases.indexOf(existingBinding)
    : -1;
  if (selectedIndex >= 0) appDatabases[selectedIndex] = selectedBinding;
  else appDatabases.push(selectedBinding);

  const app: AppState = {
    slug: asAppSlug(slug),
    enabled: existing?.enabled ?? true,
    uid,
    gid,
    home: asAbsoluteAppPath(homeContainer),
    documentRoot,
    entrypointMode,
    phpVersion,
    phpService: managedPhp.service,
    fpmProfile,
    tls,
    accessLog,
    databases: appDatabases,
    database: appDatabases[0]!,
    mainDomain: asDomainName(domain),
    aliases: aliases.map(asDomainName),
    redis,
    deploy: existing?.deploy ?? defaultDeployConfig(homeContainer),
    vhostTemplate: existing?.vhostTemplate ?? { kind: "upstream" },
    poolTemplate: existing?.poolTemplate ?? { kind: "upstream" },
    createdAt: existing?.createdAt ?? now,
    updatedAt: now,
  };

  // Rebuild domain map for this app
  const domains = { ...state.domains };
  // Remove previous domains owned by this app
  for (const [d, owner] of Object.entries(domains)) {
    if (owner.kind === "app" && owner.slug === slug) delete domains[d];
  }
  for (const [index, d] of claimed.entries()) {
    domains[d] = { kind: "app", slug: asAppSlug(slug), primary: index === 0 };
  }

  const next: DesiredState = {
    ...state,
    apps: { ...state.apps, [slug]: app },
    domains,
    updatedAt: now,
  };

  return {
    state: next,
    app,
    reloadPlan: reloadPlanForPoolChange(app.phpService),
    created,
  };
}

function asDatabaseName(name: string): AppDatabase["name"] {
  return name as AppDatabase["name"];
}

function createSqliteBinding(
  platform: Platform,
  state: DesiredState,
  slug: string,
  now: string,
  engine: "sqlite" | "litestream",
): AppDatabaseBinding {
  const id = `${slug}_${platform.random.hex(5)}`;
  const file = { id, path: sqliteRelativePath(id, slug, engine), createdAt: now };
  if (engine === "litestream") return { engine, file } as AppDatabaseBinding;

  const occupied = new Set(
    [...resolveSqliteVacuumSchedules(state).values()].map(sqliteVacuumScheduleSlot),
  );
  return {
    engine,
    file,
    vacuumSchedule: randomSqliteVacuumSchedule(platform.random, occupied),
  } as AppDatabaseBinding;
}

function databaseBinding(
  engine: DatabaseEngine,
  service: string,
  user: string,
  password: string,
  databases: AppDatabase[],
): AppDatabaseBinding {
  const common = { service: asDatabaseService(service), user, password, databases };
  return engine === "mysql" ? { engine: "mysql", ...common } : { engine: "postgres", ...common };
}

function resolveAppDatabaseService(
  state: DesiredState,
  existing: AppState | undefined,
  input: ProvisionAppInput,
): ManagedDatabaseService {
  if (
    input.databaseEngine !== undefined &&
    !["mysql", "postgres", "sqlite", "litestream"].includes(input.databaseEngine)
  ) {
    throw validationError("database engine must be mysql, postgres, sqlite, or litestream");
  }
  if (
    (input.databaseEngine === "sqlite" || input.databaseEngine === "litestream") &&
    (input.mysqlVersion || input.postgresVersion)
  ) {
    throw validationError(
      `--database-engine ${input.databaseEngine} cannot be combined with --mysql or --postgres`,
    );
  }
  if (input.mysqlVersion && input.postgresVersion) {
    throw validationError("--mysql and --postgres cannot be used together");
  }
  if (input.databaseEngine === "mysql" && input.postgresVersion) {
    throw validationError("--database-engine mysql contradicts --postgres");
  }
  if (input.databaseEngine === "postgres" && input.mysqlVersion) {
    throw validationError("--database-engine postgres contradicts --mysql");
  }

  const requestedEngine = (input.databaseEngine ??
    (input.mysqlVersion ? "mysql" : input.postgresVersion ? "postgres" : undefined)) as
      | Exclude<DatabaseEngine, "sqlite" | "litestream">
      | undefined;
  const current = existing
    ? existing.databases.find((database) =>
      database.engine === requestedEngine &&
      (database.engine === "mysql" || database.engine === "postgres")
    ) ?? primaryDatabase(existing)
    : undefined;
  const engine = requestedEngine ?? current?.engine ?? state.defaults.database.engine;
  if (engine === "sqlite" || engine === "litestream") {
    throw validationError("SQLite and Litestream do not use a managed relational service");
  }
  const token = engine === "mysql" ? input.mysqlVersion : input.postgresVersion;
  if (
    current && current.engine !== "sqlite" && current.engine !== "litestream" &&
    current.engine === engine && token === undefined
  ) {
    const preserved = state.databaseServices.find((entry) =>
      entry.engine === engine && entry.service === current.service
    );
    if (!preserved) {
      throw validationError(`${engine} service ${current.service} is not managed`);
    }
    return preserved;
  }

  const candidates = state.databaseServices.filter((entry) => entry.engine === engine);
  let selected: ManagedDatabaseService | undefined;
  if (token) {
    selected = candidates.find((entry) => entry.version === token || entry.service === token);
  } else if (state.defaults.database.engine === engine) {
    selected = candidates.find((entry) => entry.service === state.defaults.database.service);
  } else if (candidates.length === 1) {
    selected = candidates[0];
  }
  if (!selected) {
    throw validationError(
      token
        ? `${engine} version or service ${token} is not managed`
        : `select a managed ${engine} service explicitly`,
    );
  }
  return selected;
}

export function allocateIdentity(
  state: DesiredState,
): { uid: ReturnType<typeof asUid>; gid: ReturnType<typeof asGid> } {
  const used = new Set<number>();
  for (const app of Object.values(state.apps)) {
    used.add(app.uid);
    used.add(app.gid);
  }
  let n = DEFAULT_UID_BASE;
  while (used.has(n)) n++;
  return { uid: asUid(n), gid: asGid(n) };
}

export type MaterializeAppHomeOptions = {
  recursivePerms?: boolean;
  /** Shared Redis password from stack .env (shared mode). */
  redisSharedPassword?: string;
};

/** Create app home directory structure on the host. */
export async function materializeAppHome(
  platform: Platform,
  app: AppState,
  recursivePermsOrOpts: boolean | MaterializeAppHomeOptions = true,
): Promise<void> {
  const opts: MaterializeAppHomeOptions = typeof recursivePermsOrOpts === "boolean"
    ? { recursivePerms: recursivePermsOrOpts }
    : recursivePermsOrOpts;
  const recursivePerms = opts.recursivePerms ?? true;
  const home = platform.paths.appHome(app.slug);
  const dirs = [
    home,
    join(home, "code"),
    join(home, app.documentRoot ? join("code", app.documentRoot) : "code"),
    join(home, "logs"),
    join(home, "tmp"),
    join(home, "tmp", "sessions"),
    join(home, ".bento"),
    join(home, ".ssh"),
    join(home, ".composer"),
    join(home, "credentials"),
  ];
  for (const d of dirs) {
    await platform.fs.mkdirp(d, 0o750);
  }
  for (
    const database of app.databases.filter((database) =>
      database.engine === "sqlite" || database.engine === "litestream"
    )
  ) {
    if (database.engine !== "sqlite" && database.engine !== "litestream") continue;
    const sqliteDir = sqliteHostDir(platform, database.file.id);
    const sqlitePath = sqliteHostPath(
      platform,
      database.file.id,
      app.slug,
      database.engine,
    );
    await platform.fs.mkdirp(sqliteDir, 0o700);
    if (!(await platform.fs.exists(sqlitePath))) {
      await platform.fs.writeBytes(sqlitePath, new Uint8Array(), 0o600);
    }
    const ownership = await platform.process.run([
      "chown",
      "-R",
      `${app.uid}:${app.gid}`,
      sqliteDir,
    ]);
    if (ownership.code !== 0) {
      throw safetyError(`cannot set SQLite ownership: ${ownership.stderr.trim()}`);
    }
    await platform.fs.chmod(sqliteDir, 0o700);
    await platform.fs.chmod(sqlitePath, 0o600);
  }

  await ensureAppSshKeyPair(platform, app);

  // Credentials (mode 0600); shared Redis auth comes from stack env when not on app state.
  const sharedRedisPassword = app.redis.password ?? opts.redisSharedPassword ?? "";
  const redisLines = app.redis.mode === "shared"
    ? [
      `REDIS_PASSWORD=${sharedRedisPassword}`,
      `REDIS_PREFIX=${app.redis.prefix}`,
      `REDIS_MODE=shared`,
    ]
    : [
      `REDIS_USERNAME=${app.redis.aclUsername ?? ""}`,
      `REDIS_PASSWORD=${app.redis.aclPassword ?? ""}`,
      `REDIS_ACL_USERNAME=${app.redis.aclUsername ?? ""}`,
      `REDIS_ACL_PASSWORD=${app.redis.aclPassword ?? ""}`,
      `REDIS_PREFIX=${app.redis.prefix}`,
      `REDIS_MODE=acl`,
    ];
  const database = primaryDatabase(app);
  const databaseLines = database.engine === "mysql"
    ? [
      "DB_CONNECTION=mysql",
      `MYSQL_HOST=${database.service}`,
      `MYSQL_USER=${database.user}`,
      `MYSQL_PASSWORD=${database.password}`,
      `MYSQL_DATABASE=${database.databases[0]?.name ?? app.slug}`,
    ]
    : database.engine === "postgres"
    ? [
      "DB_CONNECTION=pgsql",
      `PGHOST=${database.service}`,
      "PGPORT=5432",
      `PGUSER=${database.user}`,
      `PGPASSWORD=${database.password}`,
      `PGDATABASE=${database.databases[0]?.name ?? app.slug}`,
    ]
    : [
      "DB_CONNECTION=sqlite",
      `DB_DATABASE=${sqliteContainerPath(database.file.id, app.slug, database.engine)}`,
      "SQLITE_BUSY_TIMEOUT=5000",
    ];
  const linkedDatabaseLines = app.databases.flatMap((binding, index) => {
    const prefix = `BENTO_DB_${index + 1}`;
    if (binding.engine === "mysql" || binding.engine === "postgres") {
      return [
        `${prefix}_ENGINE=${binding.engine}`,
        `${prefix}_HOST=${binding.service}`,
        `${prefix}_USER=${binding.user}`,
        `${prefix}_PASSWORD=${binding.password}`,
        `${prefix}_DATABASES=${binding.databases.map((entry) => entry.name).join(",")}`,
      ];
    }
    return [
      `${prefix}_ENGINE=${binding.engine}`,
      `${prefix}_PATH=${sqliteContainerPath(binding.file.id, app.slug, binding.engine)}`,
    ];
  });
  const cred = [
    ...databaseLines,
    `BENTO_DATABASE_COUNT=${app.databases.length}`,
    ...linkedDatabaseLines,
    `REDIS_HOST=redis`,
    `REDIS_PORT=6379`,
    ...redisLines,
    "",
  ].join("\n");
  await platform.fs.atomicWriteText(join(home, "credentials", "app.env"), cred, 0o600);

  // Example deploy hook (exit 99 = skipped)
  const deploySh = join(home, ".bento", "deploy.sh");
  if (!(await platform.fs.exists(deploySh))) {
    await platform.fs.atomicWriteText(
      deploySh,
      `#!/bin/sh\n# Replace this hook with your deploy steps.\n# Exit 0 success, 99 skipped, other failed.\necho "bento: default deploy hook (skipped)" >&2\nexit 99\n`,
      0o750,
    );
  }

  // deploy.json without webhook secret
  await platform.fs.atomicWriteText(
    join(home, ".bento", "deploy.json"),
    `${
      JSON.stringify(
        {
          timeoutSec: app.deploy.timeoutSec,
          workdir: app.deploy.workdir,
          argv: app.deploy.argv,
          queuePolicy: app.deploy.queuePolicy,
        },
        null,
        2,
      )
    }\n`,
    0o640,
  );

  // queue.json
  const queuePath = join(home, ".bento", "queue.json");
  if (!(await platform.fs.exists(queuePath))) {
    await platform.fs.atomicWriteText(
      queuePath,
      `${JSON.stringify({ schemaVersion: 1, jobs: [] }, null, 2)}\n`,
      0o600,
    );
  }

  // Placeholder index
  const docRoot = join(home, "code", app.documentRoot || ".");
  await platform.fs.mkdirp(docRoot);
  const index = join(docRoot, "index.php");
  if (!(await platform.fs.exists(index))) {
    await platform.fs.atomicWriteText(
      index,
      `<?php\necho "bento app ${app.slug}\\n";\n`,
      0o644,
    );
  }

  if (recursivePerms) {
    // Initial recursive policy while the tree is still small (chown needs root/CAP_CHOWN).
    await applyAppPermissionPolicy(platform, app, { recursive: true });
  } else {
    await applyAppPermissionPolicy(platform, app, { recursive: false });
  }
}

/** Ensure every app has a stable Ed25519 deploy key for private Git clones. */
export async function ensureAppSshKeyPair(
  platform: Platform,
  app: AppState,
): Promise<void> {
  const sshDir = join(platform.paths.appHome(app.slug), ".ssh");
  const privateKey = join(sshDir, "id_ed25519");
  const publicKey = `${privateKey}.pub`;
  await platform.fs.mkdirp(sshDir, 0o700);
  await platform.fs.chmod(sshDir, 0o700);

  const hasPrivateKey = await platform.fs.exists(privateKey);
  const hasPublicKey = await platform.fs.exists(publicKey);
  if (!hasPrivateKey && hasPublicKey) {
    throw safetyError(
      `refusing to replace orphaned SSH public key for app ${app.slug}`,
      `Move or remove ${publicKey}, then provision the app again.`,
    );
  }

  if (!hasPrivateKey) {
    const result = await platform.process.run([
      "ssh-keygen",
      "-q",
      "-t",
      "ed25519",
      "-N",
      "",
      "-C",
      `bento-app-${app.slug}`,
      "-f",
      privateKey,
    ], { timeoutMs: 10_000 });
    if (result.code !== 0) {
      throw serviceError(
        `failed to generate SSH key pair for app ${app.slug}: ${
          (result.stderr || result.stdout || `ssh-keygen exited ${result.code}`).trim()
        }`,
        "Install OpenSSH ssh-keygen, check the app home permissions, and retry.",
      );
    }
  } else if (!hasPublicKey) {
    const result = await platform.process.run(
      ["ssh-keygen", "-y", "-f", privateKey],
      { timeoutMs: 10_000 },
    );
    if (result.code !== 0 || !result.stdout.trim()) {
      throw serviceError(
        `failed to recreate SSH public key for app ${app.slug}`,
        `Check ${privateKey} and retry app provisioning.`,
      );
    }
    await platform.fs.atomicWriteText(
      publicKey,
      `${result.stdout.trim()} bento-app-${app.slug}\n`,
      0o644,
    );
  }

  // Recording process runners used by tests may not materialize command outputs.
  if (await platform.fs.exists(privateKey)) await platform.fs.chmod(privateKey, 0o600);
  if (await platform.fs.exists(publicKey)) await platform.fs.chmod(publicKey, 0o644);
}

export type AppDataPlaneResult = {
  /** True when the selected relational database adapter completed. */
  databaseApplied: boolean;
  /** Compatibility signal retained for existing MySQL harness callers. */
  mysqlApplied: boolean;
  redisApplied: boolean;
  /** Operator-facing note when best-effort work was deferred. */
  deferredNotes: string[];
};

/**
 * Apply live MySQL/Redis side effects for a provisioned app.
 *
 * Explicit database request (createDatabase / databases just added): fail hard if MySQL is down.
 * Without an explicit database: best-effort account setup may defer.
 */
export async function applyAppDataPlane(
  platform: Platform,
  app: AppState,
  opts: {
    /** When true, MySQL must be up and grants applied for each recorded database. */
    explicitDatabase: boolean;
    databaseEngine?: DatabaseEngine;
    databaseService?: string;
    databaseName?: string;
  },
): Promise<AppDataPlaneResult> {
  const deferredNotes: string[] = [];
  let databaseApplied = false;
  let redisApplied = false;

  let mysqlApplied = false;
  const selectedDatabases = opts.explicitDatabase
    ? app.databases.filter((database) => {
      if (opts.databaseEngine && database.engine !== opts.databaseEngine) return false;
      if (
        opts.databaseService &&
        (database.engine === "mysql" || database.engine === "postgres") &&
        database.service !== opts.databaseService
      ) return false;
      if (
        opts.databaseName &&
        (database.engine === "mysql" || database.engine === "postgres") &&
        !database.databases.some((entry) => entry.name === opts.databaseName)
      ) return false;
      return true;
    })
    : app.databases;
  for (const database of selectedDatabases) {
    const scopedApp = { ...app, databases: [database] };
    if (database.engine === "mysql") {
      if (opts.explicitDatabase) {
        const rootPassword = await requireMysqlRootPassword(platform);
        if (!(await isMysqlReachable(platform, database.service))) {
          throw serviceError(
            `MySQL service ${database.service} is unavailable; database was not recorded`,
            "Start the stack MySQL service, confirm MYSQL_ROOT_PASSWORD, then retry `bento app create --db` or `bento mysql db`.",
          );
        }
        for (const dbName of database.databases.map((d) => d.name)) {
          await applyAppMysqlGrants(platform, scopedApp, dbName, rootPassword);
        }
        databaseApplied = true;
        mysqlApplied = true;
      } else {
        const applied = await tryBestEffortMysqlAccount(
          platform,
          scopedApp,
          await loadMysqlRootPassword(platform),
        );
        databaseApplied ||= applied;
        mysqlApplied ||= applied;
        if (!applied) {
          deferredNotes.push(
            `MySQL account setup deferred for ${app.slug}; retry when ${database.service} is up`,
          );
        }
      }
    } else if (database.engine === "postgres") {
      if (opts.explicitDatabase) {
        const rootPassword = await requirePostgresRootPassword(platform);
        if (!(await isPostgresReachable(platform, database.service))) {
          throw serviceError(
            `PostgreSQL service ${database.service} is unavailable; database was not recorded`,
            "Start PostgreSQL, confirm POSTGRES_PASSWORD, then retry `bento app create --db`.",
          );
        }
        for (const dbName of database.databases.map((d) => d.name)) {
          await applyAppPostgresDatabase(platform, scopedApp, dbName, rootPassword);
        }
        databaseApplied = true;
      } else {
        const applied = await tryBestEffortPostgresRole(
          platform,
          scopedApp,
          await loadPostgresRootPassword(platform),
        );
        databaseApplied ||= applied;
        if (!applied) {
          deferredNotes.push(
            `PostgreSQL role setup deferred for ${app.slug}; retry when ${database.service} is up`,
          );
        }
      }
    } else {
      databaseApplied = true;
    }
  }

  const redisShared = await loadRedisPassword(platform);
  redisApplied = await tryApplyAppRedisAcl(platform, app, redisShared);
  if (app.redis.mode === "acl" && !redisApplied) {
    deferredNotes.push(
      `Redis ACL apply deferred for ${app.slug}; re-apply when redis is up`,
    );
  }

  return {
    databaseApplied,
    mysqlApplied,
    redisApplied,
    deferredNotes,
  };
}

export function getAppOrThrow(state: DesiredState, slug: string): AppState {
  const app = state.apps[slug];
  if (!app) throw notFoundError(`app not found: ${slug}`);
  return app;
}

export type AppLifecycleResult = {
  state: DesiredState;
  app: AppState;
  reloadPlan: ReloadPlan;
};

function appLifecycleReloadPlan(app: AppState): ReloadPlan {
  return mergeReloadPlans(
    reloadPlanForPoolChange(app.phpService),
    reloadPlanForRunnerChange(`${app.phpService}-runner`),
  );
}

/** Disable or enable runtime config while retaining the app and all durable data. */
export function setAppEnabled(
  state: DesiredState,
  slug: string,
  enabled: boolean,
  now: string,
): AppLifecycleResult {
  const current = getAppOrThrow(state, slug);
  const app = { ...current, enabled, updatedAt: now };
  return {
    state: {
      ...state,
      apps: { ...state.apps, [slug]: app },
      updatedAt: now,
    },
    app,
    reloadPlan: appLifecycleReloadPlan(current),
  };
}

/**
 * Remove an app from Bento's desired state after an exact typed confirmation.
 * Durable home and database data are intentionally left for operator-owned cleanup.
 */
export function deleteApp(
  state: DesiredState,
  slug: string,
  confirmation?: string,
  now: string = new Date().toISOString(),
): AppLifecycleResult {
  const app = getAppOrThrow(state, slug);
  const expected = `delete ${slug}`;
  if (confirmation !== expected) {
    throw safetyError(
      `refusing to remove app ${slug}: confirmation must be exactly '${expected}'`,
      `Retry with --confirm '${expected}'. Durable home and database data will be retained.`,
    );
  }
  const apps = { ...state.apps };
  delete apps[slug];
  const domains = { ...state.domains };
  for (const [domain, owner] of Object.entries(domains)) {
    if (owner.kind === "app" && owner.slug === slug) delete domains[domain];
  }
  return {
    state: {
      ...state,
      apps,
      domains,
      cronJobs: state.cronJobs.filter((job) => job.app !== slug),
      workers: state.workers.filter((worker) => worker.app !== slug),
      updatedAt: now,
    },
    app,
    reloadPlan: appLifecycleReloadPlan(app),
  };
}

export function capacityWarnings(state: DesiredState): string[] {
  const warnings: string[] = [];
  for (const v of state.phpVersions) {
    const apps = Object.values(state.apps).filter((a) => a.enabled && a.phpVersion === v.version);
    let sum = 0;
    for (const a of apps) {
      const p = FPM_PROFILES[a.fpmProfile] ?? FPM_PROFILES[DEFAULT_FPM_PROFILE]!;
      sum += p.maxChildren;
    }
    if (sum > v.processCap) {
      warnings.push(
        `PHP ${v.version} (${v.service}): sum of pool max_children=${sum} exceeds process cap ${v.processCap}`,
      );
    }
  }
  return warnings;
}

export type { FpmProfile, PhpVersion };
