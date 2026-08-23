import { validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";

const TOKEN_PATTERN = /^[A-Za-z0-9._~+/=-]+$/;

/** Validate an opaque remotely-managed Cloudflare tunnel token before it reaches a file. */
export function validateCloudflareTunnelToken(value: string): string {
  const token = value.trim();
  if (token.length < 20 || token.length > 4096 || !TOKEN_PATTERN.test(token)) {
    throw validationError(
      "invalid Cloudflare tunnel token; paste the token issued by Cloudflare without whitespace",
    );
  }
  return token;
}

export async function loadCloudflareTunnelToken(platform: Platform): Promise<string | undefined> {
  const path = platform.paths.paths.cloudflareTunnelTokenFile;
  if (!(await platform.fs.exists(path))) return undefined;
  const token = (await platform.fs.readText(path)).trim();
  return token ? validateCloudflareTunnelToken(token) : undefined;
}

export async function writeCloudflareTunnelToken(
  platform: Platform,
  value: string,
): Promise<string> {
  const token = validateCloudflareTunnelToken(value);
  await platform.fs.mkdirp(platform.paths.paths.operatorSecretsDir, 0o700);
  await platform.fs.atomicWriteText(
    platform.paths.paths.cloudflareTunnelTokenFile,
    `${token}\n`,
    0o600,
  );
  return token;
}
