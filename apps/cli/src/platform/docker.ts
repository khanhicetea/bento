import { join } from "node:path";
import { safetyError } from "#/domain/errors.ts";
import type { PathPolicy, ProcessRunner, RunOptions, RunResult } from "#/platform/interfaces.ts";
import { RuntimeCommand } from "#/platform/runtime.ts";

/** Refuse volume or image destruction even for commands with global Compose flags. */
export function assertSafeComposeArgs(args: string[]): void {
  const lower = args.map((arg) => arg.toLowerCase());
  if (!lower.includes("down")) return;
  if (
    lower.some(
      (arg) =>
        arg === "-v" ||
        arg === "--volumes" ||
        arg.startsWith("--volumes=") ||
        arg === "--rmi" ||
        arg.startsWith("--rmi="),
    )
  ) {
    throw safetyError(
      "refusing docker compose down with volume/image destruction",
      "Remove -v/--volumes/--rmi. Durable MySQL/PostgreSQL/Redis volumes must not be deleted through Bento.",
    );
  }
}

/** Interactive commands must inherit all three streams; captured runners break shells. */
export async function attachDockerCommand(command: string[], cwd: string): Promise<number> {
  const [binary, ...args] = command;
  if (!binary) throw new Error("empty docker command");
  return (
    await new RuntimeCommand(binary, {
      args,
      cwd,
      stdin: "inherit",
      stdout: "inherit",
      stderr: "inherit",
    }).output()
  ).code;
}

/** Stack-scoped Docker operations. Files are supplied by the Compose state resolver, not guessed from .env. */
export function createDockerUtils(
  root: string,
  process: ProcessRunner,
  attach: (command: string[], cwd: string) => Promise<number> = attachDockerCommand,
) {
  const compose = (files: string[], args: string[]): string[] => {
    assertSafeComposeArgs(args);
    return [
      "docker",
      "compose",
      "--project-directory",
      root,
      ...files.flatMap((file) => ["-f", join(root, file)]),
      ...args,
    ];
  };

  const execCommand = (service: string, argv: string[], tty = false): string[] => [
    "docker",
    "compose",
    "exec",
    tty ? "-it" : "-T",
    service,
    ...argv,
  ];

  return {
    compose,
    execCommand,
    /** Pass argv as argv; never interpolate user input through a shell. */
    exec(service: string, argv: string[], options?: RunOptions): Promise<RunResult> {
      return process.run(execCommand(service, argv), { ...options, cwd: root });
    },
    /** Script is intentionally interpreted by the container shell; secret input belongs on stdin. */
    execScript(service: string, script: string, options?: RunOptions): Promise<RunResult> {
      return process.run(execCommand(service, ["sh", "-c", script]), { ...options, cwd: root });
    },
    attach(command: string[]): Promise<number> {
      return attach(command, root);
    },
  };
}

export function docker(platform: { paths: PathPolicy; process: ProcessRunner }) {
  return createDockerUtils(platform.paths.paths.root, platform.process);
}
