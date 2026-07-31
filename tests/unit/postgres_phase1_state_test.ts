import { assert, assertEquals, assertRejects } from "@std/assert";
import { createEmptyState } from "../../src/domain/state.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { parseDesiredState, stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { addPostgresVersion } from "../../src/services/postgres.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { STATE_SCHEMA_VERSION } from "../../src/version.ts";

Deno.test("schema v4 persists linked domains and multiple database-engine bindings", () => {
  const platform = createPlatform("/tmp/unused", Deno.cwd());
  let state = addPostgresVersion(createEmptyState("2026-01-01T00:00:00.000Z"), "17");
  state = provisionApp(platform, state, {
    slug: "alpha",
    domain: "alpha.test",
    aliases: ["www.alpha.test"],
    createDatabase: true,
  }).state;
  state = provisionApp(platform, state, {
    slug: "alpha",
    domain: "alpha.test",
    aliases: ["www.alpha.test"],
    databaseEngine: "postgres",
    postgresVersion: "17",
    createDatabase: true,
    databaseName: "alpha_events",
  }).state;

  const raw = JSON.parse(stateToJson(state));
  assertEquals(raw.schemaVersion, STATE_SCHEMA_VERSION);
  assertEquals(raw.apps.alpha.databases.map((database: { engine: string }) => database.engine), [
    "mysql",
    "postgres",
  ]);
  assertEquals("database" in raw.apps.alpha, false);
  assertEquals("mainDomain" in raw.apps.alpha, false);
  assertEquals("aliases" in raw.apps.alpha, false);
  assertEquals(raw.domains["alpha.test"], { kind: "app", slug: "alpha", primary: true });
  assertEquals(raw.domains["www.alpha.test"], {
    kind: "app",
    slug: "alpha",
    primary: false,
  });

  const parsed = parseDesiredState(raw);
  assert(parsed.ok);
  assertEquals(parsed.value.apps.alpha?.mainDomain, "alpha.test");
  assertEquals(parsed.value.apps.alpha?.aliases, ["www.alpha.test"]);
});

Deno.test("schema v4 rejects legacy app-owned ingress and singular database fields", () => {
  const platform = createPlatform("/tmp/unused", Deno.cwd());
  const state = provisionApp(platform, createEmptyState(), {
    slug: "demo",
    domain: "demo.test",
  }).state;
  const raw = JSON.parse(stateToJson(state));
  raw.apps.demo.database = raw.apps.demo.databases[0];
  raw.apps.demo.mainDomain = "demo.test";
  assertEquals(parseDesiredState(raw).ok, false);
});

Deno.test("state store rejects an old schema without rewriting it", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-v4-state-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
    const store = new StateStore(platform);
    await platform.fs.mkdirp(root);
    const original = `${JSON.stringify({ ...createEmptyState(), schemaVersion: 3 }, null, 2)}\n`;
    await platform.fs.atomicWriteText(platform.paths.paths.stateFile, original, 0o600);
    await assertRejects(() => store.load(), Error, "unsupported state schemaVersion 3");
    assertEquals(await platform.fs.readText(platform.paths.paths.stateFile), original);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
