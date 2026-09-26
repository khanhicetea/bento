/** Load and save validated desired state in the private stack SQLite database. */

import type { DesiredState } from "#/domain/state.ts";
import { createEmptyState } from "#/domain/state.ts";
import type { Platform } from "#/platform/mod.ts";
import { safetyError } from "#/domain/errors.ts";
import {
  loadStateDatabase,
  migrateStateDatabase,
  saveStateDatabase,
  stateDatabaseInitialized,
  type MigrationResult,
} from "#/services/state_database.ts";
import {
  DEFAULT_COMPOSE_PROJECT_NAME,
  parseDotEnv,
  validateComposeProjectName,
} from "#/services/stack_env.ts";
import { initializeRcloneConfig } from "#/services/rclone.ts";

export type StackInitOptions = {
  /** Stable stack identity; deliberately independent from the stack directory. */
  projectName?: string;
};

export class StateStore {
  constructor(private readonly platform: Platform) {}

  async exists(): Promise<boolean> {
    return await stateDatabaseInitialized(this.platform);
  }

  /** Apply all pending numbered database schema migrations under the stack lock. */
  async migrate(): Promise<MigrationResult> {
    await this.platform.fs.mkdirp(this.platform.paths.paths.root, 0o700);
    await this.platform.fs.mkdirp(this.platform.paths.paths.lockDir, 0o700);
    const release = await this.platform.lock.exclusive(this.platform.paths.paths.renderLock);
    try {
      return await migrateStateDatabase(this.platform);
    } finally {
      await release();
    }
  }

  /** Read and validate desired state without mutating the database. */
  async load(): Promise<DesiredState> {
    return await loadStateDatabase(this.platform);
  }

  async save(state: DesiredState): Promise<void> {
    const next = {
      ...state,
      updatedAt: this.platform.clock.nowIso(),
    };
    await saveStateDatabase(this.platform, next);
  }

  /** Initialize a new stack exactly once; existing desired state is never overwritten. */
  async init(options: StackInitOptions = {}): Promise<DesiredState> {
    const path = this.platform.paths.paths.stateDb;
    await this.platform.fs.mkdirp(this.platform.paths.paths.root, 0o700);
    await this.platform.fs.mkdirp(this.platform.paths.paths.lockDir, 0o700);
    const release = await this.platform.lock.exclusive(this.platform.paths.paths.renderLock);
    try {
      await migrateStateDatabase(this.platform);
      if (await stateDatabaseInitialized(this.platform)) {
        throw safetyError(
          `stack is already initialized at ${path}`,
          "Continue using the existing stack. To create a replacement, export or back up its data first, then initialize a different empty stack root.",
        );
      }

      let requestedProject = options.projectName;
      if (
        requestedProject === undefined &&
        (await this.platform.fs.exists(this.platform.paths.paths.envFile))
      ) {
        const existingEnv = parseDotEnv(
          await this.platform.fs.readText(this.platform.paths.paths.envFile),
        );
        requestedProject = existingEnv.COMPOSE_PROJECT_NAME?.trim() || undefined;
      }
      const projectName = validateComposeProjectName(
        requestedProject ?? DEFAULT_COMPOSE_PROJECT_NAME,
      );
      const state = createEmptyState(this.platform.clock.nowIso());
      await this.platform.fs.mkdirp(this.platform.paths.paths.generatedDir);
      await this.platform.fs.mkdirp(this.platform.paths.paths.overlaysDir);
      await this.platform.fs.mkdirp(this.platform.paths.paths.customDir);
      await this.platform.fs.mkdirp(this.platform.paths.paths.backupsDir);
      await initializeRcloneConfig(this.platform);
      await this.platform.fs.mkdirp(this.platform.paths.paths.certsDir);
      await this.platform.fs.mkdirp(this.platform.paths.paths.homesDir);
      await this.save(state);
      // Seed stack secrets once. Reconciliation fills missing/empty secrets while
      // preserving every existing non-empty value byte-for-byte.
      await this.reconcileStackEnv(projectName, options.projectName !== undefined);
      return state;
    } finally {
      await release();
    }
  }

  /** Add missing stack settings/secrets without replacing existing non-empty values. */
  async reconcileStackEnv(
    projectName = DEFAULT_COMPOSE_PROJECT_NAME,
    initializeTopology = false,
  ): Promise<void> {
    projectName = validateComposeProjectName(projectName);
    const envPath = this.platform.paths.paths.envFile;
    if (!(await this.platform.fs.exists(envPath))) {
      await this.platform.fs.atomicWriteText(
        envPath,
        defaultEnvContent({
          mysqlRootPassword: this.platform.random.hex(24),
          postgresRootPassword: this.platform.random.hex(24),
          redisPassword: this.platform.random.hex(24),
          projectName,
        }),
        0o600,
      );
      return;
    }

    const existing = await this.platform.fs.readText(envPath);
    const env = parseDotEnv(existing);
    const configuredProject = env.COMPOSE_PROJECT_NAME?.trim();
    if (configuredProject && configuredProject !== projectName) {
      throw safetyError(
        `stack is already named '${configuredProject}', refusing to rename it to '${projectName}'`,
        "Stack names prefix Docker volumes and are immutable after initialization. Use the existing name or import into a new stack with --name.",
      );
    }
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
    if (initializeTopology) {
      if (!configuredProject) missing.push(`COMPOSE_PROJECT_NAME=${projectName}`);
      if (env.NGINX_HOST_NETWORK === undefined) missing.push("NGINX_HOST_NETWORK=1");
      if (env.NGINX_HTTP_PORT === undefined) missing.push("NGINX_HTTP_PORT=");
      if (env.NGINX_HTTPS_PORT === undefined) missing.push("NGINX_HTTPS_PORT=");
    }
    if (missing.length === 0) return;

    const separator = existing.length === 0 || existing.endsWith("\n") ? "" : "\n";
    await this.platform.fs.atomicWriteText(
      envPath,
      `${existing}${separator}${missing.join("\n")}\n`,
      0o600,
    );
  }

  /** Mutate state under exclusive lock. */
  async withExclusive<T>(fn: (state: DesiredState) => Promise<T> | T): Promise<T> {
    const release = await this.platform.lock.exclusive(this.platform.paths.paths.renderLock);
    try {
      const state = await this.load();
      return await fn(state);
    } finally {
      await release();
    }
  }

  /** Load under shared lock for read-only operations. */
  async withShared<T>(fn: (state: DesiredState) => Promise<T> | T): Promise<T> {
    const release = await this.platform.lock.shared(this.platform.paths.paths.renderLock);
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
    "# Host networking is the default. Set to 0 for a stack-private Nginx network.",
    "NGINX_HOST_NETWORK=1",
    "# Bridge-mode host publications; blank means internal-only (overlays may publish instead).",
    "NGINX_HTTP_PORT=",
    "NGINX_HTTPS_PORT=",
    "# Stable stack name used to prefix Compose containers, networks, and volumes.",
    `COMPOSE_PROJECT_NAME=${opts.projectName}`,
    "",
  ].join("\n");
}
