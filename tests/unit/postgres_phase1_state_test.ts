import { assertEquals, assertRejects } from "@std/assert";
import { createEmptyState } from "../../src/domain/state.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { migrateV1ToV2, parseDesiredState, stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { StateStore } from "../../src/services/state_store.ts";

function v1Fixture() {
  const platform = createPlatform("/tmp/unused", Deno.cwd());
  const current = provisionApp(
    platform,
    createEmptyState("2026-01-01T00:00:00.000Z"),
    {
      slug: "alpha",
      domain: "alpha.test",
      createDatabase: true,
      databaseName: "alpha_archive",
    },
  ).state;
  const app = current.apps["alpha"]!;
  return {
    schemaVersion: 1,
    defaults: {
      phpVersion: current.defaults.phpVersion,
      mysqlVersion: current.defaults.database.version,
      fpmProfile: current.defaults.fpmProfile,
      redisMode: current.defaults.redisMode,
    },
    phpVersions: current.phpVersions,
    mysqlVersions: current.databaseServices.map(({ engine: _engine, ...service }) => service),
    apps: {
      "alpha": {
        ...app,
        database: undefined,
        mysqlService: app.database.service,
        mysqlUser: app.database.user,
        mysqlPassword: app.database.password,
        databases: app.database.databases,
      },
    },
    proxies: current.proxies,
    domains: current.domains,
    cronJobs: current.cronJobs,
    workers: current.workers,
    createdAt: current.createdAt,
    updatedAt: current.updatedAt,
  };
}

function serializableV1() {
  return JSON.parse(JSON.stringify(v1Fixture()));
}

Deno.test("PG-01 pure v1 migration preserves every MySQL durable identifier and secret", () => {
  const old = serializableV1();
  const result = migrateV1ToV2(old);
  assertEquals(result.ok, true);
  if (!result.ok) return;
  const app = result.value.apps["alpha"]!;
  assertEquals(result.value.schemaVersion, 2);
  assertEquals(result.value.defaults.database, {
    engine: "mysql",
    version: old.defaults.mysqlVersion,
    service: old.mysqlVersions[0].service,
  });
  assertEquals(result.value.databaseServices[0], {
    engine: "mysql",
    ...old.mysqlVersions[0],
  });
  assertEquals(app.database, {
    engine: "mysql",
    service: old.apps["alpha"].mysqlService,
    user: old.apps["alpha"].mysqlUser,
    password: old.apps["alpha"].mysqlPassword,
    databases: old.apps["alpha"].databases,
  });
  assertEquals(app.createdAt, old.apps["alpha"].createdAt);
  assertEquals(app.updatedAt, old.apps["alpha"].updatedAt);
});

Deno.test("schema v2 rejects mixed-engine and loose legacy app fields", () => {
  const state = JSON.parse(stateToJson(createEmptyState()));
  state.databaseServices.push({
    engine: "postgres",
    version: "17",
    service: "postgres17",
    image: "postgres:17",
    volume: "postgres17-data",
  });
  state.apps.demo = {
    slug: "demo",
    enabled: true,
    uid: 10000,
    gid: 10000,
    home: "/home/demo",
    mainDomain: "demo.test",
    aliases: [],
    documentRoot: "public",
    entrypointMode: "front-controller",
    phpVersion: "8.5",
    phpService: "php85",
    fpmProfile: "small",
    tls: { kind: "shared" },
    accessLog: false,
    database: {
      engine: "postgres",
      service: "postgres17",
      user: "demo",
      password: "secret",
      databases: [],
      mysqlService: "mysql84",
    },
    redis: { mode: "shared", prefix: "demo:" },
    deploy: {
      enabled: false,
      queuePolicy: "latest",
      timeoutSec: 900,
      workdir: "/home/demo",
      argv: ["true"],
    },
    vhostTemplate: { kind: "upstream" },
    poolTemplate: { kind: "upstream" },
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
  };
  assertEquals(parseDesiredState(state).ok, false);
});

Deno.test("explicit state migration backs up v1 and atomically installs validated v2", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg1-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
    const store = new StateStore(platform);
    await platform.fs.mkdirp(root);
    const original = `${JSON.stringify(serializableV1(), null, 2)}\n`;
    await platform.fs.atomicWriteText(platform.paths.paths.stateFile, original, 0o600);

    await assertRejects(() => store.migrateV1ToV2("yes"));
    assertEquals(await platform.fs.readText(platform.paths.paths.stateFile), original);

    const migrated = await store.migrateV1ToV2("migrate-v1-to-v2");
    assertEquals(await platform.fs.readText(migrated.backupPath), original);
    assertEquals((await platform.fs.stat(migrated.backupPath)).mode & 0o777, 0o600);
    assertEquals((await store.load()).schemaVersion, 2);
    assertEquals((await platform.fs.stat(platform.paths.paths.stateFile)).mode & 0o777, 0o600);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("invalid v1, invalid migrated v2, and unsupported versions never replace source", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg1-invalid-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
    const store = new StateStore(platform);
    await platform.fs.mkdirp(root);
    for (
      const raw of [
        { ...serializableV1(), apps: { broken: {} } },
        {
          ...serializableV1(),
          mysqlVersions: [{ ...serializableV1().mysqlVersions[0], service: "custom-db" }],
        },
        createEmptyState(),
      ]
    ) {
      const original = `${JSON.stringify(raw, null, 2)}\n`;
      await platform.fs.atomicWriteText(platform.paths.paths.stateFile, original, 0o600);
      await assertRejects(() => store.migrateV1ToV2("migrate-v1-to-v2"));
      assertEquals(await platform.fs.readText(platform.paths.paths.stateFile), original);
    }
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
