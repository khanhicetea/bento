/**
 * Operator status view: services, apps, runtimes, domains, TLS, proxies, DB, capacity.
 * Secrets are never included in the report object (safe for --json).
 */

import type { DesiredState, ManagedDatabaseService } from "../domain/state.ts";
import type { Platform } from "../platform/mod.ts";
import { capacityWarnings } from "./app.ts";
import { FPM_PROFILES } from "../domain/types.ts";
import { resolveComposeFiles } from "./compose.ts";
import { loadStackComposeEnvironment } from "./stack_env.ts";
import { sqliteContainerPath } from "./sqlite_paths.ts";

export type RoleStatus = {
  name: string;
  kind: "nginx" | "redis" | "php-fpm" | "php-runner" | "mysql" | "postgres" | "litestream";
  /** observed | expected-only */
  state: "running" | "stopped" | "unknown" | "config-ready";
  detail?: string;
};

export type StatusReport = {
  stackRoot: string;
  stackName: string;
  ingress: {
    mode: "host" | "bridge";
    httpPort?: number;
    httpsPort?: number;
    http3: boolean;
  };
  defaults: {
    phpVersion: string;
    databaseEngine: "mysql" | "postgres";
    databaseVersion: string;
    databaseService: string;
    fpmProfile: string;
    redisMode: string;
  };
  roles: RoleStatus[];
  phpVersions: Array<{
    version: string;
    service: string;
    runner: string;
    processCap: number;
    appCount: number;
    poolMaxSum: number;
    overCap: boolean;
  }>;
  mysqlVersions: DatabaseVersionStatus[];
  postgresVersions: DatabaseVersionStatus[];
  apps: Array<{
    slug: string;
    enabled: boolean;
    uid: number;
    gid: number;
    domain: string;
    aliases: string[];
    php: string;
    fpmProfile: string;
    entrypointMode: string;
    tls: string;
    accessLog: boolean;
    databaseEngine: "mysql" | "postgres" | "sqlite" | "litestream";
    databaseService: string;
    databases: string[];
    redisMode: string;
    deploy: boolean;
  }>;
  proxies: Array<{
    name: string;
    domain: string;
    upstreams: string[];
    tls: string;
  }>;
  domains: Array<{ domain: string; owner: string }>;
  composeFiles: string[];
  cronJobs: number;
  workers: number;
  warnings: string[];
  notes: string[];
  generation?: {
    assetVersion?: string;
    renderedAt?: string;
  };
};

type DatabaseVersionStatus = {
  version: string;
  service: string;
  volume: string;
  appCount: number;
  health: "ok" | "unknown" | "down" | "error";
  healthDetail?: string;
};

export async function buildStatus(
  platform: Platform,
  state: DesiredState,
): Promise<StatusReport> {
  const warnings = capacityWarnings(state);
  const notes: string[] = [];
  const composeEnvironment = await loadStackComposeEnvironment(platform);
  const nginxEnvironment = composeEnvironment.nginx;
  if (
    nginxEnvironment.hostNetwork &&
    (nginxEnvironment.httpPort !== undefined || nginxEnvironment.httpsPort !== undefined)
  ) {
    warnings.push("Nginx bridge-mode port settings are ignored while host networking is active");
  }
  if (
    !nginxEnvironment.hostNetwork && nginxEnvironment.httpPort === undefined &&
    nginxEnvironment.httpsPort === undefined
  ) {
    notes.push("Nginx uses the private stack network and has no host ports published");
  }

  const phpVersions = state.phpVersions.map((v) => {
    const apps = Object.values(state.apps).filter((a) => a.enabled && a.phpVersion === v.version);
    let poolMaxSum = 0;
    for (const a of apps) {
      poolMaxSum += FPM_PROFILES[a.fpmProfile]?.maxChildren ?? 0;
    }
    return {
      version: v.version,
      service: v.service,
      runner: `${v.service}-runner`,
      processCap: v.processCap,
      appCount: apps.length,
      poolMaxSum,
      overCap: poolMaxSum > v.processCap,
    };
  });

  let generation: StatusReport["generation"];
  const genMeta = `${platform.paths.paths.generatedDir}/.generation.json`;
  if (await platform.fs.exists(genMeta)) {
    try {
      const raw = JSON.parse(await platform.fs.readText(genMeta)) as {
        assetVersion?: string;
        renderedAt?: string;
      };
      generation = {
        assetVersion: raw.assetVersion,
        renderedAt: raw.renderedAt,
      };
    } catch {
      warnings.push("generation metadata is unreadable");
    }
  } else {
    warnings.push("stack has not been rendered yet");
  }

  // Compose file list (deterministic order)
  let composeFiles: string[] = [];
  try {
    composeFiles = await resolveComposeFiles(platform, state);
  } catch {
    composeFiles = [];
  }

  // Role observation via docker compose ps (best-effort; soft when Docker down)
  const runningNames = await observeRunningServices(platform);
  const roles = buildExpectedRoles(state, runningNames, notes);

  // Database health is probed only for observed running containers. Stopped services
  // remain down/config-ready rather than being reported as healthy.
  const databaseVersions = await Promise.all(
    state.databaseServices.map((managed) =>
      databaseVersionStatus(platform, state, managed, runningNames)
    ),
  );
  const mysqlVersions = databaseVersions.filter((_, index) =>
    state.databaseServices[index]?.engine === "mysql"
  );
  const postgresVersions = databaseVersions.filter((_, index) =>
    state.databaseServices[index]?.engine === "postgres"
  );

  return {
    stackRoot: platform.paths.paths.root,
    stackName: composeEnvironment.projectName,
    ingress: {
      mode: nginxEnvironment.hostNetwork ? "host" : "bridge",
      ...(nginxEnvironment.httpPort !== undefined ? { httpPort: nginxEnvironment.httpPort } : {}),
      ...(nginxEnvironment.httpsPort !== undefined
        ? { httpsPort: nginxEnvironment.httpsPort }
        : {}),
      http3: nginxEnvironment.http3,
    },
    defaults: {
      phpVersion: state.defaults.phpVersion,
      databaseEngine: state.defaults.database.engine,
      databaseVersion: state.defaults.database.version,
      databaseService: state.defaults.database.service,
      fpmProfile: state.defaults.fpmProfile,
      redisMode: state.defaults.redisMode,
    },
    roles,
    phpVersions,
    mysqlVersions,
    postgresVersions,
    apps: Object.values(state.apps)
      .sort((a, b) => a.slug.localeCompare(b.slug))
      .map((a) => ({
        slug: a.slug,
        enabled: a.enabled,
        uid: a.uid,
        gid: a.gid,
        domain: a.mainDomain,
        aliases: a.aliases.map(String),
        php: a.phpVersion,
        fpmProfile: a.fpmProfile,
        entrypointMode: a.entrypointMode,
        tls: a.tls.kind,
        accessLog: a.accessLog,
        databaseEngine: a.database.engine,
        databaseService: a.database.engine === "sqlite"
          ? "local-file"
          : a.database.engine === "litestream"
          ? "litestream"
          : a.database.service,
        databases: a.database.engine === "sqlite" || a.database.engine === "litestream"
          ? [sqliteContainerPath(a.database.file.id, a.slug, a.database.engine)]
          : a.database.databases.map((d) => d.name),
        redisMode: a.redis.mode,
        deploy: a.deploy.enabled,
      })),
    proxies: Object.values(state.proxies)
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((p) => ({
        name: p.name,
        domain: p.mainDomain,
        upstreams: [...p.upstreams],
        tls: p.tls.kind,
      })),
    domains: Object.entries(state.domains)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([domain, owner]) => ({
        domain,
        owner: owner.kind === "app" ? `app:${owner.slug}` : `proxy:${owner.name}`,
      })),
    composeFiles,
    cronJobs: state.cronJobs.length,
    workers: state.workers.length,
    warnings,
    notes,
    generation,
  };
}

async function databaseVersionStatus(
  platform: Platform,
  state: DesiredState,
  managed: ManagedDatabaseService,
  runningNames: Set<string> | null,
): Promise<DatabaseVersionStatus> {
  const appCount =
    Object.values(state.apps).filter((app) =>
      app.database.engine === managed.engine && app.database.service === managed.service
    ).length;
  let health: DatabaseVersionStatus["health"] = "unknown";
  let healthDetail: string | undefined;
  if (runningNames === null) {
    healthDetail = "docker unavailable";
  } else if (!runningNames.has(managed.service)) {
    health = "down";
    healthDetail = "service not running; config ready for next start";
  } else {
    const command = managed.engine === "mysql"
      ? ["mysqladmin", "ping", "-h", "127.0.0.1", "--silent"]
      : ["pg_isready", "--username", "postgres", "--dbname", "postgres"];
    const probe = await platform.process.run(
      ["docker", "compose", "exec", "-T", managed.service, ...command],
      { cwd: platform.paths.paths.root, timeoutMs: 3_000 },
    ).catch(() => ({ code: 1, stdout: "", stderr: "probe failed" }));
    if (probe.code === 0) health = "ok";
    else {
      health = "error";
      healthDetail = redactStatusText(
        (probe.stderr || probe.stdout || "probe failed").trim().slice(0, 120),
      );
    }
  }
  return {
    version: managed.version,
    service: managed.service,
    volume: managed.volume,
    appCount,
    health,
    ...(healthDetail ? { healthDetail } : {}),
  };
}

function buildExpectedRoles(
  state: DesiredState,
  running: Set<string> | null,
  notes: string[],
): RoleStatus[] {
  const roles: RoleStatus[] = [];
  const push = (
    name: string,
    kind: RoleStatus["kind"],
  ) => {
    if (running === null) {
      roles.push({
        name,
        kind,
        state: "unknown",
        detail: "docker unavailable; generated config is ready when services start",
      });
      return;
    }
    if (running.has(name)) {
      roles.push({ name, kind, state: "running" });
    } else {
      roles.push({
        name,
        kind,
        state: "config-ready",
        detail: "not running; config ready for next start (not a reload failure)",
      });
    }
  };

  push("nginx", "nginx");
  push("redis", "redis");
  for (const v of state.phpVersions) {
    push(v.service, "php-fpm");
    push(`${v.service}-runner`, "php-runner");
  }
  for (const database of state.databaseServices) {
    push(database.service, database.engine);
  }
  if (state.sqliteBackup?.enabled) push("litestream", "litestream");

  if (running === null) {
    notes.push(
      "Service process observation skipped (Docker unavailable). Status does not imply reload success.",
    );
  } else {
    const stopped = roles.filter((r) => r.state === "config-ready");
    if (stopped.length) {
      notes.push(
        `${stopped.length} expected role(s) not running; generated config is ready for their next startup.`,
      );
    }
  }

  return roles;
}

/**
 * Best-effort set of running compose service names.
 * Returns null when Docker is unavailable.
 */
async function observeRunningServices(platform: Platform): Promise<Set<string> | null> {
  const result = await platform.process.run(
    ["docker", "compose", "ps", "--services", "--status", "running"],
    { cwd: platform.paths.paths.root, timeoutMs: 4_000 },
  ).catch(() => ({ code: 1, stdout: "", stderr: "unavailable" }));

  if (result.code !== 0) {
    // Older compose may not support --status; try plain ps --services
    const fallback = await platform.process.run(
      ["docker", "compose", "ps", "--services"],
      { cwd: platform.paths.paths.root, timeoutMs: 4_000 },
    ).catch(() => ({ code: 1, stdout: "", stderr: "unavailable" }));
    if (fallback.code !== 0) return null;
    // Without status filter we cannot know running; treat as unknown
    return null;
  }

  const names = result.stdout
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  return new Set(names);
}

export function formatStatus(report: StatusReport): string {
  const lines: string[] = [];
  lines.push(`Bento status`);
  lines.push(`  stack: ${report.stackName} (${report.stackRoot})`);
  const ingressDetail = report.ingress.mode === "host"
    ? "direct host :80/:443"
    : `http=${report.ingress.httpPort ?? "internal-only"}, https=${
      report.ingress.httpsPort ?? "internal-only"
    }${report.ingress.http3 && report.ingress.httpsPort ? ", HTTP/3 UDP published" : ""}`;
  lines.push(`  ingress: ${report.ingress.mode} (${ingressDetail})`);
  if (report.generation?.renderedAt) {
    lines.push(
      `  generation: ${report.generation.renderedAt} (assets ${
        report.generation.assetVersion ?? "?"
      })`,
    );
  }
  lines.push(
    `  defaults: php=${report.defaults.phpVersion} database=${report.defaults.databaseEngine}:${report.defaults.databaseService}(${report.defaults.databaseVersion}) fpm=${report.defaults.fpmProfile} redis=${report.defaults.redisMode}`,
  );
  lines.push("");
  lines.push("Roles:");
  for (const r of report.roles) {
    const detail = r.detail ? `  (${r.detail})` : "";
    lines.push(`  ${r.name}  [${r.kind}]  ${r.state}${detail}`);
  }
  lines.push("");
  lines.push("PHP runtimes:");
  for (const v of report.phpVersions) {
    const flag = v.overCap ? " WARN over capacity" : "";
    lines.push(
      `  ${v.version}  fpm=${v.service}  runner=${v.runner}  apps=${v.appCount}  pools=${v.poolMaxSum}/${v.processCap}${flag}`,
    );
  }
  lines.push("");
  lines.push("MySQL services:");
  for (const m of report.mysqlVersions) {
    const health = m.healthDetail ? `${m.health}: ${m.healthDetail}` : m.health;
    lines.push(
      `  ${m.version}  service=${m.service}  volume=${m.volume}  apps=${m.appCount}  health=${health}`,
    );
  }
  lines.push("");
  lines.push("PostgreSQL services:");
  for (const database of report.postgresVersions) {
    const health = database.healthDetail
      ? `${database.health}: ${database.healthDetail}`
      : database.health;
    lines.push(
      `  ${database.version}  service=${database.service}  volume=${database.volume}  apps=${database.appCount}  health=${health}`,
    );
  }
  lines.push("");
  lines.push("Apps:");
  if (report.apps.length === 0) lines.push("  (none)");
  for (const a of report.apps) {
    lines.push(
      `  ${a.slug}  ${
        a.enabled ? "enabled" : "disabled"
      }  uid=${a.uid}  ${a.domain}  php=${a.php}/${a.fpmProfile}  tls=${a.tls}  entry=${a.entrypointMode}  db=${a.databaseEngine}:${a.databaseService}[${
        a.databases.join(",") || "-"
      }]  redis=${a.redisMode}  deploy=${a.deploy ? "on" : "off"}`,
    );
  }
  lines.push("");
  lines.push("Proxies:");
  if (report.proxies.length === 0) lines.push("  (none)");
  for (const p of report.proxies) {
    lines.push(`  ${p.name}  ${p.domain} -> ${p.upstreams.join(", ")}  tls=${p.tls}`);
  }
  lines.push("");
  lines.push("Domains:");
  if (report.domains.length === 0) lines.push("  (none)");
  for (const d of report.domains) {
    lines.push(`  ${d.domain}  ${d.owner}`);
  }
  lines.push("");
  lines.push("Compose files (deterministic order):");
  if (report.composeFiles.length === 0) lines.push("  (none resolved)");
  for (const f of report.composeFiles) {
    lines.push(`  - ${f}`);
  }
  lines.push("");
  lines.push(
    `Background: cron_jobs=${report.cronJobs} workers=${report.workers}`,
  );
  if (report.notes.length) {
    lines.push("");
    lines.push("Notes:");
    for (const n of report.notes) lines.push(`  - ${n}`);
  }
  if (report.warnings.length) {
    lines.push("");
    lines.push("Warnings:");
    for (const w of report.warnings) lines.push(`  - ${w}`);
  }
  return lines.join("\n") + "\n";
}

/**
 * JSON-safe status: report already omits secrets; this is a belt-and-suspenders redaction
 * of any accidental secret-like strings in free-text fields.
 */
export function statusToJson(report: StatusReport): string {
  const text = JSON.stringify(report, null, 2);
  return redactStatusText(text) + "\n";
}

function redactStatusText(text: string): string {
  return text
    .replace(/(password["']?\s*[:=]\s*["']?)([^"'\s,}\]]+)/gi, "$1***")
    .replace(/(secret["']?\s*[:=]\s*["']?)([^"'\s,}\]]+)/gi, "$1***")
    .replace(/(hmacSecret["']?\s*:\s*["'])([^"']+)/g, "$1***")
    .replace(/((?:MYSQL_PWD|PGPASSWORD|POSTGRES_PASSWORD)=)(\S+)/g, "$1***")
    .replace(/((?:CREATE|ALTER)\s+ROLE[\s\S]{0,200}?\sPASSWORD\s+(?:E)?["'])([^"']+)/gi, "$1***")
    .replace(/((?:postgres(?:ql)?):\/\/[^:\s/]+:)([^@\s/]+)(@)/gi, "$1***$3")
    .replace(/(^|\n)([^:\n]*:[^:\n]*:[^:\n]*:[^:\n]*:)([^\n]+)/g, "$1$2***");
}
