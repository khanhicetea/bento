import { Database } from "bun:sqlite";
import { join } from "node:path";
import { runtime as bunRuntime, assertEquals, assertRejects } from "../runtime.ts";
import { runCli } from "../../src/main.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { createProxy, setProxyEnabled } from "../../src/services/proxy.ts";
import {
  STATE_DATABASE_SCHEMA_VERSION,
  migrateStateDatabase,
} from "../../src/services/state_database.ts";
import { StateStore } from "../../src/services/state_store.ts";

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
    assertEquals(migration, [{ version: 1, name: "minicrond-owned-user-jobs" }]);
    const tables = database
      .query<{ name: string }, []>(
        "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
      )
      .all()
      .map((row) => row.name);
    assertEquals(tables.includes("applications"), true);
    assertEquals(tables.includes("app_database_bindings"), true);
    assertEquals(tables.includes("domains"), true);
    assertEquals(tables.includes("cron_jobs"), false);
    assertEquals(tables.includes("workers"), false);
    assertEquals(tables.includes("app_deploy_arguments"), false);
    assertEquals(tables.includes("cron_job_arguments"), false);
    assertEquals(tables.includes("worker_arguments"), false);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("old development schema is refused without deleting user job rows", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-retired-db-" });
  try {
    const platform = createPlatform(root, bunRuntime.cwd());
    await migrateStateDatabase(platform);
    {
      using database = new Database(platform.paths.paths.stateDb);
      database.exec("CREATE TABLE cron_jobs (name TEXT); INSERT INTO cron_jobs VALUES ('keep-me')");
      database.run(
        "UPDATE schema_migrations SET name = 'normalized-desired-state' WHERE version = 1",
      );
    }
    await assertRejects(
      () => migrateStateDatabase(platform),
      Error,
      "unsupported desired state database migration 1",
    );
    using database = new Database(platform.paths.paths.stateDb, { readonly: true });
    assertEquals(
      database.query<{ name: string }, []>("SELECT name FROM cron_jobs").get()?.name,
      "keep-me",
    );
    assertEquals(
      database.query<{ name: string }, []>("SELECT name FROM schema_migrations").get()?.name,
      "normalized-desired-state",
    );
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
    state = setProxyEnabled(state, "api", false, now).state;
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

bunRuntime.test("explicit migrate command applies migrations for CLI-only stacks", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-cli-migrate-" });
  try {
    const code = await runCli(["--stack", root, "--repo-root", bunRuntime.cwd(), "migrate"]);
    assertEquals(code, 0);
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

bunRuntime.test(
  "state database persists process runtime fields without PHP runtime output",
  async () => {
    const root = await bunRuntime.makeTempDir({ prefix: "bento-process-db-" });
    try {
      const platform = createPlatform(root, bunRuntime.cwd());
      const store = new StateStore(platform);
      let state = await store.init();
      state = provisionApp(platform, state, {
        slug: "api",
        domain: "api.example",
        kind: "process",
        processLanguage: "bun",
        processVersion: "1.2.20",
        processCommand: ["bun", "run", "server.ts"],
        processHealthPath: "/ready",
      }).state;
      await store.save(state);

      const loaded = await store.load();
      const app = loaded.apps.api;
      assertEquals(app?.kind, "process");
      if (!app || app.kind !== "process") throw new Error("expected process app");
      assertEquals(app.runtime.language, "bun");
      assertEquals(app.runtime.version, "1.2.20");
      assertEquals(app.runtime.command, ["bun", "run", "server.ts"]);
      assertEquals(app.runtime.healthPath, "/ready");
      const serialized = JSON.parse(stateToJson(loaded)) as {
        apps: Record<string, Record<string, unknown>>;
      };
      assertEquals(typeof serialized.apps.api?.runtime, "object");
      assertEquals(serialized.apps.api?.phpVersion, undefined);
      assertEquals(serialized.apps.api?.poolTemplate, undefined);
    } finally {
      await bunRuntime.remove(root, { recursive: true });
    }
  },
);
