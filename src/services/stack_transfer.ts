import { isAbsolute, join, relative, resolve } from "@std/path";
import type { DesiredState } from "../domain/state.ts";
import { conflictError, platformError, safetyError, validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import { composeArgs } from "./compose.ts";
import { loadStackEnv, updateStackEnv, validateComposeProjectName } from "./stack_env.ts";

export const STACK_ARCHIVE = "stack.tar.gz";
export const REDIS_ARCHIVE = "redis-data.tar.gz";
const VOLUME_HELPER_IMAGE = "ubuntu:24.04";

export type StackExportResult = {
  directory: string;
  files: string[];
};

export type StackImportResult = {
  directory: string;
  volumes: string[];
};

export type StackImportOptions = {
  projectName?: string;
  nginxHostNetwork?: boolean;
  /** Undefined preserves the archive; null clears the publication. */
  httpPort?: number | null;
  /** Undefined preserves the archive; null clears the publication. */
  httpsPort?: number | null;
};

export type DatabaseTransferVolume = {
  kind: "database";
  engine: "mysql" | "postgres";
  service: string;
  logical: string;
  docker: string;
};

export type RedisTransferVolume = {
  kind: "redis";
  service: "redis";
  logical: "redis-data";
  docker: string;
};

export type StackTransferVolumes = {
  databases: DatabaseTransferVolume[];
  redis: RedisTransferVolume;
};

export function composeProjectName(env: Record<string, string>): string {
  try {
    return validateComposeProjectName(env.COMPOSE_PROJECT_NAME?.trim() || "bento");
  } catch {
    throw validationError(
      `invalid COMPOSE_PROJECT_NAME for volume transfer: ${env.COMPOSE_PROJECT_NAME ?? "bento"}`,
    );
  }
}

export function volumeArchiveName(logicalVolume: string): string {
  if (!/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(logicalVolume)) {
    throw validationError(`invalid Docker volume name for transfer: ${logicalVolume}`);
  }
  return `${logicalVolume}.tar.gz`;
}

export function stackVolumeNames(
  state: DesiredState,
  project: string,
): StackTransferVolumes {
  const databases: DatabaseTransferVolume[] = [...state.databaseServices]
    .sort((a, b) => a.service.localeCompare(b.service))
    .map((entry) => ({
      kind: "database",
      engine: entry.engine,
      service: entry.service,
      logical: entry.volume,
      docker: `${project}_${entry.volume}`,
    }));
  const redis: RedisTransferVolume = {
    kind: "redis",
    service: "redis",
    logical: "redis-data",
    docker: `${project}_redis-data`,
  };
  assertUniqueVolumeArchives([...databases, redis]);
  return { databases, redis };
}

/** Data services are stopped/restarted in this deterministic order for raw copies. */
export function stackDataServiceNames(state: DesiredState): string[] {
  return [
    ...[...state.databaseServices]
      .sort((a, b) => a.service.localeCompare(b.service))
      .map((entry) => String(entry.service)),
    "redis",
  ];
}

/**
 * Export the bind-mounted stack and raw, cleanly-stopped database volumes.
 * The destination must be outside the stack and empty. Each durable volume gets
 * its own archive named after its logical Compose volume.
 */
export async function exportStack(
  platform: Platform,
  state: DesiredState,
  destination: string,
): Promise<StackExportResult> {
  const root = resolve(platform.paths.paths.root);
  const output = resolve(destination);
  assertSeparatePath(root, output, "export directory must be outside the stack root");
  await ensureEmptyDirectory(platform, output);

  const env = await loadStackEnv(platform);
  const project = composeProjectName(env);
  const volumes = stackVolumeNames(state, project);
  const allTransferVolumes = [...volumes.databases, volumes.redis];
  const allVolumes = allTransferVolumes.map((volume) => volume.docker);

  await requireCommand(platform, ["docker", "info"], "Docker is unavailable");
  for (const volume of allVolumes) {
    await requireCommand(
      platform,
      ["docker", "volume", "inspect", volume],
      `required Docker volume does not exist: ${volume}`,
    );
  }

  const dataServices = stackDataServiceNames(state);
  const ps = await runCompose(platform, state, ["ps", "--services", "--filter", "status=running"]);
  const running = new Set(ps.stdout.split(/\r?\n/).map((v) => v.trim()).filter(Boolean));
  const restart = dataServices.filter((service) => running.has(service));
  const archiveNames = [
    STACK_ARCHIVE,
    ...volumes.databases.map((volume) => volumeArchiveName(volume.logical)),
    REDIS_ARCHIVE,
  ];
  const partials = archiveNames.map((name) => join(output, `.${name}.partial`));

  let primaryError: unknown;
  let restartFailure: unknown;
  try {
    if (restart.length > 0) await runCompose(platform, state, ["stop", ...restart]);

    for (const volume of volumes.databases) {
      const archive = volumeArchiveName(volume.logical);
      await archiveVolume(platform, output, `.${archive}.partial`, volume);
    }
    await archiveVolume(platform, output, `.${REDIS_ARCHIVE}.partial`, volumes.redis);
    await requireCommand(
      platform,
      [
        "tar",
        "--xattrs",
        "--acls",
        "--numeric-owner",
        "--exclude=./runtime",
        "--exclude=./locks",
        "--exclude=./.asset-cache",
        "-czpf",
        partials[0]!,
        "-C",
        root,
        ".",
      ],
      "failed to archive stack directory",
    );

    for (const [index, finalName] of archiveNames.entries()) {
      await platform.fs.rename(partials[index]!, join(output, finalName));
    }
  } catch (err) {
    primaryError = err;
    for (
      const name of [
        ...partials,
        ...archiveNames.map((archive) => join(output, archive)),
      ]
    ) {
      if (await platform.fs.exists(name)) await platform.fs.remove(name);
    }
  } finally {
    if (restart.length > 0) {
      try {
        await runCompose(platform, state, ["start", ...restart]);
      } catch (restartError) {
        restartFailure = restartError;
      }
    }
  }
  if (primaryError !== undefined) throw primaryError;
  if (restartFailure !== undefined) throw restartFailure;

  return {
    directory: output,
    files: archiveNames.map((name) => join(output, name)),
  };
}

/** Restore into an empty stack root and newly-created Docker volumes, then start the stack. */
export async function importStack(
  platform: Platform,
  source: string,
  options: StackImportOptions = {},
): Promise<StackImportResult> {
  if (options.projectName !== undefined) validateComposeProjectName(options.projectName);
  for (
    const [name, port] of [["httpPort", options.httpPort], [
      "httpsPort",
      options.httpsPort,
    ]] as const
  ) {
    if (
      port !== undefined && port !== null &&
      (!Number.isInteger(port) || port < 1 || port > 65535)
    ) {
      throw validationError(`${name} must be between 1 and 65535`);
    }
  }
  const input = resolve(source);
  const root = resolve(platform.paths.paths.root);
  assertSeparatePath(root, input, "import directory must be outside the destination stack root");

  const stackArchive = join(input, STACK_ARCHIVE);
  await requireArchiveFile(platform, stackArchive);
  await validateTarArchive(platform, stackArchive);
  await ensureEmptyDirectory(platform, root);
  await requireCommand(platform, ["docker", "info"], "Docker is unavailable");

  await requireCommand(
    platform,
    [
      "tar",
      "--xattrs",
      "--acls",
      "--numeric-owner",
      "-xzpf",
      stackArchive,
      "-C",
      root,
    ],
    "failed to restore stack directory",
  );

  // Validate imported desired state and derive volume identities from imported .env.
  const { StateStore } = await import("./state_store.ts");
  const state = await new StateStore(platform).load();
  const envUpdates: Record<string, string> = {};
  if (options.projectName !== undefined) {
    envUpdates.COMPOSE_PROJECT_NAME = validateComposeProjectName(options.projectName);
  }
  if (options.nginxHostNetwork !== undefined) {
    envUpdates.NGINX_HOST_NETWORK = options.nginxHostNetwork ? "1" : "0";
  }
  if (options.httpPort !== undefined) {
    envUpdates.NGINX_HTTP_PORT = options.httpPort === null ? "" : String(options.httpPort);
  }
  if (options.httpsPort !== undefined) {
    envUpdates.NGINX_HTTPS_PORT = options.httpsPort === null ? "" : String(options.httpsPort);
  }
  if (Object.keys(envUpdates).length > 0) await updateStackEnv(platform, envUpdates);
  const project = composeProjectName(await loadStackEnv(platform));
  const volumes = stackVolumeNames(state, project);
  const allVolumes = [...volumes.databases, volumes.redis];
  const volumeArchives = allVolumes.map((volume) => ({
    volume,
    archive: volumeArchiveName(volume.logical),
  }));
  // Redis has a stable explicit filename; this assertion protects format drift.
  if (volumeArchiveName(volumes.redis.logical) !== REDIS_ARCHIVE) {
    throw validationError("Redis volume archive naming is inconsistent");
  }
  const expectedArchives = new Set([STACK_ARCHIVE, ...volumeArchives.map((v) => v.archive)]);
  await refuseUnexpectedImportArchives(platform, input, expectedArchives);
  for (const entry of volumeArchives) {
    const archive = join(input, entry.archive);
    await requireArchiveFile(platform, archive);
    await validateTarArchive(platform, archive);
  }

  const created: string[] = [];
  try {
    for (const volume of allVolumes) {
      const inspect = await platform.process.run(["docker", "volume", "inspect", volume.docker]);
      if (inspect.code === 0) {
        throw conflictError(
          `destination Docker volume already exists: ${volume.docker}`,
          "Import onto a clean Docker project or remove the conflicting empty volume manually.",
        );
      }
      await requireCommand(
        platform,
        ["docker", "volume", "create", volume.docker],
        `failed to create Docker volume: ${volume.docker}`,
      );
      created.push(volume.docker);
    }

    for (const entry of volumeArchives) {
      await restoreVolume(platform, input, entry.archive, entry.volume);
    }

    // Regenerate with the importing Bento version, then build/start the complete chain.
    const { RenderService } = await import("./render.ts");
    await new RenderService(platform).apply(state, { renderOnly: true, skipValidate: true });
    await runCompose(platform, state, ["up", "-d", "--build"]);
  } catch (err) {
    // Only volumes created by this failed import are eligible for cleanup.
    for (const volume of created.reverse()) {
      await platform.process.run(["docker", "volume", "rm", volume]);
    }
    throw err;
  }

  return { directory: root, volumes: allVolumes.map((v) => v.docker) };
}

async function archiveVolume(
  platform: Platform,
  output: string,
  filename: string,
  volume: { logical: string; docker: string },
): Promise<void> {
  const command = [
    "docker",
    "run",
    "--rm",
    "-v",
    `${volume.docker}:/volume:ro`,
    "-v",
    `${output}:/backup`,
    VOLUME_HELPER_IMAGE,
    "tar",
    "--xattrs",
    "--acls",
    "--numeric-owner",
    "-czpf",
    `/backup/${filename}`,
    "-C",
    "/volume",
    ".",
  ];
  await requireCommand(platform, command, `failed to archive Docker volume ${volume.docker}`);
}

async function restoreVolume(
  platform: Platform,
  input: string,
  filename: string,
  volume: { logical: string; docker: string },
): Promise<void> {
  const command = [
    "docker",
    "run",
    "--rm",
    "-v",
    `${volume.docker}:/volume`,
    "-v",
    `${input}:/backup:ro`,
    VOLUME_HELPER_IMAGE,
    "tar",
    "--xattrs",
    "--acls",
    "--numeric-owner",
    "-xzpf",
    `/backup/${filename}`,
    "-C",
    "/volume",
  ];
  await requireCommand(platform, command, `failed to restore Docker volume ${volume.docker}`);
}

function assertUniqueVolumeArchives(
  volumes: Array<{ logical: string; docker: string }>,
): void {
  const logical = new Set<string>();
  const docker = new Set<string>();
  const archives = new Set<string>();
  for (const volume of volumes) {
    const archive = volumeArchiveName(volume.logical);
    if (logical.has(volume.logical) || docker.has(volume.docker) || archives.has(archive)) {
      throw validationError(`duplicate Docker volume identity for transfer: ${volume.logical}`);
    }
    logical.add(volume.logical);
    docker.add(volume.docker);
    archives.add(archive);
  }
}

async function refuseUnexpectedImportArchives(
  platform: Platform,
  input: string,
  expected: Set<string>,
): Promise<void> {
  for (const name of await platform.fs.readDir(input)) {
    const archiveLike = /\.(?:tar(?:\.(?:gz|zst|xz))?|tgz|zip)$/i.test(name);
    if (archiveLike && !expected.has(name)) {
      throw validationError(`unexpected archive in stack import directory: ${name}`);
    }
  }
}

async function requireArchiveFile(platform: Platform, path: string): Promise<void> {
  if (!(await platform.fs.exists(path)) || !(await platform.fs.stat(path)).isFile) {
    throw validationError(`import archive is missing: ${path}`);
  }
}

async function validateTarArchive(platform: Platform, path: string): Promise<void> {
  const result = await platform.process.run(["tar", "-tzf", path]);
  if (result.code !== 0) throw validationError(`invalid or corrupt archive: ${path}`);
  for (const entry of result.stdout.split(/\r?\n/).filter(Boolean)) {
    const normalized = entry.replace(/^\.\//, "");
    if (isAbsolute(normalized) || normalized.split("/").includes("..")) {
      throw safetyError(`archive contains unsafe path: ${entry}`);
    }
  }
}

async function ensureEmptyDirectory(platform: Platform, path: string): Promise<void> {
  if (await platform.fs.exists(path)) {
    const stat = await platform.fs.stat(path);
    if (!stat.isDirectory) throw conflictError(`path is not a directory: ${path}`);
    const entries = await platform.fs.readDir(path);
    if (entries.length > 0) throw conflictError(`directory must be empty: ${path}`);
  } else {
    await platform.fs.mkdirp(path, 0o700);
  }
}

function assertSeparatePath(root: string, other: string, message: string): void {
  const rel = relative(root, other);
  if (rel === "" || (!rel.startsWith("..") && !isAbsolute(rel))) throw safetyError(message);
  // Also reject a source directory that contains the stack destination.
  const inverse = relative(other, root);
  if (inverse === "" || (!inverse.startsWith("..") && !isAbsolute(inverse))) {
    throw safetyError(message);
  }
}

async function runCompose(platform: Platform, state: DesiredState, args: string[]) {
  const command = await composeArgs(platform, state, args);
  return await requireCommand(platform, command, `docker compose ${args.join(" ")} failed`);
}

async function requireCommand(platform: Platform, command: string[], message: string) {
  const result = await platform.process.run(command);
  if (result.code !== 0) {
    const detail = result.stderr.trim() || result.stdout.trim();
    throw platformError(`${message}${detail ? `: ${detail}` : ""}`);
  }
  return result;
}
