/** Bento version and build metadata. */
export const BENTO_VERSION = "0.1.0";
export const BUN_TARGET_VERSION = "1.4.0";
export const STATE_SCHEMA_VERSION = 3 as const;
export const ASSET_VERSION = "0.1.0";

export function versionBanner(): string {
  return `bento ${BENTO_VERSION} (bun ${BUN_TARGET_VERSION})`;
}
