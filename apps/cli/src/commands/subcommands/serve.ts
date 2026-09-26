import type { ArgsWith } from "#/commands/args.ts";
import type { CliContext } from "#/commands/context.ts";
import { bind, type RunState, type YargsBuilder } from "#/commands/shared.ts";
import { runWebServer } from "#/server/server.ts";

export function registerServeCommand(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command(
    "serve",
    "Serve the oRPC API and DaisyUI web control plane",
    (y: YargsBuilder) =>
      y
        .option("host", {
          type: "string",
          default: "127.0.0.1",
          describe: "Listener hostname (loopback recommended)",
        })
        .option("port", {
          type: "number",
          default: 8080,
          describe: "Listener port (0 selects an available port)",
        })
        .option("open", {
          type: "boolean",
          default: false,
          describe: "Open the UI in the default browser",
        }),
    bind(state, async (argv: ArgsWith<"host" | "port" | "open">, ctx: CliContext) => {
      if (!Number.isInteger(argv.port) || argv.port < 0 || argv.port > 65535)
        throw new Error("--port must be an integer between 0 and 65535");
      const basicAuth = Bun.env.WEB_BASIC_AUTH;
      if (basicAuth !== undefined && (!/^[^:\r\n]+:[^\r\n]+$/.test(basicAuth) || basicAuth.length > 1024)) {
        throw new Error("WEB_BASIC_AUTH must be a non-empty user:password value");
      }
      if (!["127.0.0.1", "localhost", "::1"].includes(argv.host) && basicAuth === undefined) {
        ctx.log.warn(
          `web management is exposed on ${argv.host} without authentication; use only for temporary testing`,
        );
      }
      await ctx.store.migrate();
      return await runWebServer(ctx, {
        hostname: argv.host,
        port: argv.port,
        open: argv.open,
        ...(basicAuth !== undefined ? { basicAuth } : {}),
      });
    }),
  );
}
