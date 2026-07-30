import { join } from "@std/path";
import type { Platform } from "../platform/mod.ts";

export const SQLITE_CONTAINER_ROOT = "/sqlite";

export type SqliteFileEngine = "sqlite" | "litestream";

/** Plain SQLite uses .db so the Litestream *.sqlite watcher cannot pick it up. */
export function sqliteFileName(slug: string, engine: SqliteFileEngine = "litestream"): string {
  return `${slug}.${engine === "sqlite" ? "db" : "sqlite"}`;
}

export function sqliteRelativePath(
  fileId: string,
  slug: string,
  engine: SqliteFileEngine = "litestream",
): string {
  return `sqlite/${fileId}/${sqliteFileName(slug, engine)}`;
}

export function sqliteContainerPath(
  fileId: string,
  slug: string,
  engine: SqliteFileEngine = "litestream",
): string {
  return `${SQLITE_CONTAINER_ROOT}/${fileId}/${sqliteFileName(slug, engine)}`;
}

export function sqliteHostDir(platform: Platform, fileId: string): string {
  return join(platform.paths.paths.root, "sqlite", fileId);
}

export function sqliteHostPath(
  platform: Platform,
  fileId: string,
  slug: string,
  engine: SqliteFileEngine = "litestream",
): string {
  return join(sqliteHostDir(platform, fileId), sqliteFileName(slug, engine));
}
