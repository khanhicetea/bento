/** Local SQLite maintenance and logical backup through the PHP runner image. */

import { join } from "@std/path";
import type { DesiredState } from "../domain/state.ts";
import { databaseBindings } from "../domain/state.ts";
import { notFoundError, serviceError, validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import { composeArgs } from "./compose.ts";
import { sqliteContainerPath } from "./sqlite_paths.ts";

export type SqliteBackupArtifact = {
  engine: "sqlite";
  path: string;
  database: string;
  service: string;
  bytes: number;
};

export async function runSqliteBackup(
  platform: Platform,
  state: DesiredState,
  slug: string,
  compress: "zstd" | "gzip" | "none" = "zstd",
  fileId?: string,
): Promise<SqliteBackupArtifact> {
  const app = state.apps[slug];
  if (!app) throw notFoundError(`app not found: ${slug}`);
  const databases = databaseBindings(app, "sqlite");
  const database = databases.find((entry) => !fileId || entry.file.id === fileId);
  if (!database) throw validationError(`app ${slug} has no matching plain SQLite database`);

  const timestamp = platform.clock.nowIso().replace(/[:.]/g, "-");
  const extension = compress === "none"
    ? "sqlite"
    : compress === "gzip"
    ? "sqlite.gz"
    : "sqlite.zst";
  const name = `${database.file.id}_${timestamp}.${extension}`;
  const directory = join(platform.paths.paths.backupsDir, "sqlite", slug);
  const finalPath = join(directory, name);
  const partialPath = `${finalPath}.partial`;
  const containerDirectory = `/var/backups/bento/sqlite/${slug}`;
  const containerFinal = `${containerDirectory}/${name}`;
  const containerPartial = `${containerFinal}.partial`;
  const raw = `${containerDirectory}/.${name}.raw`;
  const source = sqliteContainerPath(database.file.id, app.slug, "sqlite");
  await platform.fs.mkdirp(directory, 0o700);

  // .backup creates a real SQLite file, so keep that snapshot in the mounted
  // backup directory and compress it there. Avoid shell pipelines: the runner
  // invokes POSIX `sh`, which does not support `pipefail` on every image.
  const backup = `sqlite3 ${shellQuote(source)} ${shellQuote(".timeout 30000")} ${
    shellQuote(`.backup '${raw}'`)
  }`;
  const publish = compress === "gzip"
    ? 'gzip -c "$RAW" > "$PARTIAL"'
    : compress === "zstd"
    ? 'zstd -3 -q -c "$RAW" > "$PARTIAL"'
    : 'cat "$RAW" > "$PARTIAL"';
  const script = [
    "set -eu",
    "umask 077",
    `test -f ${shellQuote(source)} || { echo 'SQLite database file is missing' >&2; exit 1; }`,
    `RAW=${shellQuote(raw)}`,
    `PARTIAL=${shellQuote(containerPartial)}`,
    `FINAL=${shellQuote(containerFinal)}`,
    'trap \'rm -f "$RAW" "$PARTIAL"\' EXIT',
    backup,
    'test -s "$RAW"',
    publish,
    'test -s "$PARTIAL"',
    'chmod 600 "$PARTIAL"',
    'mv -f "$PARTIAL" "$FINAL"',
    'rm -f "$RAW"',
    "trap - EXIT",
  ].join("\n");

  const result = await platform.process.run(
    await composeArgs(platform, state, [
      "exec",
      "-T",
      `${app.phpService}-runner`,
      "sh",
      "-c",
      script,
    ]),
    { cwd: platform.paths.paths.root, timeoutMs: 30 * 60_000 },
  );
  if (result.code !== 0) {
    await platform.fs.remove(partialPath).catch(() => {});
    throw serviceError(
      `SQLite backup failed for ${slug}: ${(result.stderr || result.stdout).trim()}`,
    );
  }
  const stat = await platform.fs.stat(finalPath).catch(() => null);
  if (!stat?.isFile || stat.size === 0) {
    throw serviceError(`SQLite backup for ${slug} did not produce a non-empty artifact`);
  }
  return {
    engine: "sqlite",
    path: finalPath,
    database: database.file.id,
    service: "sqlite",
    bytes: stat.size,
  };
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}
