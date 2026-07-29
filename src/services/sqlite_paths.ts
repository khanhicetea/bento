import { join } from "@std/path";
import type { Platform } from "../platform/mod.ts";

export const SQLITE_CONTAINER_ROOT = "/sqlite";

export function sqliteRelativePath(fileId: string, slug: string): string {
  return `sqlite/${fileId}/${slug}.sqlite`;
}

export function sqliteContainerPath(fileId: string, slug: string): string {
  return `${SQLITE_CONTAINER_ROOT}/${fileId}/${slug}.sqlite`;
}

export function sqliteHostDir(platform: Platform, fileId: string): string {
  return join(platform.paths.paths.root, "sqlite", fileId);
}

export function sqliteHostPath(platform: Platform, fileId: string, slug: string): string {
  return join(sqliteHostDir(platform, fileId), `${slug}.sqlite`);
}
