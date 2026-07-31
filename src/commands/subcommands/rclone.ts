/** Ephemeral rclone sidecar passthrough. */

import { materializeDockerAssets } from "../../services/assets_materialize.ts";
import { rcloneComposeCommand } from "../../services/rclone.ts";
import type { CliContext } from "../context.ts";
import type { CliArgs } from "../args.ts";
import { bind, type RunState, trailing, type YargsBuilder } from "../shared.ts";

export function registerRcloneCommand(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command(
    "rclone",
    "Run rclone in an isolated sidecar (-- <rclone arguments>)",
    (y: YargsBuilder) => y,
    bind(state, cmdRclone),
  );
}

async function cmdRclone(argv: CliArgs, ctx: CliContext): Promise<number> {
  const args = trailing(argv, 1);
  if (args.length === 0) {
    ctx.log.error("usage: bento rclone -- <rclone arguments>");
    return 2;
  }
  const state = await ctx.store.load();
  // A freshly initialized stack has no generated Compose files yet. Render before
  // invoking the profile-only sidecar, without starting application services.
  await materializeDockerAssets(
    ctx.platform,
    state.phpVersions.map((version) => String(version.version)),
  );
  await ctx.render.apply(state, { renderOnly: true, skipValidate: true });
  const command = await rcloneComposeCommand(ctx.platform, state, args);
  ctx.log.info(`running rclone ${args.join(" ")}`);
  const [cmd, ...cmdArgs] = command;
  const child = new Deno.Command(cmd!, {
    args: cmdArgs,
    cwd: ctx.stackRoot,
    stdin: "inherit",
    stdout: "inherit",
    stderr: "inherit",
  });
  return (await child.output()).code;
}
