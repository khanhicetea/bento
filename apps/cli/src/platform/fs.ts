import { dirname, join } from "node:path";
import {
  appendFile,
  chmod,
  copyFile,
  lstat,
  mkdir,
  open,
  readFile,
  readdir,
  rename,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import type { FileSystem } from "#/platform/interfaces.ts";
import { platformError } from "#/domain/errors.ts";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export function createFileSystem(): FileSystem {
  return {
    async readText(path) {
      try {
        return await readFile(path, "utf8");
      } catch (cause) {
        throw platformError(`failed to read ${path}`, cause);
      }
    },
    async readBytes(path) {
      try {
        return new Uint8Array(await readFile(path));
      } catch (cause) {
        throw platformError(`failed to read ${path}`, cause);
      }
    },
    async writeText(path, content, mode) {
      await this.writeBytes(path, encoder.encode(content), mode);
    },
    async writeBytes(path, content, mode) {
      try {
        await mkdir(dirname(path), { recursive: true });
        if (mode === undefined) {
          await writeFile(path, content);
        } else {
          // Open without truncating; restrict an existing file before replacing its bytes.
          // Creation also uses the requested mode, so new secrets are never written as public files.
          const handle = await open(path, "a", mode);
          try {
            await handle.chmod(mode);
            await handle.truncate(0);
            await handle.writeFile(content);
          } finally {
            await handle.close();
          }
        }
      } catch (cause) {
        throw platformError(`failed to write ${path}`, cause);
      }
    },
    async appendText(path, content) {
      try {
        await mkdir(dirname(path), { recursive: true });
        await appendFile(path, content);
      } catch (cause) {
        throw platformError(`failed to append ${path}`, cause);
      }
    },
    async exists(path) {
      try {
        await lstat(path);
        return true;
      } catch {
        return false;
      }
    },
    async mkdirp(path, mode) {
      try {
        await mkdir(path, { recursive: true });
        if (mode !== undefined) await chmod(path, mode);
      } catch (cause) {
        throw platformError(`failed to create directory ${path}`, cause);
      }
    },
    async remove(path, opts) {
      try {
        await rm(path, { recursive: opts?.recursive ?? false, force: true });
      } catch (cause) {
        throw platformError(`failed to remove ${path}`, cause);
      }
    },
    async rename(from, to) {
      try {
        await mkdir(dirname(to), { recursive: true });
        await rename(from, to);
      } catch (cause) {
        throw platformError(`failed to rename ${from} -> ${to}`, cause);
      }
    },
    async chmod(path, mode) {
      try {
        await chmod(path, mode);
      } catch (cause) {
        throw platformError(`failed to chmod ${path}`, cause);
      }
    },
    async copyFile(from, to) {
      try {
        await mkdir(dirname(to), { recursive: true });
        await copyFile(from, to);
      } catch (cause) {
        throw platformError(`failed to copy ${from} -> ${to}`, cause);
      }
    },
    async readDir(path) {
      try {
        return (await readdir(path)).sort();
      } catch (cause) {
        throw platformError(`failed to read directory ${path}`, cause);
      }
    },
    async stat(path) {
      try {
        const value = await stat(path);
        return {
          isFile: value.isFile(),
          isDirectory: value.isDirectory(),
          mode: value.mode,
          size: value.size,
          modifiedAt: value.mtime,
        };
      } catch (cause) {
        throw platformError(`failed to stat ${path}`, cause);
      }
    },
    async lstat(path) {
      try {
        const value = await lstat(path);
        return {
          isFile: value.isFile(),
          isDirectory: value.isDirectory(),
          isSymlink: value.isSymbolicLink(),
          mode: value.mode,
          size: value.size,
        };
      } catch (cause) {
        throw platformError(`failed to lstat ${path}`, cause);
      }
    },
    async atomicWriteText(path, content, mode) {
      await this.atomicWriteBytes(path, encoder.encode(content), mode);
    },
    async atomicWriteBytes(path, content, mode) {
      const dir = dirname(path);
      await mkdir(dir, { recursive: true });
      const tmp = join(dir, `.${crypto.randomUUID()}.tmp`);
      try {
        // A new temp file must not expose secret bytes before chmod runs.
        await writeFile(tmp, content, { mode });
        if (mode !== undefined) await chmod(tmp, mode);
        await rename(tmp, path);
      } catch (cause) {
        await rm(tmp, { force: true }).catch(() => undefined);
        throw platformError(`failed atomic write to ${path}`, cause);
      }
    },
  };
}

export { decoder, encoder };
