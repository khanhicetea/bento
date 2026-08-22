import { os, ORPCError } from "@orpc/server";
import { z } from "zod";
import { isCompiledDistribution, resolveAssetRoot } from "../platform/assets.ts";
import { loadStackComposeEnvironment } from "../services/stack_env.ts";
import type { CliContext } from "../commands/context.ts";
import { redact } from "../ui/output.ts";

const EMPTY_INPUT = z.object({}).optional();
const EXECUTABLE_COMMANDS = new Set([
  "version",
  "init",
  "render",
  "apply",
  "status",
  "doctor",
  "support-bundle",
  "test-stack",
  "app",
  "php",
  "mysql",
  "postgres",
  "sqlite",
  "proxy",
  "tls",
  "deploy",
  "cron",
  "worker",
  "logs",
  "template",
  "permissions",
  "backup",
  "restore",
  "compose",
  "exec",
  "rclone",
  "stack",
  "maintenance",
]);

export const commandCatalog = [
  {
    category: "Stack",
    commands: [
      "status",
      "doctor",
      "init --name bento",
      "render",
      "apply --preview",
      "apply",
      "support-bundle",
    ],
  },
  {
    category: "Applications",
    commands: [
      "app list",
      "app show <slug>",
      "app create <slug> --domain <domain> --docroot public --db",
      "app update <slug> --domain <domain>",
      "app enable <slug>",
      "app disable <slug>",
      "app delete <slug> --confirm 'delete <slug>'",
      "app prune <slug> --confirm delete",
      "app shell <slug> --print",
      "exec <slug> -- <command>",
    ],
  },
  {
    category: "Databases",
    commands: [
      "mysql list",
      "mysql add <version>",
      "mysql db <app> <database>",
      "mysql size",
      "mysql processlist",
      "mysql shell --app <app> --print",
      "postgres list",
      "postgres add <major>",
      "postgres db <app> <database>",
      "postgres size",
      "postgres processlist",
      "postgres shell --app <app> --print",
      "sqlite backup local <app>",
      "sqlite backup enable <app>",
      "sqlite backup status",
      "sqlite backup sync",
      "sqlite backup verify --app <app>",
      "sqlite backup export --app <app> --output <path>",
      "backup --all",
      "restore --file <path> --app <slug>",
    ],
  },
  {
    category: "PHP",
    commands: ["php list", "php add <version>", "php reload <version>", "php remove <version>"],
  },
  {
    category: "Routing & TLS",
    commands: [
      "proxy list",
      "proxy create <name> --domain <domain> --upstream <url>",
      "proxy delete <name> --confirm 'delete <name>'",
      "tls set --app <slug> --mode self-ca",
      "tls ca export --output <path>",
    ],
  },
  {
    category: "Jobs",
    commands: [
      "cron list <app>",
      "cron add --app <app> --name <name> --schedule '<cron>' --cmd '<command>'",
      "cron edit <app> <name>",
      "cron remove <app> <name>",
      "cron reload <app>",
      "worker list <app>",
      "worker add --app <app> --name <name> --cmd '<command>'",
      "worker start <app> <name>",
      "worker stop <app> <name>",
      "worker restart <app> <name>",
      "worker inspect <app> <name>",
      "worker remove <app> <name>",
    ],
  },
  {
    category: "Operations",
    commands: [
      "deploy status <app>",
      "deploy enable <app>",
      "deploy disable <app>",
      "deploy rotate <app>",
      "deploy drain <app>",
      "deploy instructions <app>",
      "logs access enable --app <slug>",
      "logs access disable --app <slug>",
      "logs access rotate --app <slug>",
      "logs access report --app <slug>",
      "permissions check",
      "permissions repair --dry-run",
      "maintenance run",
      "maintenance register",
      "maintenance unregister",
      "stack ingress show",
      "compose files",
    ],
  },
  {
    category: "Templates",
    commands: [
      "template drift --app <slug>",
      "template select --app <slug> --kind vhost --source <path>",
      "template return --app <slug> --kind vhost",
    ],
  },
] as const;

export type WebSnapshot = Awaited<ReturnType<typeof createSnapshot>>;

export function createWebRouter(ctx: CliContext) {
  return {
    health: os.input(EMPTY_INPUT).handler(() => ({ ok: true, service: "bento" as const })),
    snapshot: os.input(EMPTY_INPUT).handler(async () => await createSnapshot(ctx)),
    catalog: os.input(EMPTY_INPUT).handler(() => commandCatalog),
    execute: os
      .input(
        z.object({
          argv: z.array(z.string().max(4096)).min(1).max(100),
          timeoutSec: z.number().int().min(1).max(3600).optional(),
        }),
      )
      .handler(async ({ input }) => {
        assertWebCommand(input.argv);
        if (input.argv[0] === "php" && input.argv[1] === "reload") {
          return await reloadPhp(ctx, input.argv[2]);
        }
        return await executeCli(ctx, input.argv, input.timeoutSec ?? 300);
      }),
  };
}

export type AppRouter = ReturnType<typeof createWebRouter>;

async function createSnapshot(ctx: CliContext) {
  if (!(await ctx.store.exists())) {
    return emptySnapshot(ctx, false);
  }
  let state;
  try {
    state = await ctx.store.load();
  } catch (error) {
    return emptySnapshot(ctx, true, redact(error instanceof Error ? error.message : String(error)));
  }
  let projectName: string | undefined;
  try {
    projectName = (await loadStackComposeEnvironment(ctx.platform)).projectName;
  } catch {
    /* status remains useful */
  }
  return {
    initialized: true as const,
    stateExists: true,
    error: undefined,
    stackRoot: ctx.stackRoot,
    projectName,
    updatedAt: state.updatedAt,
    apps: Object.values(state.apps)
      .sort((a, b) => a.slug.localeCompare(b.slug))
      .map((app) => ({
        slug: app.slug,
        enabled: app.enabled,
        domain: app.mainDomain,
        aliases: app.aliases,
        phpVersion: app.phpVersion,
        fpmProfile: app.fpmProfile,
        tls: app.tls.kind,
        accessLog: app.accessLog,
        deployEnabled: app.deploy.enabled,
        databases: app.databases.map((db) => ({
          engine: db.engine,
          service: db.engine === "mysql" || db.engine === "postgres" ? db.service : undefined,
          names:
            db.engine === "mysql" || db.engine === "postgres"
              ? db.databases.map((item) => item.name)
              : [],
          file: db.engine === "sqlite" || db.engine === "litestream" ? db.file.path : undefined,
        })),
      })),
    proxies: Object.values(state.proxies)
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((proxy) => ({
        name: proxy.name,
        domain: proxy.mainDomain,
        aliases: proxy.aliases,
        upstreams: proxy.upstreams,
        tls: proxy.tls.kind,
        accessLog: proxy.accessLog,
      })),
    phpVersions: state.phpVersions.map((item) => ({
      version: item.version,
      service: item.service,
      processCap: item.processCap,
    })),
    mysqlVersions: state.databaseServices
      .filter((item) => item.engine === "mysql")
      .map((item) => ({ version: item.version, service: item.service })),
    postgresVersions: state.databaseServices
      .filter((item) => item.engine === "postgres")
      .map((item) => ({ version: item.version, service: item.service })),
    cronJobs: state.cronJobs.map((job) => ({
      name: job.name,
      app: job.app,
      schedule: job.schedule,
      timezone: job.timezone,
      enabled: job.enabled,
      command: job.command,
    })),
    workers: state.workers.map((worker) => ({
      name: worker.name,
      app: worker.app,
      enabled: worker.enabled,
      autorestart: worker.autorestart,
      command: worker.command,
    })),
  };
}

function emptySnapshot(ctx: CliContext, stateExists: boolean, error?: string) {
  return {
    initialized: false as const,
    stateExists,
    error,
    stackRoot: ctx.stackRoot,
    apps: [],
    proxies: [],
    phpVersions: [],
    mysqlVersions: [],
    postgresVersions: [],
    cronJobs: [],
    workers: [],
  };
}

export function assertWebCommand(argv: string[]): void {
  const root = argv[0]!;
  if (!EXECUTABLE_COMMANDS.has(root))
    throw new ORPCError("BAD_REQUEST", {
      message: `Command '${root}' is not available in the web UI`,
    });
  if (argv.some((arg) => arg.includes("\0") || arg.includes("\n") || arg.includes("\r")))
    throw new ORPCError("BAD_REQUEST", {
      message: "Command arguments cannot contain control characters",
    });
  const sub = argv[1];
  const unsupported =
    root === "tui" ||
    root === "serve" ||
    (root === "app" && sub === "shell" && !argv.includes("--print")) ||
    ((root === "mysql" || root === "postgres") && sub === "shell" && !argv.includes("--print")) ||
    (root === "logs" && argv.includes("--attach")) ||
    (root === "template" && sub === "select" && !argv.includes("--source")) ||
    (root === "exec" && argv.length < 3) ||
    root === "rclone";
  if (unsupported)
    throw new ORPCError("UNPROCESSABLE_CONTENT", {
      message: "This action requires an interactive terminal and cannot run in the browser",
    });
}

async function reloadPhp(ctx: CliContext, version: string | undefined) {
  if (!version) throw new ORPCError("BAD_REQUEST", { message: "php reload requires a version" });
  const state = await ctx.store.load();
  const managed = state.phpVersions.find(
    (item) => item.version === version || item.service === version,
  );
  if (!managed)
    throw new ORPCError("NOT_FOUND", { message: `PHP version '${version}' is not managed` });
  const started = performance.now();
  await ctx.render.apply(state, {
    reloadPlan: { nginx: false, phpFpm: new Set([managed.service]), phpRunner: new Set() },
    skipValidate: false,
  });
  return {
    code: 0,
    stdout: `Reloaded FPM service ${managed.service}\n`,
    stderr: "",
    truncated: false,
    timedOut: false,
    durationMs: Math.round(performance.now() - started),
  };
}

async function executeCli(ctx: CliContext, argv: string[], timeoutSec: number) {
  const command = cliInvocation(ctx, argv);
  const started = performance.now();
  const child = Bun.spawn(command, {
    cwd: ctx.stackRoot,
    env: { ...process.env, BENTO_STACK_ROOT: ctx.stackRoot },
    stdin: "ignore",
    stdout: "pipe",
    stderr: "pipe",
  });
  const stdoutPromise = new Response(child.stdout).text();
  const stderrPromise = new Response(child.stderr).text();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    child.kill();
  }, timeoutSec * 1000);
  const code = await child.exited;
  clearTimeout(timer);
  const [stdout, stderr] = await Promise.all([stdoutPromise, stderrPromise]);
  const limit = 512_000;
  return {
    code: timedOut ? 124 : code,
    stdout: tail(stdout, limit),
    stderr: tail(stderr, limit),
    truncated: stdout.length > limit || stderr.length > limit,
    timedOut,
    durationMs: Math.round(performance.now() - started),
  };
}

function cliInvocation(ctx: CliContext, argv: string[]): string[] {
  const global = ["--stack", ctx.stackRoot, ...(ctx.json ? ["--json"] : [])];
  if (isCompiledDistribution()) return [process.execPath, ...global, ...argv];
  return [process.execPath, "run", `${resolveAssetRoot()}/src/main.ts`, ...global, ...argv];
}

function tail(value: string, limit: number): string {
  return value.length <= limit
    ? value
    : `[output truncated; last ${limit} characters]\n${value.slice(-limit)}`;
}
