import { safetyError, validationError } from "../../domain/errors.ts";
import type { AppState, ProcessLanguage } from "../../domain/state.ts";
import { isPhpApp, isProcessApp } from "../../domain/state.ts";
import {
  applyAppDataPlane,
  capacityWarnings,
  deleteApp,
  materializeAppHome,
  provisionApp,
  setAppEnabled,
} from "../../services/app.ts";
import { loadRedisPassword } from "../../services/stack_env.ts";
import { composeArgs } from "../../services/compose.ts";
import { emptyReloadPlan } from "../../domain/reload.ts";
import {
  isProcessAppHealthy,
  recreateRunningProcessApp,
  removeProcessAppContainer,
  startProcessApp,
  stopProcessApp,
} from "../../services/process_app.ts";
import { sqliteContainerPath } from "../../services/sqlite_paths.ts";
import { executeAppPrune, planAppPrune, writeAppPruneManifest } from "../../services/app_prune.ts";
import { printTable } from "../../ui/output.ts";
import type { CliContext } from "../context.ts";
import type { ArgsWith, CliArgs } from "../args.ts";
import { bind, noApplyOption, type RunState, wantsNoApply, type YargsBuilder } from "../shared.ts";
import { runCliExec } from "./exec.ts";

export function registerAppCommands(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command("app", "Provision and inspect applications", (y: YargsBuilder) =>
    y
      .command("list", "List applications", () => {}, bind(state, cmdAppList))
      .command(
        "show <slug>",
        "Show one application (secrets redacted)",
        (y2: YargsBuilder) => y2.positional("slug", { type: "string", demandOption: true }),
        bind(state, cmdAppShow),
      )
      .command(
        "create <slug>",
        "Create or update an application",
        (y2: YargsBuilder) =>
          y2
            .positional("slug", { type: "string", demandOption: true })
            .option("domain", {
              type: "string",
              demandOption: true,
              describe: "Primary domain",
            })
            .option("alias", {
              type: "string",
              describe: "Comma-separated domain aliases",
            })
            .option("docroot", {
              type: "string",
              describe: "Document root relative to app home",
            })
            .option("runtime", {
              type: "string",
              choices: ["php", "node", "bun", "python"],
              default: "php",
              describe: "Application runtime kind",
            })
            .option("runtime-version", {
              type: "string",
              describe: "Exact Node.js, Bun, or Python runtime version",
            })
            .option("start", {
              type: "string",
              array: true,
              describe: "Process app start argv (repeat values after --start)",
            })
            .option("port", {
              type: "number",
              describe: "Private process HTTP port inside the app container",
            })
            .option("health-path", {
              type: "string",
              describe: "Optional HTTP health path; TCP readiness is the default",
            })
            .option("workdir", {
              type: "string",
              describe: "Process command working directory inside the app home",
            })
            .option("php", { type: "string", describe: "PHP version" })
            .option("fpm", {
              type: "string",
              describe: "FPM capacity profile",
            })
            .option("database-engine", {
              type: "string",
              choices: ["mysql", "postgres", "sqlite", "litestream"],
              describe:
                "Database engine (sqlite is local; litestream adds continuous S3 replication)",
            })
            .option("mysql", {
              type: "string",
              describe: "MySQL version/service shorthand",
            })
            .option("postgres", {
              type: "string",
              describe: "PostgreSQL version/service",
            })
            .option("database", {
              type: "string",
              describe: "Initial database name",
            })
            .option("db", {
              type: "boolean",
              default: false,
              describe: "Create a database for the app",
            })
            .option("legacy", {
              type: "boolean",
              default: false,
              describe: "Allow direct PHP file execution",
            })
            .option("front", {
              type: "boolean",
              default: false,
              describe: "Force front-controller routing",
            })
            .option("access-log", {
              type: "boolean",
              default: false,
              describe: "Enable per-app access logs",
            })
            .option("no-apply", {
              type: "boolean",
              default: false,
              describe: "Skip render/apply after state mutation",
            })
            .option("skip-validate", {
              type: "boolean",
              default: false,
              describe: "Skip validators when applying",
            }),
        bind(state, cmdAppCreate),
      )
      .command(
        "update <slug>",
        "Update an application (same options as create)",
        (y2: YargsBuilder) =>
          y2
            .positional("slug", { type: "string", demandOption: true })
            .option("domain", { type: "string", demandOption: true })
            .option("alias", { type: "string" })
            .option("docroot", { type: "string" })
            .option("runtime", {
              type: "string",
              choices: ["php", "node", "bun", "python"],
            })
            .option("runtime-version", { type: "string" })
            .option("start", { type: "string", array: true })
            .option("port", { type: "number" })
            .option("health-path", { type: "string" })
            .option("workdir", { type: "string" })
            .option("php", { type: "string" })
            .option("fpm", { type: "string" })
            .option("database-engine", {
              type: "string",
              choices: ["mysql", "postgres", "sqlite", "litestream"],
            })
            .option("mysql", { type: "string" })
            .option("postgres", { type: "string" })
            .option("database", { type: "string" })
            .option("db", { type: "boolean", default: false })
            .option("legacy", { type: "boolean", default: false })
            .option("front", { type: "boolean", default: false })
            .option("access-log", { type: "boolean", default: false })
            .option("no-apply", { type: "boolean", default: false })
            .option("skip-validate", { type: "boolean", default: false }),
        bind(state, cmdAppCreate),
      )
      .command(
        "start <slug>",
        "Build and start a process app privately",
        (y2: YargsBuilder) => y2.positional("slug", { type: "string", demandOption: true }),
        bind(state, cmdAppStart),
      )
      .command(
        "stop <slug>",
        "Stop a process app container",
        (y2: YargsBuilder) => y2.positional("slug", { type: "string", demandOption: true }),
        bind(state, cmdAppStop),
      )
      .command(
        "enable <slug>",
        "Enable an application and its runtime configuration",
        (y2: YargsBuilder) =>
          noApplyOption(y2.positional("slug", { type: "string", demandOption: true })),
        bind(state, cmdAppEnable),
      )
      .command(
        "disable <slug>",
        "Disable runtime configuration while retaining app data",
        (y2: YargsBuilder) =>
          noApplyOption(y2.positional("slug", { type: "string", demandOption: true })),
        bind(state, cmdAppDisable),
      )
      .command(
        "delete <slug>",
        "Remove an application from Bento (durable data is retained)",
        appDeleteOptions,
        bind(state, cmdAppDelete),
      )
      .command("remove <slug>", "Alias for app delete", appDeleteOptions, bind(state, cmdAppDelete))
      .command(
        "prune <slug>",
        "Permanently delete data retained after app removal",
        (y2: YargsBuilder) =>
          y2.positional("slug", { type: "string", demandOption: true }).option("confirm", {
            type: "string",
            describe: "Non-interactive exact confirmation text: delete",
          }),
        bind(state, cmdAppPrune),
      )
      .command(
        "shell <slug>",
        "Attach an interactive shell using the app runtime",
        (y2: YargsBuilder) =>
          y2
            .positional("slug", { type: "string", demandOption: true })
            .option("workdir", {
              type: "string",
              describe: "Working directory inside app home",
            })
            .option("php", {
              type: "string",
              describe: "Managed PHP version override",
            })
            .option("print", {
              type: "boolean",
              default: false,
              describe: "Print compose argv instead of attaching",
            }),
        bind(state, cmdAppShell),
      )
      .demandCommand(
        1,
        "Specify an app subcommand: create|list|show|update|start|stop|enable|disable|delete|prune|shell",
      )
      .recommendCommands(),
  );
}

async function cmdAppList(_argv: CliArgs, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const rows = Object.values(state.apps)
    .sort((a, b) => a.slug.localeCompare(b.slug))
    .map((a) => [
      a.slug,
      a.enabled ? "enabled" : "disabled",
      String(a.uid),
      a.mainDomain,
      isPhpApp(a) ? `php@${a.phpVersion}` : `${a.runtime.language}@${a.runtime.version}`,
      isPhpApp(a) ? a.fpmProfile : a.runtime.service,
      a.tls.kind,
      a.databases
        .map((database) =>
          database.engine === "sqlite" || database.engine === "litestream"
            ? `${database.engine}/${sqliteContainerPath(database.file.id, a.slug, database.engine)}`
            : `${database.engine}/${database.service}`,
        )
        .join(", "),
    ]);
  ctx.log.out(
    printTable(
      ["slug", "status", "uid", "domain", "runtime", "service/profile", "tls", "database"],
      rows,
    ),
  );
  return 0;
}

async function cmdAppShow(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  const { slug } = argv;
  const state = await ctx.store.load();
  const app = state.apps[slug];
  if (!app) {
    ctx.log.error(`app not found: ${slug}`);
    return 3;
  }
  ctx.log.out(JSON.stringify(redactAppForOutput(app), null, 2));
  return 0;
}

export function redactAppForOutput(app: AppState): AppState {
  const databases = app.databases.map((database) =>
    database.engine === "sqlite" || database.engine === "litestream"
      ? database
      : { ...database, password: "***" },
  );
  const redacted: AppState = {
    ...app,
    databases,
    database: databases[0]!,
    redis: {
      ...app.redis,
      password: app.redis.password ? "***" : undefined,
      aclPassword: app.redis.aclPassword ? "***" : undefined,
    },
    deploy: {
      ...app.deploy,
      hmacSecret: app.deploy.hmacSecret ? "***" : undefined,
    },
  };
  if (redacted.kind === "php") return redacted;
  const {
    documentRoot: _documentRoot,
    entrypointMode: _entrypointMode,
    phpVersion: _phpVersion,
    phpService: _phpService,
    fpmProfile: _fpmProfile,
    poolTemplate: _poolTemplate,
    ...processOutput
  } = redacted;
  return processOutput as AppState;
}

async function cmdAppCreate(argv: ArgsWith<"slug" | "domain">, ctx: CliContext): Promise<number> {
  const { slug, domain } = argv;
  const aliases = argv.alias?.split(",").filter(Boolean) ?? [];
  const processSelected = argv.runtime !== undefined && argv.runtime !== "php";
  if (
    !processSelected &&
    (argv.runtimeVersion ||
      argv.start ||
      argv.healthPath ||
      argv.workdir ||
      argv.port !== undefined)
  ) {
    throw validationError("process runtime options require --runtime node, bun, or python");
  }
  if (
    processSelected &&
    (argv.php || argv.fpm || argv.docroot || argv.front === true || argv.legacy === true)
  ) {
    throw validationError("PHP runtime options cannot be combined with a process runtime");
  }
  const noApply = wantsNoApply(argv);
  const skipValidate = argv.skipValidate === true;
  const explicitDb = argv.db === true;
  const result = await ctx.store.withExclusive(async (state) => {
    const requestedDatabaseEngine = (argv.databaseEngine ??
      (argv.mysql ? "mysql" : argv.postgres ? "postgres" : undefined)) as
      | "mysql"
      | "postgres"
      | "sqlite"
      | "litestream"
      | undefined;
    const requestedDatabaseToken =
      requestedDatabaseEngine === "mysql"
        ? argv.mysql
        : requestedDatabaseEngine === "postgres"
          ? argv.postgres
          : undefined;
    const processLanguage = processSelected ? (argv.runtime as ProcessLanguage) : undefined;
    const provisioned = provisionApp(ctx.platform, state, {
      slug,
      domain,
      kind: processLanguage ? "process" : "php",
      processLanguage,
      processVersion: argv.runtimeVersion,
      processCommand: normalizeStartArgv(argv.start),
      processWorkdir: argv.workdir,
      processPort: argv.port,
      processHealthPath: argv.healthPath,
      aliases,
      documentRoot: argv.docroot,
      entrypointMode:
        argv.legacy === true ? "legacy" : argv.front === true ? "front-controller" : undefined,
      phpVersion: argv.php,
      fpmProfile: argv.fpm,
      databaseEngine: argv.databaseEngine,
      mysqlVersion: argv.mysql,
      postgresVersion: argv.postgres,
      createDatabase: explicitDb,
      databaseName: argv.database,
      accessLog: argv.accessLog === true,
    });
    // Live database/Redis side effects run before state save (explicit --db fails closed).
    const plane = await applyAppDataPlane(ctx.platform, provisioned.app, {
      explicitDatabase: explicitDb,
      databaseEngine: requestedDatabaseEngine,
      databaseService: requestedDatabaseToken
        ? state.databaseServices.find(
            (service) =>
              service.engine === requestedDatabaseEngine &&
              (service.version === requestedDatabaseToken ||
                service.service === requestedDatabaseToken),
          )?.service
        : undefined,
      databaseName: argv.database ?? (explicitDb ? slug : undefined),
    });
    const redisShared = await loadRedisPassword(ctx.platform);
    await materializeAppHome(ctx.platform, provisioned.app, {
      recursivePerms: true,
      redisSharedPassword: redisShared,
    });
    await ctx.store.save(provisioned.state);
    if (!noApply) {
      await ctx.render.apply(provisioned.state, {
        reloadPlan: provisioned.reloadPlan,
        skipValidate,
        alreadyLocked: true,
      });
    }
    return { provisioned, plane };
  });
  if (
    !noApply &&
    !result.provisioned.created &&
    isProcessApp(result.provisioned.app) &&
    result.provisioned.app.enabled
  ) {
    const app = result.provisioned.app;
    const recreated = await recreateRunningProcessApp(ctx.platform, result.provisioned.state, app);
    if (recreated && recreated.code !== 0) {
      ctx.log.error(
        `process app state was saved, but container recreation failed: ${(
          recreated.stderr || recreated.stdout
        ).trim()}`,
      );
      return 1;
    }
  }
  ctx.log.info(
    `${
      result.provisioned.created ? "created" : "updated"
    } app ${result.provisioned.app.slug} uid=${result.provisioned.app.uid} domain=${result.provisioned.app.mainDomain}`,
  );
  for (const note of result.plane.deferredNotes) ctx.log.warn(note);
  for (const w of capacityWarnings(result.provisioned.state)) ctx.log.warn(w);
  return 0;
}

function normalizeStartArgv(value: string | string[] | undefined): string[] | undefined {
  if (value === undefined) return undefined;
  return Array.isArray(value) ? value : [value];
}

async function cmdAppStart(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const app = state.apps[argv.slug];
  if (!app) {
    ctx.log.error(`app not found: ${argv.slug}`);
    return 3;
  }
  if (!isProcessApp(app)) {
    ctx.log.error("app start is only needed for process apps; PHP uses the shared runtime");
    return 2;
  }
  await ctx.render.apply(state, { reloadPlan: emptyReloadPlan(), skipValidate: false });
  const result = await startProcessApp(ctx.platform, state, app);
  if (!result || result.code !== 0) {
    ctx.log.error(
      `failed to start ${app.slug}: ${(result?.stderr || result?.stdout || "").trim()}`,
    );
    return 1;
  }
  ctx.log.info(
    `started process app ${app.slug} privately; run 'bento app enable ${app.slug}' after it is healthy`,
  );
  return 0;
}

async function cmdAppStop(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const app = state.apps[argv.slug];
  if (!app) {
    ctx.log.error(`app not found: ${argv.slug}`);
    return 3;
  }
  if (!isProcessApp(app)) {
    ctx.log.error("app stop is only available for process apps");
    return 2;
  }
  const result = await stopProcessApp(ctx.platform, state, app);
  if (!result || result.code !== 0) {
    ctx.log.error(`failed to stop ${app.slug}: ${(result?.stderr || result?.stdout || "").trim()}`);
    return 1;
  }
  ctx.log.info(`stopped process app ${app.slug}`);
  return 0;
}

function appDeleteOptions(y: YargsBuilder): YargsBuilder {
  return noApplyOption(
    y.positional("slug", { type: "string", demandOption: true }).option("confirm", {
      type: "string",
      describe: "Exact confirmation text: delete <slug>",
    }),
  );
}

async function mutateAppEnabled(
  argv: ArgsWith<"slug">,
  ctx: CliContext,
  enabled: boolean,
): Promise<number> {
  const noApply = wantsNoApply(argv);
  const result = await ctx.store.withExclusive(async (state) => {
    const current = state.apps[argv.slug];
    if (!current) throw new Error(`app not found: ${argv.slug}`);
    if (enabled && !noApply && isProcessApp(current)) {
      if (!(await isProcessAppHealthy(ctx.platform, state, current))) {
        throw safetyError(
          `refusing to publish process app ${current.slug} before its container is running`,
          `Run 'bento app start ${current.slug}', verify health, then enable it.`,
        );
      }
    }
    const changed = setAppEnabled(state, argv.slug, enabled, ctx.platform.clock.nowIso());
    await ctx.store.save(changed.state);
    if (!noApply) {
      await ctx.render.apply(changed.state, {
        reloadPlan: changed.reloadPlan,
        skipValidate: false,
        alreadyLocked: true,
      });
    }
    return changed;
  });
  if (!enabled && !noApply && isProcessApp(result.app)) {
    const stopped = await ctx.platform.process.run(
      await composeArgs(ctx.platform, result.state, ["stop", result.app.runtime.service]),
      { cwd: ctx.platform.paths.paths.root, timeoutMs: 60_000 },
    );
    if (stopped.code !== 0) {
      ctx.log.warn(
        `public route was disabled, but the private process container did not stop: ${(
          stopped.stderr || stopped.stdout
        ).trim()}`,
      );
    }
  }
  ctx.log.info(
    `${result.app.enabled ? "enabled" : "disabled"} app ${argv.slug}${
      noApply ? " (state only; run bento apply)" : ""
    }`,
  );
  return 0;
}

async function cmdAppEnable(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  return await mutateAppEnabled(argv, ctx, true);
}

async function cmdAppDisable(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  return await mutateAppEnabled(argv, ctx, false);
}

async function cmdAppDelete(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  const noApply = wantsNoApply(argv);
  const result = await ctx.store.withExclusive(async (state) => {
    const current = state.apps[argv.slug];
    const removed = deleteApp(state, argv.slug, argv.confirm, ctx.platform.clock.nowIso());
    if (current && isProcessApp(current) && !noApply) {
      const stopped = await removeProcessAppContainer(ctx.platform, state, current);
      if (stopped && stopped.code !== 0) {
        throw safetyError(
          `refusing to remove process app ${current.slug} while its container cannot be stopped`,
          "Restore Docker/Compose access and retry; durable data remains unchanged.",
        );
      }
    }
    await writeAppPruneManifest(ctx.platform, removed.app);
    await ctx.store.save(removed.state);
    if (!noApply) {
      await ctx.render.apply(removed.state, {
        reloadPlan: removed.reloadPlan,
        skipValidate: false,
        alreadyLocked: true,
      });
    }
    return removed;
  });
  ctx.log.info(
    `removed app ${result.app.slug}; durable home and database data retained${
      noApply ? " (state only; run bento apply)" : ""
    }`,
  );
  return 0;
}

async function cmdAppPrune(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const plan = await planAppPrune(ctx.platform, state, argv.slug);

  ctx.log.out(`The following retained data for app ${plan.slug} will be permanently deleted:`);
  if (plan.manifestFound) {
    for (const binding of plan.bindings) {
      const engineLabel =
        binding.engine === "mysql"
          ? "MySQL"
          : binding.engine === "postgres"
            ? "PostgreSQL"
            : "SQLite";
      for (const database of binding.databases) {
        ctx.log.out(
          binding.engine === "sqlite" || binding.engine === "litestream"
            ? `  - SQLite directory (${binding.engine}): ${ctx.platform.paths.paths.root}/sqlite/${database}`
            : `  - ${engineLabel} database: ${database} (${binding.databaseService})`,
        );
      }
      if (binding.engine !== "sqlite" && binding.engine !== "litestream") {
        const identity =
          binding.engine === "mysql" ? `${binding.databaseUser}@%` : binding.databaseUser;
        ctx.log.out(
          `  - ${engineLabel} ${
            binding.engine === "mysql" ? "account" : "role"
          }: ${identity} (${binding.databaseService})`,
        );
      }
    }
  } else {
    ctx.log.warn(
      "cleanup metadata is unavailable; database data cannot be identified and will not be deleted",
    );
  }
  ctx.log.out(`  - App home: ${plan.home}`);
  ctx.log.out("");

  const confirmation =
    argv.confirm ?? globalThis.prompt("Type 'delete' to permanently clean these parts:");
  const result = await ctx.store.withExclusive(async (current) => {
    const checked = await planAppPrune(ctx.platform, current, argv.slug);
    if (JSON.stringify(checked) !== JSON.stringify(plan)) {
      throw safetyError(
        `retained data for ${plan.slug} changed while awaiting confirmation`,
        "Review the cleanup list and retry.",
      );
    }
    return await executeAppPrune(ctx.platform, checked, confirmation);
  });
  for (const part of result.cleaned) ctx.log.info(`cleaned ${part}`);
  return 0;
}

async function cmdAppShell(argv: ArgsWith<"slug">, ctx: CliContext): Promise<number> {
  // Interactive shell alias under `app shell` (no trailing command).
  return await runCliExec(ctx, {
    slug: argv.slug,
    argv: [],
    workdir: argv.workdir,
    phpVersionOverride: argv.php,
    printOnly: argv.print === true,
  });
}
