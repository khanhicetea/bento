import { materializeAppHome } from "../../services/app.ts";
import {
  addPostgresVersion,
  assertPostgresShellSecretsOffArgv,
  buildPostgresShellPlan,
  createPostgresAppDatabaseLive,
  executePostgresShell,
  listPostgresVersions,
  queryPostgresActivity,
  queryPostgresDatabaseSizes,
  removePostgresVersion,
  resolvePostgresServices,
} from "../../services/postgres.ts";
import { loadRedisPassword, requirePostgresRootPassword } from "../../services/stack_env.ts";
import { printTable } from "../../ui/output.ts";
import type { ArgsWith, CliArgs } from "../args.ts";
import type { CliContext } from "../context.ts";
import { bind, noApplyOption, type RunState, wantsNoApply, type YargsBuilder } from "../shared.ts";

export function registerPostgresCommands(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command(
    "postgres",
    "Manage PostgreSQL (add-only versions)",
    (y: YargsBuilder) =>
      y
        .command(
          "list",
          "List PostgreSQL versions",
          () => {},
          bind(state, cmdPostgresList),
        )
        .command(
          "add <version>",
          "Add a PostgreSQL major-version service",
          (y2: YargsBuilder) =>
            noApplyOption(y2.positional("version", { type: "string", demandOption: true })),
          bind(state, cmdPostgresAdd),
        )
        .command(
          "remove <version>",
          "Blocked: PostgreSQL version removal is unavailable",
          (y2: YargsBuilder) => y2.positional("version", { type: "string", demandOption: true }),
          bind(state, cmdPostgresRemove),
        )
        .command(
          "db <app> <database>",
          "Create and record a namespaced app database",
          (y2: YargsBuilder) =>
            y2.positional("app", { type: "string", demandOption: true })
              .positional("database", { type: "string", demandOption: true }),
          bind(state, cmdPostgresDb),
        )
        .command(
          "shell",
          "Open psql with protected credentials",
          (y2: YargsBuilder) =>
            y2.option("root", { type: "boolean", default: false })
              .option("app", { type: "string", describe: "Connect as an app role" })
              .option("service", { type: "string", describe: "PostgreSQL service/version" })
              .option("database", { type: "string", describe: "Default database" })
              .option("print", {
                type: "boolean",
                default: false,
                describe: "Print secret-free plan instead of opening",
              }),
          bind(state, cmdPostgresShell),
        )
        .command(
          "size",
          "Show PostgreSQL database sizes",
          (y2: YargsBuilder) =>
            y2.option("app", { type: "string" }).option("service", { type: "string" }),
          bind(state, cmdPostgresSize),
        )
        .command(
          "processlist",
          "Show PostgreSQL activity without query text",
          (y2: YargsBuilder) =>
            y2.option("app", { type: "string" }).option("service", { type: "string" }),
          bind(state, cmdPostgresProcesslist),
        )
        .demandCommand(1, "Specify a postgres subcommand: add|list|db|shell|size|processlist")
        .recommendCommands(),
  );
}

async function cmdPostgresList(_argv: CliArgs, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const rows = listPostgresVersions(state).map((entry) => [
    entry.version,
    entry.service,
    entry.volume,
    entry.image,
  ]);
  ctx.log.out(printTable(["version", "service", "volume", "image"], rows));
  return 0;
}

async function cmdPostgresAdd(argv: ArgsWith<"version">, ctx: CliContext): Promise<number> {
  const noApply = wantsNoApply(argv);
  await ctx.store.withExclusive(async (state) => {
    const next = addPostgresVersion(state, argv.version);
    await ctx.store.save(next);
    if (!noApply) {
      await ctx.render.apply(next, { skipValidate: true, alreadyLocked: true });
    }
    return next;
  });
  ctx.log.info(
    noApply
      ? `added PostgreSQL ${argv.version} (state only; run bento apply)`
      : `added PostgreSQL ${argv.version}`,
  );
  return 0;
}

async function cmdPostgresRemove(
  argv: ArgsWith<"version">,
  ctx: CliContext,
): Promise<number> {
  removePostgresVersion(await ctx.store.load(), argv.version);
}

async function cmdPostgresDb(
  argv: ArgsWith<"app" | "database">,
  ctx: CliContext,
): Promise<number> {
  await ctx.store.withExclusive(async (state) => {
    const rootPassword = await requirePostgresRootPassword(ctx.platform);
    const next = await createPostgresAppDatabaseLive(
      ctx.platform,
      state,
      argv.app,
      argv.database,
      rootPassword,
    );
    const app = next.apps[argv.app]!;
    await materializeAppHome(ctx.platform, app, {
      recursivePerms: false,
      redisSharedPassword: await loadRedisPassword(ctx.platform),
    });
    await ctx.store.save(next);
    return next;
  });
  ctx.log.info(`created database ${argv.database} for app ${argv.app}`);
  return 0;
}

async function cmdPostgresShell(
  argv: ArgsWith<"root" | "print">,
  ctx: CliContext,
): Promise<number> {
  const appSlug = argv.app ?? "";
  if (argv.root === Boolean(appSlug)) {
    ctx.log.error("usage: bento postgres shell --root [--service postgres17] | --app <slug>");
    return 2;
  }
  const state = await ctx.store.load();
  let plan;
  if (argv.root) {
    const service = resolvePostgresServices(state, { service: argv.service })[0];
    if (!service) {
      ctx.log.error("no PostgreSQL service managed");
      return 3;
    }
    plan = buildPostgresShellPlan(ctx.platform, { kind: "root", service }, {
      database: argv.database,
      interactive: !argv.print,
    });
  } else {
    const app = state.apps[appSlug];
    if (!app) {
      ctx.log.error(`app not found: ${appSlug}`);
      return 3;
    }
    plan = buildPostgresShellPlan(ctx.platform, { kind: "app", app }, {
      database: argv.database,
      interactive: !argv.print,
    });
    assertPostgresShellSecretsOffArgv(plan, [app.database.password]);
  }

  if (argv.print) {
    const safe = {
      service: plan.service,
      user: plan.user,
      database: plan.database,
      stage: plan.stage?.command,
      open: plan.open.command,
      cleanup: plan.cleanup?.command,
    };
    ctx.log.out(
      ctx.json ? JSON.stringify(safe, null, 2) : [
        ...(plan.stage ? [`stage: ${plan.stage.command.join(" ")}`] : []),
        `open:  ${plan.open.command.join(" ")}`,
        ...(plan.cleanup ? [`cleanup: ${plan.cleanup.command.join(" ")}`] : []),
      ].join("\n"),
    );
    return 0;
  }

  return await executePostgresShell(ctx.platform, plan, async (command) => {
    const [cmd, ...args] = command;
    return (await new Deno.Command(cmd!, {
      args,
      cwd: ctx.stackRoot,
      stdin: "inherit",
      stdout: "inherit",
      stderr: "inherit",
    }).output()).code;
  });
}

async function cmdPostgresSize(argv: CliArgs, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const rootPassword = await requirePostgresRootPassword(ctx.platform);
  const services = resolvePostgresServices(state, { service: argv.service, app: argv.app });
  const rows: Array<{ service: string; database: string; bytes: string; size: string }> = [];
  for (const service of services) {
    const databases = argv.app
      ? state.apps[argv.app]?.database.databases.map((entry) => entry.name) ?? []
      : [];
    // An app with no recorded databases must not broaden into a service-wide query.
    if (argv.app && databases.length === 0) continue;
    for (
      const row of await queryPostgresDatabaseSizes(
        ctx.platform,
        service,
        rootPassword,
        databases,
      )
    ) rows.push({ service, ...row });
  }
  ctx.log.out(
    ctx.json ? JSON.stringify(rows, null, 2) : printTable(
      ["service", "database", "bytes", "size"],
      rows.map((row) => [row.service, row.database, row.bytes, row.size]),
    ),
  );
  return 0;
}

async function cmdPostgresProcesslist(argv: CliArgs, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const rootPassword = await requirePostgresRootPassword(ctx.platform);
  const services = resolvePostgresServices(state, { service: argv.service, app: argv.app });
  const rows: Array<{
    service: string;
    pid: string;
    user: string;
    database: string;
    state: string;
    client: string;
    backendStart: string;
    queryStart: string;
  }> = [];
  const appUser = argv.app ? state.apps[argv.app]?.database.user : undefined;
  for (const service of services) {
    for (const row of await queryPostgresActivity(ctx.platform, service, rootPassword)) {
      if (appUser && row.user !== appUser) continue;
      rows.push({ service, ...row });
    }
  }
  ctx.log.out(
    ctx.json ? JSON.stringify(rows, null, 2) : printTable(
      ["service", "pid", "user", "database", "state", "client", "backend_start", "query_start"],
      rows.map((row) => [
        row.service,
        row.pid,
        row.user,
        row.database,
        row.state,
        row.client,
        row.backendStart,
        row.queryStart,
      ]),
    ),
  );
  return 0;
}
