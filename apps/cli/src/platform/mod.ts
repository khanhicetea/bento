import type { Platform } from "#/platform/interfaces.ts";
import { createClock } from "#/platform/clock.ts";
import { createRandom } from "#/platform/random.ts";
import { createFileSystem } from "#/platform/fs.ts";
import { createFileLock } from "#/platform/lock.ts";
import { createProcessRunner } from "#/platform/process.ts";
import { createAssetResolver } from "#/platform/assets.ts";
import { createPathPolicy } from "#/platform/paths.ts";

export type { Platform } from "#/platform/interfaces.ts";
export type {
  AssetResolver,
  Clock,
  FileLock,
  FileSystem,
  PathPolicy,
  ProcessRunner,
  Random,
  RunOptions,
  RunResult,
  StackPaths,
} from "#/platform/interfaces.ts";

export { createClock, createFixedClock } from "#/platform/clock.ts";
export { createRandom, createSeededRandom } from "#/platform/random.ts";
export { createFileSystem } from "#/platform/fs.ts";
export { createFileLock, createMemoryLock } from "#/platform/lock.ts";
export { createProcessRunner, createRecordingProcessRunner } from "#/platform/process.ts";
export {
  createAssetResolver,
  isCompiledDistribution,
  resolveAssetRoot,
} from "#/platform/assets.ts";
export { containerAppHome, createPathPolicy, resolveStackPaths } from "#/platform/paths.ts";

/** Build the default production platform for a stack root. */
export function createPlatform(stackRoot: string, repoRoot?: string): Platform {
  const fs = createFileSystem();
  return {
    clock: createClock(),
    random: createRandom(),
    fs,
    lock: createFileLock(),
    process: createProcessRunner(),
    assets: createAssetResolver(fs, repoRoot),
    paths: createPathPolicy(stackRoot),
  };
}
