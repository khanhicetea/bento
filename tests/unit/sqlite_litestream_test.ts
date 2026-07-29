import { assert, assertEquals, assertStringIncludes } from "@std/assert";
import { createEmptyState } from "../../src/domain/state.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { migrateV2ToV3, parseDesiredState, stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { assembleComposeDocuments } from "../../src/services/compose.ts";
import { generateLitestreamConfig } from "../../src/services/generate.ts";

Deno.test("SQLite state has one explicit private file and no relational credentials", () => {
  const platform = createPlatform("/tmp/sqlite-state-test", Deno.cwd());
  const result = provisionApp(platform, createEmptyState("2026-07-29T00:00:00.000Z"), {
    slug: "lite",
    domain: "lite.test",
    databaseEngine: "sqlite",
    sqlitePath: "data/sqlite/database.sqlite",
    createDatabase: true,
  });
  assertEquals(result.app.database.engine, "sqlite");
  const json = stateToJson(result.state);
  assertStringIncludes(json, '"path": "sqlite/sqlite_');
  assertStringIncludes(json, '/database.sqlite"');
  assert(!json.includes('"password"'), "SQLite-only app binding must not serialize DB passwords");
  assert(parseDesiredState(JSON.parse(json)).ok);
});

Deno.test("v2 to v3 migration preserves relational state except schema version", () => {
  const v3 = createEmptyState("2026-07-29T00:00:00.000Z");
  const v2 = { ...structuredClone(v3), schemaVersion: 2 };
  const migrated = migrateV2ToV3(v2);
  assert(migrated.ok);
  const actual = JSON.parse(stateToJson(migrated.value));
  assertEquals(actual, { ...v2, schemaVersion: 3 });
});

Deno.test("stack SQLite backup renders one constrained-root directory watcher", () => {
  const platform = createPlatform("/tmp/sqlite-compose-test", Deno.cwd());
  const provisioned = provisionApp(platform, createEmptyState("2026-07-29T00:00:00.000Z"), {
    slug: "lite",
    domain: "lite.test",
    databaseEngine: "sqlite",
  });
  const second = provisionApp(platform, provisioned.state, {
    slug: "other",
    domain: "other.test",
    databaseEngine: "sqlite",
  });
  second.state.sqliteBackup = {
    provider: "litestream",
    destination: "primary-s3",
    syncInterval: "60s",
    snapshotInterval: "1h",
    snapshotRetention: "168h",
    l0Retention: "24h",
    enabled: true,
  };

  const configs = generateLitestreamConfig(second.state);
  assertEquals(configs.length, 1);
  assert(typeof configs[0]!.content === "string");
  assertStringIncludes(configs[0]!.content, "dir: /sqlite");
  assertStringIncludes(configs[0]!.content, 'pattern: "database.sqlite"');
  assertStringIncludes(configs[0]!.content, "recursive: true");
  assertStringIncludes(configs[0]!.content, "watch: true");
  assertStringIncludes(configs[0]!.content, "meta-dir: /var/lib/litestream");
  assertStringIncludes(configs[0]!.content, "sync-interval: 60s");
  assert(!configs[0]!.content.includes("- path: /sqlite/"));
  assertEquals(configs[0]!.relPath, "litestream/litestream.yml");

  const files = assembleComposeDocuments(platform, second.state, {
    projectName: "test",
    litestreamEnabled: true,
    nginx: { hostNetwork: true, http3: false },
  });
  const compose = files.find((file) => file.relPath.endsWith("litestream.yml"));
  assert(compose);
  assert(typeof compose.content === "string");
  assertStringIncludes(compose.content, "backup-egress");
  assertStringIncludes(compose.content, "user: '0:0'");
  assertStringIncludes(compose.content, "DAC_OVERRIDE");
  assertStringIncludes(compose.content, "CHOWN");
  assertStringIncludes(compose.content, "FOWNER");
  assertStringIncludes(compose.content, "litestream/litestream:0.5.15");
  assertStringIncludes(compose.content, "/etc/litestream/litestream.yml");
  assertStringIncludes(compose.content, "./sqlite:/sqlite");
  assertStringIncludes(compose.content, "./litestream-meta:/var/lib/litestream");
  assert(!compose.content.includes("./homes/"));
  assert(!compose.content.includes("19999"));
  assert(!compose.content.includes("setfacl"));
  assert(!compose.content.includes("build:"));
  assert(!compose.content.includes("s6"));
  assert(!compose.content.includes("networks:\n      - private"));
});

Deno.test("stack watcher remains rendered after the last SQLite app leaves state", () => {
  const state = createEmptyState("2026-07-29T00:00:00.000Z");
  state.sqliteBackup = {
    provider: "litestream",
    destination: "primary-s3",
    syncInterval: "60s",
    snapshotInterval: "1h",
    snapshotRetention: "168h",
    l0Retention: "24h",
    enabled: true,
  };
  assertEquals(generateLitestreamConfig(state).length, 1);
  const platform = createPlatform("/tmp/sqlite-retained-compose-test", Deno.cwd());
  const compose = assembleComposeDocuments(platform, state).find((file) =>
    file.relPath.endsWith("litestream.yml")
  );
  assert(compose, "retained SQLite directories remain watched until prune");
});
