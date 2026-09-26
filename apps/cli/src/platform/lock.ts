import { dirname } from "node:path";
import { mkdir } from "node:fs/promises";
import type { Subprocess } from "bun";
import type { FileLock } from "#/platform/interfaces.ts";
import { platformError } from "#/domain/errors.ts";

async function acquire(path: string, shared: boolean, nonblocking: boolean): Promise<(() => Promise<void>) | null> {
  await mkdir(dirname(path), { recursive: true });
  const command = ["flock", shared ? "--shared" : "--exclusive"];
  if (nonblocking) command.push("--nonblock");
  command.push(path, "sh", "-c", "printf 'locked\\n'; cat >/dev/null");

  let child: Subprocess | undefined;
  try {
    child = Bun.spawn(command, {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
    });
    if (!(child.stdout instanceof ReadableStream)) throw new Error("flock stdout unavailable");
    const reader = child.stdout.getReader();
    const marker = new TextEncoder().encode("locked\n");
    let received = 0;
    let valid = true;
    try {
      while (received < marker.length) {
        const chunk = await reader.read();
        if (chunk.done) {
          valid = false;
          break;
        }
        for (const byte of chunk.value) {
          if (received >= marker.length || byte !== marker[received]) {
            valid = false;
            break;
          }
          received++;
        }
        if (!valid) break;
      }
    } finally {
      reader.releaseLock();
    }
    if (!valid || received !== marker.length) {
      const code = await child.exited;
      if (nonblocking && code === 1) return null;
      const stderr = child.stderr instanceof ReadableStream ? await new Response(child.stderr).text() : "";
      throw new Error(stderr.trim() || `flock exited ${code}`);
    }

    let released = false;
    return async () => {
      if (released) return;
      released = true;
      const stdin = child!.stdin;
      if (typeof stdin !== "number" && stdin !== undefined) await stdin.end();
      await child!.exited;
    };
  } catch (cause) {
    child?.kill();
    throw cause;
  }
}

/** Linux advisory locks held by a small util-linux flock subprocess. */
export function createFileLock(): FileLock {
  return {
    async exclusive(path) {
      try {
        return (await acquire(path, false, false))!;
      } catch (cause) {
        throw platformError(`failed to acquire exclusive lock ${path}`, cause);
      }
    },
    async tryExclusive(path) {
      try {
        return await acquire(path, false, true);
      } catch (cause) {
        throw platformError(`failed to try exclusive lock ${path}`, cause);
      }
    },
    async shared(path) {
      try {
        return (await acquire(path, true, false))!;
      } catch (cause) {
        throw platformError(`failed to acquire shared lock ${path}`, cause);
      }
    },
  };
}

/** In-memory lock for unit tests. */
export function createMemoryLock(): FileLock {
  const exclusiveOwners = new Map<string, number>();
  const sharedCounts = new Map<string, number>();
  let waiters: Array<() => void> = [];
  function notify() {
    const current = waiters;
    waiters = [];
    for (const waiter of current) waiter();
  }
  return {
    async exclusive(path) {
      while (true) {
        if (!exclusiveOwners.has(path) && (sharedCounts.get(path) ?? 0) === 0) {
          exclusiveOwners.set(path, 1);
          break;
        }
        await new Promise<void>((resolve) => waiters.push(resolve));
      }
      let released = false;
      return async () => {
        if (released) return;
        released = true;
        exclusiveOwners.delete(path);
        notify();
      };
    },
    async tryExclusive(path) {
      if (exclusiveOwners.has(path) || (sharedCounts.get(path) ?? 0) !== 0) return null;
      exclusiveOwners.set(path, 1);
      let released = false;
      return async () => {
        if (!released) {
          released = true;
          exclusiveOwners.delete(path);
          notify();
        }
      };
    },
    async shared(path) {
      while (exclusiveOwners.has(path)) await new Promise<void>((resolve) => waiters.push(resolve));
      sharedCounts.set(path, (sharedCounts.get(path) ?? 0) + 1);
      let released = false;
      return async () => {
        if (released) return;
        released = true;
        const count = (sharedCounts.get(path) ?? 1) - 1;
        if (count <= 0) sharedCounts.delete(path);
        else sharedCounts.set(path, count);
        notify();
      };
    },
  };
}
