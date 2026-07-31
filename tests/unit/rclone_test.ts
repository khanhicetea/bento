import { assertEquals, assertStringIncludes, assertThrows } from "@std/assert";
import { join } from "@std/path";
import { createEmptyState } from "../../src/domain/state.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import type { Platform } from "../../src/platform/mod.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { redact } from "../../src/ui/output.ts";
import {
  initializeRcloneConfig,
  rcloneComposeCommand,
  readRcloneBackupTarget,
  saveRcloneBackupTarget,
  uploadBackupArtifacts,
  validateRcloneBackupTarget,
} from "../../src/services/rclone.ts";

function testPlatform(root: string): Platform & {
  process: ReturnType<typeof createRecordingProcessRunner>;
} {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-08-01T03:15:00.000Z"),
    random: createSeededRandom("aabbccddeeff0088"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(() => ({ code: 0, stdout: "", stderr: "" })),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

Deno.test("rclone sidecar configuration is private and backup paths are mounted read-only", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-rclone-" });
  try {
    const platform = testPlatform(root);
    await initializeRcloneConfig(platform);
    const config = platform.paths.paths.rcloneConfigFile;
    assertEquals(await platform.fs.exists(config), true);
    assertEquals((await platform.fs.stat(config)).mode & 0o777, 0o600);
    assertEquals((await platform.fs.stat(platform.paths.paths.rcloneDir)).mode & 0o777, 0o700);

    const command = await rcloneComposeCommand(platform, createEmptyState(), ["listremotes"]);
    assertEquals(command.slice(-5), ["run", "--rm", "--no-deps", "rclone", "listremotes"]);
    assertStringIncludes(command.join(" "), "docker-compose.base.yml");
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("redaction preserves timestamped rclone diagnostics while hiding pgpass records", () => {
  const diagnostic = "2026/07/31 03:36:54 ERROR : report.sql.zst: Access denied";
  assertEquals(redact(diagnostic), diagnostic);
  assertEquals(redact("db:5432:app:user:super-secret"), "db:5432:app:user:***");
});

Deno.test("scheduled rclone uploads preserve backup-relative paths and validate destinations", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-rclone-upload-" });
  try {
    const platform = testPlatform(root);
    const target = await saveRcloneBackupTarget(platform, "archive", "/bento//production/");
    assertEquals(target.prefix, "bento/production");
    assertEquals(await readRcloneBackupTarget(platform), target);

    const artifact = join(platform.paths.paths.backupsDir, "postgres17", "demo", "demo.sql.zst");
    await uploadBackupArtifacts(platform, createEmptyState(), [{
      engine: "postgres",
      service: "postgres17",
      database: "demo",
      path: artifact,
      bytes: 42,
    }], target);
    const call = platform.process.calls[0]!;
    assertEquals(call.command.slice(-3), [
      "copyto",
      "/backups/postgres17/demo/demo.sql.zst",
      "archive:bento/production/postgres17/demo/demo.sql.zst",
    ]);

    assertThrows(
      () => validateRcloneBackupTarget("archive:bad", "ok"),
      Error,
      "rclone remote",
    );
    assertThrows(
      () => validateRcloneBackupTarget("archive", "../outside"),
      Error,
      "rclone prefix",
    );
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
