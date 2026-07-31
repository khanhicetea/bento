/**
 * PostgreSQL managed-version administration and protected SQL execution.
 * SQL and credentials are streamed over stdin and never placed on host argv.
 */

import { basename, isAbsolute, join, relative, resolve } from "@std/path";
import type { AppState, DesiredState, ManagedPostgresVersion } from "../domain/state.ts";
import { databaseBindings, postgresImage, postgresServiceName } from "../domain/state.ts";
import { asDatabaseName, asPostgresVersion } from "../domain/types.ts";
import {
  conflictError,
  notFoundError,
  safetyError,
  serviceError,
  validationError,
} from "../domain/errors.ts";
import type { Platform, RunResult } from "../platform/mod.ts";
import { parsePostgresVersion, unwrap } from "../schemas/validators.ts";

function postgresDatabase(app: AppState, service?: string) {
  const database = service
    ? databaseBindings(app, "postgres").find((binding) => binding.service === service)
    : databaseBindings(app, "postgres")[0];
  if (!database) {
    throw validationError(
      service
        ? `app ${app.slug} has no PostgreSQL database binding for ${service}`
        : `app ${app.slug} has no PostgreSQL database binding`,
    );
  }
  return database;
}

/** Stable names derived from Bento's major-only PostgreSQL version format. */
export function postgresVersionDetails(versionInput: string): ManagedPostgresVersion {
  const version = asPostgresVersion(
    unwrap(parsePostgresVersion(versionInput), "postgresVersion"),
  );
  const service = postgresServiceName(version);
  return {
    engine: "postgres",
    version,
    service,
    image: postgresImage(version),
    volume: `${service}-data`,
  };
}

export function addPostgresVersion(
  state: DesiredState,
  versionInput: string,
): DesiredState {
  const managed = postgresVersionDetails(versionInput);
  if (
    state.databaseServices.some((entry) =>
      entry.engine === "postgres" && entry.version === managed.version
    )
  ) {
    throw conflictError(`PostgreSQL version ${managed.version} is already managed`);
  }
  return {
    ...state,
    databaseServices: [...state.databaseServices, managed].sort((a, b) =>
      a.service.localeCompare(b.service)
    ),
    updatedAt: new Date().toISOString(),
  };
}

export function listPostgresVersions(state: DesiredState): ManagedPostgresVersion[] {
  return [...state.databaseServices.filter((entry) => entry.engine === "postgres")]
    .sort((a, b) => Number(a.version) - Number(b.version));
}

export function removePostgresVersion(_state: DesiredState, _version: string): never {
  throw safetyError(
    "automated PostgreSQL version removal is unsupported",
    "PostgreSQL service removal would couple with durable volume destruction and is intentionally unavailable.",
  );
}

function assertSqlText(value: string, kind: "identifier" | "literal"): void {
  if (value.includes("\0")) {
    throw validationError(`PostgreSQL ${kind} must not contain a NUL byte`);
  }
}

/** Quote an arbitrary PostgreSQL identifier without shell interpolation. */
export function postgresIdentifier(value: string): string {
  assertSqlText(value, "identifier");
  return `"${value.replaceAll('"', '""')}"`;
}

/** Quote an arbitrary PostgreSQL string literal using escape-string syntax. */
export function postgresLiteral(value: string): string {
  assertSqlText(value, "literal");
  return `E'${value.replaceAll("\\", "\\\\").replaceAll("'", "''")}'`;
}

function pgpassPassword(password: string): string {
  if (/[\r\n\0]/.test(password)) {
    throw validationError("PostgreSQL password must not contain a line break or NUL byte");
  }
  return password.replaceAll("\\", "\\\\").replaceAll(":", "\\:");
}

/**
 * Run SQL through psql using temporary mode-0600 credential and SQL files.
 * Both sensitive values are sent on stdin; argv contains only stable tooling.
 */
export async function execPostgresSql(
  platform: Platform,
  service: string,
  sql: string,
  password: string,
): Promise<RunResult> {
  return await execPostgresSqlAs(platform, service, "postgres", "postgres", sql, password);
}

/** Execute as an app role while keeping username, database, SQL, and password off host argv. */
export async function execPostgresAppSql(
  platform: Platform,
  service: string,
  user: string,
  database: string,
  sql: string,
  password: string,
): Promise<RunResult> {
  return await execPostgresSqlAs(platform, service, user, database, sql, password);
}

async function execPostgresSqlAs(
  platform: Platform,
  service: string,
  user: string,
  database: string,
  sql: string,
  password: string,
): Promise<RunResult> {
  for (const [value, kind] of [[user, "user"], [database, "database"]] as const) {
    if (/\r|\n|\0/.test(value) || value === "") {
      throw validationError(`PostgreSQL ${kind} must not be empty or contain a line break`);
    }
  }
  const script = [
    "set -eu",
    "umask 077",
    "PASS=$(mktemp)",
    "SQL=$(mktemp)",
    'trap \'rm -f "$PASS" "$SQL"\' EXIT',
    "IFS= read -r dbuser",
    "IFS= read -r database",
    "IFS= read -r passline",
    'printf \'%s\\n\' "$passline" > "$PASS"',
    "IFS= read -r marker",
    '[ "$marker" = "__END_PGPASS__" ]',
    'cat > "$SQL"',
    'chmod 600 "$PASS" "$SQL"',
    'PGPASSFILE="$PASS" psql --username="$dbuser" --dbname="$database" --no-psqlrc --set=ON_ERROR_STOP=1 --file="$SQL"',
  ].join("\n");
  const stdin = [
    user,
    database,
    `*:*:*:${user.replaceAll("\\", "\\\\").replaceAll(":", "\\:")}:${pgpassPassword(password)}`,
    "__END_PGPASS__",
    sql,
    "",
  ].join("\n");

  return await platform.process.run(
    ["docker", "compose", "exec", "-T", service, "sh", "-c", script],
    { cwd: platform.paths.paths.root, stdin, timeoutMs: 60_000 },
  );
}

/** SQL that creates an app role once and then enforces its non-privileged attributes. */
export function postgresRoleSql(app: AppState): string {
  const role = postgresLiteral(postgresDatabase(app).user);
  const password = postgresLiteral(postgresDatabase(app).password);
  return [
    `SELECT format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION', ${role}, ${password})`,
    `WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = ${role}) \\gexec`,
    `ALTER ROLE ${
      postgresIdentifier(postgresDatabase(app).user)
    } WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;`,
  ].join("\n");
}

/** Administrator SQL for an owned, private app database. */
export function postgresDatabaseSql(app: AppState, database: string): string {
  const dbLiteral = postgresLiteral(database);
  const roleLiteral = postgresLiteral(postgresDatabase(app).user);
  const db = postgresIdentifier(database);
  const role = postgresIdentifier(postgresDatabase(app).user);
  return [
    `SELECT format('CREATE DATABASE %I OWNER %I', ${dbLiteral}, ${roleLiteral})`,
    `WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = ${dbLiteral}) \\gexec`,
    `ALTER DATABASE ${db} OWNER TO ${role};`,
    `REVOKE ALL PRIVILEGES ON DATABASE ${db} FROM PUBLIC;`,
    `GRANT CONNECT, TEMPORARY ON DATABASE ${db} TO ${role};`,
  ].join("\n");
}

/** Database-local schema policy; another app role receives no PUBLIC access. */
export function postgresSchemaSql(app: AppState): string {
  const role = postgresIdentifier(postgresDatabase(app).user);
  return [
    "REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC;",
    `ALTER SCHEMA public OWNER TO ${role};`,
    `GRANT USAGE, CREATE ON SCHEMA public TO ${role};`,
  ].join("\n");
}

export async function applyAppPostgresRole(
  platform: Platform,
  app: AppState,
  rootPassword: string,
): Promise<void> {
  if (postgresDatabase(app).engine !== "postgres") {
    throw validationError(`app ${app.slug} is not PostgreSQL-backed`);
  }
  const result = await execPostgresSql(
    platform,
    postgresDatabase(app).service,
    postgresRoleSql(app),
    rootPassword,
  );
  if (result.code !== 0) {
    throw serviceError(
      `PostgreSQL role setup failed for ${app.slug} on ${postgresDatabase(app).service}: ${
        (result.stderr || result.stdout || "unknown error").trim()
      }`,
      "Ensure PostgreSQL is running and POSTGRES_PASSWORD matches the container, then retry app provisioning.",
    );
  }
}

export async function applyAppPostgresDatabase(
  platform: Platform,
  app: AppState,
  database: string,
  rootPassword: string,
): Promise<void> {
  await applyAppPostgresRole(platform, app, rootPassword);
  const create = await execPostgresSql(
    platform,
    postgresDatabase(app).service,
    postgresDatabaseSql(app, database),
    rootPassword,
  );
  if (create.code !== 0) {
    throw serviceError(
      `PostgreSQL database setup failed for ${database} on ${postgresDatabase(app).service}: ${
        (create.stderr || create.stdout || "unknown error").trim()
      }`,
      "The database was not recorded; correct PostgreSQL availability/credentials and retry.",
    );
  }
  const schema = await execPostgresSqlAs(
    platform,
    postgresDatabase(app).service,
    "postgres",
    database,
    postgresSchemaSql(app),
    rootPassword,
  );
  if (schema.code !== 0) {
    throw serviceError(
      `PostgreSQL schema isolation failed for ${database} on ${postgresDatabase(app).service}: ${
        (schema.stderr || schema.stdout || "unknown error").trim()
      }`,
      "The database was not recorded; correct PostgreSQL permissions and retry.",
    );
  }
}

export async function tryBestEffortPostgresRole(
  platform: Platform,
  app: AppState,
  rootPassword: string | undefined,
): Promise<boolean> {
  if (!rootPassword || !(await isPostgresReachable(platform, postgresDatabase(app).service))) {
    return false;
  }
  try {
    await applyAppPostgresRole(platform, app, rootPassword);
    return true;
  } catch {
    return false;
  }
}

/** True when the PostgreSQL server in the managed container accepts connections. */
export async function isPostgresReachable(
  platform: Platform,
  service: string,
): Promise<boolean> {
  try {
    const result = await platform.process.run(
      [
        "docker",
        "compose",
        "exec",
        "-T",
        service,
        "pg_isready",
        "--username=postgres",
        "--dbname=postgres",
        "--quiet",
      ],
      { cwd: platform.paths.paths.root, timeoutMs: 8_000 },
    );
    return result.code === 0;
  } catch {
    return false;
  }
}

/** Authenticated reachability check for administration paths that require SQL. */
export async function verifyPostgresSql(
  platform: Platform,
  service: string,
  password: string,
): Promise<void> {
  const result = await execPostgresSql(platform, service, "SELECT 1;", password);
  if (result.code !== 0) {
    throw serviceError(
      `PostgreSQL authentication failed on ${service}: ${
        (result.stderr || result.stdout || "unknown error").trim()
      }`,
      "Ensure the PostgreSQL service is running and POSTGRES_PASSWORD matches the container.",
    );
  }
}

// ---------------------------------------------------------------------------
// Routine database administration (product §6.5)
// ---------------------------------------------------------------------------

/** Pure state transition for one additional private, app-owned database. */
export function createPostgresAppDatabase(
  state: DesiredState,
  slug: string,
  database: string,
  now: string,
  service?: string,
): DesiredState {
  const app = state.apps[slug];
  if (!app) throw notFoundError(`app not found: ${slug}`);
  if (!/^[a-zA-Z0-9_]+$/.test(database)) {
    throw validationError(`invalid database name ${database}`);
  }
  if (database !== slug && !database.startsWith(`${slug}_`)) {
    throw validationError(
      `database ${database} outside app namespace; use ${slug} or ${slug}_*`,
    );
  }
  const current = postgresDatabase(app, service);
  if (current.databases.some((entry) => entry.name === database)) {
    throw conflictError(`database ${database} already recorded for app ${slug}`);
  }
  const binding = {
    ...current,
    databases: [
      ...current.databases,
      { name: asDatabaseName(database), createdAt: now },
    ],
  };
  const databases = app.databases.map((entry) => entry === current ? binding : entry);
  const nextApp: AppState = {
    ...app,
    databases,
    database: databases[0]!,
    updatedAt: now,
  };
  return {
    ...state,
    apps: { ...state.apps, [slug]: nextApp },
    updatedAt: now,
  };
}

/** Apply PostgreSQL policy before returning state that may safely be recorded. */
export async function createPostgresAppDatabaseLive(
  platform: Platform,
  state: DesiredState,
  slug: string,
  database: string,
  rootPassword: string,
  service?: string,
): Promise<DesiredState> {
  const validated = createPostgresAppDatabase(
    state,
    slug,
    database,
    platform.clock.nowIso(),
    service,
  );
  const app = validated.apps[slug]!;
  const binding = postgresDatabase(app, service);
  if (!(await isPostgresReachable(platform, binding.service))) {
    throw serviceError(
      `PostgreSQL service ${binding.service} is unavailable; database ${database} was not recorded`,
      "Start the PostgreSQL service, confirm POSTGRES_PASSWORD, then retry `bento postgres db`.",
    );
  }
  await applyAppPostgresDatabase(
    platform,
    { ...app, databases: [binding], database: binding },
    database,
    rootPassword,
  );
  return validated;
}

export type PostgresShellIdentity =
  | { kind: "root"; service: string }
  | { kind: "app"; app: AppState };

export type PostgresShellPlan = {
  service: string;
  user: string;
  database: string;
  stage?: { command: string[]; stdin: string };
  open: { command: string[]; interactive: boolean };
  cleanup?: { command: string[] };
  credentialPath: string;
};

/** Build a root or app-authenticated psql shell with no password on argv. */
export function buildPostgresShellPlan(
  platform: Platform,
  identity: PostgresShellIdentity,
  opts?: { database?: string; interactive?: boolean; service?: string },
): PostgresShellPlan {
  const interactive = opts?.interactive ?? true;
  if (identity.kind === "root") {
    const database = opts?.database ?? "postgres";
    const credentialPath = "/etc/bento/postgres/root.pgpass";
    return {
      service: identity.service,
      user: "postgres",
      database,
      credentialPath,
      open: {
        command: [
          "docker",
          "compose",
          "exec",
          interactive ? "-it" : "-T",
          identity.service,
          "env",
          `PGPASSFILE=${credentialPath}`,
          "psql",
          "--no-psqlrc",
          "--username=postgres",
          `--dbname=${database}`,
        ],
        interactive,
      },
    };
  }

  const { service, user, password, databases } = postgresDatabase(identity.app, opts?.service);
  const database = opts?.database ?? databases[0]?.name ?? "postgres";
  if (opts?.database && !databases.some((entry) => entry.name === opts.database)) {
    throw validationError(`database ${opts.database} is not recorded for app ${identity.app.slug}`);
  }
  const credentialPath = `/tmp/bento-postgres-${platform.random.hex(8)}.pgpass`;
  const pgpass = `*:*:*:${user.replaceAll("\\", "\\\\").replaceAll(":", "\\:")}:${
    pgpassPassword(password)
  }\n`;
  const stageScript = [
    "set -eu",
    "umask 077",
    "complete=0",
    'trap \'[ "$complete" = 1 ] || rm -f "$1"\' EXIT',
    'cat > "$1"',
    'chmod 600 "$1"',
    "complete=1",
  ].join("\n");
  return {
    service,
    user,
    database,
    credentialPath,
    stage: {
      command: [
        "docker",
        "compose",
        "exec",
        "-T",
        service,
        "sh",
        "-c",
        stageScript,
        "sh",
        credentialPath,
      ],
      stdin: pgpass,
    },
    open: {
      command: [
        "docker",
        "compose",
        "exec",
        interactive ? "-it" : "-T",
        service,
        "env",
        `PGPASSFILE=${credentialPath}`,
        "psql",
        "--no-psqlrc",
        `--username=${user}`,
        `--dbname=${database}`,
      ],
      interactive,
    },
    cleanup: {
      command: ["docker", "compose", "exec", "-T", service, "rm", "-f", credentialPath],
    },
  };
}

export function assertPostgresShellSecretsOffArgv(
  plan: PostgresShellPlan,
  secrets: string[],
): void {
  const argv = [
    ...(plan.stage?.command ?? []),
    ...plan.open.command,
    ...(plan.cleanup?.command ?? []),
  ].join(" ");
  for (const secret of secrets) {
    if (secret && argv.includes(secret)) {
      throw serviceError("PostgreSQL shell plan leaked a secret onto host argv");
    }
  }
}

/** Stage credentials, run a shell callback, and clean up even on open failure. */
export async function executePostgresShell(
  platform: Platform,
  plan: PostgresShellPlan,
  open: (command: string[]) => Promise<number>,
): Promise<number> {
  if (plan.stage) {
    const result = await platform.process.run(plan.stage.command, {
      cwd: platform.paths.paths.root,
      stdin: plan.stage.stdin,
      timeoutMs: 15_000,
    });
    if (result.code !== 0) {
      throw serviceError(
        `failed to stage PostgreSQL credentials: ${
          (result.stderr || result.stdout || "unknown error").trim()
        }`,
      );
    }
  }
  try {
    return await open(plan.open.command);
  } finally {
    if (plan.cleanup) {
      await platform.process.run(plan.cleanup.command, {
        cwd: platform.paths.paths.root,
        timeoutMs: 10_000,
      }).catch(() => ({ code: 1, stdout: "", stderr: "" }));
    }
  }
}

export function postgresDatabaseSizeSql(databases: string[]): string {
  const where = databases.length
    ? `datname IN (${databases.map(postgresLiteral).join(", ")})`
    : "datname NOT IN ('postgres', 'template0', 'template1')";
  return [
    "SELECT datname, pg_database_size(datname), pg_size_pretty(pg_database_size(datname))",
    "FROM pg_database",
    `WHERE datallowconn AND ${where}`,
    "ORDER BY pg_database_size(datname) DESC;",
  ].join("\n");
}

/** Activity deliberately omits query text to avoid exposing SQL literals/secrets. */
export function postgresActivitySql(): string {
  return [
    "SELECT pid, usename, COALESCE(datname, ''), state, COALESCE(client_addr::text, ''),",
    "  COALESCE(backend_start::text, ''), COALESCE(query_start::text, '')",
    "FROM pg_stat_activity",
    "WHERE backend_type = 'client backend'",
    "ORDER BY pid;",
  ].join("\n");
}

export type PostgresSizeRow = { database: string; bytes: string; size: string };
export type PostgresActivityRow = {
  pid: string;
  user: string;
  database: string;
  state: string;
  client: string;
  backendStart: string;
  queryStart: string;
};

function parseTabRows(stdout: string, columns: number): string[][] {
  const rows: string[][] = [];
  for (const line of stdout.split("\n")) {
    if (!line.trim()) continue;
    const fields = line.split("\t");
    if (fields.length !== columns) continue;
    rows.push(fields);
  }
  return rows;
}

async function queryPostgresRows(
  platform: Platform,
  service: string,
  password: string,
  sql: string,
  label: string,
): Promise<string[][]> {
  const copySql = `COPY (${sql.replace(/;\s*$/, "")}) TO STDOUT WITH (FORMAT text);`;
  const result = await execPostgresSql(platform, service, copySql, password);
  if (result.code !== 0) {
    throw serviceError(
      `PostgreSQL ${label} query failed on ${service}: ${
        (result.stderr || result.stdout || "unknown error").trim()
      }`,
      "Ensure PostgreSQL is running and POSTGRES_PASSWORD matches the container.",
    );
  }
  return parseTabRows(result.stdout, label === "size" ? 3 : 7);
}

export async function queryPostgresDatabaseSizes(
  platform: Platform,
  service: string,
  rootPassword: string,
  databases: string[] = [],
): Promise<PostgresSizeRow[]> {
  return (await queryPostgresRows(
    platform,
    service,
    rootPassword,
    postgresDatabaseSizeSql(databases),
    "size",
  )).map(([database, bytes, size]) => ({ database: database!, bytes: bytes!, size: size! }));
}

export async function queryPostgresActivity(
  platform: Platform,
  service: string,
  rootPassword: string,
): Promise<PostgresActivityRow[]> {
  return (await queryPostgresRows(
    platform,
    service,
    rootPassword,
    postgresActivitySql(),
    "activity",
  )).map(([pid, user, database, state, client, backendStart, queryStart]) => ({
    pid: pid!,
    user: user!,
    database: database!,
    state: state!,
    client: client!,
    backendStart: backendStart!,
    queryStart: queryStart!,
  }));
}

/** Resolve managed PostgreSQL service(s), rejecting cross-engine app selection. */
export type PostgresBackupArtifact = {
  engine: "postgres";
  path: string;
  database: string;
  service: string;
  bytes: number;
};

/** Run matching-major pg_dump in the selected service with atomic publication. */
export async function runPostgresBackup(
  platform: Platform,
  target: { service: string; database: string },
  compress: "zstd" | "gzip" | "none" = "zstd",
): Promise<PostgresBackupArtifact> {
  const ts = platform.clock.nowIso().replace(/[:.]/g, "-");
  const ext = compress === "none" ? "sql" : compress === "gzip" ? "sql.gz" : "sql.zst";
  const finalName = `${target.service}_${target.database}_${ts}.${ext}`;
  const dir = join(platform.paths.paths.backupsDir, target.service, target.database);
  await platform.fs.mkdirp(dir, 0o700);
  const finalPath = join(dir, finalName);
  const partialPath = `${finalPath}.partial`;
  const containerFinal = `/var/backups/bento/${target.database}/${finalName}`;
  const containerPartial = `${containerFinal}.partial`;
  const dump = `PGPASSFILE=/etc/bento/postgres/root.pgpass pg_dump --username=postgres --dbname=${
    pgShellQuote(target.database)
  } --no-owner --no-acl`;
  const pipeline = compress === "gzip"
    ? `${dump} | gzip -c`
    : compress === "zstd"
    ? `${dump} | zstd -3 -q -c`
    : dump;
  const script = [
    "set -e",
    "set -o pipefail",
    "umask 077",
    "test -r /etc/bento/postgres/root.pgpass || { echo 'missing generated PostgreSQL root credential file; run bento render' >&2; exit 1; }",
    `test -d ${
      pgShellQuote(`/var/backups/bento/${target.database}`)
    } || { echo 'PostgreSQL backup bind is not active; run bento render then bento compose -- up -d' >&2; exit 1; }`,
    `PARTIAL=${pgShellQuote(containerPartial)}`,
    `FINAL=${pgShellQuote(containerFinal)}`,
    "trap 'rm -f \"$PARTIAL\"' EXIT",
    `${pipeline} > "$PARTIAL"`,
    'test -s "$PARTIAL"',
    'chmod 600 "$PARTIAL"',
    'mv -f "$PARTIAL" "$FINAL"',
    "trap - EXIT",
  ].join("\n");
  const result = await platform.process.run(
    ["docker", "compose", "exec", "-T", target.service, "sh", "-c", script],
    { cwd: platform.paths.paths.root, timeoutMs: 30 * 60_000 },
  );
  if (result.code !== 0) {
    await platform.fs.remove(partialPath).catch(() => {});
    throw new Error(`dump failed for ${target.database}: ${result.stderr || result.stdout}`);
  }
  if (!(await platform.fs.exists(finalPath))) {
    throw new Error(`dump for ${target.database} was empty; not publishing`);
  }
  const stat = await platform.fs.stat(finalPath);
  if (!stat.isFile || stat.size === 0) {
    await platform.fs.remove(finalPath).catch(() => {});
    throw new Error(`dump for ${target.database} was empty; not publishing`);
  }
  return {
    engine: "postgres",
    path: finalPath,
    database: target.database,
    service: target.service,
    bytes: stat.size,
  };
}

export type PostgresRestoreRequest = {
  file: string;
  app: AppState;
  targetDatabase: string;
  replaceOriginal?: string;
};

/** Restore a portable plain-SQL dump as the app role, then reapply isolation. */
export async function runPostgresRestore(
  platform: Platform,
  req: PostgresRestoreRequest,
): Promise<void> {
  const { app } = req;
  if (postgresDatabase(app).engine !== "postgres") {
    throw validationError(`app ${app.slug} is not PostgreSQL-backed`);
  }
  if (req.replaceOriginal !== undefined && req.replaceOriginal !== req.targetDatabase) {
    throw safetyError("replace confirmation must exactly match the target database name");
  }
  if (!/^[a-zA-Z0-9_]+$/.test(req.targetDatabase)) {
    throw validationError(`invalid target database ${req.targetDatabase}`);
  }
  if (
    req.targetDatabase !== app.slug &&
    !req.targetDatabase.startsWith(`${app.slug}_`)
  ) {
    throw validationError("target database outside app namespace");
  }
  if (!(await platform.fs.exists(req.file))) {
    throw notFoundError(`backup file not found: ${req.file}`);
  }
  const source = await platform.fs.stat(req.file);
  if (!source.isFile || source.size === 0) {
    throw validationError(`backup file is empty or not a regular file: ${req.file}`);
  }

  const serviceDir = join(platform.paths.paths.backupsDir, postgresDatabase(app).service);
  await platform.fs.mkdirp(serviceDir, 0o700);
  let containerFile = postgresPathInsideBackupMount(serviceDir, req.file);
  let stagedFile: string | undefined;
  if (!containerFile) {
    const stageDir = join(serviceDir, ".restore");
    await platform.fs.mkdirp(stageDir, 0o700);
    stagedFile = join(stageDir, `${platform.random.hex(8)}-${basename(req.file)}`);
    await platform.fs.copyFile(req.file, stagedFile);
    await platform.fs.chmod(stagedFile, 0o600);
    containerFile = `/var/backups/bento/.restore/${basename(stagedFile)}`;
  }

  const db = postgresIdentifier(req.targetDatabase);
  const role = postgresIdentifier(postgresDatabase(app).user);
  const terminate = `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ${
    postgresLiteral(req.targetDatabase)
  } AND pid <> pg_backend_pid();`;
  const createCommands = [
    ...(req.replaceOriginal ? [terminate, `DROP DATABASE IF EXISTS ${db};`] : []),
    `CREATE DATABASE ${db} OWNER ${role};`,
    `REVOKE ALL PRIVILEGES ON DATABASE ${db} FROM PUBLIC;`,
    `GRANT CONNECT, TEMPORARY ON DATABASE ${db} TO ${role};`,
  ];
  const decompress = req.file.endsWith(".gz")
    ? `gzip -dc -- ${pgShellQuote(containerFile)}`
    : req.file.endsWith(".zst") || req.file.endsWith(".zstd")
    ? `zstd -dc -- ${pgShellQuote(containerFile)}`
    : `cat -- ${pgShellQuote(containerFile)}`;
  const script = [
    "set -e",
    "set -o pipefail",
    "umask 077",
    "PASS=$(mktemp)",
    "trap 'rm -f \"$PASS\"' EXIT",
    "IFS= read -r passline",
    'printf \'%s\\n\' "$passline" > "$PASS"',
    'chmod 600 "$PASS"',
    "test -r /etc/bento/postgres/root.pgpass || { echo 'missing generated PostgreSQL root credential file; run bento render' >&2; exit 1; }",
    `test -r ${
      pgShellQuote(containerFile)
    } || { echo 'PostgreSQL backup bind is not active; run bento render then bento compose -- up -d' >&2; exit 1; }`,
    ...createCommands.map((sql) =>
      `PGPASSFILE=/etc/bento/postgres/root.pgpass psql --username=postgres --dbname=postgres --no-psqlrc --set=ON_ERROR_STOP=1 --command=${
        pgShellQuote(sql)
      }`
    ),
    `${decompress} | PGPASSFILE="$PASS" psql --username=${
      pgShellQuote(postgresDatabase(app).user)
    } --dbname=${pgShellQuote(req.targetDatabase)} --no-psqlrc --set=ON_ERROR_STOP=1`,
    `PGPASSFILE=/etc/bento/postgres/root.pgpass psql --username=postgres --dbname=${
      pgShellQuote(req.targetDatabase)
    } --no-psqlrc --set=ON_ERROR_STOP=1 --command=${pgShellQuote(postgresSchemaSql(app))}`,
  ].join("\n");
  const stdin = `*:*:*:${
    postgresDatabase(app).user.replaceAll("\\", "\\\\").replaceAll(":", "\\:")
  }:${pgpassPassword(postgresDatabase(app).password)}\n`;
  try {
    const result = await platform.process.run(
      ["docker", "compose", "exec", "-T", postgresDatabase(app).service, "sh", "-c", script],
      { cwd: platform.paths.paths.root, stdin, timeoutMs: 60 * 60_000 },
    );
    if (result.code !== 0) {
      throw new Error(
        `restore failed (destination may be partial; restore is not object-level atomic): ${
          result.stderr || result.stdout
        }`,
      );
    }
  } finally {
    if (stagedFile) await platform.fs.remove(stagedFile).catch(() => {});
  }
}

function postgresPathInsideBackupMount(serviceDir: string, file: string): string | undefined {
  const rel = relative(resolve(serviceDir), resolve(file));
  if (!rel || isAbsolute(rel) || rel === ".." || rel.startsWith("../") || rel.startsWith("..\\")) {
    return undefined;
  }
  return `/var/backups/bento/${rel.replaceAll("\\", "/")}`;
}

function pgShellQuote(value: string): string {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}

export function resolvePostgresServices(
  state: DesiredState,
  opts?: { service?: string; app?: string },
): string[] {
  const selectedApp = opts?.app ? state.apps[opts.app] : undefined;
  if (opts?.app && !selectedApp) throw notFoundError(`app not found: ${opts.app}`);
  if (selectedApp?.database.engine !== undefined && selectedApp.database.engine !== "postgres") {
    throw validationError(`app ${opts!.app} is not PostgreSQL-backed`);
  }
  if (opts?.service) {
    const found = state.databaseServices.find((entry) =>
      entry.engine === "postgres" &&
      (entry.service === opts.service || entry.version === opts.service)
    );
    if (!found) throw notFoundError(`PostgreSQL service not found: ${opts.service}`);
    if (selectedApp && selectedApp.database.service !== found.service) {
      throw validationError(
        `app ${opts.app} is assigned to ${selectedApp.database.service}, not ${found.service}`,
      );
    }
    return [found.service];
  }
  if (selectedApp) return [selectedApp.database.service];
  return state.databaseServices
    .filter((entry) => entry.engine === "postgres")
    .map((entry) => entry.service);
}
