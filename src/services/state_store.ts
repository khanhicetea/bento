/**
 * Load/save desired state with exclusive locking and atomic writes.
 *
 * Loads are read-only and never rewrite state.json (key order and defaults stay as the
 * operator left them). Only the current state schema is accepted.
 */

import type { DesiredState } from "../domain/state.ts";
import { createEmptyState } from "../domain/state.ts";
import { loadStateFromJson, migrateV1ToV2, stateToJson } from "../schemas/state.ts";
import type { Platform } from "../platform/mod.ts";
import { safetyError, stateError } from "../domain/errors.ts";
import { STATE_SCHEMA_VERSION } from "../version.ts";
import { parseDotEnv } from "./stack_env.ts";

export class StateStore {
  constructor(private readonly platform: Platform) {}

  async exists(): Promise<boolean> {
    return await this.platform.fs.exists(this.platform.paths.paths.stateFile);
  }

  /** Read and validate the current state document without rewriting it. */
  async load(): Promise<DesiredState> {
    const path = this.platform.paths.paths.stateFile;
    if (!(await this.platform.fs.exists(path))) {
      throw stateError(`no desired state at ${path}`, {
        recovery: "Run `bento init` to create an empty state document.",
      });
    }
    const text = await this.platform.fs.readText(path);
    return loadStateFromJson(text);
  }

  async save(state: DesiredState): Promise<void> {
    const path = this.platform.paths.paths.stateFile;
    const next = {
      ...state,
      schemaVersion: STATE_SCHEMA_VERSION,
      updatedAt: this.platform.clock.nowIso(),
    };
    // Validate by round-tripping through schema before write
    const json = stateToJson(next);
    loadStateFromJson(json);
    await this.platform.fs.atomicWriteText(path, json, 0o600);
  }

  /** Initialize empty state if missing; refuse to overwrite. */
  async init(force = false): Promise<DesiredState> {
    const path = this.platform.paths.paths.stateFile;
    if (await this.platform.fs.exists(path) && !force) {
      throw stateError(`state already exists at ${path}`, {
        recovery: "Use --force only if you intentionally want to reset desired state.",
      });
    }
    const state = createEmptyState(this.platform.clock.nowIso());
    await this.platform.fs.mkdirp(this.platform.paths.paths.root);
    await this.platform.fs.mkdirp(this.platform.paths.paths.lockDir);
    await this.platform.fs.mkdirp(this.platform.paths.paths.generatedDir);
    await this.platform.fs.mkdirp(this.platform.paths.paths.overlaysDir);
    await this.platform.fs.mkdirp(this.platform.paths.paths.customDir);
    await this.platform.fs.mkdirp(this.platform.paths.paths.backupsDir);
    await this.platform.fs.mkdirp(this.platform.paths.paths.certsDir);
    await this.platform.fs.mkdirp(this.platform.paths.paths.homesDir);
    await this.save(state);
    // Seed stack secrets once. Reconciliation fills missing/empty secrets while
    // preserving every existing non-empty value byte-for-byte.
    await this.reconcileStackEnv();
    return state;
  }

  /** Add missing stack secrets without replacing any existing non-empty value. */
  async reconcileStackEnv(): Promise<void> {
    const envPath = this.platform.paths.paths.envFile;
    if (!(await this.platform.fs.exists(envPath))) {
      await this.platform.fs.atomicWriteText(
        envPath,
        defaultEnvContent({
          mysqlRootPassword: this.platform.random.hex(24),
          postgresRootPassword: this.platform.random.hex(24),
          redisPassword: this.platform.random.hex(24),
          projectName: "bento",
        }),
        0o600,
      );
      return;
    }

    const existing = await this.platform.fs.readText(envPath);
    const env = parseDotEnv(existing);
    const missing: string[] = [];
    if (!env.MYSQL_ROOT_PASSWORD) {
      missing.push(`MYSQL_ROOT_PASSWORD=${this.platform.random.hex(24)}`);
    }
    if (!env.POSTGRES_PASSWORD?.trim()) {
      missing.push(`POSTGRES_PASSWORD=${this.platform.random.hex(24)}`);
    }
    if (!env.REDIS_PASSWORD) {
      missing.push(`REDIS_PASSWORD=${this.platform.random.hex(24)}`);
    }
    if (missing.length === 0) return;

    const separator = existing.length === 0 || existing.endsWith("\n") ? "" : "\n";
    await this.platform.fs.atomicWriteText(
      envPath,
      `${existing}${separator}${missing.join("\n")}\n`,
      0o600,
    );
  }

  /**
   * Explicitly migrate schema v1 after exact operator confirmation.
   * Validation happens before the backup and atomic replacement; routine loads never call this.
   */
  async migrateV1ToV2(confirmation: string | undefined): Promise<{
    state: DesiredState;
    backupPath: string;
  }> {
    if (confirmation !== "migrate-v1-to-v2") {
      throw safetyError(
        "state migration confirmation must be exactly 'migrate-v1-to-v2'",
        "Re-run with --confirm migrate-v1-to-v2 after backing up the stack.",
      );
    }
    const release = await this.platform.lock.exclusive(
      this.platform.paths.paths.renderLock,
    );
    try {
      const path = this.platform.paths.paths.stateFile;
      if (!(await this.platform.fs.exists(path))) {
        throw stateError(`no desired state at ${path}`);
      }
      const original = await this.platform.fs.readText(path);
      let raw: unknown;
      try {
        raw = JSON.parse(original);
      } catch {
        throw stateError("state.json is not valid JSON; migration made no changes");
      }
      const migrated = migrateV1ToV2(raw);
      if (!migrated.ok) {
        throw stateError(
          `cannot migrate schema v1 state: ${migrated.errors.join("; ")}`,
          { recovery: "Repair or restore the v1 state. The source file was not changed." },
        );
      }
      // Revalidate serialized v2 before creating the backup or replacing state.json.
      const nextJson = stateToJson(migrated.value);
      loadStateFromJson(nextJson);
      const stamp = this.platform.clock.nowIso().replace(/[:.]/g, "-");
      const backupPath = `${path}.v1-${stamp}.bak`;
      await this.platform.fs.atomicWriteText(backupPath, original, 0o600);
      await this.platform.fs.atomicWriteText(path, nextJson, 0o600);
      return { state: migrated.value, backupPath };
    } finally {
      await release();
    }
  }

  /** Mutate state under exclusive lock. */
  async withExclusive<T>(fn: (state: DesiredState) => Promise<T> | T): Promise<T> {
    const release = await this.platform.lock.exclusive(
      this.platform.paths.paths.renderLock,
    );
    try {
      const state = await this.load();
      return await fn(state);
    } finally {
      await release();
    }
  }

  /** Load under shared lock for read-only operations. */
  async withShared<T>(fn: (state: DesiredState) => Promise<T> | T): Promise<T> {
    const release = await this.platform.lock.shared(
      this.platform.paths.paths.renderLock,
    );
    try {
      const state = await this.load();
      return await fn(state);
    } finally {
      await release();
    }
  }
}

function defaultEnvContent(opts: {
  mysqlRootPassword: string;
  postgresRootPassword: string;
  redisPassword: string;
  projectName: string;
}): string {
  return [
    "# Bento stack environment (operator-owned, sensitive)",
    `MYSQL_ROOT_PASSWORD=${opts.mysqlRootPassword}`,
    `POSTGRES_PASSWORD=${opts.postgresRootPassword}`,
    `REDIS_PASSWORD=${opts.redisPassword}`,
    "TZ=UTC",
    "# Shared native Nginx ACME issuer settings.",
    "ACME_EMAIL=",
    "ACME_URL=https://acme-v02.api.letsencrypt.org/directory",
    "# Enable HTTP/3/QUIC listeners and Alt-Svc headers in generated Nginx vhosts.",
    "HTTP3=false",
    `COMPOSE_PROJECT_NAME=${opts.projectName}`,
    "",
  ].join("\n");
}
