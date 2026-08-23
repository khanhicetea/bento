import { dirname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import { encodeHex } from "./hex.ts";
import type { AssetResolver, FileSystem } from "./interfaces.ts";
import { platformError } from "../domain/errors.ts";

/**
 * Resolve immutable assets from repository (source mode) or embedded compile includes.
 * Operator state always lives under the external stack root, never next to the binary.
 *
 * Bun standalone builds embed the templates directory with `bun build --asset=templates`.
 * `import.meta.url` resolves inside Bun's virtual filesystem, so defaultRepoRoot()
 * works for source and compiled distributions without reading the host CWD.
 */
export function createAssetResolver(fs: FileSystem, repoRoot?: string): AssetResolver {
  const root = resolveAssetRoot(repoRoot);

  async function resolveAsset(assetPath: string): Promise<string> {
    const clean = normalize(assetPath).replace(/^(\.\.(\/|\\|$))+/, "");
    // Prefer templates/ tree (source repo layout and compile --include=templates)
    const candidates = [join(root, "templates", clean), join(root, clean)];
    for (const c of candidates) {
      if (await fs.exists(c)) return c;
    }
    throw platformError(`asset not found: ${assetPath}`);
  }

  return {
    async readText(assetPath: string): Promise<string> {
      const path = await resolveAsset(assetPath);
      return await fs.readText(path);
    },
    async readBytes(assetPath: string): Promise<Uint8Array> {
      const path = await resolveAsset(assetPath);
      return await fs.readBytes(path);
    },
    async materialize(assetPath: string, destDir: string): Promise<string> {
      const path = await resolveAsset(assetPath);
      const dest = join(destDir, assetPath);
      await fs.mkdirp(dirname(dest));
      await fs.copyFile(path, dest);
      return dest;
    },
    async digest(): Promise<string> {
      // Stable digest of templates tree for parity tracking (source == compiled).
      const templatesRoot = join(root, "templates");
      const files: string[] = [];
      async function walk(dir: string, prefix: string) {
        if (!(await fs.exists(dir))) return;
        const names = await fs.readDir(dir);
        for (const name of names) {
          const full = join(dir, name);
          const rel = prefix ? `${prefix}/${name}` : name;
          const st = await fs.stat(full);
          if (st.isDirectory) await walk(full, rel);
          else if (st.isFile) files.push(rel);
        }
      }
      await walk(templatesRoot, "");
      files.sort();
      const chunks: string[] = [];
      for (const rel of files) {
        const bytes = await fs.readBytes(join(templatesRoot, rel));
        const hash = await crypto.subtle.digest("SHA-256", bytes.slice());
        chunks.push(`${rel}:${encodeHex(new Uint8Array(hash))}`);
      }
      const combined = new TextEncoder().encode(chunks.join("\n"));
      const digest = await crypto.subtle.digest("SHA-256", combined.slice());
      return encodeHex(new Uint8Array(digest));
    },
  };
}

/** True when running inside a Bun standalone executable with embedded assets. */
export function isCompiledDistribution(): boolean {
  return import.meta.url.includes("/$bunfs/") || import.meta.url.startsWith("bun:");
}

/**
 * Resolve the immutable asset root.
 * - Explicit repoRoot (tests / source overrides) wins when provided.
 * - Otherwise walk from this module to the package root (source: repo; compiled: VFS root).
 */
export function resolveAssetRoot(repoRoot?: string): string {
  if (repoRoot && repoRoot.length > 0) return repoRoot;
  return defaultRepoRoot();
}

function defaultRepoRoot(): string {
  // src/platform/assets.ts -> package root (repository or compiled VFS root)
  try {
    const here = dirname(fileURLToPath(import.meta.url));
    // Bundling collapses module URLs to /$bunfs/root/<executable>; assets are
    // embedded directly below that root. Source modules remain under src/platform.
    return isCompiledDistribution() ? here : join(here, "..", "..");
  } catch {
    return process.cwd();
  }
}
