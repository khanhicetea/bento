import { Database } from "bun:sqlite";
import { join } from "node:path";
import { runtime as bunRuntime, assertEquals, assertRejects } from "../runtime.ts";
import { runCli } from "../../src/main.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { addCronJob } from "../../src/services/cron.ts";
import { createProxy } from "../../src/services/proxy.ts";
import {
  STATE_DATABASE_SCHEMA_VERSION,
  migrateStateDatabase,
} from "../../src/services/state_database.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { addWorker } from "../../src/services/worker.ts";

bunRuntime.test("state database migrations are numbered, private, and idempotent", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-state-db-" });
  try {
    const platform = createPlatform(root, bunRuntime.cwd());
    const first = await migrateStateDatabase(platform);
    assertEquals(first, {
      fromVersion: 0,
      toVersion: STATE_DATABASE_SCHEMA_VERSION,
      applied: [1],
    });
    assertEquals((await bunRuntime.stat(platform.paths.paths.stateDb)).mode & 0o777, 0o600);

    const second = await migrateStateDatabase(platform);
    assertEquals(second, {
      fromVersion: STATE_DATABASE_SCHEMA_VERSION,
      toVersion: STATE_DATABASE_SCHEMA_VERSION,
      applied: [],
    });

    using database = new Database(platform.paths.paths.stateDb, { readonly: true });
    const migration = database
      .query<
        { version: number; name: string },
        []
      >("SELECT version, name FROM schema_migrations ORDER BY version")
      .all();
    assertEquals(migration, [{ version: 1, name: "normalized-desired-state" }]);
    const tables = database
      .query<{ name: string }, []>(
        "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
      )
      .all()
      .map((row) => row.name);
    assertEquals(tables.includes("applications"), true);
    assertEquals(tables.includes("app_database_bindings"), true);
    assertEquals(tables.includes("domains"), true);
    assertEquals(tables.includes("cron_jobs"), true);
    assertEquals(tables.includes("workers"), true);
    assertEquals(tables.includes("app_deploy_arguments"), false);
    assertEquals(tables.includes("cron_job_arguments"), false);
    assertEquals(tables.includes("worker_arguments"), false);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("state database round-trips nested state and JSON argument fields", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-state-roundtrip-" });
  try {
    const now = "2026-08-23T12:00:00.000Z";
    const platform = createPlatform(root, bunRuntime.cwd());
    platform.clock = createFixedClock(now);
    const store = new StateStore(platform);
    await store.init();

    let state = provisionApp(platform, await store.load(), {
      slug: "files",
      domain: "files.test",
      aliases: ["www.files.test"],
      databaseEngine: "sqlite",
    }).state;
    state = provisionApp(platform, state, {
      slug: "files",
      domain: "files.test",
      aliases: ["www.files.test"],
      databaseEngine: "litestream",
    }).state;
    const app = state.apps.files!;
    const databases = app.databases.map((binding) =>
      binding.engine === "litestream" ? { ...binding, backupVerifiedAt: now } : binding,
    );
    state = {
      ...state,
      sqliteBackup: {
        provider: "litestream",
        destination: "s3://example/sqlite",
        syncInterval: "10s",
        snapshotInterval: "24h",
        snapshotRetention: "168h",
        l0Retention: "24h",
        enabled: true,
      },
      apps: {
        ...state.apps,
        files: {
          ...app,
          tls: { kind: "external", certPath: "site.crt", keyPath: "site.key" },
          accessLog: true,
          databases,
          database: databases[0]!,
          redis: {
            mode: "acl",
            prefix: "files:",
            password: "shared-secret",
            aclUsername: "app_files",
            aclPassword: "acl-secret",
          },
          deploy: {
            enabled: true,
            hmacSecret: "deploy-secret",
            queuePolicy: "fifo",
            timeoutSec: 321,
            workdir: "/home/files/code",
            argv: ["sh", "-lc", "deploy --safe"],
          },
          vhostTemplate: {
            kind: "custom",
            sourcePath: "custom/files-vhost.tpl",
            copiedFromVersion: "0.1.0",
            activatedAt: now,
          },
          poolTemplate: {
            kind: "custom",
            sourcePath: "custom/files-pool.tpl",
            activatedAt: now,
          },
        },
      },
    };
    state = createProxy(
      state,
      {
        name: "api",
        domain: "api.test",
        aliases: ["z.api.test", "a.api.test"],
        upstreams: ["http://127.0.0.1:3000/v1", "http://127.0.0.1:3001/v1"],
        tls: { kind: "acme" },
        accessLog: true,
      },
      now,
    ).state;
    state = addCronJob(
      state,
      {
        app: "files",
        name: "tick",
        schedule: "0 * * * *",
        command: ["php artisan schedule:run >> logs/schedule.log"],
        commandMode: "shell",
        output: "inherit",
        timeoutSec: 60,
        lock: "schedule",
      },
      platform,
    ).state;
    state = addWorker(
      state,
      {
        app: "files",
        name: "queue",
        command: ["php", "artisan", "queue:work"],
        autorestart: false,
        stopsignal: "QUIT",
        stopwaitsecs: 45,
      },
      platform,
    ).state;

    await store.save(state);
    using database = new Database(platform.paths.paths.stateDb, { readonly: true });
    assertEquals(
      database
        .query<
          { deploy_argv_json: string },
          []
        >("SELECT deploy_argv_json FROM applications WHERE slug = 'files'")
        .get()?.deploy_argv_json,
      JSON.stringify(state.apps.files!.deploy.argv),
    );
    assertEquals(
      database
        .query<
          { command_json: string },
          []
        >("SELECT command_json FROM cron_jobs WHERE name = 'tick'")
        .get()?.command_json,
      JSON.stringify(state.cronJobs[0]!.command),
    );
    assertEquals(
      database
        .query<
          { command_json: string },
          []
        >("SELECT command_json FROM workers WHERE name = 'queue'")
        .get()?.command_json,
      JSON.stringify(state.workers[0]!.command),
    );
    assertEquals(
      JSON.parse(stateToJson(await store.load())),
      JSON.parse(stateToJson({ ...state, updatedAt: now })),
    );
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("failed relational replacement rolls back the complete prior state", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-state-rollback-" });
  try {
    const platform = createPlatform(root, bunRuntime.cwd());
    const store = new StateStore(platform);
    await store.init();
    const before = await store.load();
    const duplicatePhp = { ...before.phpVersions[0]!, image: "duplicate:image" };

    await assertRejects(
      () => store.save({ ...before, phpVersions: [...before.phpVersions, duplicatePhp] }),
      Error,
      "failed to save desired state database",
    );
    assertEquals(await store.load(), before);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("state database never falls back to state.json", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-no-json-state-" });
  try {
    const platform = createPlatform(root, bunRuntime.cwd());
    const store = new StateStore(platform);
    await platform.fs.mkdirp(root);
    const legacyPath = join(root, "state.json");
    const legacy = stateToJson(createEmptyState("2026-01-01T00:00:00.000Z"));
    await platform.fs.atomicWriteText(legacyPath, legacy, 0o600);

    await store.migrate();
    assertEquals(await store.exists(), false);
    await assertRejects(() => store.load(), Error, "no desired state");

    const initialized = await store.init({ projectName: "sqlite-only" });
    assertEquals(initialized.createdAt !== "2026-01-01T00:00:00.000Z", true);
    assertEquals(await platform.fs.readText(legacyPath), legacy);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("tui runs migrations before interactive-terminal validation", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-tui-migrate-" });
  try {
    const code = await runCli(["--stack", root, "--repo-root", bunRuntime.cwd(), "tui"]);
    assertEquals(code, 2);
    using database = new Database(join(root, "state.db"), { readonly: true });
    const version = database
      .query<{ version: number }, []>("SELECT version FROM schema_migrations ORDER BY version DESC")
      .get();
    assertEquals(version?.version, STATE_DATABASE_SCHEMA_VERSION);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test(
  "future database migrations are refused without changing their marker",
  async () => {
    const root = await bunRuntime.makeTempDir({ prefix: "bento-future-state-db-" });
    try {
      const platform = createPlatform(root, bunRuntime.cwd());
      const store = new StateStore(platform);
      await store.migrate();
      {
        using database = new Database(platform.paths.paths.stateDb);
        database.run(
          "INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future', ?)",
          ["2026-01-01T00:00:00.000Z"],
        );
      }

      await assertRejects(
        () => store.migrate(),
        Error,
        "unsupported desired state database migration",
      );
      using database = new Database(platform.paths.paths.stateDb, { readonly: true });
      const future = database
        .query<{ version: number }, []>("SELECT version FROM schema_migrations WHERE version = 999")
        .get();
      assertEquals(future?.version, 999);
    } finally {
      await bunRuntime.remove(root, { recursive: true });
    }
  },
);
