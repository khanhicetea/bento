import { join } from "@std/path";
import {
  getBackupScheduleStatus,
  registerBackupSchedule,
  runScheduledBackup,
  unregisterBackupSchedule,
} from "../../services/backup_schedule.ts";
import {
  type DatabaseBackupArtifact,
  runDatabaseBackup,
  runDatabaseRestore,
} from "../../services/database_backup.ts";
import { readRcloneBackupTarget } from "../../services/rclone.ts";
import { syncSqliteBackup } from "../../services/sqlite.ts";
import type { CliContext } from "../context.ts";
import type { ArgsWith, CliArgs } from "../args.ts";
import { bind, type RunState, type YargsBuilder } from "../shared.ts";

export function registerBackupCommands(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser
    .command(
      "backup",
      "Logical database backup",
      (y: YargsBuilder) =>
        y
          .command(
            "$0",
            "Back up managed databases",
            backupOptions,
            bind(state, cmdBackup),
          )
          .command(
            "schedule",
            "Manage unattended logical backups",
            (schedule: YargsBuilder) =>
              schedule
                .command(
                  "register",
                  "Register this stack in the user's crontab",
                  (register: YargsBuilder) =>
                    register
                      .option("schedule", {
                        type: "string",
                        default: "15 3 * * *",
                        describe: "Five-field cron schedule",
                      })
                      .option("bin", {
                        type: "string",
                        demandOption: true,
                        describe: "Absolute path to the executable Bento binary",
                      })
                      .option("rclone-remote", {
                        type: "string",
                        describe: "Configured rclone remote name to upload each new scheduled dump",
                      })
                      .option("rclone-prefix", {
                        type: "string",
                        describe:
                          "Optional path below the rclone remote (requires --rclone-remote)",
                      }),
                  bind(state, cmdScheduleRegister),
                )
                .command(
                  "status",
                  "Show registration and bounded last-run status",
                  (status: YargsBuilder) => status,
                  bind(state, cmdScheduleStatus),
                )
                .command(
                  "unregister",
                  "Remove only this stack's managed crontab block",
                  (unregister: YargsBuilder) => unregister,
                  bind(state, cmdScheduleUnregister),
                )
                .command(
                  "run",
                  "Run the scheduled all-database backup now",
                  (run: YargsBuilder) => run,
                  bind(state, cmdScheduleRun),
                )
                .demandCommand(1, "Choose register, status, unregister, or run"),
          ),
      undefined,
    )
    .command(
      "restore",
      "Logical database restore",
      (y: YargsBuilder) =>
        y
          .option("file", { type: "string", demandOption: true, describe: "Dump path" })
          .option("app", { type: "string", demandOption: true })
          .option("engine", {
            type: "string",
            choices: ["mysql", "postgres"],
            describe: "Target engine when the app has more than one relational binding",
          })
          .option("target", {
            type: "string",
            demandOption: true,
            describe: "Target database name",
            alias: "database",
          })
          .option("replace", {
            type: "string",
            describe: "Exact target name confirmation for replacement",
          }),
      bind(state, cmdRestore),
    );
}

function backupOptions(y: YargsBuilder): YargsBuilder {
  return y
    .option("app", { type: "string", describe: "App slug" })
    .option("database", { type: "string", describe: "Single database" })
    .option("all", {
      type: "boolean",
      default: false,
      describe: "Backup all managed databases",
    })
    .option("gzip", { type: "boolean", default: false, describe: "gzip compress" })
    .option("none", {
      type: "boolean",
      default: false,
      describe: "No compression",
    });
}

async function cmdBackup(argv: CliArgs, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const scope = argv.all === true
    ? "all" as const
    : argv.database
    ? "database" as const
    : "app" as const;
  if (scope !== "all" && !argv.app) {
    ctx.log.error("usage: bento backup --app <app> [--database name] | --all");
    return 2;
  }
  if (argv.gzip === true && argv.none === true) {
    ctx.log.error("--gzip and --none cannot be used together");
    return 2;
  }

  // A database-scoped request may still target a local SQLite file on an app
  // that also owns a Litestream file. Only reject an explicitly selected remote
  // file; app/all scope performs local backups and then syncs remote replicas.
  const litestreamApps = scope === "all"
    ? Object.values(state.apps).filter((app) =>
      app.databases.some((database) => database.engine === "litestream")
    )
    : scope === "database"
    ? argv.app &&
        state.apps[argv.app]?.databases.some((database) =>
          database.engine === "litestream" && database.file.id === argv.database
        )
      ? [state.apps[argv.app]!]
      : []
    : argv.app &&
        state.apps[argv.app]?.databases.some((database) => database.engine === "litestream")
    ? [state.apps[argv.app]!]
    : [];
  if (scope === "database" && litestreamApps.length > 0) {
    ctx.log.error("Litestream has one explicit SQLite file; omit --database");
    return 2;
  }
  const artifacts = await runDatabaseBackup(ctx.platform, state, {
    scope,
    slug: argv.app,
    database: argv.database,
    compress: argv.gzip === true ? "gzip" : argv.none === true ? "none" : "zstd",
  });
  logBackupArtifacts(ctx, artifacts);
  for (const app of litestreamApps) {
    await syncSqliteBackup(ctx.platform, state, app.slug);
    ctx.log.info(`remote sync confirmed for Litestream app ${app.slug}`);
  }
  return 0;
}

async function cmdScheduleRegister(
  argv: ArgsWith<"schedule" | "bin">,
  ctx: CliContext,
): Promise<number> {
  // Loading validates that the selected stack has been initialized before any
  // host crontab process is invoked.
  await ctx.store.load();
  const result = await registerBackupSchedule(ctx.platform, {
    schedule: argv.schedule,
    bentoBin: argv.bin,
    rcloneRemote: argv.rcloneRemote,
    rclonePrefix: argv.rclonePrefix,
  });
  ctx.log.info(
    result.changed ? "backup schedule registered" : "backup schedule already registered",
  );
  const rcloneTarget = await readRcloneBackupTarget(ctx.platform);
  if (rcloneTarget) {
    ctx.log.info(
      `scheduled dumps upload to rclone remote ${rcloneTarget.remote}:${
        rcloneTarget.prefix || "/"
      }`,
    );
  } else {
    warnOnHostOnly(ctx);
  }
  return 0;
}

async function cmdScheduleStatus(_argv: CliArgs, ctx: CliContext): Promise<number> {
  const status = await getBackupScheduleStatus(ctx.platform);
  const backupsDir = ctx.platform.paths.paths.backupsDir;
  const rcloneTarget = await readRcloneBackupTarget(ctx.platform);
  if (ctx.json) {
    ctx.log.out(JSON.stringify({
      ...status,
      onHostBackupsDir: backupsDir,
      rclone: rcloneTarget && { remote: rcloneTarget.remote, prefix: rcloneTarget.prefix },
    }));
  } else {
    ctx.log.out(`registered: ${status.installed ? "yes" : "no"}`);
    ctx.log.out(`schedule: ${status.schedule ?? "not registered"}`);
    ctx.log.out(`last run: ${status.lastRun?.status ?? "none"}`);
    if (status.lastRun) {
      ctx.log.out(`started: ${status.lastRun.startedAt}`);
      ctx.log.out(`finished: ${status.lastRun.finishedAt ?? "unknown/interrupted"}`);
      ctx.log.out(
        `artifacts: ${status.lastRun.artifactCount} (${status.lastRun.artifactBytes} bytes)`,
      );
      if (status.lastRun.error) ctx.log.out(`error: ${status.lastRun.error}`);
    }
    ctx.log.out(`on-host backups: ${backupsDir}`);
    ctx.log.out(
      `rclone upload: ${
        rcloneTarget ? `${rcloneTarget.remote}:${rcloneTarget.prefix || "/"}` : "disabled"
      }`,
    );
  }
  if (!rcloneTarget) warnOnHostOnly(ctx);
  return 0;
}

async function cmdScheduleUnregister(_argv: CliArgs, ctx: CliContext): Promise<number> {
  const result = await unregisterBackupSchedule(ctx.platform);
  ctx.log.info(
    result.changed ? "backup schedule unregistered" : "backup schedule was not registered",
  );
  ctx.log.info("existing dumps and the last-run record were not removed");
  return 0;
}

async function cmdScheduleRun(_argv: CliArgs, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const artifacts = await runScheduledBackup(ctx.platform, state);
  logBackupArtifacts(ctx, artifacts);
  return 0;
}

function logBackupArtifacts(ctx: CliContext, artifacts: DatabaseBackupArtifact[]): void {
  for (const artifact of artifacts) {
    ctx.log.info(
      `backup ${artifact.database} -> ${artifact.path} (${artifact.bytes} bytes)`,
    );
  }
}

function warnOnHostOnly(ctx: CliContext): void {
  ctx.log.warn(
    `logical dumps remain only on this host under ${
      join(ctx.stackRoot, "backups")
    }; Bento does not create an off-host copy`,
  );
}

async function cmdRestore(
  argv: ArgsWith<"file" | "app" | "target" | "engine">,
  ctx: CliContext,
): Promise<number> {
  const { file, app, target } = argv;
  if (argv.replace && argv.replace !== target) {
    ctx.log.error("replace confirmation must exactly match target database name");
    return 10;
  }
  ctx.log.warn(
    "restore is not object-level atomic; a failed import can leave a partial destination",
  );
  await ctx.store.withExclusive(async (state) => {
    const next = await runDatabaseRestore(ctx.platform, state, {
      file,
      slug: app,
      targetDatabase: target,
      replaceOriginal: argv.replace,
      engine: argv.engine as "mysql" | "postgres" | undefined,
    });
    if (next !== state) await ctx.store.save(next);
    return next;
  });
  ctx.log.info(`restore completed into ${target}`);
  return 0;
}
