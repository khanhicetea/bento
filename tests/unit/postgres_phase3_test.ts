import { assertEquals, assertRejects, assertThrows } from "@std/assert";
import { createEmptyState } from "../../src/domain/state.ts";
import type { Platform, RunOptions, RunResult } from "../../src/platform/mod.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import {
  addPostgresVersion,
  execPostgresSql,
  isPostgresReachable,
  listPostgresVersions,
  postgresIdentifier,
  postgresLiteral,
  postgresVersionDetails,
  removePostgresVersion,
  verifyPostgresSql,
} from "../../src/services/postgres.ts";

function testPlatform(
  root: string,
  handler?: (command: string[], options?: RunOptions) => Promise<RunResult> | RunResult,
): Platform & { process: ReturnType<typeof createRecordingProcessRunner> } {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-26T12:00:00.000Z"),
    random: createSeededRandom("aabbccddeeff0011"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(handler),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

Deno.test("PostgreSQL managed-version helpers validate stable names", () => {
  assertEquals(postgresVersionDetails("17"), {
    engine: "postgres",
    version: "17",
    service: "postgres17",
    image: "postgres:17",
    volume: "postgres17-data",
  });
  for (const invalid of ["", "0", "17.2", "v17", "latest", "17-alpine", " 17"]) {
    assertThrows(() => postgresVersionDetails(invalid));
  }
});

Deno.test("PostgreSQL add/list is sorted and rejects duplicates", () => {
  let state = createEmptyState("2026-07-26T12:00:00.000Z");
  state = addPostgresVersion(state, "17");
  state = addPostgresVersion(state, "15");
  assertEquals(listPostgresVersions(state).map((entry) => entry.version), ["15", "17"]);
  assertThrows(() => addPostgresVersion(state, "17"), Error, "already managed");
});

Deno.test("PostgreSQL version removal is always refused", () => {
  assertThrows(
    () => removePostgresVersion(createEmptyState(), "17"),
    Error,
    "removal is unsupported",
  );
});

Deno.test("PostgreSQL identifier and literal quoting contains hostile input", () => {
  assertEquals(postgresIdentifier("plain_name"), '"plain_name"');
  assertEquals(postgresIdentifier("my-app"), '"my-app"');
  assertEquals(postgresIdentifier('role"name'), '"role""name"');
  assertEquals(postgresIdentifier('x"; DROP ROLE postgres; --'), '"x""; DROP ROLE postgres; --"');
  assertEquals(postgresLiteral("plain_name"), "E'plain_name'");
  assertEquals(
    postgresLiteral("it's\\hostile'; DROP DATABASE x; --"),
    "E'it''s\\\\hostile''; DROP DATABASE x; --'",
  );
  assertThrows(() => postgresIdentifier("bad\0name"));
  assertThrows(() => postgresLiteral("bad\0value"));
});

Deno.test("protected PostgreSQL SQL execution keeps SQL and password off argv", async () => {
  const password = "root:secret\\value";
  const sql = "SELECT 'hostile'; -- private payload";
  const platform = testPlatform("/tmp/postgres-phase3", () => ({
    code: 0,
    stdout: "1\n",
    stderr: "",
  }));
  const result = await execPostgresSql(platform, "postgres17", sql, password);
  assertEquals(result.code, 0);
  const call = platform.process.calls[0]!;
  const argv = call.command.join(" ");
  assertEquals(argv.includes(password), false);
  assertEquals(argv.includes(sql), false);
  assertEquals(argv.includes("postgres17"), true);
  assertEquals(argv.includes("mktemp"), true);
  assertEquals(argv.includes("chmod 600"), true);
  const stdin = String(call.options?.stdin);
  assertEquals(stdin.includes("root\\:secret\\\\value"), true);
  assertEquals(stdin.includes(sql), true);
});

Deno.test("PostgreSQL reachability uses pg_isready and authenticated check reports failure", async () => {
  const platform = testPlatform("/tmp/postgres-phase3", (command) => ({
    code: command.includes("pg_isready") ? 0 : 1,
    stdout: "",
    stderr: "authentication failed",
  }));
  assertEquals(await isPostgresReachable(platform, "postgres17"), true);
  const reachabilityArgv = platform.process.calls[0]!.command.join(" ");
  assertEquals(reachabilityArgv.includes("pg_isready"), true);
  assertEquals(reachabilityArgv.includes("password"), false);
  await assertRejects(
    () => verifyPostgresSql(platform, "postgres17", "not-on-argv"),
    Error,
    "authentication failed",
  );
  assertEquals(platform.process.calls[1]!.command.join(" ").includes("not-on-argv"), false);
});
