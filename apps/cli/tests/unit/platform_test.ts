import { test } from "bun:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createFileLock, createMemoryLock } from "../../src/platform/lock.ts";

async function withTempDir(fn: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(join(tmpdir(), "bento-platform-"));
  try {
    await fn(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}

test("memory lock releases are idempotent even after another owner acquires", async () => {
  const lock = createMemoryLock();
  const path = "lock";
  const releaseFirst = await lock.exclusive(path);
  const waiting = lock.exclusive(path);
  await releaseFirst();
  const releaseSecond = await waiting;
  await releaseFirst();
  assert.equal(await lock.tryExclusive(path), null);
  await releaseSecond();

  const releaseShared1 = await lock.shared(path);
  const releaseShared2 = await lock.shared(path);
  await releaseShared1();
  await releaseShared1();
  assert.equal(await lock.tryExclusive(path), null);
  await releaseShared2();
  const releaseExclusive = await lock.tryExclusive(path);
  assert.notEqual(releaseExclusive, null);
  await releaseExclusive!();
});

test("file lock coordinates shared and exclusive owners across instances", async () => {
  await withTempDir(async (root) => {
    const path = join(root, "locks", "render.lock");
    const first = createFileLock();
    const second = createFileLock();
    const releaseShared = await first.shared(path);
    try {
      assert.equal(await second.tryExclusive(path), null);
    } finally {
      await releaseShared();
    }
    const releaseExclusive = await second.tryExclusive(path);
    assert.notEqual(releaseExclusive, null);
    try {
      assert.equal(await first.tryExclusive(path), null);
    } finally {
      await releaseExclusive!();
    }
    await releaseExclusive!();
    const releaseAgain = await first.tryExclusive(path);
    assert.notEqual(releaseAgain, null);
    await releaseAgain!();
  });
});

test("asset names cannot escape lookup or materialization directories", async () => {
  await withTempDir(async (root) => {
    const fs = createFileSystem();
    const assets = createAssetResolver(fs, root);
    await fs.writeText(join(root, "templates", "safe.txt"), "asset");
    const dest = join(root, "output", "subdir");
    assert.equal(await assets.readText("safe.txt"), "asset");
    assert.equal(await readFile(await assets.materialize("safe.txt", dest), "utf8"), "asset");

    await writeFile(join(root, "private.txt"), "not an asset");
    for (const path of [
      "../private.txt",
      "../../private.txt",
      "/private.txt",
      "a/../private.txt",
      "a\\..\\private.txt",
      "./safe.txt",
    ]) {
      await assert.rejects(assets.readText(path), /invalid asset path/);
      await assert.rejects(assets.materialize(path, dest), /invalid asset path/);
    }
    assert.equal(await readFile(join(root, "private.txt"), "utf8"), "not an asset");
  });
});

test("explicit file modes are applied to normal and atomic writes", async () => {
  await withTempDir(async (root) => {
    const fs = createFileSystem();
    const normal = join(root, "normal", "secret");
    const atomic = join(root, "atomic", "secret");
    await fs.writeText(normal, "one", 0o600);
    await fs.atomicWriteText(atomic, "one", 0o600);
    await fs.chmod(normal, 0o644);
    await fs.writeText(normal, "two", 0o600);
    await fs.atomicWriteText(atomic, "two", 0o600);
    assert.equal(await readFile(normal, "utf8"), "two");
    assert.equal(await readFile(atomic, "utf8"), "two");
    assert.equal((await stat(normal)).mode & 0o777, 0o600);
    assert.equal((await stat(atomic)).mode & 0o777, 0o600);
  });
});
