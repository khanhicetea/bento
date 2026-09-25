import { test as bunTest } from "bun:test";
import assertModule from "node:assert/strict";
import type { Stats } from "node:fs";
import {
  chmod,
  chown,
  copyFile,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rename,
  rm,
  rmdir,
  stat,
  symlink,
  utimes,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { RuntimeCommand } from "../src/platform/runtime.ts";
export { encodeHex } from "../src/platform/hex.ts";

export { RuntimeCommand };

function test(
  nameOrOptions:
    | string
    | {
        name: string;
        fn: () => void | Promise<unknown>;
        ignore?: boolean;
        sanitizeOps?: boolean;
        sanitizeResources?: boolean;
      },
  fn?: () => void | Promise<unknown>,
): void {
  if (typeof nameOrOptions === "string") {
    bunTest(nameOrOptions, fn!);
  } else if (nameOrOptions.ignore) {
    bunTest.skip(nameOrOptions.name, nameOrOptions.fn);
  } else {
    bunTest(nameOrOptions.name, nameOrOptions.fn);
  }
}

function info(value: Stats) {
  return {
    isFile: value.isFile(),
    isDirectory: value.isDirectory(),
    isSymlink: value.isSymbolicLink(),
    isSocket: value.isSocket(),
    mode: Number(value.mode),
    size: Number(value.size),
    uid: value.uid,
    gid: value.gid,
    mtime: value.mtime,
  };
}

const cliRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

export const runtime = {
  test,
  Command: RuntimeCommand,
  cwd: () => cliRoot,
  uid: () => process.getuid?.() ?? null,
  env: {
    get: (name: string) => Bun.env[name],
    set: (name: string, value: string) => {
      process.env[name] = value;
    },
    delete: (name: string) => {
      delete process.env[name];
    },
    toObject: () => ({ ...process.env }) as Record<string, string>,
  },
  makeTempDir: async ({ prefix = "bento-test-" }: { prefix?: string } = {}) =>
    await mkdtemp(join(tmpdir(), prefix)),
  mkdir,
  remove: async (path: string | URL, options?: { recursive?: boolean }) => {
    if (options?.recursive) await rm(path, { recursive: true, force: true });
    else {
      try {
        await rmdir(path);
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code === "ENOTDIR") await rm(path, { force: true });
        else throw error;
      }
    }
  },
  readTextFile: async (path: string | URL) => await readFile(path, "utf8"),
  readFile: async (path: string | URL) => new Uint8Array(await readFile(path)),
  writeTextFile: async (
    path: string | URL,
    data: string,
    options?: { append?: boolean; mode?: number },
  ) => {
    await writeFile(path, data, {
      flag: options?.append ? "a" : "w",
      mode: options?.mode,
    });
  },
  writeFile: async (path: string | URL, data: Uint8Array) => {
    await writeFile(path, data);
  },
  stat: async (path: string | URL) => info(await stat(path)),
  lstat: async (path: string | URL) => info(await lstat(path)),
  readDir: async function* (path: string | URL) {
    for (const entry of await readdir(path, { withFileTypes: true })) {
      yield {
        name: entry.name,
        isFile: entry.isFile(),
        isDirectory: entry.isDirectory(),
        isSymlink: entry.isSymbolicLink(),
      };
    }
  },
  chmod,
  chown,
  copyFile,
  rename,
  symlink,
  utime: utimes,
};

export function assert(condition: unknown, message?: string): asserts condition {
  if (message === undefined) assertModule.ok(condition);
  else assertModule.ok(condition, message);
}

export function assertEquals<T>(actual: T, expected: T, message?: string): void {
  if (message === undefined) assertModule.deepStrictEqual(actual, expected);
  else assertModule.deepStrictEqual(actual, expected, message);
}

export function assertNotEquals<T>(actual: T, expected: T, message?: string): void {
  if (message === undefined) assertModule.notDeepStrictEqual(actual, expected);
  else assertModule.notDeepStrictEqual(actual, expected, message);
}

export function assertStringIncludes(actual: string, expected: string, message?: string): void {
  assertModule.ok(
    actual.includes(expected),
    message ?? `Expected ${JSON.stringify(actual)} to include ${JSON.stringify(expected)}`,
  );
}

export function assertMatch(actual: string, expected: RegExp, message?: string): void {
  if (message === undefined) assertModule.match(actual, expected);
  else assertModule.match(actual, expected, message);
}

export function assertThrows(
  fn: () => unknown,
  ErrorClass?: new (...args: any[]) => Error,
  messageIncludes?: string,
): Error {
  let thrown: unknown;
  try {
    fn();
  } catch (error) {
    thrown = error;
  }
  assertModule.ok(thrown, "Expected function to throw");
  if (ErrorClass) assertModule.ok(thrown instanceof ErrorClass, `Expected ${ErrorClass.name}`);
  if (messageIncludes) assertModule.ok(String((thrown as Error).message).includes(messageIncludes));
  return thrown as Error;
}

export async function assertRejects(
  fn: () => Promise<unknown>,
  ErrorClass?: new (...args: any[]) => Error,
  messageIncludes?: string,
): Promise<Error> {
  let thrown: unknown;
  try {
    await fn();
  } catch (error) {
    thrown = error;
  }
  assertModule.ok(thrown, "Expected promise to reject");
  if (ErrorClass) assertModule.ok(thrown instanceof ErrorClass, `Expected ${ErrorClass.name}`);
  if (messageIncludes) assertModule.ok(String((thrown as Error).message).includes(messageIncludes));
  return thrown as Error;
}
