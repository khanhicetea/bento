import { databaseBindings } from "../../domain/state.ts";
import { notFoundError, validationError } from "../../domain/errors.ts";
import { runDatabaseBackup } from "../../services/database_backup.ts";
import type { CliContext } from "../context.ts";
import type { ArgsWith } from "../args.ts";
import { bind, type RunState, type YargsBuilder } from "../shared.ts";
import {
  enableSqliteBackup,
  exportSqliteBackup,
  getSqliteBackupStatus,
  requireSqliteApp,
  sqliteCompose,
  syncSqliteBackup,
  verifySqliteBackup,
} from "../../services/sqlite.ts";

export function registerSqliteCommands(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command(
    "sqlite",
    "Manage local and continuously replicated SQLite databases",
    (y: YargsBuilder) =>
      y.command(
        "backup",
        "Manage local logical backups and stack-wide Litestream backup",
        (backup: YargsBuilder) =>
          backup
            .command(
              "local [app]",
              "Create a consistent backup of a plain local SQLite file",
              (cmd: YargsBuilder) =>
                cmd
                  .positional("app", { type: "string" })
                  .option("app", { type: "string", describe: "App slug" })
                  .option("file", {
                    type: "string",
                    alias: "database",
                    describe: "SQLite file id when the app has multiple local files",
                  })
                  .option("gzip", { type: "boolean", default: false, describe: "gzip compress" })
                  .option("none", {
                    type: "boolean",
                    default: false,
                    describe: "Do not compress",
                  }),
              bind(state, cmdLocal),
            )
            .command(
              "enable <app>",
              "Enable the directory watcher and prove one app's S3 replica",
              (cmd: YargsBuilder) =>
                cmd
                  .positional("app", { type: "string", demandOption: true })
                  .option("destination", { type: "string", default: "primary-s3" })
                  .option("rpo", { type: "string", default: "60s" })
                  .option("retention", { type: "string", default: "168h" }),
              bind(state, cmdEnable),
            )
            .command(
              "status",
              "Show stack watcher and app replication status",
              (cmd: YargsBuilder) => cmd.option("app", { type: "string", demandOption: true }),
              bind(state, cmdStatus),
            )
            .command(
              "sync",
              "Force and wait for remote replication",
              (cmd: YargsBuilder) => cmd.option("app", { type: "string", demandOption: true }),
              bind(state, cmdSync),
            )
            .command(
              "verify",
              "Restore a temporary copy and run a full integrity check",
              (cmd: YargsBuilder) => cmd.option("app", { type: "string", demandOption: true }),
              bind(state, cmdVerify),
            )
            .command(
              "export",
              "Export an S3 replica to a new local SQLite database file",
              (cmd: YargsBuilder) =>
                cmd
                  .option("app", { type: "string", demandOption: true })
                  .option("output", { type: "string", demandOption: true }),
              bind(state, cmdExport),
            )
            .demandCommand(1, "Choose local, enable, status, sync, verify, or export"),
        undefined,
      ).demandCommand(1, "Choose backup"),
  );
}

async function cmdLocal(argv: ArgsWith<"app">, ctx: CliContext): Promise<number> {
  if (!argv.app) {
    ctx.log.error("usage: bento sqlite backup local <app> [--file <sqlite-file-id>]");
    return 2;
  }
  if (argv.gzip === true && argv.none === true) {
    throw validationError("--gzip and --none cannot be used together");
  }

  const state = await ctx.store.load();
  const app = state.apps[argv.app];
  if (!app) throw notFoundError(`app not found: ${argv.app}`);
  const databases = databaseBindings(app, "sqlite");
  if (databases.length === 0) {
    throw validationError(`app ${argv.app} has no plain SQLite database`);
  }

  const requestedFile = argv.file ?? argv.database;
  const database = requestedFile
    ? databases.find((entry) => entry.file.id === requestedFile)
    : databases.length === 1
    ? databases[0]
    : undefined;
  if (!database) {
    if (requestedFile) {
      throw validationError(`app ${argv.app} has no matching plain SQLite file ${requestedFile}`);
    }
    throw validationError(
      `app ${argv.app} has multiple plain SQLite files; specify --file <sqlite-file-id>`,
    );
  }

  const artifacts = await runDatabaseBackup(ctx.platform, state, {
    scope: "database",
    slug: argv.app,
    database: database.file.id,
    compress: argv.gzip === true ? "gzip" : argv.none === true ? "none" : "zstd",
  });
  for (const artifact of artifacts) {
    ctx.log.info(`backup ${artifact.database} -> ${artifact.path} (${artifact.bytes} bytes)`);
  }
  return 0;
}

async function cmdEnable(argv: ArgsWith<"app">, ctx: CliContext): Promise<number> {
  if (argv.destination && argv.destination !== "primary-s3") {
    ctx.log.error("phase 1 supports only destination primary-s3");
    return 2;
  }
  const requestedRpo = argv.rpo ?? "60s";
  const requestedRetention = argv.retention ?? "168h";
  const next = await ctx.store.withExclusive(async (current) => {
    const changed = await enableSqliteBackup(
      ctx.platform,
      current,
      argv.app,
      requestedRpo,
      requestedRetention,
    );
    await ctx.store.save(changed);
    await ctx.render.apply(changed, { alreadyLocked: true, skipValidate: false });
    return changed;
  });

  // Directory policy is loaded at process startup. A graceful recreation final-syncs
  // an existing daemon and avoids runtime registration or per-database reconciliation.
  const up = await sqliteCompose(ctx.platform, next, [
    "up",
    "-d",
    "--force-recreate",
    "litestream",
  ]);
  if (up.code !== 0) throw new Error(`Litestream container failed to start: ${up.stderr.trim()}`);

  const proof = await verifySqliteBackup(ctx.platform, next, argv.app);
  await ctx.store.withExclusive(async (current) => {
    const { database } = requireSqliteApp(current, argv.app);
    database.backupVerifiedAt = ctx.platform.clock.nowIso();
    await ctx.store.save(current);
  });
  ctx.log.info(
    `stack-wide SQLite backup enabled; ${argv.app} upload and full restore verified`,
  );
  if (proof) ctx.log.info(proof);
  return 0;
}

async function cmdStatus(argv: ArgsWith<"app">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const status = await getSqliteBackupStatus(ctx.platform, state, argv.app);
  const report = {
    ...status,
    scope: "stack-wide directory watcher",
    destination: status.configured
      ? "s3://<redacted>/bento/<stack>/<sqlite-file-id>/<app-slug>.sqlite"
      : undefined,
  };
  ctx.log.out(
    ctx.json
      ? JSON.stringify(report)
      : Object.entries(report).map(([k, v]) => `${k}: ${v ?? "never"}`).join("\n"),
  );
  return status.configured && status.containerRunning && status.replicationStatus === "replicating"
    ? 0
    : 8;
}

async function cmdSync(argv: ArgsWith<"app">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const detail = await syncSqliteBackup(ctx.platform, state, argv.app);
  ctx.log.info(`remote sync confirmed for ${argv.app}${detail ? `: ${detail}` : ""}`);
  return 0;
}

async function cmdExport(argv: ArgsWith<"app" | "output">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const output = await exportSqliteBackup(ctx.platform, state, argv.app, argv.output);
  ctx.log.info(`exported ${argv.app} S3 replica to ${output}`);
  return 0;
}

async function cmdVerify(argv: ArgsWith<"app">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const detail = await verifySqliteBackup(ctx.platform, state, argv.app);
  await ctx.store.withExclusive(async (current) => {
    const { database } = requireSqliteApp(current, argv.app);
    database.backupVerifiedAt = ctx.platform.clock.nowIso();
    await ctx.store.save(current);
  });
  ctx.log.info(`full restore verification succeeded for ${argv.app}${detail ? `: ${detail}` : ""}`);
  return 0;
}
