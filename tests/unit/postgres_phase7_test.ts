import { assertEquals, assertRejects, assertStringIncludes } from "@std/assert";
import { join } from "@std/path";
import { createEmptyState } from "../../src/domain/state.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import type { Platform, RunOptions, RunResult } from "../../src/platform/mod.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { deleteApp, provisionApp } from "../../src/services/app.ts";
import {
  executeAppPrune,
  planAppPrune,
  writeAppPruneManifest,
} from "../../src/services/app_prune.ts";
import { createSupportBundle, runDoctor } from "../../src/services/doctor.ts";
import { addPostgresVersion } from "../../src/services/postgres.ts";
import { buildStatus, formatStatus, statusToJson } from "../../src/services/status.ts";

function testPlatform(
  root: string,
  handler?: (command: string[], options?: RunOptions) => Promise<RunResult> | RunResult,
): Platform & { process: ReturnType<typeof createRecordingProcessRunner> } {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-28T12:00:00.000Z"),
    random: createSeededRandom("aabbccddeeff0077"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(handler),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

Deno.test("Phase 7 status reports PostgreSQL roles, health, bindings, and no secrets", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg7-status-" });
  try {
    const platform = testPlatform(root, (command) => {
      if (command.includes("ps") && command.includes("--services")) {
        return {
          code: 0,
          stdout: "nginx\nredis\nphp85\nphp85-runner\nmysql84\npostgres17\n",
          stderr: "",
        };
      }
      return { code: 0, stdout: "accepting connections\n", stderr: "" };
    });
    let state = addPostgresVersion(createEmptyState(), "17");
    const provisioned = provisionApp(platform, state, {
      slug: "pgapp",
      domain: "pgapp.test",
      databaseEngine: "postgres",
      postgresVersion: "17",
      createDatabase: true,
    });
    state = provisioned.state;
    const report = await buildStatus(platform, state);
    assertEquals(
      report.roles.some((role) => role.kind === "postgres" && role.state === "running"),
      true,
    );
    assertEquals(report.postgresVersions[0]?.health, "ok");
    assertEquals(report.postgresVersions[0]?.service, "postgres17");
    assertEquals(report.apps[0]?.databaseEngine, "postgres");
    assertEquals(report.apps[0]?.databaseService, "postgres17");
    const human = formatStatus(report);
    const json = statusToJson(report);
    assertStringIncludes(human, "PostgreSQL services:");
    assertStringIncludes(human, "db=postgres:postgres17[pgapp]");
    assertEquals(human.includes(provisioned.app.database.password), false);
    assertEquals(json.includes(provisioned.app.database.password), false);
    for (const call of platform.process.calls) {
      assertEquals(call.command.join(" ").includes(provisioned.app.database.password), false);
    }
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 7 stopped PostgreSQL is config-ready/down and is not probed", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg7-down-" });
  try {
    const platform = testPlatform(
      root,
      (command) =>
        command.includes("--services")
          ? { code: 0, stdout: "mysql84\n", stderr: "" }
          : { code: 0, stdout: "", stderr: "" },
    );
    const report = await buildStatus(platform, addPostgresVersion(createEmptyState(), "17"));
    assertEquals(report.roles.find((role) => role.name === "postgres17")?.state, "config-ready");
    assertEquals(report.postgresVersions[0]?.health, "down");
    assertEquals(platform.process.calls.some((call) => call.command.includes("pg_isready")), false);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 7 doctor checks PostgreSQL health, volume, and credential modes", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg7-doctor-" });
  try {
    const platform = testPlatform(root, (command) => {
      if (command[0] === "df") {
        return {
          code: 0,
          stdout:
            "Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/x 100 10 90 10% /\n",
          stderr: "",
        };
      }
      if (command.includes("redis-cli")) return { code: 0, stdout: "PONG\n", stderr: "" };
      if (command.includes("--format")) return { code: 0, stdout: "25.0.0\n", stderr: "" };
      if (command.includes("--short")) return { code: 0, stdout: "2.30.0\n", stderr: "" };
      return { code: 0, stdout: "ok\n", stderr: "" };
    });
    const state = addPostgresVersion(createEmptyState(), "17");
    await platform.fs.mkdirp(join(root, "generated/postgres/postgres17"));
    await platform.fs.writeText(
      join(root, "generated/postgres/postgres17/root.pgpass"),
      "secret\n",
      0o600,
    );
    await platform.fs.mkdirp(join(root, "generated/mysql/mysql84"));
    await platform.fs.writeText(join(root, "generated/mysql/mysql84/root.cnf"), "secret\n", 0o600);
    const report = await runDoctor(platform, state);
    assertEquals(report.checks.some((check) => check.id === "postgres:postgres17"), true);
    assertEquals(report.checks.some((check) => check.id === "volume:postgres17-data"), true);
    assertEquals(
      report.checks.some((check) =>
        check.id === "secret-mode:root.pgpass" && check.status === "pass"
      ),
      true,
    );
    assertEquals(platform.process.calls.some((call) => call.command.includes("pg_isready")), true);
    assertEquals(
      platform.process.calls.some((call) => call.command.join(" ").includes("secret")),
      false,
    );
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 7 PostgreSQL prune terminates only recorded DB sessions then drops role", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg7-prune-" });
  try {
    const platform = testPlatform(root);
    await platform.fs.writeText(
      platform.paths.paths.envFile,
      "POSTGRES_PASSWORD=root-pg-secret\n",
      0o600,
    );
    let state = addPostgresVersion(createEmptyState(), "17");
    const provisioned = provisionApp(platform, state, {
      slug: "pgapp",
      domain: "pgapp.test",
      postgresVersion: "17",
      createDatabase: true,
    });
    await writeAppPruneManifest(platform, provisioned.app);
    state = deleteApp(provisioned.state, "pgapp", "delete pgapp", platform.clock.nowIso()).state;
    const plan = await planAppPrune(platform, state, "pgapp");
    assertEquals(plan.bindings[0]?.engine, "postgres");
    assertEquals(plan.bindings[0]?.databaseService, "postgres17");
    await assertRejects(() => executeAppPrune(platform, plan, "yes"), Error, "exactly 'delete'");
    await executeAppPrune(platform, plan, "delete");
    const call = platform.process.calls.at(-1)!;
    const stdin = String(call.options?.stdin);
    assertStringIncludes(stdin, "pg_terminate_backend");
    assertStringIncludes(stdin, 'DROP DATABASE IF EXISTS "pgapp"');
    assertStringIncludes(stdin, 'DROP ROLE IF EXISTS "pgapp"');
    assertEquals(stdin.includes("other_app"), false);
    assertEquals(call.command.join(" ").includes("root-pg-secret"), false);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 7 prune rejects malformed and cross-app retained manifests", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg7-prune-invalid-" });
  try {
    const platform = testPlatform(root);
    const state = addPostgresVersion(createEmptyState(), "17");
    const home = platform.paths.appHome("pgapp");
    await platform.fs.mkdirp(join(home, ".bento"));
    const path = join(home, ".bento/prune.json");
    await platform.fs.writeText(
      path,
      JSON.stringify({
        version: 3,
        slug: "pgapp",
        bindings: [{
          engine: "postgres",
          databaseService: "postgres17",
          databaseUser: "pgapp",
          databases: ["other_app"],
        }],
      }),
      0o600,
    );
    await assertRejects(() => planAppPrune(platform, state, "pgapp"), Error, "unsafe");
    await platform.fs.writeText(path, "{not-json", 0o600);
    await assertRejects(() => planAppPrune(platform, state, "pgapp"), Error, "invalid");
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("Phase 7 support bundle redacts PostgreSQL environment, pgpass, SQL, and URI secrets", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-pg7-support-" });
  try {
    const leaked = "pg-super-secret";
    const handler = async (command: string[]): Promise<RunResult> => {
      if (command[0] === "tar") {
        const output = await new Deno.Command("tar", {
          args: command.slice(1),
          stdout: "piped",
          stderr: "piped",
        }).output();
        return {
          code: output.code,
          stdout: new TextDecoder().decode(output.stdout),
          stderr: new TextDecoder().decode(output.stderr),
        };
      }
      if (command[0] === "df") return { code: 1, stdout: "", stderr: "unavailable" };
      return {
        code: 1,
        stdout:
          `PGPASSWORD=${leaked}\n*:*:*:postgres:${leaked}\npostgresql://postgres:${leaked}@db/app\n`,
        stderr: `ALTER ROLE demo PASSWORD E'${leaked}'`,
      };
    };
    const platform = testPlatform(root, handler);
    const state = addPostgresVersion(createEmptyState(), "17");
    await platform.fs.writeText(
      platform.paths.paths.envFile,
      `POSTGRES_PASSWORD=${leaked}\n`,
      0o600,
    );
    const bundle = await createSupportBundle(platform, state, join(root, "support.tar.gz"));
    const unpack = join(root, "unpacked");
    await Deno.mkdir(unpack);
    const extracted = await new Deno.Command("tar", { args: ["-xzf", bundle, "-C", unpack] })
      .output();
    assertEquals(extracted.code, 0);
    const files = await Array.fromAsync(Deno.readDir(unpack));
    for (const entry of files) {
      if (!entry.isFile) continue;
      assertEquals(
        (await Deno.readTextFile(join(unpack, entry.name))).includes(leaked),
        false,
        entry.name,
      );
    }
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
