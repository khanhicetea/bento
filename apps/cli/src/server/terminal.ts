import type { CliContext } from "../commands/context.ts";
import { composeArgs } from "../services/compose.ts";
import { buildCliExec, cliRunComposeCommand } from "../services/php.ts";

const MAX_TERMINAL_INPUT_BYTES = 64 * 1024;
const DEFAULT_COLS = 80;
const DEFAULT_ROWS = 24;

type TerminalClientMessage =
  | { type: "input"; data: string }
  | { type: "resize"; cols: number; rows: number };

type TerminalOutput = {
  data: (data: Uint8Array) => void;
  exit: (exitCode: number | null, signalCode: number | null) => void;
  error: () => void;
  close: () => void;
};

export type TerminalSession = {
  command: string[];
  cwd: string;
  app: string;
  terminal?: Bun.Terminal;
  process?: Bun.Subprocess;
  closeOutput?: () => void;
  cleanup: () => Promise<void>;
  closing?: Promise<void>;
  closed: boolean;
};

export async function prepareTerminalSession(
  ctx: CliContext,
  app: string,
): Promise<TerminalSession> {
  const state = await ctx.store.load();
  const plan = buildCliExec(ctx.platform, state, app, ["bash"]);
  const containerName = `bento-web-shell-${ctx.platform.random.hex(12)}`;
  const command = await composeArgs(
    ctx.platform,
    state,
    cliRunComposeCommand(plan, { tty: true, containerName }),
  );
  return {
    command,
    cwd: ctx.stackRoot,
    app: plan.slug,
    closed: false,
    async cleanup() {
      await ctx.platform.process.run(["docker", "rm", "--force", containerName], {
        cwd: ctx.stackRoot,
        timeoutMs: 10_000,
      });
    },
  };
}

export function isSameOriginRequest(request: Request): boolean {
  const origin = request.headers.get("origin");
  if (!origin) return false;
  try {
    return new URL(origin).origin === new URL(request.url).origin;
  } catch {
    return false;
  }
}

export function parseTerminalClientMessage(value: string): TerminalClientMessage | null {
  if (new TextEncoder().encode(value).byteLength > MAX_TERMINAL_INPUT_BYTES) return null;
  try {
    const message: unknown = JSON.parse(value);
    if (!message || typeof message !== "object" || !("type" in message)) return null;
    if (message.type === "input" && "data" in message && typeof message.data === "string") {
      return { type: "input", data: message.data };
    }
    if (
      message.type === "resize" &&
      "cols" in message &&
      "rows" in message &&
      Number.isInteger(message.cols) &&
      Number.isInteger(message.rows) &&
      Number(message.cols) >= 2 &&
      Number(message.cols) <= 500 &&
      Number(message.rows) >= 1 &&
      Number(message.rows) <= 300
    ) {
      return { type: "resize", cols: Number(message.cols), rows: Number(message.rows) };
    }
  } catch {
    // Invalid client messages are ignored without affecting the shell process.
  }
  return null;
}

export function startTerminalSession(session: TerminalSession, output: TerminalOutput): void {
  if (session.closed || session.process) return;
  session.closeOutput = output.close;

  let terminal: Bun.Terminal | undefined;
  try {
    terminal = new Bun.Terminal({
      cols: DEFAULT_COLS,
      rows: DEFAULT_ROWS,
      name: "xterm-256color",
      data(_terminal, data) {
        if (!session.closed) output.data(data);
      },
    });
    session.terminal = terminal;
    session.process = Bun.spawn(session.command, {
      cwd: session.cwd,
      terminal,
      onExit(_process, exitCode, signalCode) {
        if (!session.closed) output.exit(exitCode, signalCode);
      },
    });
  } catch {
    terminal?.close();
    session.terminal = undefined;
    output.error();
  }
}

export function handleTerminalMessage(session: TerminalSession, value: string): boolean {
  if (session.closed || !session.terminal) return false;
  const message = parseTerminalClientMessage(value);
  if (!message) return false;
  if (message.type === "input") session.terminal.write(message.data);
  else session.terminal.resize(message.cols, message.rows);
  return true;
}

export function closeTerminalSession(session: TerminalSession): Promise<void> {
  if (session.closing) return session.closing;
  session.closed = true;
  try {
    session.process?.kill("SIGTERM");
  } catch {
    // The process may already have exited.
  }
  session.closeOutput?.();
  session.terminal?.close();
  session.closing = session.cleanup().catch(() => {
    // The container may not have been created or may already have removed itself.
  });
  return session.closing;
}
