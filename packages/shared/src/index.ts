import { oc } from "@orpc/contract";
import { z } from "zod";

const emptyInputSchema = z.object({}).optional();

const databaseSchema = z.object({
  engine: z.string(),
  service: z.string().optional(),
  names: z.array(z.string()),
  file: z.string().optional(),
});

const appSchema = z.object({
  slug: z.string(),
  enabled: z.boolean(),
  domain: z.string(),
  aliases: z.array(z.string()),
  phpVersion: z.string(),
  fpmProfile: z.string(),
  tls: z.string(),
  accessLog: z.boolean(),
  deployEnabled: z.boolean(),
  databases: z.array(databaseSchema),
});

const webSnapshotBaseSchema = z.object({
  stateExists: z.boolean(),
  error: z.string().optional(),
  stackRoot: z.string(),
  projectName: z.string().optional(),
  updatedAt: z.string().optional(),
  apps: z.array(appSchema),
  proxies: z.array(
    z.object({
      name: z.string(),
      domain: z.string(),
      aliases: z.array(z.string()),
      upstreams: z.array(z.string()),
      tls: z.string(),
      accessLog: z.boolean(),
    }),
  ),
  phpVersions: z.array(
    z.object({ version: z.string(), service: z.string(), processCap: z.number() }),
  ),
  mysqlVersions: z.array(z.object({ version: z.string(), service: z.string() })),
  postgresVersions: z.array(z.object({ version: z.string(), service: z.string() })),
  cronJobs: z.array(
    z.object({
      name: z.string(),
      app: z.string(),
      schedule: z.string(),
      timezone: z.string(),
      enabled: z.boolean(),
      command: z.array(z.string()),
    }),
  ),
  workers: z.array(
    z.object({
      name: z.string(),
      app: z.string(),
      enabled: z.boolean(),
      autorestart: z.boolean(),
      command: z.array(z.string()),
    }),
  ),
});

export const webSnapshotSchema = z.discriminatedUnion("initialized", [
  webSnapshotBaseSchema.extend({ initialized: z.literal(true) }),
  webSnapshotBaseSchema.extend({ initialized: z.literal(false) }),
]);

export type WebSnapshot = z.infer<typeof webSnapshotSchema>;

export type CommandCategory = { category: string; commands: string[] };

export const commandCatalog: CommandCategory[] = [
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
];

export const webContract = oc.router({
  health: oc
    .input(emptyInputSchema)
    .output(z.object({ ok: z.literal(true), service: z.literal("bento") })),
  snapshot: oc.input(emptyInputSchema).output(webSnapshotSchema),
  catalog: oc
    .input(emptyInputSchema)
    .output(z.array(z.object({ category: z.string(), commands: z.array(z.string()) }))),
  execute: oc
    .input(
      z.object({
        argv: z.array(z.string().max(4096)).min(1).max(100),
        timeoutSec: z.number().int().min(1).max(3600).optional(),
      }),
    )
    .output(
      z.object({
        code: z.number(),
        stdout: z.string(),
        stderr: z.string(),
        truncated: z.boolean(),
        timedOut: z.boolean(),
        durationMs: z.number(),
      }),
    ),
});

export type WebContract = typeof webContract;
