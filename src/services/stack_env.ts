/**
 * Load operator-owned stack environment (.env) without putting secrets on argv.
 */

import type { Platform } from "../platform/mod.ts";
import { secretError, validationError } from "../domain/errors.ts";
import { DEFAULT_ACME_URL } from "./tls.ts";

export const DEFAULT_COMPOSE_PROJECT_NAME = "bento";

export type NginxComposeEnvironment = {
  hostNetwork: boolean;
  httpPort?: number;
  httpsPort?: number;
  http3: boolean;
};

export type StackComposeEnvironment = {
  projectName: string;
  nginx: NginxComposeEnvironment;
};

/** Validate the stable, explicit Compose identity used to prefix stack resources. */
export function validateComposeProjectName(value: string): string {
  const project = value.trim();
  if (!/^[a-z0-9][a-z0-9_-]*$/.test(project)) {
    throw validationError(
      `invalid stack name '${value}'; use lowercase letters, digits, hyphens, or underscores`,
    );
  }
  return project;
}

/** Parse a strict operator boolean instead of silently accepting misspellings. */
export function parseEnvBoolean(
  value: string | undefined,
  defaultValue: boolean,
  name: string,
): boolean {
  if (value === undefined || value.trim() === "") return defaultValue;
  const normalized = value.trim().toLowerCase();
  if (["1", "true", "yes", "on"].includes(normalized)) return true;
  if (["0", "false", "no", "off"].includes(normalized)) return false;
  throw validationError(`${name} must be one of 1/0, true/false, yes/no, or on/off`);
}

function parseOptionalPort(value: string | undefined, name: string): number | undefined {
  if (value === undefined || value.trim() === "") return undefined;
  if (!/^\d+$/.test(value.trim())) throw validationError(`${name} must be a TCP/UDP port`);
  const port = Number(value.trim());
  if (!Number.isSafeInteger(port) || port < 1 || port > 65535) {
    throw validationError(`${name} must be between 1 and 65535`);
  }
  return port;
}

/** Parse a dotenv-style document into a flat string map. */
export function parseDotEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const eq = line.indexOf("=");
    if (eq <= 0) continue;
    const key = line.slice(0, eq).trim();
    if (!key) continue;
    let value = line.slice(eq + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    out[key] = value;
  }
  return out;
}

/** Read stack `.env` if present. */
export async function loadStackEnv(platform: Platform): Promise<Record<string, string>> {
  const path = platform.paths.paths.envFile;
  if (!(await platform.fs.exists(path))) return {};
  try {
    return parseDotEnv(await platform.fs.readText(path));
  } catch {
    return {};
  }
}

/** Runtime topology settings. Host-network Nginx remains the compatibility default. */
export async function loadStackComposeEnvironment(
  platform: Platform,
): Promise<StackComposeEnvironment> {
  const env = await loadStackEnv(platform);
  const projectName = validateComposeProjectName(
    env.COMPOSE_PROJECT_NAME?.trim() || DEFAULT_COMPOSE_PROJECT_NAME,
  );
  const hostNetwork = parseEnvBoolean(
    env.NGINX_HOST_NETWORK,
    true,
    "NGINX_HOST_NETWORK",
  );
  const httpPort = parseOptionalPort(env.NGINX_HTTP_PORT, "NGINX_HTTP_PORT");
  const httpsPort = parseOptionalPort(env.NGINX_HTTPS_PORT, "NGINX_HTTPS_PORT");
  if (!hostNetwork && httpPort !== undefined && httpPort === httpsPort) {
    throw validationError("NGINX_HTTP_PORT and NGINX_HTTPS_PORT must be different");
  }
  return {
    projectName,
    nginx: {
      hostNetwork,
      ...(httpPort !== undefined ? { httpPort } : {}),
      ...(httpsPort !== undefined ? { httpsPort } : {}),
      http3: parseEnvBoolean(env.HTTP3, false, "HTTP3"),
    },
  };
}

/** Update selected operator environment keys while preserving unrelated lines and comments. */
export async function updateStackEnv(
  platform: Platform,
  updates: Record<string, string>,
): Promise<void> {
  const path = platform.paths.paths.envFile;
  const existing = await platform.fs.exists(path) ? await platform.fs.readText(path) : "";
  const entries = new Map(Object.entries(updates));
  const updated = new Set<string>();
  const lines = existing.split("\n").flatMap((line) => {
    const match = line.match(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=/);
    if (!match || !entries.has(match[1]!)) return [line];
    const key = match[1]!;
    if (updated.has(key)) return [];
    updated.add(key);
    return [`${key}=${entries.get(key)!}`];
  });
  while (lines.length > 0 && lines.at(-1) === "") lines.pop();
  for (const [key, value] of entries) {
    if (!updated.has(key)) lines.push(`${key}=${value}`);
  }
  await platform.fs.atomicWriteText(path, `${lines.join("\n")}\n`, 0o600);
}

export async function loadMysqlRootPassword(
  platform: Platform,
): Promise<string | undefined> {
  const env = await loadStackEnv(platform);
  const value = env.MYSQL_ROOT_PASSWORD;
  if (value === undefined || value === "") return undefined;
  return value;
}

export async function requireMysqlRootPassword(platform: Platform): Promise<string> {
  const value = await loadMysqlRootPassword(platform);
  if (value === undefined) {
    throw secretError(
      "MYSQL_ROOT_PASSWORD is not set in the stack .env",
      "Run `bento init` or set MYSQL_ROOT_PASSWORD in the stack .env (mode 0600).",
    );
  }
  return value;
}

export async function loadPostgresRootPassword(
  platform: Platform,
): Promise<string | undefined> {
  const env = await loadStackEnv(platform);
  const value = env.POSTGRES_PASSWORD;
  if (value === undefined || value.trim() === "") return undefined;
  return value;
}

export async function requirePostgresRootPassword(platform: Platform): Promise<string> {
  const value = await loadPostgresRootPassword(platform);
  if (value === undefined) {
    throw secretError(
      "POSTGRES_PASSWORD is not set in the stack .env",
      "Run `bento init` or set POSTGRES_PASSWORD in the stack .env (mode 0600).",
    );
  }
  return value;
}

export async function loadRedisPassword(platform: Platform): Promise<string> {
  const env = await loadStackEnv(platform);
  return env.REDIS_PASSWORD ?? "";
}

export type AcmeEnvironment = { email?: string; url: string };

/** Shared native Nginx ACME issuer settings from the stack environment. */
export async function loadAcmeEnvironment(platform: Platform): Promise<AcmeEnvironment> {
  const env = await loadStackEnv(platform);
  const url = env.ACME_URL?.trim() || DEFAULT_ACME_URL;
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    throw validationError(`ACME_URL is not a valid URL: ${url}`);
  }
  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    throw validationError("ACME_URL must use http or https");
  }
  const email = env.ACME_EMAIL?.trim();
  return { url, ...(email ? { email } : {}) };
}

/** Whether generated TLS virtual hosts should enable HTTP/3 (HTTP3=true). */
export async function loadHttp3Enabled(platform: Platform): Promise<boolean> {
  const env = await loadStackEnv(platform);
  return parseEnvBoolean(env.HTTP3, false, "HTTP3");
}
