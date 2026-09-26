import { runtime as bunRuntime, assertEquals, assertStringIncludes } from "../runtime.ts";
import { dirname } from "node:path";
import { createEmptyState } from "../../src/domain/state.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import type { Platform, RunOptions, RunResult } from "../../src/platform/mod.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { provisionApp } from "../../src/services/app.ts";
import { formatDoctor, runDoctor } from "../../src/services/doctor.ts";
import { sqliteHostPath } from "../../src/services/sqlite_paths.ts";

function testPlatform(
  root: string,
  handler?: (command: string[], options?: RunOptions) => Promise<RunResult> | RunResult,
): Platform & { process: ReturnType<typeof createRecordingProcessRunner> } {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-28T12:00:00.000Z"),
    random: createSeededRandom("d0c70a7e57aabbcc"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(handler ?? healthyCommand),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

function healthyCommand(command: string[]): RunResult {
  if (command[0] === "uname" && command[1] === "-s") {
    return { code: 0, stdout: "Linux\n", stderr: "" };
  }
  if (command[0] === "uname" && command[1] === "-m") {
    return { code: 0, stdout: "x86_64\n", stderr: "" };
  }
  if (command[0] === "df") {
    return {
      code: 0,
      stdout: "Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/x 100 10 90 10% /\n",
      stderr: "",
    };
  }
  if (command[0] === "timedatectl") return { code: 0, stdout: "yes\n", stderr: "" };
  if (command[0] === "ss") {
    return {
      code: 0,
      stdout: "LISTEN 0 128 0.0.0.0:80 0.0.0.0:*\n",
      stderr: "",
    };
  }
  if (command[0] === "docker" && command[1] === "version") {
    return { code: 0, stdout: "25.0.0\n", stderr: "" };
  }
  if (command[0] === "docker" && command[1] === "info") {
    return { code: 0, stdout: "overlay2|x86_64|[]\n", stderr: "" };
  }
  if (command.includes("--short")) return { code: 0, stdout: "2.30.0\n", stderr: "" };
  if (command.includes("redis-cli")) return { code: 0, stdout: "PONG\n", stderr: "" };
  if (command.includes("sqlite3")) return { code: 0, stdout: "ok\n", stderr: "" };
  return { code: 0, stdout: "ok\n", stderr: "" };
}

bunRuntime.test("doctor reports invalid stack environment and incomplete generation instead of throwing", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-doctor-env-" });
  try {
    const platform = testPlatform(root);
    await platform.fs.writeText(platform.paths.paths.envFile, "NGINX_HOST_NETWORK=perhaps\n", 0o600);
    await platform.fs.mkdirp(platform.paths.paths.generatedDir);
    await platform.fs.writeText(
      `${platform.paths.paths.generatedDir}/.generation.json`,
      JSON.stringify({ managedFiles: ["nginx/missing.conf"] }),
      0o644,
    );

    const report = await runDoctor(platform, createEmptyState(platform.clock.nowIso()));
    assertEquals(report.checks.find((check) => check.id === "stack-environment")?.status, "fail");
    const generation = report.checks.find((check) => check.id === "generation");
    assertEquals(generation?.status, "fail");
    assertStringIncludes(generation?.detail ?? "", "managed generated file");
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("doctor authenticates the Redis health probe with the stack password", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-doctor-redis-" });
  try {
    const platform = testPlatform(root, (command) => {
      if (command.some((part) => part.includes("redis-cli"))) {
        return { code: 0, stdout: "PONG\n", stderr: "" };
      }
      return healthyCommand(command);
    });
    await platform.fs.writeText(platform.paths.paths.envFile, "REDIS_PASSWORD=doctor-secret\n", 0o600);

    const report = await runDoctor(platform, createEmptyState(platform.clock.nowIso()));
    const check = report.checks.find((candidate) => candidate.id === "redis");
    assertEquals(check?.status, "pass");
    const call = platform.process.calls.find((candidate) =>
      candidate.command.some((part) => part.includes("redis-cli")),
    );
    assertEquals(call?.command.includes("doctor-secret"), false);
    assertEquals(call?.options?.stdin, "doctor-secret\n");
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("doctor runs a read-only quick_check for SQLite bindings", async () => {
  const root = await bunRuntime.makeTempDir({
    prefix: "bento-doctor-sqlite-",
  });
  try {
    const platform = testPlatform(root);
    const provisioned = provisionApp(platform, createEmptyState(platform.clock.nowIso()), {
      slug: "localdb",
      domain: "localdb.test",
      databaseEngine: "sqlite",
    });
    const database = provisioned.app.databases[0]!;
    if (database.engine !== "sqlite") throw new Error("expected SQLite binding");
    const path = sqliteHostPath(platform, database.file.id, "localdb", "sqlite");
    await platform.fs.mkdirp(dirname(path));
    await platform.fs.writeBytes(path, new Uint8Array([1, 2, 3]), 0o600);

    const report = await runDoctor(platform, provisioned.state);
    const check = report.checks.find((candidate) => candidate.id.startsWith("sqlite:localdb:"));
    assertEquals(check?.status, "pass");
    const call = platform.process.calls.find((candidate) => candidate.command.includes("sqlite3"));
    assertEquals(call?.command.includes("-readonly"), true);
    assertEquals(call?.command.includes("PRAGMA quick_check;"), true);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("doctor output groups categories once and collects failures at the bottom", () => {
  const output = formatDoctor({
    generatedAt: "2026-07-28T12:00:00.000Z",
    stackRoot: "/srv/bento",
    ok: false,
    checks: [
      { id: "disk", category: "storage", status: "pass", detail: "ok" },
      {
        id: "docker",
        category: "runtime",
        status: "fail",
        detail: "daemon unavailable",
      },
      {
        id: "clock",
        category: "host",
        status: "warn",
        detail: "NTP unknown",
      },
      { id: "volume", category: "storage", status: "pass", detail: "ok" },
      {
        id: "nginx",
        category: "health",
        status: "fail",
        detail: "not running",
      },
    ],
    summary: { pass: 2, warn: 1, fail: 2 },
  });
  assertEquals(output.match(/^storage:$/gm)?.length, 1);
  assertStringIncludes(output, "volume: ok");
  assertStringIncludes(output, "FAILED checks:");
  assertStringIncludes(output, "runtime/docker: daemon unavailable");
  assertStringIncludes(output, "health/nginx: not running");
  assertEquals(output.indexOf("FAILED checks:") > output.indexOf("host:"), true);
  assertEquals(output.indexOf("Summary:") > output.indexOf("FAILED checks:"), true);
});
