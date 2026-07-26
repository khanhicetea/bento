import { assertEquals, assertRejects } from "@std/assert";
import { join } from "@std/path";
import { parse as parseYaml } from "@std/yaml";
import { createEmptyState, postgresImage, postgresServiceName } from "../../src/domain/state.ts";
import { asPostgresVersion } from "../../src/domain/types.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import type { Platform } from "../../src/platform/mod.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { assembleComposeDocuments, buildComposeFileList } from "../../src/services/compose.ts";
import { generatePostgresSecrets } from "../../src/services/generate.ts";
import { RenderService } from "../../src/services/render.ts";
import { StateStore } from "../../src/services/state_store.ts";
import {
  loadPostgresRootPassword,
  parseDotEnv,
  requirePostgresRootPassword,
} from "../../src/services/stack_env.ts";

function testPlatform(root: string): Platform & {
  process: ReturnType<typeof createRecordingProcessRunner>;
} {
  const fs = createFileSystem();
  const process = createRecordingProcessRunner();
  return {
    clock: createFixedClock("2026-07-26T12:00:00.000Z"),
    random: createSeededRandom("aabbccddeeff0022"),
    fs,
    lock: createMemoryLock(),
    process,
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

function withPostgres() {
  const state = createEmptyState("2026-07-26T12:00:00.000Z");
  const version = asPostgresVersion("17");
  const service = postgresServiceName(version);
  state.databaseServices.push({
    engine: "postgres",
    version,
    service,
    image: postgresImage(version),
    volume: `${service}-data`,
  });
  return state;
}

Deno.test("Phase 2 init creates PostgreSQL password once and env loader validates it", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg2-env-" });
  try {
    const platform = testPlatform(root);
    const store = new StateStore(platform);
    await store.init();
    const first = await platform.fs.readText(platform.paths.paths.envFile);
    const env = parseDotEnv(first);
    assertEquals(!!env.POSTGRES_PASSWORD, true);
    assertEquals(await loadPostgresRootPassword(platform), env.POSTGRES_PASSWORD);
    assertEquals(await requirePostgresRootPassword(platform), env.POSTGRES_PASSWORD);

    await store.init(true);
    assertEquals(await platform.fs.readText(platform.paths.paths.envFile), first);

    const existing =
      "MYSQL_ROOT_PASSWORD=mysql-kept\nPOSTGRES_PASSWORD=postgres-kept\nREDIS_PASSWORD=redis-kept\n";
    await platform.fs.atomicWriteText(platform.paths.paths.envFile, existing, 0o600);
    await store.init(true);
    assertEquals(await platform.fs.readText(platform.paths.paths.envFile), existing);
    assertEquals((await platform.fs.stat(platform.paths.paths.envFile)).mode & 0o777, 0o600);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 2 PostgreSQL compose is private, mounted correctly, and deterministically ordered", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg2-compose-" });
  try {
    const platform = testPlatform(root);
    const store = new StateStore(platform);
    const render = new RenderService(platform);
    await store.init();
    const state = withPostgres();
    await render.apply(state, { renderOnly: true, skipValidate: true });

    const invocation = buildComposeFileList(platform, state);
    assertEquals(invocation.files.slice(2, 4), [
      "generated/compose/docker-compose.mysql84.yml",
      "generated/compose/docker-compose.postgres17.yml",
    ]);

    const path = join(root, "generated/compose/docker-compose.postgres17.yml");
    const text = await platform.fs.readText(path);
    const doc = parseYaml(text) as Record<string, unknown>;
    const services = doc.services as Record<string, Record<string, unknown>>;
    const postgres = services.postgres17!;
    assertEquals(postgres.networks, ["private"]);
    assertEquals("ports" in postgres, false);
    assertEquals(postgres.logging, {
      driver: "local",
      options: { "max-size": "10m", "max-file": "3" },
    });
    assertEquals(postgres.volumes, [
      "postgres17-data:/var/lib/postgresql/data",
      "./generated/postgres/postgres17:/etc/bento/postgres:ro",
      "./backups/postgres17:/var/backups/bento",
    ]);
    assertEquals(
      (postgres.environment as Record<string, string>).PGPASSFILE,
      "/etc/bento/postgres/root.pgpass",
    );
    assertEquals((doc.volumes as Record<string, unknown>)["postgres17-data"], null);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 2 PostgreSQL 18 mounts the new major-aware data root", () => {
  const platform = testPlatform("/tmp/bento-pg18-compose");
  const state = createEmptyState("2026-07-26T12:00:00.000Z");
  const version = asPostgresVersion("18");
  const service = postgresServiceName(version);
  state.databaseServices.push({
    engine: "postgres",
    version,
    service,
    image: postgresImage(version),
    volume: `${service}-data`,
  });

  const generated = assembleComposeDocuments(platform, state).find((file) =>
    file.relPath === "compose/docker-compose.postgres18.yml"
  );
  const content = generated!.content;
  const doc = parseYaml(
    typeof content === "string" ? content : new TextDecoder().decode(content),
  ) as Record<string, unknown>;
  const services = doc.services as Record<string, Record<string, unknown>>;
  assertEquals(
    (services.postgres18!.volumes as string[])[0],
    "postgres18-data:/var/lib/postgresql",
  );
});

Deno.test("Phase 2 root pgpass has real escaped content, mode 0600, and rollback safety", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg2-pgpass-" });
  try {
    const platform = testPlatform(root);
    const store = new StateStore(platform);
    const render = new RenderService(platform);
    await store.init();
    const state = withPostgres();
    await render.apply(state, { renderOnly: true, skipValidate: true });

    const password = (await loadPostgresRootPassword(platform))!;
    const path = join(root, "generated/postgres/postgres17/root.pgpass");
    const before = await platform.fs.readBytes(path);
    assertEquals(new TextDecoder().decode(before).includes(`*:*:*:postgres:${password}`), true);
    assertEquals((await platform.fs.stat(path)).mode & 0o777, 0o600);

    const pure = generatePostgresSecrets(state, "slash\\colon:value\nignored");
    assertEquals(pure[0]?.relPath, "postgres/postgres17/root.pgpass");
    assertEquals(pure[0]?.mode, 0o600);
    assertEquals(String(pure[0]?.content).includes("slash\\\\colon\\:valueignored"), true);

    const envPath = platform.paths.paths.envFile;
    const env = await platform.fs.readText(envPath);
    await platform.fs.atomicWriteText(
      envPath,
      env.replace(`POSTGRES_PASSWORD=${password}`, "POSTGRES_PASSWORD=replacement-secret"),
      0o600,
    );
    await assertRejects(
      () =>
        render.apply(state, {
          validators: [{ name: "fail", validate: () => Promise.reject(new Error("boom")) }],
        }),
      Error,
      "validation failed",
    );
    assertEquals(await platform.fs.readBytes(path), before);
    assertEquals((await platform.fs.stat(path)).mode & 0o777, 0o600);
    for (const call of platform.process.calls) {
      assertEquals(call.command.join(" ").includes(password), false);
      assertEquals(call.command.join(" ").includes("replacement-secret"), false);
    }
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 2 PHP image builds PostgreSQL extensions with runtime libpq only", async () => {
  const dockerfile = await Deno.readTextFile("templates/docker/php/Dockerfile");
  assertEquals(dockerfile.includes("libpq-dev"), true);
  assertEquals(dockerfile.includes("pdo_pgsql"), true);
  assertEquals(dockerfile.includes("pgsql"), true);
  assertEquals(dockerfile.includes("libpq5"), true);
  const runtime = dockerfile.slice(dockerfile.lastIndexOf("FROM debian:bookworm-slim"));
  assertEquals(runtime.includes("libpq-dev"), false);
  assertEquals(runtime.includes("build-essential"), false);
  assertEquals(runtime.includes("gcc"), false);
});
