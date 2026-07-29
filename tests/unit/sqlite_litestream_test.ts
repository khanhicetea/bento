import { assert, assertEquals, assertRejects, assertStringIncludes } from "@std/assert";
import { basename, join } from "@std/path";
import { createEmptyState } from "../../src/domain/state.ts";
import { createPlatform, createRecordingProcessRunner } from "../../src/platform/mod.ts";
import { migrateV2ToV3, parseDesiredState, stateToJson } from "../../src/schemas/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { assembleComposeDocuments } from "../../src/services/compose.ts";
import {
  generateAll,
  generateLitestreamConfig,
  generateLitestreamEnvironment,
} from "../../src/services/generate.ts";
import { exportSqliteBackup } from "../../src/services/sqlite.ts";

Deno.test("SQLite state has one explicit private file and no relational credentials", () => {
  const platform = createPlatform("/tmp/sqlite-state-test", Deno.cwd());
  const result = provisionApp(platform, createEmptyState("2026-07-29T00:00:00.000Z"), {
    slug: "lite-app",
    domain: "lite-app.test",
    databaseEngine: "sqlite",
    createDatabase: true,
  });
  assert(result.app.database.engine === "sqlite");
  assert(/^lite-app_[a-f0-9]{10}$/.test(result.app.database.file.id));
  const json = stateToJson(result.state);
  assertStringIncludes(json, '"path": "sqlite/lite-app_');
  assertStringIncludes(json, '/lite-app.sqlite"');
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
    snapshotInterval: "6h",
    snapshotRetention: "168h",
    l0Retention: "24h",
    enabled: true,
  };

  const configs = generateLitestreamConfig(second.state);
  assertEquals(configs.length, 1);
  assert(typeof configs[0]!.content === "string");
  assertStringIncludes(configs[0]!.content, "dir: /sqlite");
  assertStringIncludes(configs[0]!.content, 'pattern: "*.sqlite"');
  assertStringIncludes(configs[0]!.content, "recursive: true");
  assertStringIncludes(configs[0]!.content, "watch: true");
  assertStringIncludes(configs[0]!.content, "meta-dir: /var/lib/litestream");
  assertStringIncludes(configs[0]!.content, "interval: 6h");
  assertStringIncludes(configs[0]!.content, "retention: 168h");
  assertStringIncludes(configs[0]!.content, "validation:\n  interval: 24h");
  assertStringIncludes(configs[0]!.content, "verify-compaction: false");
  assertStringIncludes(configs[0]!.content, "monitor-interval: 10s");
  assertStringIncludes(configs[0]!.content, "checkpoint-interval: 5m");
  assertStringIncludes(configs[0]!.content, "sync-interval: 60s");
  assert(!configs[0]!.content.includes("- path: /sqlite/"));
  assertEquals(configs[0]!.relPath, "litestream/litestream.yml");

  const firstEnvironment = generateLitestreamEnvironment(second.state, {
    S3_BUCKET_NAME: "bucket",
    S3_REGION: "us-east-1",
    S3_ACCESS_KEY_ID: "old-key",
    S3_SECRET_ACCESS_KEY: "old-secret",
  })[0]!;
  const changedEnvironment = generateLitestreamEnvironment(second.state, {
    S3_BUCKET_NAME: "bucket",
    S3_REGION: "us-east-1",
    S3_ACCESS_KEY_ID: "new-key",
    S3_SECRET_ACCESS_KEY: "new-secret",
  })[0]!;
  assertEquals(firstEnvironment.relPath, "secrets/litestream/stack-s3.env");
  assertEquals(firstEnvironment.mode, 0o600);
  assertStringIncludes(firstEnvironment.content as string, "AWS_ACCESS_KEY_ID=old-key");
  assertStringIncludes(changedEnvironment.content as string, "AWS_ACCESS_KEY_ID=new-key");
  assert(firstEnvironment.content !== changedEnvironment.content);

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
  assertStringIncludes(
    compose.content,
    "./generated/secrets/litestream/stack-s3.env",
  );
  assertStringIncludes(compose.content, "./sqlite:/sqlite");
  assertStringIncludes(compose.content, "./litestream-meta:/var/lib/litestream");
  assert(!compose.content.includes("./homes/"));
  assert(!compose.content.includes("19999"));
  assert(!compose.content.includes("setfacl"));
  assert(!compose.content.includes("build:"));
  assert(!compose.content.includes("s6"));
  assert(!compose.content.includes("networks:\n      - private"));
});

Deno.test("render regenerates the Litestream environment after stack .env changes", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-litestream-render-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
    const state = createEmptyState("2026-07-29T00:00:00.000Z");
    state.sqliteBackup = {
      provider: "litestream",
      destination: "primary-s3",
      syncInterval: "60s",
      snapshotInterval: "6h",
      snapshotRetention: "168h",
      l0Retention: "24h",
      enabled: true,
    };
    const writeEnv = async (accessKey: string) => {
      await platform.fs.atomicWriteText(
        platform.paths.paths.envFile,
        [
          "COMPOSE_PROJECT_NAME=test",
          "BENTO_LITESTREAM_ENABLED=true",
          "S3_BUCKET_NAME=bucket",
          "S3_REGION=us-east-1",
          `S3_ACCESS_KEY_ID=${accessKey}`,
          "S3_SECRET_ACCESS_KEY=secret",
          "",
        ].join("\n"),
        0o600,
      );
    };
    const renderedEnvironment = async () => {
      const files = await generateAll(platform, state, "digest");
      return files.find((file) => file.relPath === "secrets/litestream/stack-s3.env")!;
    };

    await writeEnv("old-key");
    const first = await renderedEnvironment();
    await writeEnv("new-key");
    const second = await renderedEnvironment();

    assertStringIncludes(first.content as string, "AWS_ACCESS_KEY_ID=old-key");
    assertStringIncludes(second.content as string, "AWS_ACCESS_KEY_ID=new-key");
    assertEquals(second.mode, 0o600);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("SQLite S3 export restores with integrity checking and refuses overwrite", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-sqlite-export-" });
  try {
    const platform = createPlatform(root, Deno.cwd());
    const process = createRecordingProcessRunner(async (command) => {
      const outputIndex = command.indexOf("-o");
      if (outputIndex >= 0) {
        const containerOutput = command[outputIndex + 1]!;
        await platform.fs.writeBytes(
          join(root, "litestream-meta", basename(containerOutput)),
          new Uint8Array([0x53, 0x51, 0x4c, 0x69, 0x74, 0x65]),
        );
      }
      return { code: 0, stdout: "restored", stderr: "" };
    });
    platform.process = process;
    await platform.fs.writeText(
      platform.paths.paths.envFile,
      "COMPOSE_PROJECT_NAME=test-stack\nS3_BUCKET_NAME=test-bucket\nS3_REGION=us-east-1\n",
    );
    const provisioned = provisionApp(platform, createEmptyState(), {
      slug: "lite",
      domain: "lite.test",
      databaseEngine: "sqlite",
    });
    provisioned.state.sqliteBackup = {
      provider: "litestream",
      destination: "primary-s3",
      syncInterval: "60s",
      snapshotInterval: "6h",
      snapshotRetention: "168h",
      l0Retention: "24h",
      enabled: true,
    };

    const output = join(root, "recovery", "lite.sqlite");
    assertEquals(
      await exportSqliteBackup(platform, provisioned.state, "lite", output),
      output,
    );
    assertEquals((await platform.fs.stat(output)).mode & 0o777, 0o600);
    const restoreCall = process.calls[0]!.command;
    assert(restoreCall.includes("-integrity-check"));
    assert(restoreCall.includes("full"));
    assert(restoreCall.some((arg) => arg.startsWith("s3://test-bucket/bento/test-stack/")));

    await assertRejects(
      () => exportSqliteBackup(platform, provisioned.state, "lite", output),
      Error,
      "refusing to overwrite existing file",
    );
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("stack watcher remains rendered after the last SQLite app leaves state", () => {
  const state = createEmptyState("2026-07-29T00:00:00.000Z");
  state.sqliteBackup = {
    provider: "litestream",
    destination: "primary-s3",
    syncInterval: "60s",
    snapshotInterval: "6h",
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
