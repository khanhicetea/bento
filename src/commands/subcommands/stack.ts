import type { DesiredState } from "../../domain/state.ts";
import { composeArgs } from "../../services/compose.ts";
import { loadStackComposeEnvironment, updateStackEnv } from "../../services/stack_env.ts";
import { exportStack, importStack } from "../../services/stack_transfer.ts";
import type { CliContext } from "../context.ts";
import type { ArgsWith, CliArgs } from "../args.ts";
import { bind, type RunState, type YargsBuilder } from "../shared.ts";

export function registerStackCommands(parser: YargsBuilder, state: RunState): YargsBuilder {
  return parser.command(
    "stack",
    "Configure, export, or import a complete stack",
    (y: YargsBuilder) =>
      y
        .command(
          "ingress",
          "Inspect or configure Nginx networking and host publications",
          (y2: YargsBuilder) =>
            y2
              .command(
                "show",
                "Show effective Nginx networking",
                (y3: YargsBuilder) => y3,
                bind(state, cmdIngressShow),
              )
              .command(
                "set <ingressMode>",
                "Use host networking or the stack-private bridge network",
                (y3: YargsBuilder) =>
                  y3
                    .positional("ingressMode", {
                      type: "string",
                      choices: ["host", "bridge"] as const,
                      demandOption: true,
                      describe: "Nginx network mode",
                    })
                    .option("http-port", {
                      type: "number",
                      describe: "Bridge-mode host HTTP port; 0 clears publication",
                    })
                    .option("https-port", {
                      type: "number",
                      describe: "Bridge-mode host HTTPS TCP/UDP port; 0 clears publication",
                    }),
                bind(state, cmdIngressSet),
              )
              .demandCommand(1, "Specify an ingress subcommand: show|set"),
        )
        .command(
          "export <directory>",
          "Export stack.tar.gz and one archive per database/Redis volume",
          (y2: YargsBuilder) =>
            y2.positional("directory", {
              type: "string",
              demandOption: true,
              describe: "Empty destination directory outside the stack root",
            }),
          bind(state, cmdStackExport),
        )
        .command(
          "import <directory>",
          "Import stack and volume archives into an empty stack root and start it",
          (y2: YargsBuilder) =>
            y2
              .positional("directory", {
                type: "string",
                demandOption: true,
                describe: "Directory containing stack.tar.gz and volume-named archives",
              })
              .option("name", {
                type: "string",
                describe: "New stack name, allowing a same-machine clone of the source stack",
              })
              .option("ingress-mode", {
                type: "string",
                choices: ["host", "bridge"] as const,
                describe: "Override imported Nginx networking before the stack starts",
              })
              .option("http-port", {
                type: "number",
                describe: "Override bridge HTTP host port; 0 clears publication",
              })
              .option("https-port", {
                type: "number",
                describe: "Override bridge HTTPS host TCP/UDP port; 0 clears publication",
              }),
          bind(state, cmdStackImport),
        )
        .demandCommand(1, "Specify a stack subcommand: ingress|export|import")
        .recommendCommands(),
  );
}

async function cmdIngressShow(_argv: CliArgs, ctx: CliContext): Promise<number> {
  await ctx.store.load();
  const environment = await loadStackComposeEnvironment(ctx.platform);
  const nginx = environment.nginx;
  const value = {
    stackName: environment.projectName,
    mode: nginx.hostNetwork ? "host" : "bridge",
    httpPort: nginx.httpPort ?? null,
    httpsPort: nginx.httpsPort ?? null,
    http3: nginx.http3,
  };
  if (ctx.json) {
    ctx.log.out(JSON.stringify(value, null, 2));
  } else {
    ctx.log.out(`Stack ingress
  name: ${value.stackName}
  mode: ${value.mode}
  HTTP host port: ${value.httpPort ?? "not published"}
  HTTPS host port: ${value.httpsPort ?? "not published"}
  HTTP/3 UDP publication: ${value.http3 && value.httpsPort ? value.httpsPort : "none"}
`);
  }
  return 0;
}

async function cmdIngressSet(
  argv: ArgsWith<"ingressMode">,
  ctx: CliContext,
): Promise<number> {
  const updates: Record<string, string> = {
    NGINX_HOST_NETWORK: argv.ingressMode === "host" ? "1" : "0",
  };
  for (
    const [key, value] of [
      ["NGINX_HTTP_PORT", argv.httpPort],
      ["NGINX_HTTPS_PORT", argv.httpsPort],
    ] as const
  ) {
    if (value === undefined) continue;
    if (!Number.isInteger(value) || value < 0 || value > 65535) {
      throw new Error(`${key} must be 0 or a port between 1 and 65535`);
    }
    updates[key] = value === 0 ? "" : String(value);
  }

  const envPath = ctx.platform.paths.paths.envFile;
  const original = await ctx.platform.fs.readText(envPath);
  let stateForCompose: DesiredState;
  try {
    stateForCompose = await ctx.store.withExclusive(async (state) => {
      await updateStackEnv(ctx.platform, updates);
      // Validate environment values before promoting generated Compose files.
      await loadStackComposeEnvironment(ctx.platform);
      await ctx.render.apply(state, {
        renderOnly: true,
        skipValidate: true,
        alreadyLocked: true,
      });
      return state;
    });
  } catch (error) {
    await ctx.platform.fs.atomicWriteText(envPath, original, 0o600);
    throw error;
  }

  const environment = await loadStackComposeEnvironment(ctx.platform);
  ctx.log.info(
    `Nginx ingress set to ${environment.nginx.hostNetwork ? "host" : "bridge"} mode`,
  );
  if (
    environment.nginx.hostNetwork && (environment.nginx.httpPort || environment.nginx.httpsPort)
  ) {
    ctx.log.warn(
      "bridge-mode port settings are retained but ignored while host networking is active",
    );
  }

  const composeVersion = await ctx.platform.process.run(
    ["docker", "compose", "version"],
    { cwd: ctx.stackRoot, timeoutMs: 5_000 },
  ).catch(() => ({ code: 1, stdout: "", stderr: "" }));
  if (composeVersion.code !== 0) {
    ctx.log.warn("Docker Compose unavailable; ingress was rendered but Nginx was not recreated");
    return 0;
  }

  const configCommand = await composeArgs(ctx.platform, stateForCompose, ["config", "--quiet"]);
  const config = await ctx.platform.process.run(configCommand, {
    cwd: ctx.stackRoot,
    timeoutMs: 15_000,
  });
  if (config.code !== 0) {
    throw new Error(`Compose ingress configuration is invalid: ${config.stderr || config.stdout}`);
  }

  const psCommand = await composeArgs(ctx.platform, stateForCompose, ["ps", "-q", "nginx"]);
  const running = await ctx.platform.process.run(psCommand, {
    cwd: ctx.stackRoot,
    timeoutMs: 10_000,
  });
  if (running.code === 0 && running.stdout.trim()) {
    const upCommand = await composeArgs(ctx.platform, stateForCompose, [
      "up",
      "-d",
      "--force-recreate",
      "nginx",
    ]);
    const recreated = await ctx.platform.process.run(upCommand, {
      cwd: ctx.stackRoot,
      timeoutMs: 120_000,
    });
    if (recreated.code !== 0) {
      throw new Error(
        `ingress was saved but Nginx recreation failed: ${recreated.stderr || recreated.stdout}`,
      );
    }
    ctx.log.info("recreated Nginx with the new network topology");
  } else {
    ctx.log.info("Nginx is not running; the new topology will be used on its next start");
  }
  return 0;
}

async function cmdStackExport(argv: ArgsWith<"directory">, ctx: CliContext): Promise<number> {
  const state = await ctx.store.load();
  ctx.log.warn("export contains application data, passwords, and private keys; store it securely");
  const result = await exportStack(ctx.platform, state, argv.directory);
  ctx.log.info(`stack exported to ${result.directory}`);
  for (const file of result.files) ctx.log.out(`  ${file}`);
  return 0;
}

async function cmdStackImport(argv: ArgsWith<"directory">, ctx: CliContext): Promise<number> {
  ctx.log.warn("import restores trusted archives and starts the destination stack");
  for (
    const [name, port] of [["--http-port", argv.httpPort], [
      "--https-port",
      argv.httpsPort,
    ]] as const
  ) {
    if (port !== undefined && (!Number.isInteger(port) || port < 0 || port > 65535)) {
      throw new Error(`${name} must be 0 or a port between 1 and 65535`);
    }
  }
  const result = await importStack(ctx.platform, argv.directory, {
    ...(argv.name ? { projectName: argv.name } : {}),
    ...(argv.ingressMode ? { nginxHostNetwork: argv.ingressMode === "host" } : {}),
    ...(argv.httpPort !== undefined
      ? { httpPort: argv.httpPort === 0 ? null : argv.httpPort }
      : {}),
    ...(argv.httpsPort !== undefined
      ? { httpsPort: argv.httpsPort === 0 ? null : argv.httpsPort }
      : {}),
  });
  ctx.log.info(`stack imported and started at ${result.directory}`);
  for (const volume of result.volumes) ctx.log.out(`  restored ${volume}`);
  return 0;
}
