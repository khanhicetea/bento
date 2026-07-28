/**
 * Generate complete candidate configuration from desired state.
 */

import type { AppState, CronJob, DesiredState, ProxySite, Worker } from "../domain/state.ts";
import type { Platform } from "../platform/mod.ts";
import { FPM_PROFILES, SHARED_SOCKET_GID } from "../domain/types.ts";
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
} from "./stack_env.ts";
import {
  renderAcmeIssuer,
  renderAcmeSslSnippet,
  renderSslCommonSnippet,
  resolveSslForSite,
} from "./tls.ts";
import { validateUpstreams } from "./proxy.ts";

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

  // Nginx core + sites
  files.push(...await generateNginx(platform, state));

  // PHP pools per app
  files.push(...await generatePhpPools(platform, state));

  // Runner: Supercronic and worker service directories supervised by s6
  files.push(...generateRunnerConfig(state));

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
        `mysql=${
          state.databaseServices.filter((v) => v.engine === "mysql").map((v) => v.version).join(",")
        }`,
        `postgres=${
          state.databaseServices.filter((v) => v.engine === "postgres").map((v) => v.version).join(
            ",",
          )
        }`,
        "",
      ].join("\n"),
    ),
    mode: 0o644,
    managed: true,
  });

  return files;
}

async function generateNginx(
  platform: Platform,
  state: DesiredState,
): Promise<GeneratedFile[]> {
  const files: GeneratedFile[] = [];
  const http3 = await loadHttp3Enabled(platform);
  const composeEnvironment = await loadStackComposeEnvironment(platform);
  const publishedHttpsPort = composeEnvironment.nginx.hostNetwork
    ? 443
    : composeEnvironment.nginx.httpsPort ?? 443;
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
    content: withManagedMarker(renderTemplate(mainTpl, {
      workerConnections: 8192,
      acmeIssuers,
    })),
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
    content: withManagedMarker(
      await platform.assets.readText("nginx/snippets/app-common.conf"),
    ),
    mode: 0o644,
    managed: true,
  });
  files.push({
    relPath: "nginx/snippets/proxy-common.conf",
    content: withManagedMarker(
      await platform.assets.readText("nginx/snippets/proxy-common.conf"),
    ),
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
        ...await generateAppVhost(
          platform,
          state,
          app,
          http3,
          httpsPortSuffix,
          publishedHttpsPort,
        ),
      );
    }
  }
  for (const proxy of Object.values(state.proxies)) {
    files.push(
      ...await generateProxyVhost(
        platform,
        proxy,
        http3,
        httpsPortSuffix,
        publishedHttpsPort,
      ),
    );
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
  if (app.vhostTemplate.kind === "custom") {
    try {
      tpl = await platform.fs.readText(app.vhostTemplate.sourcePath);
    } catch {
      tpl = await platform.assets.readText("nginx/app-vhost.conf.tpl");
    }
  } else {
    tpl = await platform.assets.readText("nginx/app-vhost.conf.tpl");
  }

  const serverNames = [app.mainDomain, ...app.aliases].join(" ");
  // App code lives under /home/<slug>/code; documentRoot is relative to that tree.
  const codeRoot = `${containerAppHome(app.slug)}/code`;
  const docRoot = app.documentRoot && app.documentRoot !== "."
    ? `${codeRoot}/${app.documentRoot}`
    : codeRoot;
  const socketPath = `/run/php-fpm/${app.phpService}/${app.slug}.sock`;
  const ssl = resolveSslForSite(app.tls, app.slug, String(app.mainDomain));
  const content = renderTemplate(tpl, {
    slug: app.slug,
    serverNames,
    docRoot,
    socketPath,
    entrypointMode: app.entrypointMode,
    frontController: app.entrypointMode === "front-controller",
    legacy: app.entrypointMode === "legacy",
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

  const files: GeneratedFile[] = [{
    relPath: `nginx/sites/${app.slug}.conf`,
    content: withManagedMarker(content),
    mode: 0o644,
    managed: true,
  }];
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
  const files: GeneratedFile[] = [{
    relPath: `nginx/sites/proxy-${proxy.name}.conf`,
    content: withManagedMarker(content),
    mode: 0o644,
    managed: true,
  }];
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

async function generatePhpPools(
  platform: Platform,
  state: DesiredState,
): Promise<GeneratedFile[]> {
  const files: GeneratedFile[] = [];
  for (const app of Object.values(state.apps)) {
    if (!app.enabled) continue;
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
      openBasedir: `${home}:/usr/share/php:/tmp${app.deploy.enabled ? ":/opt/bento/helpers" : ""}`,
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

function generateRunnerConfig(state: DesiredState): GeneratedFile[] {
  const files: GeneratedFile[] = [];
  for (const v of state.phpVersions) {
    const appsOnVersion = Object.values(state.apps).filter((a) =>
      a.enabled && a.phpVersion === v.version
    );
    const jobs = state.cronJobs.filter((j) =>
      appsOnVersion.some((a) => a.slug === j.app) && j.enabled
    );
    const workers = state.workers.filter((w) =>
      appsOnVersion.some((a) => a.slug === w.app) && w.enabled
    );

    // Per-app crontab files
    for (const app of appsOnVersion) {
      const appJobs = jobs.filter((j) => j.app === app.slug);
      // Always include deploy drain when deploy enabled
      const lines: string[] = [];
      if (app.deploy.enabled) {
        // The per-app Supercronic process already runs as the app UID/GID.
        lines.push(
          `* * * * * /opt/bento/helpers/deploy-drain.sh ${app.slug} /run/php-fpm/${app.phpService}/${app.slug}.sock`,
        );
      }
      for (const job of appJobs) {
        lines.push(formatCronLine(job, app));
        files.push({
          relPath: `runner/${v.service}/cron/jobs/${app.slug}/${job.name}.sh`,
          content: formatCronScript(job),
          // The crontab invokes this through `sh`; it only needs to be readable
          // by the s6-applyuidgid-dropped app identity.
          mode: 0o644,
          managed: true,
        });
      }
      files.push({
        relPath: `runner/${v.service}/cron/${app.slug}.crontab`,
        content: withManagedMarker(
          lines.length ? lines.join("\n") + "\n" : "# no jobs\n",
        ),
        mode: 0o644,
        managed: true,
      });
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

    const logrotateLines: string[] = [];
    for (const app of appsOnVersion) {
      // Keep rotation out of the app's own crontab: that scheduler intentionally
      // runs without root, while PHP-FPM slow logs and captured worker logs can
      // be root-owned. One root maintenance scheduler handles every app on this
      // runner without an endless shell/sleep process.
      const logrotateConfig = `/etc/bento/cron/logrotate/${app.slug}.conf`;
      files.push({
        relPath: `runner/${v.service}/cron/logrotate/${app.slug}.conf`,
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
      logrotateLines.push(
        `0 * * * * /usr/sbin/logrotate --state /run/bento-s6/logrotate-${app.slug}.status ${logrotateConfig}`,
      );

      const appJobs = jobs.filter((j) => j.app === app.slug);
      if (appJobs.length > 0 || app.deploy.enabled) {
        const service = `scheduler-${app.slug}`;
        const supercronic = `/usr/local/bin/supercronic /etc/bento/cron/${app.slug}.crontab`;
        // Open the app-owned log only after dropping privileges. Besides giving
        // the app ownership of a newly created log, this avoids root following
        // an app-controlled symlink during shell redirection.
        const scheduler = `/command/s6-applyuidgid -u ${app.uid} -g ${app.gid} -G '' sh -c ${
          shellQuote(
            `exec ${supercronic} >>${shellQuote(`${app.home}/logs/cron/scheduler.log`)} 2>&1`,
          )
        }`;
        files.push({
          relPath: `runner/${v.service}/services/${service}/run`,
          content: `#!/bin/sh\n# bento-managed: true\nexport HOME=${shellQuote(app.home)} USER=${
            shellQuote(String(app.slug))
          } BENTO_APP=${shellQuote(String(app.slug))}\nexec ${scheduler}\n`,
          mode: 0o755,
          managed: true,
        });
      }
    }

    if (logrotateLines.length > 0) {
      files.push({
        relPath: `runner/${v.service}/cron/logrotate.crontab`,
        content: withManagedMarker(`${logrotateLines.join("\n")}\n`),
        mode: 0o644,
        managed: true,
      });
      files.push({
        relPath: `runner/${v.service}/services/logrotate/run`,
        content:
          "#!/bin/sh\n# bento-managed: true\n# Root maintenance scheduler; app cron services remain unprivileged.\nexec /usr/local/bin/supercronic /etc/bento/cron/logrotate.crontab\n",
        mode: 0o755,
        managed: true,
      });
    }

    for (const w of workers) {
      const app = state.apps[w.app];
      if (!app) continue;
      const service = `worker-${app.slug}-${w.name}`;
      const cmd = w.command.map(shellQuote).join(" ");
      // Open worker logs after dropping privileges so newly created files are
      // app-owned and root never follows an app-controlled symlink.
      const workerLog = `${app.home}/logs/worker/${w.name}.log`;
      const workerErrorLog = `${app.home}/logs/worker/${w.name}.err`;
      const dropped = `/command/s6-applyuidgid -u ${app.uid} -g ${app.gid} -G '' sh -c ${
        shellQuote(
          `cd ${w.workdir} && exec ${cmd} >>${shellQuote(workerLog)} 2>>${
            shellQuote(workerErrorLog)
          }`,
        )
      }`;
      files.push({
        relPath: `runner/${v.service}/services/${service}/run`,
        content: `#!/bin/sh\n# bento-managed: true\nexport HOME=${shellQuote(app.home)} USER=${
          shellQuote(String(app.slug))
        } BENTO_APP=${shellQuote(String(app.slug))}\nexec ${dropped}\n`,
        mode: 0o755,
        managed: true,
      });
      files.push({
        relPath: `runner/${v.service}/services/${service}/down-signal`,
        content: `${s6SignalNumber(w.stopsignal)}\n`,
        mode: 0o644,
        managed: true,
      });
      files.push({
        relPath: `runner/${v.service}/services/${service}/timeout-kill`,
        content: `${w.stopwaitsecs * 1000}\n`,
        mode: 0o644,
        managed: true,
      });
      if (!w.autorestart) {
        files.push({
          relPath: `runner/${v.service}/services/${service}/finish`,
          content:
            "#!/bin/sh\n# bento-managed: true\n# Keep a one-shot worker down after it exits.\nexec /command/s6-svc -d .\n",
          mode: 0o755,
          managed: true,
        });
      }
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
  const escapedPassword = rootPassword.replace(/[\r\n]/g, "").replace(/\\/g, "\\\\")
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

export function generateMysqlSecrets(
  state: DesiredState,
  rootPassword: string,
): GeneratedFile[] {
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

function formatCronLine(job: CronJob, app: AppState): string {
  // Keep user shell source out of the crontab. Besides making the generated
  // line readable, the child script lets a user's own redirects override the
  // inherited Bento log redirect in the normal shell manner.
  const script = `/etc/bento/cron/jobs/${app.slug}/${job.name}.sh`;
  let cmd = `sh ${shellQuote(script)}`;
  if (job.timeoutSec) {
    cmd = `timeout ${job.timeoutSec}s ${cmd}`;
  }
  if (job.lock) {
    cmd = `flock -n /run/bento/${app.slug}/${job.lock}.lock -c ${shellQuote(cmd)}`;
  }
  if (job.output === "null") {
    cmd = `${cmd} >/dev/null 2>&1`;
  } else if (job.output === "log") {
    cmd = `${cmd} >> ${containerAppHome(app.slug)}/logs/cron/${job.name}.log 2>&1`;
  }
  // Supercronic already executes the crontab command through /bin/sh, and its
  // process already runs as the app UID/GID. No additional shell is needed.
  return `${job.schedule} ${cmd}`;
}

function formatCronScript(job: CronJob): string {
  const command = job.commandMode === "shell"
    ? job.command[0]!
    : `exec ${job.command.map(shellQuote).join(" ")}`;
  return withManagedMarker(
    `cd ${
      shellQuote(job.workdir)
    } || exit 1\nprintf '\\n= Run at %s =\\n\\n' "$(date '+%Y-%m-%d %H:%M:%S')"\n${command}\n`,
  );
}

function shellQuote(s: string): string {
  if (/^[a-zA-Z0-9_./:@%+=,-]+$/.test(s)) return s;
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

/** s6's down-signal file contains a signal number, not a symbolic name. */
function s6SignalNumber(signal: string): number {
  const normalized = signal.trim().toUpperCase().replace(/^SIG/, "");
  const numbers: Record<string, number> = {
    HUP: 1,
    INT: 2,
    QUIT: 3,
    KILL: 9,
    USR1: 10,
    USR2: 12,
    TERM: 15,
  };
  if (numbers[normalized] !== undefined) return numbers[normalized];
  if (/^[1-9][0-9]*$/.test(normalized)) return Number(normalized);
  return 15;
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

// silence unused import lint for Worker if not used directly
void (null as unknown as Worker);
void (null as unknown as DesiredState);
