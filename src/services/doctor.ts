/** Comprehensive, non-mutating host and stack diagnostics. */

import { basename, dirname, join, resolve } from "node:path";
import pc from "picocolors";
import type { DesiredState, TlsMode } from "../domain/state.ts";
import type { Platform, RunResult } from "../platform/mod.ts";
import { checkPermissions } from "./permissions.ts";
import { composeArgs } from "./compose.ts";
import { buildStatus, statusToJson } from "./status.ts";
import { redact } from "../ui/output.ts";
import { DEFAULT_COMPOSE_PROJECT_NAME, loadStackComposeEnvironment } from "./stack_env.ts";
import { sqliteContainerPath, sqliteHostPath } from "./sqlite_paths.ts";

export type DoctorStatus = "pass" | "warn" | "fail";
export type DoctorCheck = {
  id: string;
  category: string;
  status: DoctorStatus;
  detail: string;
};
export type DoctorReport = {
  generatedAt: string;
  stackRoot: string;
  ok: boolean;
  checks: DoctorCheck[];
  summary: Record<DoctorStatus, number>;
};

const run = async (platform: Platform, command: string[], timeoutMs = 5_000) =>
  await platform.process.run(command, { cwd: platform.paths.paths.root, timeoutMs }).catch((e) => ({
    code: 1,
    stdout: "",
    stderr: e instanceof Error ? e.message : String(e),
  }));

export async function runDoctor(platform: Platform, state: DesiredState): Promise<DoctorReport> {
  const checks: DoctorCheck[] = [];
  const add = (id: string, category: string, status: DoctorStatus, detail: string) =>
    checks.push({ id, category, status, detail: redact(detail).slice(0, 500) });
  let composeEnvironment;
  let composeEnvironmentOk = true;
  try {
    composeEnvironment = await loadStackComposeEnvironment(platform);
    add(
      "stack-name",
      "compose",
      "pass",
      `stack name ${composeEnvironment.projectName} (independent from stack directory)`,
    );
  } catch (e) {
    composeEnvironmentOk = false;
    composeEnvironment = {
      projectName: DEFAULT_COMPOSE_PROJECT_NAME,
      nginx: { hostNetwork: true, http3: false },
    };
    add(
      "stack-environment",
      "compose",
      "fail",
      `invalid stack environment: ${e instanceof Error ? e.message : String(e)}`,
    );
  }
  const nginxEnvironment = composeEnvironment.nginx;

  await addHostChecks(platform, add);
  await addGenerationChecks(platform, add);

  const docker = await run(platform, ["docker", "version", "--format", "{{.Server.Version}}"]);
  if (docker.code !== 0) {
    add("docker-version", "runtime", "fail", `Docker daemon unavailable: ${failureDetail(docker)}`);
  } else {
    const version = docker.stdout.trim();
    add(
      "docker-version",
      "runtime",
      versionAtLeast(version, 20, 10) ? "pass" : "fail",
      `Docker ${version} (minimum 20.10)`,
    );
  }
  const dockerInfo = await run(platform, [
    "docker",
    "info",
    "--format",
    "{{.Driver}}|{{.Architecture}}|{{json .SecurityOptions}}",
  ]);
  if (dockerInfo.code !== 0) {
    add(
      "docker-info",
      "runtime",
      "fail",
      `cannot inspect Docker daemon: ${failureDetail(dockerInfo)}`,
    );
  } else {
    const securityRestricted = /rootless|userns/i.test(dockerInfo.stdout);
    const incompatible = !!state.sqliteBackup?.enabled && securityRestricted;
    add(
      "docker-info",
      "runtime",
      incompatible ? "fail" : "pass",
      incompatible
        ? "Litestream requires rootful Docker without user-namespace remapping"
        : `Docker daemon accessible (${dockerInfo.stdout.trim() || "details unavailable"})`,
    );
  }

  const compose = await run(platform, ["docker", "compose", "version", "--short"]);
  if (compose.code !== 0) {
    add(
      "compose-version",
      "runtime",
      "fail",
      `Docker Compose v2 unavailable: ${failureDetail(compose)}`,
    );
  } else {
    const version = compose.stdout.trim().replace(/^v/, "");
    add(
      "compose-version",
      "runtime",
      versionAtLeast(version, 2, 20) ? "pass" : "fail",
      `Compose ${version} (minimum 2.20)`,
    );
  }

  const expectedPorts = nginxEnvironment.hostNetwork
    ? [80, 443]
    : [nginxEnvironment.httpPort, nginxEnvironment.httpsPort].filter(
        (port): port is number => port !== undefined,
      );
  add(
    "nginx-network",
    "network",
    "pass",
    nginxEnvironment.hostNetwork
      ? "Nginx uses host networking"
      : expectedPorts.length > 0
        ? `Nginx uses stack-private network; published TCP ports ${expectedPorts.join(", ")}`
        : "Nginx uses stack-private network with no base host publications",
  );
  if (
    nginxEnvironment.hostNetwork &&
    (nginxEnvironment.httpPort !== undefined || nginxEnvironment.httpsPort !== undefined)
  ) {
    add(
      "nginx-host-ports-ignored",
      "network",
      "warn",
      "NGINX_HTTP_PORT/NGINX_HTTPS_PORT are ignored in host-network mode",
    );
  }
  const ports = await run(platform, ["ss", "-H", "-ltn"]);
  for (const port of expectedPorts) {
    if (ports.code !== 0) {
      add(`port-${port}`, "network", "warn", "cannot inspect listening TCP ports (ss unavailable)");
    } else {
      const listening = ports.stdout
        .split("\n")
        .some((line) => new RegExp(`[:.]${port}\\s`).test(line));
      add(
        `port-${port}`,
        "network",
        listening ? "pass" : "warn",
        listening ? `TCP ${port} is listening` : `TCP ${port} is not listening`,
      );
    }
  }
  const hasAcme =
    Object.values(state.apps).some((app) => app.tls.kind === "acme") ||
    Object.values(state.proxies).some((proxy) => proxy.tls.kind === "acme");
  if (hasAcme && !nginxEnvironment.hostNetwork && nginxEnvironment.httpPort !== 80) {
    add(
      "acme-http-port",
      "tls",
      "warn",
      "ACME HTTP-01 requires public port 80 to forward to this stack's Nginx port 80",
    );
  }

  await addFilesystemChecks(platform, add);
  await addClockCheck(platform, add);

  const domains = [...new Set(Object.keys(state.domains))].sort();
  for (const domain of domains) {
    const dns = await run(platform, ["getent", "ahosts", domain], 3_000);
    const addresses = [
      ...new Set(
        dns.stdout
          .split("\n")
          .map((line) => line.trim().split(/\s+/)[0])
          .filter((value): value is string => !!value),
      ),
    ];
    const resolved = dns.code === 0 && addresses.length > 0;
    add(
      `dns:${domain}`,
      "dns",
      resolved ? "pass" : "fail",
      resolved
        ? `${domain} resolves to ${addresses.slice(0, 4).join(", ")}${
            addresses.length > 4 ? ` (+${addresses.length - 4} more)` : ""
          }`
        : `${domain} does not resolve: ${failureDetail(dns)}`,
    );
  }

  await addCertificateChecks(platform, state, add);

  const dockerOk = docker.code === 0 && dockerInfo.code === 0;
  await addServiceChecks(platform, state, add, dockerOk);
  await addSqliteChecks(platform, state, add, dockerOk);
  await addPermissionChecks(platform, state, add);
  await addVolumeChecks(
    platform,
    state,
    add,
    composeEnvironmentOk ? composeEnvironment.projectName : undefined,
  );

  const overlays = (await platform.fs.exists(platform.paths.paths.overlaysDir))
    ? (await platform.fs.readDir(platform.paths.paths.overlaysDir)).filter((n) =>
        /\.ya?ml$/.test(n),
      )
    : [];
  let config: RunResult;
  try {
    const configArgs = await composeArgs(platform, state, ["config", "--quiet"]);
    config = await run(platform, configArgs, 10_000);
  } catch (e) {
    config = {
      code: 1,
      stdout: "",
      stderr: e instanceof Error ? e.message : String(e),
    };
  }
  add(
    "compose-config",
    "overlays",
    config.code === 0 ? "pass" : "fail",
    config.code === 0
      ? `Compose configuration valid (${overlays.length} overlay(s))`
      : `Compose/overlay configuration invalid: ${config.stderr || config.stdout}`,
  );

  await addSecretModeChecks(platform, state, add);

  const summary = { pass: 0, warn: 0, fail: 0 };
  for (const check of checks) summary[check.status]++;
  return {
    generatedAt: platform.clock.nowIso(),
    stackRoot: platform.paths.paths.root,
    ok: summary.fail === 0,
    checks,
    summary,
  };
}

type AddCheck = (id: string, category: string, status: DoctorStatus, detail: string) => void;

async function addHostChecks(platform: Platform, add: AddCheck) {
  const kernel = await run(platform, ["uname", "-s"]);
  const architecture = await run(platform, ["uname", "-m"]);
  const kernelName = kernel.stdout.trim();
  const architectureName = architecture.stdout.trim();
  const supportedArchitecture = ["x86_64", "amd64", "aarch64", "arm64"].includes(
    architectureName.toLowerCase(),
  );
  const hostSupported =
    kernel.code === 0 && kernelName === "Linux" && architecture.code === 0 && supportedArchitecture;
  add(
    "host-platform",
    "host",
    hostSupported ? "pass" : "fail",
    hostSupported
      ? `${kernelName} ${architectureName} is supported`
      : `unsupported or unknown host platform: ${kernelName || "?"} ${architectureName || "?"}`,
  );

  for (const [tool, required, purpose] of [
    ["openssl", true, "TLS operations"],
    ["ssh-keygen", true, "app deploy keys"],
    ["getent", false, "DNS diagnostics"],
    ["ss", false, "listener diagnostics"],
    ["tar", false, "support bundles and stack transfer"],
  ] as const) {
    const found = await run(platform, ["sh", "-c", `command -v ${tool} >/dev/null 2>&1`]);
    add(
      `tool:${tool}`,
      "host",
      found.code === 0 ? "pass" : required ? "fail" : "warn",
      found.code === 0
        ? `${tool} available (${purpose})`
        : `${tool} missing; ${required ? "required" : "used"} for ${purpose}`,
    );
  }

  const access = await Promise.all(
    ["r", "w", "x"].map((mode) => run(platform, ["test", `-${mode}`, platform.paths.paths.root])),
  );
  const labels = ["read", "write", "traverse"];
  const denied = access.flatMap((result, index) => (result.code === 0 ? [] : [labels[index]!]));
  add(
    "stack-root-access",
    "host",
    denied.length === 0 ? "pass" : "fail",
    denied.length === 0
      ? "stack root is readable, writable, and traversable by the current operator"
      : `stack root denies ${denied.join(", ")} access to the current operator`,
  );

  const mount = await run(platform, [
    "findmnt",
    "-n",
    "-o",
    "FSTYPE,OPTIONS",
    "--target",
    platform.paths.paths.root,
  ]);
  if (mount.code !== 0) {
    add("stack-filesystem", "host", "warn", "filesystem type and mount options unavailable");
  } else {
    const description = mount.stdout.trim();
    const readOnly = description.split(/[ ,]/).includes("ro");
    const ephemeral = /\b(tmpfs|ramfs|overlay)\b/.test(description);
    add(
      "stack-filesystem",
      "host",
      readOnly ? "fail" : ephemeral ? "warn" : "pass",
      readOnly
        ? `stack filesystem is read-only (${description})`
        : ephemeral
          ? `stack may be on ephemeral storage (${description})`
          : `stack filesystem ${description}`,
    );
  }
}

async function addGenerationChecks(platform: Platform, add: AddCheck) {
  const metadataPath = join(platform.paths.paths.generatedDir, ".generation.json");
  if (!(await platform.fs.exists(metadataPath))) {
    add(
      "generation",
      "configuration",
      "fail",
      "generated stack is missing; run bento render or apply",
    );
    return;
  }
  try {
    const metadata = JSON.parse(await platform.fs.readText(metadataPath)) as {
      assetDigest?: string;
      assetVersion?: string;
      renderedAt?: string;
      managedFiles?: unknown;
    };
    if (
      !Array.isArray(metadata.managedFiles) ||
      !metadata.managedFiles.every(
        (p) =>
          typeof p === "string" &&
          p.length > 0 &&
          !p.startsWith("/") &&
          !p.split("/").includes(".."),
      )
    ) {
      add(
        "generation",
        "configuration",
        "fail",
        "generation metadata has an invalid managed-file manifest",
      );
      return;
    }
    const missing: string[] = [];
    for (const relative of metadata.managedFiles as string[]) {
      if (!(await platform.fs.exists(join(platform.paths.paths.generatedDir, relative)))) {
        missing.push(relative);
      }
    }
    if (missing.length > 0) {
      add(
        "generation",
        "configuration",
        "fail",
        `${missing.length} managed generated file(s) missing: ${missing.slice(0, 3).join(", ")}`,
      );
      return;
    }
    const currentDigest = metadata.assetDigest
      ? await platform.assets.digest().catch(() => undefined)
      : undefined;
    const assetMismatch = currentDigest !== undefined && currentDigest !== metadata.assetDigest;
    const assetUnknown = !metadata.assetDigest || currentDigest === undefined;
    add(
      "generation",
      "configuration",
      assetMismatch || assetUnknown ? "warn" : "pass",
      assetMismatch
        ? "generated files use different bundled assets; review and apply the current Bento version"
        : assetUnknown
          ? "managed files are present, but their bundled-asset identity cannot be verified"
          : `${metadata.managedFiles.length} managed file(s) present; rendered ${
              metadata.renderedAt ?? "at an unknown time"
            } with assets ${metadata.assetVersion ?? "unknown"}`,
    );
  } catch (e) {
    add(
      "generation",
      "configuration",
      "fail",
      `generation metadata unreadable: ${e instanceof Error ? e.message : String(e)}`,
    );
  }
}

async function addFilesystemChecks(platform: Platform, add: AddCheck) {
  for (const [id, flag, label] of [
    ["disk-space", "-Pk", "disk"],
    ["disk-inodes", "-Pi", "inodes"],
  ] as const) {
    const result = await run(platform, ["df", flag, platform.paths.paths.root]);
    const columns = result.stdout.trim().split("\n").at(-1)?.trim().split(/\s+/);
    const available = Number(columns?.[3]);
    const used = Number(columns?.[4]?.replace("%", ""));
    if (
      result.code !== 0 ||
      !Number.isFinite(available) ||
      !Number.isFinite(used) ||
      used < 0 ||
      used > 100
    ) {
      add(id, "storage", "warn", `cannot inspect ${label}`);
      continue;
    }
    const criticallyLow = label === "disk" && available * 1024 < 100 * 1024 ** 2;
    const low = label === "disk" && available * 1024 < 1024 ** 3;
    const detail =
      label === "disk"
        ? `${used}% used, ${formatBytes(available * 1024)} available on the stack filesystem`
        : `${used}% used, ${available.toLocaleString("en-US")} inodes available`;
    add(
      id,
      "storage",
      used >= 95 || criticallyLow ? "fail" : used >= 85 || low ? "warn" : "pass",
      detail,
    );
  }
}

async function addClockCheck(platform: Platform, add: AddCheck) {
  const now = platform.clock.now();
  if (!Number.isFinite(now.getTime()) || now.getUTCFullYear() < 2024) {
    const shown = Number.isFinite(now.getTime()) ? now.toISOString() : String(now);
    add("clock", "host", "fail", `host clock is not plausible: ${shown}`);
  } else {
    const ntp = await run(platform, ["timedatectl", "show", "-p", "NTPSynchronized", "--value"]);
    add(
      "clock",
      "host",
      ntp.code !== 0 ? "warn" : ntp.stdout.trim() === "yes" ? "pass" : "warn",
      ntp.code !== 0
        ? `clock ${now.toISOString()}; NTP status unavailable`
        : `clock ${now.toISOString()}; NTP synchronized=${ntp.stdout.trim()}`,
    );
  }
}

async function addServiceChecks(
  platform: Platform,
  state: DesiredState,
  add: AddCheck,
  dockerOk: boolean,
) {
  if (!dockerOk) {
    add("services", "health", "fail", "service probes skipped because Docker is unavailable");
    return;
  }
  const probes: Array<[string, string, string[]]> = [
    ["nginx", "nginx", ["nginx", "-t"]],
    ["redis", "redis", ["redis-cli", "ping"]],
    ...state.phpVersions.map((v): [string, string, string[]] => [
      `php:${v.service}`,
      v.service,
      ["php-fpm", "-t"],
    ]),
    ...state.databaseServices.map((v): [string, string, string[]] =>
      v.engine === "mysql"
        ? [`mysql:${v.service}`, v.service, ["mysqladmin", "ping", "-h", "127.0.0.1", "--silent"]]
        : [
            `postgres:${v.service}`,
            v.service,
            ["pg_isready", "--username", "postgres", "--dbname", "postgres"],
          ],
    ),
  ];
  for (const [id, service, command] of probes) {
    let result: RunResult;
    try {
      const args = await composeArgs(platform, state, ["exec", "-T", service, ...command]);
      result = await run(platform, args, 5_000);
    } catch (e) {
      result = {
        code: 1,
        stdout: "",
        stderr: e instanceof Error ? e.message : String(e),
      };
    }
    const redisOk = id === "redis" ? result.stdout.trim().toUpperCase() === "PONG" : true;
    add(
      id,
      "health",
      result.code === 0 && redisOk ? "pass" : "fail",
      result.code === 0 && redisOk
        ? `${service} healthy`
        : `${service} probe failed: ${result.stderr || result.stdout}`,
    );
  }
}

async function addSqliteChecks(
  platform: Platform,
  state: DesiredState,
  add: AddCheck,
  dockerOk: boolean,
) {
  const seen = new Set<string>();
  for (const app of Object.values(state.apps)) {
    for (const database of app.databases) {
      if (database.engine !== "sqlite" && database.engine !== "litestream") continue;
      if (seen.has(database.file.id)) continue;
      seen.add(database.file.id);
      const hostPath = sqliteHostPath(
        platform,
        database.file.id,
        String(app.slug),
        database.engine,
      );
      const id = `sqlite:${app.slug}:${database.file.id}`;
      if (!(await platform.fs.exists(hostPath))) {
        add(id, "storage", "fail", `${database.engine} database file missing: ${hostPath}`);
        continue;
      }
      const stat = await platform.fs.stat(hostPath);
      if (!stat.isFile || stat.size === 0) {
        add(
          id,
          "storage",
          "fail",
          !stat.isFile ? `${hostPath} is not a regular file` : `${hostPath} is empty`,
        );
        continue;
      }
      if (!dockerOk) {
        add(
          id,
          "storage",
          "warn",
          `${database.engine} file exists (${formatBytes(
            stat.size,
          )}); integrity check skipped because Docker is unavailable`,
        );
        continue;
      }
      let result: RunResult;
      try {
        const args = await composeArgs(platform, state, [
          "exec",
          "-T",
          `${app.phpService}-runner`,
          "sqlite3",
          "-readonly",
          sqliteContainerPath(database.file.id, String(app.slug), database.engine),
          "PRAGMA quick_check;",
        ]);
        result = await run(platform, args, 10_000);
      } catch (e) {
        result = {
          code: 1,
          stdout: "",
          stderr: e instanceof Error ? e.message : String(e),
        };
      }
      const integrityOk = result.code === 0 && result.stdout.trim().toLowerCase() === "ok";
      add(
        id,
        "storage",
        integrityOk ? "pass" : "fail",
        integrityOk
          ? `${database.engine} file passes SQLite quick_check (${formatBytes(stat.size)})`
          : `SQLite quick_check failed: ${failureDetail(result)}`,
      );
    }
  }
}

async function addPermissionChecks(platform: Platform, state: DesiredState, add: AddCheck) {
  for (const app of Object.values(state.apps).sort((a, b) =>
    String(a.slug).localeCompare(String(b.slug)),
  )) {
    try {
      const report = await checkPermissions(platform, state, String(app.slug));
      add(
        `permissions:${app.slug}`,
        "permissions",
        report.issues.length ? "fail" : "pass",
        report.issues.length
          ? `${report.issues.length} issue(s): ${report.issues
              .slice(0, 3)
              .map((i) => `${i.path}: ${i.issue}`)
              .join("; ")}`
          : `${report.checked} paths checked`,
      );
    } catch (e) {
      add(
        `permissions:${app.slug}`,
        "permissions",
        "fail",
        e instanceof Error ? e.message : String(e),
      );
    }
  }
}

async function addVolumeChecks(
  platform: Platform,
  state: DesiredState,
  add: AddCheck,
  project?: string,
) {
  if (!project) {
    add("volumes", "storage", "warn", "volume checks skipped because stack name is invalid");
    return;
  }
  const volumes = ["redis-data", ...state.databaseServices.map((database) => database.volume)];
  for (const logical of volumes) {
    const name = `${project}_${logical}`;
    const result = await run(platform, ["docker", "volume", "inspect", name]);
    add(
      `volume:${logical}`,
      "storage",
      result.code === 0 ? "pass" : "fail",
      result.code === 0 ? `volume ${name} exists` : `volume ${name} missing`,
    );
  }
}

async function addSecretModeChecks(platform: Platform, state: DesiredState, add: AddCheck) {
  const paths = platform.paths.paths;
  const candidates = [
    paths.envFile,
    paths.stateFile,
    paths.rcloneConfigFile,
    join(paths.certsDir, "boot.key"),
  ];
  const privateDirectories = [
    paths.secretsDir,
    paths.rcloneDir,
    join(paths.certsDir, "private-ca"),
    join(paths.certsDir, "private-ca", "sites"),
  ];
  for (const database of state.databaseServices) {
    candidates.push(
      database.engine === "mysql"
        ? join(paths.mysqlDir, database.service, "root.cnf")
        : join(paths.postgresDir, database.service, "root.pgpass"),
    );
  }
  for (const app of Object.values(state.apps)) {
    candidates.push(join(platform.paths.appHome(app.slug), "credentials", "app.env"));
    if (app.tls.kind === "external") candidates.push(resolve(paths.certsDir, app.tls.keyPath));
    if (app.tls.kind === "self-ca") {
      candidates.push(join(paths.certsDir, "private-ca", "sites", `${app.slug}.key`));
    }
  }
  for (const proxy of Object.values(state.proxies)) {
    if (proxy.tls.kind === "external") candidates.push(resolve(paths.certsDir, proxy.tls.keyPath));
    if (proxy.tls.kind === "self-ca") {
      candidates.push(join(paths.certsDir, "private-ca", "sites", `proxy-${proxy.name}.key`));
    }
  }
  for (const dir of [paths.secretsDir, join(paths.certsDir, "private-ca")]) {
    if (await platform.fs.exists(dir)) {
      for (const name of await platform.fs.readDir(dir)) candidates.push(join(dir, name));
    }
  }
  for (const path of candidates) {
    if (!(await platform.fs.exists(path))) continue;
    const stat = await platform.fs.lstat(path);
    if (stat.isSymlink) {
      add(
        `secret-mode:${basename(path)}`,
        "secrets",
        "fail",
        `${path} is a symlink; secret files must be regular files inside the stack boundary`,
      );
      continue;
    }
    if (!stat.isFile) continue;
    const mode = stat.mode & 0o777;
    add(
      `secret-mode:${basename(path)}`,
      "secrets",
      (mode & 0o077) === 0 ? "pass" : "fail",
      `${path} mode ${mode.toString(8)} (expected no group/world access)`,
    );
  }
  for (const path of privateDirectories) {
    if (!(await platform.fs.exists(path))) continue;
    const stat = await platform.fs.lstat(path);
    const mode = stat.mode & 0o777;
    add(
      `secret-directory:${basename(path)}`,
      "secrets",
      stat.isDirectory && !stat.isSymlink && (mode & 0o077) === 0 ? "pass" : "fail",
      stat.isDirectory && !stat.isSymlink
        ? `${path} mode ${mode.toString(8)} (expected no group/world access)`
        : `${path} must be a private, non-symlink directory`,
    );
  }
}

type CertificateTarget = {
  name: string;
  path: string;
  keyPath?: string;
  hosts: string[];
  live?: boolean;
  sharedBoot?: boolean;
};

async function addCertificateChecks(platform: Platform, state: DesiredState, add: AddCheck) {
  for (const cert of certificatePaths(platform, state)) {
    if (cert.live) {
      const failures: string[] = [];
      for (const host of cert.hosts) {
        const command =
          `openssl s_client -connect ${shellQuote(`${host}:443`)} -servername ${shellQuote(
            host,
          )} ` +
          `</dev/null 2>/dev/null | openssl x509 -noout -checkhost ${shellQuote(host)} ` +
          "-checkend 2592000";
        const result = await run(platform, ["sh", "-c", command], 8_000);
        if (result.code !== 0) failures.push(host);
      }
      add(
        `certificate:${cert.name}`,
        "tls",
        failures.length === 0 ? "pass" : "warn",
        failures.length === 0
          ? `live certificate covers ${cert.hosts.join(", ")} and is valid for at least 30 days`
          : `could not verify live certificate hostname/30-day validity for ${failures.join(", ")}`,
      );
      continue;
    }

    if (!(await platform.fs.exists(cert.path))) {
      add(`certificate:${cert.name}`, "tls", "fail", `certificate missing: ${cert.path}`);
      continue;
    }
    if (cert.keyPath && !(await platform.fs.exists(cert.keyPath))) {
      add(`certificate:${cert.name}`, "tls", "fail", `private key missing: ${cert.keyPath}`);
      continue;
    }

    const validNow = await run(platform, [
      "openssl",
      "x509",
      "-in",
      cert.path,
      "-noout",
      "-checkend",
      "0",
    ]);
    const validThirtyDays =
      validNow.code === 0
        ? await run(platform, [
            "openssl",
            "x509",
            "-in",
            cert.path,
            "-noout",
            "-checkend",
            "2592000",
          ])
        : validNow;
    const dates = await run(platform, [
      "openssl",
      "x509",
      "-in",
      cert.path,
      "-noout",
      "-startdate",
      "-enddate",
    ]);
    const notBeforeText = dates.stdout.match(/^notBefore=(.+)$/m)?.[1];
    const notBefore = notBeforeText ? Date.parse(notBeforeText) : Number.NaN;
    const datesReadable = dates.code === 0 && Number.isFinite(notBefore);
    const notYetValid = datesReadable && platform.clock.now().getTime() < notBefore;
    const uncovered: string[] = [];
    for (const host of cert.hosts) {
      const hostname = await run(platform, [
        "openssl",
        "x509",
        "-in",
        cert.path,
        "-noout",
        "-checkhost",
        host,
      ]);
      if (hostname.code !== 0) uncovered.push(host);
    }

    let keyMatches = true;
    if (cert.keyPath) {
      const certificateKey = await run(platform, [
        "openssl",
        "x509",
        "-in",
        cert.path,
        "-noout",
        "-pubkey",
      ]);
      const privateKey = await run(platform, ["openssl", "pkey", "-in", cert.keyPath, "-pubout"]);
      keyMatches =
        certificateKey.code === 0 &&
        privateKey.code === 0 &&
        certificateKey.stdout.trim() === privateKey.stdout.trim();
    }

    const invalid =
      validNow.code !== 0 || !datesReadable || notYetValid || uncovered.length > 0 || !keyMatches;
    const expiring = validThirtyDays.code !== 0;
    const status: DoctorStatus = invalid ? "fail" : expiring || cert.sharedBoot ? "warn" : "pass";
    const details: string[] = [];
    if (validNow.code !== 0 || !datesReadable) details.push("expired or unreadable");
    else if (notYetValid) details.push(`not valid before ${notBeforeText}`);
    else if (expiring) details.push("expires within 30 days");
    else details.push("valid for at least 30 days");
    if (uncovered.length > 0) details.push(`does not cover ${uncovered.join(", ")}`);
    else if (cert.hosts.length > 0) details.push(`covers ${cert.hosts.join(", ")}`);
    if (!keyMatches) details.push("certificate/private-key mismatch");
    if (cert.sharedBoot) details.push("shared boot certificate is a non-production fallback");
    add(`certificate:${cert.name}`, "tls", status, `${cert.name}: ${details.join("; ")}`);
  }
}

function certificatePaths(platform: Platform, state: DesiredState): CertificateTarget[] {
  const certs: CertificateTarget[] = [];
  const add = (name: string, aliases: string[], tls: TlsMode, id: string) => {
    const hosts = [name, ...aliases];
    if (tls.kind === "self-ca") {
      const base = join(platform.paths.paths.certsDir, "private-ca", "sites", id);
      certs.push({ name, hosts, path: `${base}.crt`, keyPath: `${base}.key` });
    }
    if (tls.kind === "external") {
      certs.push({
        name,
        hosts,
        path: resolve(platform.paths.paths.certsDir, tls.certPath),
        keyPath: resolve(platform.paths.paths.certsDir, tls.keyPath),
      });
    }
    if (tls.kind === "acme") {
      certs.push({
        name,
        hosts,
        path: join(platform.paths.paths.certsDir, "acme-state"),
        live: true,
      });
    }
  };
  for (const app of Object.values(state.apps)) {
    add(String(app.mainDomain), app.aliases.map(String), app.tls, String(app.slug));
  }
  for (const proxy of Object.values(state.proxies)) {
    add(String(proxy.mainDomain), proxy.aliases.map(String), proxy.tls, `proxy-${proxy.name}`);
  }
  if (
    [...Object.values(state.apps), ...Object.values(state.proxies)].some(
      (site) => site.tls.kind === "shared",
    )
  ) {
    certs.push({
      name: "shared boot certificate",
      hosts: [],
      path: join(platform.paths.paths.certsDir, "boot.crt"),
      keyPath: join(platform.paths.paths.certsDir, "boot.key"),
      sharedBoot: true,
    });
  }
  return certs;
}

function failureDetail(result: RunResult): string {
  return (result.stderr || result.stdout || `exit ${result.code}`).trim().slice(0, 180);
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KiB`;
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MiB`;
  return `${(value / 1024 ** 3).toFixed(1)} GiB`;
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function versionAtLeast(value: string, major: number, minor: number): boolean {
  const m = value.match(/(\d+)\.(\d+)/);
  return !!m && (Number(m[1]) > major || (Number(m[1]) === major && Number(m[2]) >= minor));
}

export function formatDoctor(report: DoctorReport): string {
  const lines = [
    `Bento doctor (${report.ok ? "healthy" : "problems found"})`,
    `  stack: ${report.stackRoot}`,
    "",
  ];
  const routineChecks = report.checks.filter((check) => check.status !== "fail");
  const categories = [...new Set(routineChecks.map((check) => check.category))];
  for (const category of categories) {
    lines.push(`${category}:`);
    for (const check of routineChecks.filter((candidate) => candidate.category === category)) {
      const label = check.status.toUpperCase();
      const colored = check.status === "pass" ? pc.green(label) : pc.yellow(label);
      lines.push(`  [${colored}] ${check.id}: ${check.detail}`);
    }
  }

  const failures = report.checks.filter((check) => check.status === "fail");
  if (failures.length > 0) {
    lines.push("", "FAILED checks:");
    for (const check of failures) {
      lines.push(`  [${pc.red("FAIL")}] ${check.category}/${check.id}: ${check.detail}`);
    }
  }
  lines.push(
    "",
    `Summary: ${report.summary.pass} passed, ${report.summary.warn} warnings, ${report.summary.fail} failed`,
  );
  return lines.join("\n") + "\n";
}

/** Create a tar.gz containing only redacted, operator-safe diagnostic text. */
export async function createSupportBundle(
  platform: Platform,
  state: DesiredState,
  output: string,
): Promise<string> {
  const destination = resolve(output);
  const temp = join(platform.paths.paths.root, `.support-${platform.random.id()}`);
  await platform.fs.mkdirp(dirname(destination), 0o700);
  await platform.fs.mkdirp(temp, 0o700);
  try {
    const doctor = await runDoctor(platform, state);
    const status = await buildStatus(platform, state);
    await platform.fs.writeText(join(temp, "doctor.json"), safeJson(doctor), 0o600);
    await platform.fs.writeText(join(temp, "status.json"), redact(statusToJson(status)), 0o600);
    await platform.fs.writeText(
      join(temp, "state.redacted.json"),
      safeJson(redactObject(state)),
      0o600,
    );
    if (await platform.fs.exists(platform.paths.paths.envFile)) {
      const env = await platform.fs.readText(platform.paths.paths.envFile);
      await platform.fs.writeText(
        join(temp, "environment.redacted.txt"),
        redactEnvironment(env),
        0o600,
      );
    }
    const composePs = await composeArgs(platform, state, ["ps", "--all"]).catch(() => [
      "docker",
      "compose",
      "ps",
      "--all",
    ]);
    for (const [name, command] of [
      ["docker-info.txt", ["docker", "info"]],
      ["compose-ps.txt", composePs],
      ["system.txt", ["uname", "-a"]],
    ] as Array<[string, string[]]>) {
      const result: RunResult = await run(platform, command, 10_000);
      await platform.fs.writeText(
        join(temp, name),
        redact(`${result.stdout}\n${result.stderr}`),
        0o600,
      );
    }
    const partial = `${destination}.partial`;
    await platform.fs.remove(partial).catch(() => undefined);
    const tar = await platform.process.run(["tar", "-czf", partial, "-C", temp, "."], {
      timeoutMs: 30_000,
    });
    if (tar.code !== 0) {
      await platform.fs.remove(partial).catch(() => undefined);
      throw new Error(`failed to create support bundle: ${tar.stderr || tar.stdout}`);
    }
    await platform.fs.chmod(partial, 0o600);
    await platform.fs.rename(partial, destination);
    return destination;
  } finally {
    await platform.fs.remove(temp, { recursive: true });
  }
}

function redactObject(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactObject);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) {
      out[key] = /(pass(word)?|secret|token|private.?key|hmac|credential)/i.test(key)
        ? "***"
        : redactObject(item);
    }
    return out;
  }
  return typeof value === "string" ? redact(value) : value;
}
function safeJson(value: unknown) {
  return JSON.stringify(value, null, 2) + "\n";
}
function redactEnvironment(text: string): string {
  return text
    .split("\n")
    .map((line) => {
      const match = line.match(/^([^#=]+)=(.*)$/);
      if (!match) return line;
      return /(PASS|SECRET|TOKEN|KEY|CREDENTIAL|AUTH)/i.test(match[1]!) ? `${match[1]}=***` : line;
    })
    .join("\n");
}
