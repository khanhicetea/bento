import { printTable } from "#/ui/output.ts";
import type { CliContext } from "#/commands/context.ts";
import type { ArgsWith, CliArgs } from "#/commands/args.ts";
import { bind, noApplyOption, type RunState, wantsNoApply, type YargsBuilder } from "#/commands/shared.ts";

export function registerProxyCommands(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command("proxy", "Reverse-proxy sites", (y: YargsBuilder) =>
    y
      .command("list", "List reverse proxies", () => {}, bind(state, cmdProxyList))
      .command(
        "create <name>",
        "Create a reverse-proxy site",
        (y2: YargsBuilder) =>
          noApplyOption(
            y2
              .positional("name", { type: "string", demandOption: true })
              .option("domain", {
                type: "string",
                demandOption: true,
                describe: "Primary domain",
              })
              .option("upstream", {
                type: "string",
                array: true,
                demandOption: true,
                describe: "Upstream URL; repeat for multiple servers",
              })
              .option("alias", {
                type: "string",
                describe: "Comma-separated domain aliases",
              }),
          ),
        bind(state, cmdProxyCreate),
      )
      .command("delete <name>", "Remove a reverse-proxy site", proxyDeleteOptions, bind(state, cmdProxyDelete))
      .command("remove <name>", "Alias for proxy delete", proxyDeleteOptions, bind(state, cmdProxyDelete))
      .demandCommand(1, "Specify a proxy subcommand: create|list|delete")
      .recommendCommands(),
  );
}

async function cmdProxyList(_argv: CliArgs, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  const rows = Object.values(state.proxies).map((p) => [p.name, p.mainDomain, p.upstreams.join(", "), p.tls.kind]);
  ctx.log.out(printTable(["name", "domain", "upstream", "tls"], rows));
  return 0;
}

async function cmdProxyCreate(argv: ArgsWith<"name" | "domain" | "upstream">, ctx: CliContext): Promise<number> {
  const { name, domain, upstream } = argv;
  const upstreams = Array.isArray(upstream) ? upstream : [upstream];
  const noApply = wantsNoApply(argv);
  await ctx.routing.saveProxy(
    {
      operation: "create",
      name,
      domain,
      upstreams,
      aliases: argv.alias?.split(",") ?? [],
    },
    { apply: !noApply, skipValidate: true },
  );
  ctx.log.info(noApply ? `created proxy ${name} (state only; run bento apply)` : `created proxy ${name}`);
  return 0;
}

function proxyDeleteOptions(y: YargsBuilder): YargsBuilder {
  return noApplyOption(
    y.positional("name", { type: "string", demandOption: true }).option("confirm", {
      type: "string",
      describe: "Exact confirmation text: delete <name>",
    }),
  );
}

async function cmdProxyDelete(argv: ArgsWith<"name">, ctx: CliContext): Promise<number> {
  const noApply = wantsNoApply(argv);
  const result = await ctx.routing.removeProxy(argv.name, argv.confirm, { apply: !noApply });
  ctx.log.info(`removed proxy ${result.proxy.name}${noApply ? " (state only; run bento apply)" : ""}`);
  return 0;
}
