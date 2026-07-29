import { join } from "@std/path";
import type { Platform } from "../platform/mod.ts";

export const DEFAULT_SQLITE_PATH = "data/sqlite/database.sqlite";
export const SQLITE_CONTAINER_ROOT = "/sqlite";

export function sqliteRelativePath(fileId: string): string {
  return `sqlite/${fileId}/database.sqlite`;
}

export function sqliteContainerPath(fileId: string): string {
  return `${SQLITE_CONTAINER_ROOT}/${fileId}/database.sqlite`;
}

export function sqliteHostDir(platform: Platform, fileId: string): string {
  return join(platform.paths.paths.root, "sqlite", fileId);
}

export function sqliteHostPath(platform: Platform, fileId: string): string {
  return join(sqliteHostDir(platform, fileId), "database.sqlite");
}
