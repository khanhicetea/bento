/**
 * Generate complete candidate configuration from desired state.
 */

import { isPhpApp, type AppState, type DesiredState, type ProxySite } from "../domain/state.ts";
import type { Platform } from "../platform/mod.ts";
import { FPM_PROFILES, SHARED_SOCKET_GID } from "../domain/types.ts";
import { validationError } from "../domain/errors.ts";
import { ASSET_VERSION } from "../version.ts";
import { renderTemplate } from "./template.ts";
import { type GeneratedFile, withManagedMarker } from "./render.ts";
import { containerAppHome } from "../platform/paths.ts";
import { assembleComposeDocuments } from "./compose.ts";
import {
  loadAcmeEnvironment,
  loadHttp3Enabled,
  loadMysqlRootPassword,
  loadPostgresRootPassword,
  loadStackComposeEnvironment,
  loadStackEnv,
} from "./stack_env.ts";
import {
  renderAcmeIssuer,
  renderAcmeSslSnippet,
  renderSslCommonSnippet,
  resolveSslForSite,
} from "./tls.ts";
import { validateUpstreams } from "./proxy.ts";
import { loadCloudflareTunnelToken } from "./cloudflare_tunnel.ts";
import {
  appInternalJobs,
  minicrondBootstrapConfig,
  rootInternalJobs,
} from "./minicrond_internal.ts";

export async function generateAll(
  platform: Platform,
  state: DesiredState,
  assetDigest: string,
): Promise<GeneratedFile[]> {
  const files: GeneratedFile[] = [];

  // Compose assembly (stack name and ingress topology come from operator-owned .env).
  const composeEnvironment = await loadStackComposeEnvironment(platform);
  const composeFiles = assembleComposeDocuments(platform, state, composeEnvironment);
  for (const f of composeFiles) files.push(f);
  const cloudflareToken = (await loadCloudflareTunnelToken(platform)) ?? "";
  files.push({
    relPath: "secrets/cloudflare/tunnel.env",
    content: withManagedMarker(`TUNNEL_TOKEN=${cloudflareToken}\n`),
    mode: 0o600,
    managed: true,
  });

  // Nginx core + sites
  files.push(...(await generateNginx(platform, state)));

  // PHP pools per app
  files.push(...(await generatePhpPools(platform, state)));

  // Runner: per-app and root minicrond services supervised by s6
  files.push(...generateRunnerConfig(state));

  // One dedicated Litestream daemon discovers every managed SQLite database.
  files.push(...generateLitestreamConfig(state));
  files.push(...generateLitestreamEnvironment(state, await loadStackEnv(platform)));

  // Database administrator client files (restricted; passwords from stack .env).
  const mysqlRootPassword = (await loadMysqlRootPassword(platform)) ?? "";
  files.push(...generateMysqlSecrets(state, mysqlRootPassword));
  const postgresRootPassword = (await loadPostgresRootPassword(platform)) ?? "";
  files.push(...generatePostgresSecrets(state, postgresRootPassword));

  // Generation marker
  files.push({
    relPath: "MANIFEST.txt",
    content: withManagedMarker(
      [
        `assetVersion=${ASSET_VERSION}`,
        `assetDigest=${assetDigest}`,
        `apps=${Object.keys(state.apps).sort().join(",")}`,
        `php=${state.phpVersions.map((v) => v.version).join(",")}`,
        `mysql=${state.databaseServices
          .filter((v) => v.engine === "mysql")
          .map((v) => v.version)
          .join(",")}`,
        `postgres=${state.databaseServices
          .filter((v) => v.engine === "postgres")
          .map((v) => v.version)
          .join(",")}`,
        "",
      ].join("\n"),
    ),
    mode: 0o644,
    managed: true,
  });

  return files;
}

async function generateNginx(platform: Platform, state: DesiredState): Promise<GeneratedFile[]> {
  const files: GeneratedFile[] = [];
  const http3 = await loadHttp3Enabled(platform);
  const composeEnvironment = await loadStackComposeEnvironment(platform);
  const publishedHttpsPort = composeEnvironment.nginx.hostNetwork
    ? 443
    : (composeEnvironment.nginx.httpsPort ?? 443);
  const httpsPortSuffix = publishedHttpsPort === 443 ? "" : `:${publishedHttpsPort}`;
  // Nginx templates are compiled immutable assets. Fail candidate generation when
  // one is missing instead of silently rendering a stale in-code fallback.
  const mainTpl = await platform.assets.readText("nginx/nginx.conf.tpl");
  // Keep the shared issuer present from the first Nginx start. The ACME module
  // cannot introduce a previously absent issuer with a worker reload alone.
  const acme = await loadAcmeEnvironment(platform);
  const acmeIssuers = renderAcmeIssuer(acme.url, acme.email);
  files.push({
    relPath: "nginx/nginx.conf",
    content: withManagedMarker(
      renderTemplate(mainTpl, {
        workerConnections: 8192,
        acmeIssuers,
      }),
    ),
    mode: 0o644,
    managed: true,
  });

  // Shared TLS snippets. ACME identifiers are inferred independently from each
  // including server block's server_name values.
  files.push({
    relPath: "nginx/snippets/ssl-common.conf",
    content: withManagedMarker(renderSslCommonSnippet()),
    mode: 0o644,
    managed: true,
  });
  files.push({
    relPath: "nginx/snippets/boot-ssl.conf",
    content: withManagedMarker(
      await platform.assets.readText("docker/nginx/snippets/boot-ssl.conf"),
    ),
    mode: 0o644,
    managed: true,
  });
  files.push({
    relPath: "nginx/snippets/acme-ssl.conf",
    content: withManagedMarker(renderAcmeSslSnippet()),
    mode: 0o644,
    managed: true,
  });
  files.push({
    relPath: "nginx/snippets/app-common.conf",
    content: withManagedMarker(await platform.assets.readText("nginx/snippets/app-common.conf")),
    mode: 0o644,
    managed: true,
  });
  files.push({
    relPath: "nginx/snippets/proxy-common.conf",
    content: withManagedMarker(await platform.assets.readText("nginx/snippets/proxy-common.conf")),
    mode: 0o644,
    managed: true,
  });

  const defaultVhostTpl = await platform.assets.readText("nginx/default-vhost.conf.tpl");
  files.push({
    relPath: "nginx/sites/00-default.conf",
    content: withManagedMarker(renderTemplate(defaultVhostTpl, { http3 })),
    mode: 0o644,
    managed: true,
  });

  for (const app of Object.values(state.apps)) {
    if (app.enabled) {
      files.push(
        ...(await generateAppVhost(
          platform,
          state,
          app,
          http3,
          httpsPortSuffix,
          publishedHttpsPort,
        )),
      );
    }
  }
  for (const proxy of Object.values(state.proxies)) {
    if (proxy.enabled) {
      files.push(
        ...(await generateProxyVhost(platform, proxy, http3, httpsPortSuffix, publishedHttpsPort)),
      );
    }
  }

  return files;
}

async function generateAppVhost(
  platform: Platform,
  _state: DesiredState,
  app: AppState,
  http3: boolean,
  httpsPortSuffix: string,
  httpsAdvertisedPort: number,
): Promise<GeneratedFile[]> {
  let tpl: string;
  const upstreamTemplate = isPhpApp(app)
    ? "nginx/app-vhost.conf.tpl"
    : "nginx/process-app-vhost.conf.tpl";
  if (app.vhostTemplate.kind === "custom") {
    try {
      tpl = await platform.fs.readText(app.vhostTemplate.sourcePath);
    } catch {
      tpl = await platform.assets.readText(upstreamTemplate);
    }
  } else {
    tpl = await platform.assets.readText(upstreamTemplate);
  }

  const serverNames = [app.mainDomain, ...app.aliases].join(" ");
  const codeRoot = `${containerAppHome(app.slug)}/code`;
  const docRoot = isPhpApp(app)
    ? app.documentRoot && app.documentRoot !== "."
      ? `${codeRoot}/${app.documentRoot}`
      : codeRoot
    : codeRoot;
  const socketPath = isPhpApp(app)
    ? `/run/php-fpm/${app.phpService}/${app.slug}.sock`
    : `/run/bento-apps/${app.slug}/http.sock`;
  const ssl = resolveSslForSite(app.tls, app.slug, String(app.mainDomain));
  const content = renderTemplate(tpl, {
    slug: app.slug,
    serverNames,
    docRoot,
    socketPath,
    entrypointMode: isPhpApp(app) ? app.entrypointMode : "process",
    frontController: isPhpApp(app) && app.entrypointMode === "front-controller",
    legacy: isPhpApp(app) && app.entrypointMode === "legacy",
    processApp: !isPhpApp(app),
    upstreamName: `app_${String(app.slug).replaceAll("-", "_")}`,
    accessLog: app.accessLog,
    accessLogPath: `/var/log/nginx/${app.slug}.access.log`,
    tlsKind: app.tls.kind,
    realTls: app.tls.kind !== "shared",
    redirectHttps: ssl.redirectHttps,
    httpsPortSuffix,
    httpsAdvertisedPort,
    sslInclude: ssl.includePath,
    sslCertificate: ssl.certificatePath,
    sslCertificateKey: ssl.certificateKeyPath,
    http3,
    deployEnabled: app.deploy.enabled,
    deploySecret: app.deploy.hmacSecret ?? "",
    uid: app.uid,
    gid: app.gid,
    home: containerAppHome(app.slug),
  });

  const files: GeneratedFile[] = [
    {
      relPath: `nginx/sites/${app.slug}.conf`,
      content: withManagedMarker(content),
      mode: 0o644,
      managed: true,
    },
  ];
  if (ssl.snippetRelPath && ssl.snippetContent) {
    files.push({
      relPath: ssl.snippetRelPath,
      content: withManagedMarker(ssl.snippetContent),
      mode: 0o644,
      managed: true,
    });
  }
  return files;
}

async function generateProxyVhost(
  platform: Platform,
  proxy: ProxySite,
  http3: boolean,
  httpsPortSuffix: string,
  httpsAdvertisedPort: number,
): Promise<GeneratedFile[]> {
  const tpl = await platform.assets.readText("nginx/proxy-vhost.conf.tpl");
  const serverNames = [proxy.mainDomain, ...proxy.aliases].join(" ");
  const ssl = resolveSslForSite(proxy.tls, `proxy-${proxy.name}`, String(proxy.mainDomain));
  const upstream = validateUpstreams(proxy.upstreams);
  const content = renderTemplate(tpl, {
    name: proxy.name,
    serverNames,
    upstreamName: `upstream_${proxy.name}`,
    upstreamServers: upstream.servers,
    upstreamScheme: upstream.scheme,
    upstreamUri: upstream.uri,
    accessLog: proxy.accessLog,
    accessLogPath: `/var/log/nginx/proxy-${proxy.name}.access.log`,
    tlsKind: proxy.tls.kind,
    realTls: proxy.tls.kind !== "shared",
    redirectHttps: ssl.redirectHttps,
    httpsPortSuffix,
    httpsAdvertisedPort,
    sslInclude: ssl.includePath,
    sslCertificate: ssl.certificatePath,
    sslCertificateKey: ssl.certificateKeyPath,
    http3,
  });
  const files: GeneratedFile[] = [
    {
      relPath: `nginx/sites/proxy-${proxy.name}.conf`,
      content: withManagedMarker(content),
      mode: 0o644,
      managed: true,
    },
  ];
  if (ssl.snippetRelPath && ssl.snippetContent) {
    files.push({
      relPath: ssl.snippetRelPath,
      content: withManagedMarker(ssl.snippetContent),
      mode: 0o644,
      managed: true,
    });
  }
  return files;
}

async function generatePhpPools(platform: Platform, state: DesiredState): Promise<GeneratedFile[]> {
  const files: GeneratedFile[] = [];
  for (const app of Object.values(state.apps)) {
    if (!app.enabled || !isPhpApp(app)) continue;
    let tpl: string;
    if (app.poolTemplate.kind === "custom") {
      try {
        tpl = await platform.fs.readText(app.poolTemplate.sourcePath);
      } catch {
        tpl = await readOrDefault(platform, "php/pool.conf.tpl", DEFAULT_POOL);
      }
    } else {
      tpl = await readOrDefault(platform, "php/pool.conf.tpl", DEFAULT_POOL);
    }
    const profile = FPM_PROFILES[app.fpmProfile] ?? FPM_PROFILES.small!;
    const dynamic = profile.manager === "dynamic";
    const home = containerAppHome(app.slug);
    const content = renderTemplate(tpl, {
      slug: app.slug,
      uid: app.uid,
      gid: app.gid,
      home,
      processManager: profile.manager,
      dynamic,
      ondemand: profile.manager === "ondemand",
      maxChildren: profile.maxChildren,
      startServers: dynamic ? profile.startServers : 0,
      minSpare: dynamic ? profile.minSpare : 0,
      maxSpare: dynamic ? profile.maxSpare : 0,
      processIdleTimeout: dynamic ? "" : profile.processIdleTimeout,
      socketPath: `/run/php-fpm/${app.slug}.sock`,
      openBasedir: `${home}:/usr/share/php:/tmp${app.databases
        .filter((database) => database.engine === "sqlite" || database.engine === "litestream")
        .map((database) =>
          database.engine === "sqlite" || database.engine === "litestream"
            ? `:/sqlite/${database.file.id}`
            : "",
        )
        .join("")}${app.deploy.enabled ? ":/opt/bento/helpers" : ""}`,
      deployEnabled: app.deploy.enabled,
    });
    files.push({
      relPath: `php/${app.phpService}/pools/${app.slug}.conf`,
      // PHP-FPM pool files are INI-style: only ';' comments are valid.
      content: withManagedMarker(content, "semicolon"),
      mode: 0o644,
      managed: true,
    });
  }
  // Ensure per-version pool directory placeholder + include snippet for the image
  for (const v of state.phpVersions) {
    files.push({
      relPath: `php/${v.service}/pools/.keep`,
      content: withManagedMarker(`; pools for ${v.service}\n`, "semicolon"),
      mode: 0o644,
      managed: true,
    });
    files.push({
      relPath: `php/${v.service}/zz-bento-pools.conf`,
      content: withManagedMarker(
        `; Include bind-mounted per-app pools\ninclude=/usr/local/etc/php-fpm.d/bento/*.conf\n`,
        "semicolon",
      ),
      mode: 0o644,
      managed: true,
    });
  }
  return files;
}

export function generateLitestreamConfig(state: DesiredState): GeneratedFile[] {
  const backup = state.sqliteBackup;
  if (!backup?.enabled) return [];

  const lines = [
    "logging:",
    "  level: info",
    "  type: json",
    "socket:",
    "  enabled: true",
    "  path: /run/litestream/control.sock",
    "  permissions: 0600",
    "snapshot:",
    `  interval: ${backup.snapshotInterval}`,
    `  retention: ${backup.snapshotRetention}`,
    `l0-retention: ${backup.l0Retention}`,
    "validation:",
    "  interval: 24h",
    "verify-compaction: false",
    "shutdown-sync-timeout: 30s",
    "dbs:",
    "  - dir: /sqlite",
    '    pattern: "*.sqlite"',
    "    recursive: true",
    "    watch: true",
    "    meta-dir: /var/lib/litestream",
    "    monitor-interval: 10s",
    "    checkpoint-interval: 5m",
    "    busy-timeout: 5s",
    "    replica:",
    `      sync-interval: ${backup.syncInterval}`,
    "      url: s3://${S3_BUCKET_NAME}/bento/${COMPOSE_PROJECT_NAME}?endpoint=${S3_ENDPOINT}&region=${S3_REGION}",
    "",
  ];
  return [
    {
      relPath: "litestream/litestream.yml",
      content: withManagedMarker(lines.join("\n")),
      // Contains environment references but never credential values.
      mode: 0o644,
      managed: true,
    },
  ];
}

export function generateLitestreamEnvironment(
  state: DesiredState,
  env: Record<string, string>,
): GeneratedFile[] {
  if (!state.sqliteBackup?.enabled) return [];

  const required = [
    "S3_BUCKET_NAME",
    "S3_REGION",
    "S3_ACCESS_KEY_ID",
    "S3_SECRET_ACCESS_KEY",
  ] as const;
  for (const key of required) {
    if (!env[key]) throw validationError(`${key} is required in the stack .env`);
  }
  for (const key of [...required, "S3_ENDPOINT"] as const) {
    if (env[key]?.includes("\n") || env[key]?.includes("\r")) {
      throw validationError(`${key} in the stack .env must not contain line breaks`);
    }
  }

  return [
    {
      relPath: "secrets/litestream/stack-s3.env",
      content: withManagedMarker(
        [
          `S3_BUCKET_NAME=${env.S3_BUCKET_NAME}`,
          `S3_REGION=${env.S3_REGION}`,
          `S3_ENDPOINT=${env.S3_ENDPOINT ?? ""}`,
          `AWS_ACCESS_KEY_ID=${env.S3_ACCESS_KEY_ID}`,
          `AWS_SECRET_ACCESS_KEY=${env.S3_SECRET_ACCESS_KEY}`,
          `AWS_REGION=${env.S3_REGION}`,
          "",
        ].join("\n"),
      ),
      mode: 0o600,
      managed: true,
    },
  ];
}

function generateRunnerConfig(state: DesiredState): GeneratedFile[] {
  const files: GeneratedFile[] = [];
  for (const v of state.phpVersions) {
    const appsOnVersion = Object.values(state.apps).filter(
      (a) => isPhpApp(a) && a.enabled && a.phpVersion === v.version,
    );
    // User definitions belong exclusively to each app's minicrond registry.
    for (const app of appsOnVersion) {
      const internalJobs = appInternalJobs(state, app);
      const config = `runner/${v.service}/minicrond/${app.slug}`;
      files.push(
        {
          relPath: `${config}/config.toml`,
          content: minicrondBootstrapConfig(internalJobs),
          mode: 0o644,
          managed: true,
        },
        {
          relPath: `runner/${v.service}/services/minicrond-${app.slug}/run`,
          content: `#!/bin/sh\n# bento-managed: true\n# config-sha256: ${Bun.hash(internalJobs)}\nexport BASE_PATH=/scheduler/apps/${app.slug}/\nexec /usr/local/bin/bento-minicrond-start ${app.uid} ${app.gid} ${shellQuote(app.home)} ${app.slug} ${shellQuote(`${app.home}/.local/share/minicron`)} /etc/bento/minicrond/${app.slug}/config.toml\n`,
          mode: 0o755,
          managed: true,
        },
      );
    }

    // A mutable /run scan tree is reconciled from these read-only service
    // directories. This lets s6-svscan discover additions/removals without
    // recycling the runner container or unrelated services.
    files.push({
      relPath: `runner/${v.service}/services/.keep`,
      content: withManagedMarker("# s6 service definitions\n"),
      mode: 0o644,
      managed: true,
    });

    for (const app of appsOnVersion) {
      // FPM and app logs can be root-owned; keep rotation in the root
      // minicrond instance, never inside an app-owned scheduler.
      files.push({
        relPath: `runner/${v.service}/minicrond/logrotate/${app.slug}.conf`,
        content: withManagedMarker(
          `"${app.home}/logs/cron/*.log" "${app.home}/logs/php/*.log" "${app.home}/logs/worker/*.log" "${app.home}/logs/worker/*.err" {
  size 10M
  rotate 2
  missingok
  notifempty
  nocompress
  copytruncate
}
`,
        ),
        mode: 0o644,
        managed: true,
      });
    }

    if (appsOnVersion.length > 0) {
      const internalJobs = rootInternalJobs(appsOnVersion);
      files.push({
        relPath: `runner/${v.service}/minicrond/root-config.toml`,
        content: minicrondBootstrapConfig(internalJobs),
        mode: 0o644,
        managed: true,
      });
      files.push({
        relPath: `runner/${v.service}/services/minicrond-root/run`,
        content: `#!/bin/sh\n# bento-managed: true\nexec /usr/local/bin/bento-minicrond-start 0 0 /root root /var/lib/bento/minicron /etc/bento/minicrond/root-config.toml\n`,
        mode: 0o755,
        managed: true,
      });
    }
  }
  return files;
}

/**
 * Materialize root MySQL client option files with real password content from stack env.
 * Mode is always 0600; files are disposable generated config (not durable secrets store).
 */
export function generatePostgresSecrets(
  state: DesiredState,
  rootPassword: string,
): GeneratedFile[] {
  const files: GeneratedFile[] = [];
  // .pgpass escapes backslashes and field delimiters. Strip line breaks so an
  // operator-supplied value cannot create a second credential record.
  const escapedPassword = rootPassword
    .replace(/[\r\n]/g, "")
    .replace(/\\/g, "\\\\")
    .replace(/:/g, "\\:");
  for (const postgres of state.databaseServices.filter((v) => v.engine === "postgres")) {
    files.push({
      relPath: `postgres/${postgres.service}/root.pgpass`,
      content: withManagedMarker(`*:*:*:postgres:${escapedPassword}\n`),
      mode: 0o600,
      managed: true,
    });
  }
  return files;
}

export function generateMysqlSecrets(state: DesiredState, rootPassword: string): GeneratedFile[] {
  const files: GeneratedFile[] = [];
  for (const m of state.databaseServices.filter((v) => v.engine === "mysql")) {
    // MySQL accepts # comments; marker keeps file in the managed set.
    files.push({
      relPath: `mysql/${m.service}/root.cnf`,
      content: withManagedMarker(`[client]
user=root
password=${rootPassword.replace(/\n/g, "")}
protocol=socket
socket=/var/run/mysqld/mysqld.sock
`),
      mode: 0o600,
      managed: true,
    });
  }
  return files;
}

function shellQuote(s: string): string {
  if (/^[a-zA-Z0-9_./:@%+=,-]+$/.test(s)) return s;
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

async function readOrDefault(
  platform: Platform,
  assetPath: string,
  fallback: string,
): Promise<string> {
  try {
    return await platform.assets.readText(assetPath);
  } catch {
    return fallback;
  }
}

const DEFAULT_POOL = `[{{slug}}]
user = {{uid}}
group = {{gid}}
listen = {{socketPath}}
listen.owner = {{uid}}
listen.group = ${SHARED_SOCKET_GID}
listen.mode = 0660
pm = {{processManager}}
pm.max_children = {{maxChildren}}
{{#dynamic}}
pm.start_servers = {{startServers}}
pm.min_spare_servers = {{minSpare}}
pm.max_spare_servers = {{maxSpare}}
{{/dynamic}}
{{#ondemand}}
pm.process_idle_timeout = {{processIdleTimeout}}
{{/ondemand}}
php_admin_value[open_basedir] = {{openBasedir}}
php_admin_value[upload_tmp_dir] = {{home}}/tmp
php_admin_value[session.save_path] = {{home}}/tmp/sessions
slowlog = {{home}}/logs/php/slow.log
request_slowlog_timeout = 15s
`;
