import { Database } from "bun:sqlite";
import { runtime as bunRuntime, assertEquals } from "../runtime.ts";
import { join } from "node:path";
import { runCli } from "../../src/main.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { createFileLock } from "../../src/platform/lock.ts";
import { StateStore } from "../../src/services/state_store.ts";

async function loadState(stack: string) {
  return await new StateStore(createPlatform(stack, bunRuntime.cwd())).load();
}

async function fileExists(path: string): Promise<boolean> {
  try {
    await bunRuntime.stat(path);
    return true;
  } catch {
    return false;
  }
}

async function withStack(fn: (stack: string) => Promise<void>) {
  const stack = await bunRuntime.makeTempDir({ prefix: "bento-cli-" });
  try {
    await fn(stack);
  } finally {
    await bunRuntime.remove(stack, { recursive: true });
  }
}

bunRuntime.test("cli init render status app create", async () => {
  await withStack(async (stack) => {
    const base = ["--stack", stack, "--repo-root", bunRuntime.cwd()];
    assertEquals(await runCli([...base, "init"]), 0);
    assertEquals(await runCli([...base, "migrate"]), 0);
    assertEquals((await bunRuntime.stat(join(stack, "state.db"))).mode & 0o777, 0o600);
    assertEquals(await runCli([...base, "render"]), 0);
    assertEquals(await runCli([...base, "status"]), 0);
    // Without --db: best-effort MySQL may defer when the service is down.
    assertEquals(await runCli([...base, "app", "create", "demo", "--domain", "demo.test", "--no-apply"]), 0);
    // Explicit --db must fail closed when MySQL is unavailable (no database recorded).
    const dbFail = await runCli([
      ...base,
      "app",
      "create",
      "needsdb",
      "--domain",
      "needsdb.test",
      "--db",
      "--no-apply",
    ]);
    assertEquals(dbFail !== 0, true);
    // A missing app must never reach Docker, even with forwarded CLI flags.
    assertEquals((await runCli([...base, "app", "minicrond", "missing", "--", "logs", "--follow"])) !== 0, true);
    assertEquals(await runCli([...base, "app", "list"]), 0);
    assertEquals(await runCli([...base, "render"]), 0);

    // Root client option file materializes real password from stack .env (mode 0600).
    const rootCnf = join(stack, "generated/mysql/mysql84/root.cnf");
    const cnfText = await bunRuntime.readTextFile(rootCnf);
    assertEquals(cnfText.includes("password="), true);
    assertEquals(cnfText.includes("{{MYSQL_ROOT_PASSWORD}}"), false);
    const cnfStat = await bunRuntime.stat(rootCnf);
    assertEquals((cnfStat.mode ?? 0) & 0o777, 0o600);

    // generated app vhost exists
    const vhost = join(stack, "generated/nginx/sites/demo.conf");
    const text = await bunRuntime.readTextFile(vhost);
    assertEquals(text.includes("demo.test"), true);
    assertEquals(text.includes("index.php"), true); // front-controller routes via index.php

    // Disable removes all runtime config but preserves app state/data; enable restores it.
    assertEquals(await runCli([...base, "app", "disable", "demo", "--no-apply"]), 0);
    assertEquals(await runCli([...base, "render"]), 0);
    assertEquals(await fileExists(vhost), false);
    assertEquals(await fileExists(join(stack, "homes/demo/code/public/index.php")), true);
    assertEquals(await runCli([...base, "app", "enable", "demo", "--no-apply"]), 0);
    assertEquals(await runCli([...base, "render"]), 0);
    assertEquals(await fileExists(vhost), true);

    // domain collision
    const code = await runCli([...base, "app", "create", "other", "--domain", "demo.test", "--no-apply"]);
    assertEquals(code !== 0, true);

    // php add/remove
    assertEquals(await runCli([...base, "php", "add", "8.3"]), 0);
    assertEquals(await runCli([...base, "php", "list"]), 0);
    // cannot remove default
    assertEquals((await runCli([...base, "php", "remove", "8.5"])) !== 0, true);
    assertEquals(await runCli([...base, "php", "remove", "8.3"]), 0);

    // mysql remove blocked
    assertEquals((await runCli([...base, "mysql", "remove", "8.4"])) !== 0, true);

    // PostgreSQL add/list render a stable private service; duplicates and removal are blocked.
    assertEquals(await runCli([...base, "postgres", "--help"]), 0);
    assertEquals(await runCli([...base, "postgres", "add", "17", "--no-apply"]), 0);
    assertEquals(await runCli([...base, "postgres", "list"]), 0);
    assertEquals((await runCli([...base, "postgres", "add", "17", "--no-apply"])) !== 0, true);
    assertEquals((await runCli([...base, "postgres", "add", "17.2", "--no-apply"])) !== 0, true);
    assertEquals((await runCli([...base, "postgres", "remove", "17"])) !== 0, true);
    assertEquals(await runCli([...base, "render"]), 0);
    assertEquals(await fileExists(join(stack, "generated/compose/docker-compose.postgres17.yml")), true);

    // PostgreSQL app selection is persisted; contradictory flags and engine moves fail closed.
    assertEquals(
      await runCli([
        ...base,
        "app",
        "create",
        "pgdemo",
        "--domain",
        "pgdemo.test",
        "--database-engine",
        "postgres",
        "--postgres",
        "postgres17",
        "--no-apply",
      ]),
      0,
    );
    const pgState = await loadState(stack);
    assertEquals(pgState.apps.pgdemo!.databases[0]!.engine, "postgres");
    assertEquals(pgState.apps.pgdemo!.databases[0]!.service, "postgres17");
    const pgCred = await bunRuntime.readTextFile(join(stack, "homes/pgdemo/credentials/app.env"));
    assertEquals(pgCred.includes("DB_CONNECTION=pgsql"), true);
    assertEquals(pgCred.includes("MYSQL_"), false);
    assertEquals(
      (await runCli([
        ...base,
        "app",
        "create",
        "badflags",
        "--domain",
        "badflags.test",
        "--mysql",
        "8.4",
        "--postgres",
        "17",
        "--no-apply",
      ])) !== 0,
      true,
    );
    assertEquals(
      await runCli([...base, "app", "update", "pgdemo", "--domain", "pgdemo.test", "--mysql", "8.4", "--no-apply"]),
      0,
    );
    const afterRefusals = await loadState(stack);
    assertEquals(afterRefusals.apps.badflags, undefined);
    assertEquals(
      afterRefusals.apps.pgdemo!.databases.map((database) => database.engine),
      ["postgres", "mysql"],
    );

    // App/proxy removal fails closed without exact typed confirmation
    assertEquals((await runCli([...base, "app", "delete", "demo"])) !== 0, true);
    assertEquals((await runCli([...base, "app", "remove", "demo"])) !== 0, true);

    // proxy
    assertEquals(
      await runCli([
        ...base,
        "proxy",
        "create",
        "api",
        "--domain",
        "api.test",
        "--upstream",
        "http://127.0.0.1:3000",
        "--upstream",
        "http://127.0.0.1:3001",
      ]),
      0,
    );
    const proxyVhost = await bunRuntime.readTextFile(join(stack, "generated/nginx/sites/proxy-api.conf"));
    assertEquals(proxyVhost.includes("upstream upstream_api {"), true);
    assertEquals(proxyVhost.includes("server 127.0.0.1:3000;"), true);
    assertEquals(proxyVhost.includes("server 127.0.0.1:3001;"), true);
    assertEquals(proxyVhost.includes("keepalive 32;"), true);
    assertEquals(proxyVhost.includes("include /etc/nginx/snippets/proxy-common.conf;"), true);
    assertEquals((await runCli([...base, "proxy", "delete", "api"])) !== 0, true);
    assertEquals((await runCli([...base, "proxy", "remove", "api"])) !== 0, true);
    // proxy still listed after unconfirmed delete, then exact confirmation removes it
    assertEquals(await runCli([...base, "proxy", "list"]), 0);
    assertEquals(await runCli([...base, "proxy", "delete", "api", "--confirm", "delete api", "--no-apply"]), 0);
    const afterProxyDelete = await loadState(stack);
    assertEquals(afterProxyDelete.proxies.api, undefined);

    // Retired Bento job/worker commands must not create ghost scheduler state.
    assertEquals(
      await runCli([
        ...base,
        "cron",
        "add",
        "--app",
        "demo",
        "--name",
        "tick",
        "--schedule",
        "*/5 * * * *",
        "--",
        "php",
        "artisan",
        "schedule:run",
      ]),
      2,
    );
    assertEquals(
      await runCli([
        ...base,
        "cron",
        "edit",
        "demo",
        "tick",
        "--schedule",
        "0 * * * *",
        "--cmd",
        "php artisan schedule:run >> logs/scheduler.log",
        "--no-apply",
      ]),
      2,
    );

    assertEquals(
      await runCli([
        ...base,
        "worker",
        "add",
        "--app",
        "demo",
        "--name",
        "queue",
        "--",
        "php",
        "artisan",
        "queue:work",
      ]),
      2,
    );

    // deploy enable
    assertEquals(await runCli([...base, "deploy", "enable", "demo"]), 0);
    assertEquals(await runCli([...base, "deploy", "status", "demo"]), 0);

    // compose wrapper materializes assets and renders before assembling argv
    assertEquals(await runCli([...base, "compose", "--print", "--", "build", "php85"]), 0);
    // compose safety
    assertEquals((await runCli([...base, "compose", "--", "down", "-v"])) !== 0, true);

    // version
    assertEquals(await runCli([...base, "version"]), 0);

    // Phase B: worker control help surface + scoped inspect usage
    assertEquals((await runCli([...base, "worker", "inspect", "missing", "x"])) !== 0, true);

    // Phase B: access logs enable (nginx-only) + rotate + report dry-run
    assertEquals(await runCli([...base, "logs", "access", "enable", "--app", "demo", "--no-apply"]), 0);
    // vhost should include access_log after apply
    assertEquals(await runCli([...base, "apply", "--render-only", "--skip-validate"]), 0);
    const vhostLogged = await bunRuntime.readTextFile(vhost);
    assertEquals(vhostLogged.includes("access_log"), true);
    assertEquals(await runCli([...base, "logs", "access", "rotate", "--app", "demo"]), 0);
    await bunRuntime.mkdir(join(stack, "logs", "nginx"), { recursive: true });
    await bunRuntime.writeTextFile(join(stack, "logs", "nginx", "demo.access.log"), "request\n");
    assertEquals(await runCli([...base, "logs", "access", "report", "--app", "demo", "--dry-run"]), 0);
    assertEquals(await runCli([...base, "logs", "access", "report", "--app", "demo", "--attach", "--dry-run"]), 0);

    // Phase B: mysql shell --print keeps secrets off printed argv
    // (stack .env has MYSQL_ROOT_PASSWORD from init)
    assertEquals(await runCli([...base, "mysql", "shell", "--root", "--print"]), 0);
    assertEquals(await runCli([...base, "mysql", "shell", "--app", "demo", "--print"]), 0);

    // PostgreSQL routine administration help and dry shell plans are process-free.
    for (const command of ["db", "shell", "size", "processlist"]) {
      assertEquals(await runCli([...base, "postgres", command, "--help"]), 0);
    }
    assertEquals(await runCli([...base, "postgres", "shell", "--root", "--service", "17", "--print"]), 0);
    assertEquals(await runCli([...base, "postgres", "shell", "--app", "pgdemo", "--print"]), 0);

    // App CLI shell / exec --print (profile-gated php*-cli; no live attach)
    assertEquals(await runCli([...base, "app", "shell", "demo", "--print"]), 0);
    assertEquals(await runCli([...base, "exec", "demo", "--print", "--", "php", "-v"]), 0);

    // Phase B: template select / drift / return
    const customTpl = join(stack, "custom-vhost.tpl");
    await bunRuntime.writeTextFile(customTpl, "# custom\nserver { listen 80; }\n");
    assertEquals(
      await runCli([
        ...base,
        "template",
        "select",
        "--app",
        "demo",
        "--kind",
        "vhost",
        "--source",
        customTpl,
        "--no-apply",
      ]),
      0,
    );
    assertEquals(await runCli([...base, "template", "drift", "--app", "demo"]), 0);
    assertEquals(await runCli([...base, "template", "return", "--app", "demo", "--kind", "vhost", "--no-apply"]), 0);
    // custom source preserved under stack custom/
    const customCopied = join(stack, "custom/apps/demo/vhost/vhost.conf.tpl");
    assertEquals(
      await bunRuntime
        .stat(customCopied)
        .then(() => true)
        .catch(() => false),
      true,
    );

    // Phase B: maintenance run + apply --preview
    assertEquals(await runCli([...base, "maintenance", "run", "--retain-days", "14"]), 0);
    assertEquals(await runCli([...base, "apply", "--preview"]), 0);

    // Phase B: batched --no-apply then single apply
    assertEquals(
      await runCli([...base, "worker", "add", "--app", "demo", "--name", "batch", "--no-apply", "--", "sleep", "60"]),
      2,
    );
    assertEquals(
      await runCli([
        ...base,
        "cron",
        "add",
        "--app",
        "demo",
        "--name",
        "batch-tick",
        "--schedule",
        "0 * * * *",
        "--no-apply",
        "--",
        "true",
      ]),
      2,
    );
    assertEquals(await runCli([...base, "apply", "--render-only", "--skip-validate"]), 0);

    // Exact typed confirmation removes desired state/config but retains durable home data.
    assertEquals(await runCli([...base, "app", "delete", "demo", "--confirm", "delete demo", "--no-apply"]), 0);
    const afterDelete = await loadState(stack);
    assertEquals(afterDelete.apps.demo, undefined);
    assertEquals(await fileExists(join(stack, "homes/demo/code/public/index.php")), true);

    // Future desired-state versions are rejected and not rewritten.
    using database = new Database(join(stack, "state.db"));
    database.run("UPDATE stack_config SET state_schema_version = 999 WHERE id = 1");
    assertEquals((await runCli([...base, "status"])) !== 0, true);
    const version = database
      .query<{ state_schema_version: number }, []>("SELECT state_schema_version FROM stack_config WHERE id = 1")
      .get();
    assertEquals(version?.state_schema_version, 999);
  });
});

bunRuntime.test("cli refuses to reinitialize an existing stack", async () => {
  await withStack(async (stack) => {
    const base = ["--stack", stack, "--repo-root", bunRuntime.cwd()];
    assertEquals(await runCli([...base, "init", "--name", "original"]), 0);
    const envPath = join(stack, ".env");
    const originalState = await loadState(stack);
    const originalEnv = await bunRuntime.readTextFile(envPath);

    assertEquals((await runCli([...base, "init", "--name", "replacement"])) !== 0, true);
    assertEquals((await runCli([...base, "init", "--force"])) !== 0, true);
    assertEquals(await loadState(stack), originalState);
    assertEquals(await bunRuntime.readTextFile(envPath), originalEnv);
  });
});

bunRuntime.test("cli backup keeps legacy flags and exposes schedule help/run without crontab", async () => {
  await withStack(async (stack) => {
    const base = ["--stack", stack, "--repo-root", bunRuntime.cwd()];
    assertEquals(await runCli([...base, "init"]), 0);

    // The existing top-level option remains routed to the default backup command.
    assertEquals(await runCli([...base, "backup", "--all", "--none"]), 0);
    assertEquals(await runCli([...base, "backup", "--all", "--engine", "mysql", "--none"]), 0);

    // Help and an empty all-database run do not use schedule status/register paths,
    // so this smoke coverage never reads or mutates the host user's crontab.
    assertEquals(await runCli([...base, "backup", "schedule", "--help"]), 0);
    assertEquals(await runCli([...base, "backup", "schedule", "run"]), 0);
    assertEquals(await fileExists(join(stack, "backups/.schedule/last-run.json")), true);

    // Manual backups preserve the typed conflict exit code when a scheduled/manual
    // batch already owns the shared stack backup lock.
    const release = await createFileLock().tryExclusive(join(stack, "locks/database-backup.lock"));
    try {
      assertEquals(await runCli([...base, "backup", "--all", "--none"]), 4);
    } finally {
      await release?.();
    }
  });
});

bunRuntime.test("invalid state database is not overwritten on read", async () => {
  await withStack(async (stack) => {
    const base = ["--stack", stack, "--repo-root", bunRuntime.cwd()];
    await runCli([...base, "init"]);
    const path = join(stack, "state.db");
    const original = new TextEncoder().encode("not a sqlite database");
    await bunRuntime.writeFile(path, original);
    assertEquals((await runCli([...base, "status"])) !== 0, true);
    assertEquals(await bunRuntime.readFile(path), original);
  });
});

bunRuntime.test("cli tls set + permissions + backup/restore dry paths", async () => {
  await withStack(async (stack) => {
    const base = ["--stack", stack, "--repo-root", bunRuntime.cwd()];
    assertEquals(await runCli([...base, "init"]), 0);
    assertEquals(await runCli([...base, "app", "create", "demo", "--domain", "demo.test", "--no-apply"]), 0);

    // Private CA mode creates a per-site SAN leaf and permits public-CA export.
    assertEquals(await runCli([...base, "tls", "set", "--app", "demo", "--mode", "self-ca"]), 0);
    assertEquals(await fileExists(join(stack, "certs/private-ca/sites/demo.crt")), true);
    const caExport = join(stack, "exported-ca.crt");
    assertEquals(await runCli([...base, "tls", "ca", "export", "--output", caExport]), 0);
    assertEquals(await fileExists(caExport), true);
    const caVhost = await bunRuntime.readTextFile(join(stack, "generated/nginx/sites/demo.conf"));
    assertEquals(caVhost.includes("ssl_certificate     /etc/nginx/certs/private-ca/sites/demo.crt;"), true);
    assertEquals(caVhost.includes("ssl_certificate_key /etc/nginx/certs/private-ca/sites/demo.key;"), true);
    assertEquals(caVhost.includes("ssl-common.conf"), true);
    assertEquals(await fileExists(join(stack, "generated/nginx/snippets/ssl-demo.conf")), false);
    assertEquals(caVhost.includes("return 301 https://"), true);

    // Private CA -> ACME (no cert files needed for ACME mode recording)
    assertEquals(await runCli([...base, "tls", "set", "--app", "demo", "--mode", "acme", "--no-apply"]), 0);
    assertEquals(await runCli([...base, "apply", "--render-only", "--skip-validate"]), 0);
    const acmeVhost = await bunRuntime.readTextFile(join(stack, "generated/nginx/sites/demo.conf"));
    const acmeMain = await bunRuntime.readTextFile(join(stack, "generated/nginx/nginx.conf"));
    const acmeSsl = await bunRuntime.readTextFile(join(stack, "generated/nginx/snippets/acme-ssl.conf"));
    assertEquals(acmeVhost.includes("acme-challenge"), false);
    assertEquals(acmeVhost.includes("return 301 https://"), true);
    assertEquals(acmeMain.includes("acme_issuer bento_acme"), true);
    assertEquals(acmeSsl.includes("acme_certificate bento_acme;"), true);

    // TLS external requires cert+key; missing paths fail closed
    assertEquals(
      (await runCli([
        ...base,
        "tls",
        "set",
        "--app",
        "demo",
        "--mode",
        "external",
        "--cert",
        "missing.crt",
        "--key",
        "missing.key",
        "--no-apply",
      ])) !== 0,
      true,
    );

    // External with valid restricted key
    const certs = join(stack, "certs");
    await bunRuntime.mkdir(certs, { recursive: true });
    const cert = join(certs, "demo.crt");
    const key = join(certs, "demo.key");
    await bunRuntime.writeTextFile(cert, "CERT\n");
    await bunRuntime.writeTextFile(key, "KEY\n");
    await bunRuntime.chmod(key, 0o600);
    assertEquals(
      await runCli([
        ...base,
        "tls",
        "set",
        "--app",
        "demo",
        "--mode",
        "external",
        "--cert",
        "demo.crt",
        "--key",
        "demo.key",
        "--no-apply",
      ]),
      0,
    );
    assertEquals(await runCli([...base, "apply", "--render-only", "--skip-validate"]), 0);
    const extVhost = await bunRuntime.readTextFile(join(stack, "generated/nginx/sites/demo.conf"));
    assertEquals(extVhost.includes("return 301 https://"), true);
    assertEquals(extVhost.includes("boot-ssl.conf"), false);

    // Permissions check / dry-run repair (no root required)
    assertEquals(await runCli([...base, "permissions", "check", "demo"]), 0);
    assertEquals(await runCli([...base, "permissions", "repair", "demo", "--dry-run"]), 0);
    assertEquals(await runCli([...base, "permissions", "repair", "demo", "--shallow"]), 0);

    // Backup uses the generated in-container root option file; no shell export is required.
    const prev = bunRuntime.env.get("MYSQL_ROOT_PASSWORD");
    bunRuntime.env.delete("MYSQL_ROOT_PASSWORD");
    try {
      // demo has no databases recorded, so this completes without invoking Docker.
      assertEquals(await runCli([...base, "backup", "--app", "demo", "--none"]), 0);

      // Restore missing file fails before docker.
      assertEquals(
        (await runCli([
          ...base,
          "restore",
          "--file",
          join(stack, "no-such.sql"),
          "--app",
          "demo",
          "--target",
          "demo",
        ])) !== 0,
        true,
      );
      // Replace confirmation mismatch fails closed.
      const dump = join(stack, "empty.sql");
      await bunRuntime.writeTextFile(dump, "-- empty\n");
      assertEquals(
        (await runCli([
          ...base,
          "restore",
          "--file",
          dump,
          "--app",
          "demo",
          "--target",
          "demo",
          "--replace",
          "wrong",
        ])) !== 0,
        true,
      );
    } finally {
      if (prev !== undefined) bunRuntime.env.set("MYSQL_ROOT_PASSWORD", prev);
    }

    // Legacy routing via CLI
    assertEquals(
      await runCli([
        ...base,
        "app",
        "create",
        "legacy",
        "--domain",
        "legacy.test",
        "--legacy",
        "--docroot",
        "htdocs",
        "--no-apply",
      ]),
      0,
    );
    assertEquals(await runCli([...base, "apply", "--render-only", "--skip-validate"]), 0);
    const legacyVhost = await bunRuntime.readTextFile(join(stack, "generated/nginx/sites/legacy.conf"));
    assertEquals(legacyVhost.includes("if ($uri !~ ^/index\\.php$)"), false);
    assertEquals(legacyVhost.includes("try_files $uri =404;"), true);
  });
});
