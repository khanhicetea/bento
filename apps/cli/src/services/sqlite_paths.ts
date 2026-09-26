import { join } from "node:path";
import type { Platform } from "#/platform/mod.ts";

export const SQLITE_CONTAINER_ROOT = "/sqlite";

export function sqliteFileName(slug: string): string {
  return `${slug}.db`;
}

export function sqliteRelativePath(fileId: string, slug: string): string {
  return `sqlite/${fileId}/${sqliteFileName(slug)}`;
}

export function sqliteContainerPath(fileId: string, slug: string): string {
  return `${SQLITE_CONTAINER_ROOT}/${fileId}/${sqliteFileName(slug)}`;
}

export function sqliteHostDir(platform: Platform, fileId: string): string {
  return join(platform.paths.paths.root, "sqlite", fileId);
}

export function sqliteHostPath(platform: Platform, fileId: string, slug: string): string {
  return join(sqliteHostDir(platform, fileId), sqliteFileName(slug));
}
