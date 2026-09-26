import { databaseBindings } from "#/domain/state.ts";
import { notFoundError, validationError } from "#/domain/errors.ts";
import { runDatabaseBackup } from "#/services/database_backup.ts";
import type { CliContext } from "#/commands/context.ts";
import type { ArgsWith } from "#/commands/args.ts";
import { bind, type RunState, type YargsBuilder } from "#/commands/shared.ts";

export function registerSqliteCommands(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command("sqlite", "Manage local SQLite databases", (y: YargsBuilder) =>
    y.command(
      "backup <app>",
      "Create a consistent backup of a local SQLite file",
      (cmd: YargsBuilder) =>
        cmd
          .positional("app", { type: "string", demandOption: true })
          .option("file", {
            type: "string",
            alias: "database",
            describe: "SQLite file id when the app has multiple files",
          })
          .option("gzip", { type: "boolean", default: false, describe: "gzip compress" })
          .option("none", { type: "boolean", default: false, describe: "Do not compress" }),
      bind(state, cmdBackup),
    ),
  );
}

async function cmdBackup(argv: ArgsWith<"app">, ctx: CliContext): Promise<number> {
  if (!argv.app) {
    ctx.log.error("usage: bento sqlite backup <app> [--file <sqlite-file-id>]");
    return 2;
  }
  if (argv.gzip === true && argv.none === true) {
    throw validationError("--gzip and --none cannot be used together");
  }

  const state = await ctx.store.load();
  const app = state.apps[argv.app];
  if (!app) throw notFoundError(`app not found: ${argv.app}`);
  const databases = databaseBindings(app, "sqlite");
  if (databases.length === 0) throw validationError(`app ${argv.app} has no SQLite database`);

  const requestedFile = argv.file ?? argv.database;
  const database = requestedFile
    ? databases.find((entry) => entry.file.id === requestedFile)
    : databases.length === 1
      ? databases[0]
      : undefined;
  if (!database) {
    if (requestedFile) throw validationError(`app ${argv.app} has no matching SQLite file ${requestedFile}`);
    throw validationError(`app ${argv.app} has multiple SQLite files; specify --file <sqlite-file-id>`);
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
