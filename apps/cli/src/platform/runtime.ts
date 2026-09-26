import { spawn, type Subprocess } from "bun";

export type CommandOptions = {
  args?: string[];
  cwd?: string;
  env?: Record<string, string>;
  stdin?: "piped" | "null" | "inherit";
  stdout?: "piped" | "null" | "inherit";
  stderr?: "piped" | "null" | "inherit";
};

export type CommandOutput = {
  code: number;
  success: boolean;
  signal: string | null;
  stdout: Uint8Array;
  stderr: Uint8Array;
};

function stdio(value: "piped" | "null" | "inherit" | undefined): "pipe" | "ignore" | "inherit" {
  if (value === "inherit") return "inherit";
  if (value === "null") return "ignore";
  return "pipe";
}

async function bytes(stream: ReadableStream<Uint8Array> | number | undefined): Promise<Uint8Array> {
  if (!(stream instanceof ReadableStream)) return new Uint8Array();
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

/** Bun-backed subprocess adapter used by both captured and interactive commands. */
export class RuntimeCommand {
  readonly command: string;
  readonly options: CommandOptions;

  constructor(command: string, options: CommandOptions = {}) {
    this.command = command;
    this.options = options;
  }

  spawn(): RuntimeChild {
    const process = spawn([this.command, ...(this.options.args ?? [])], {
      cwd: this.options.cwd,
      env: this.options.env ? { ...Bun.env, ...this.options.env } : undefined,
      stdin: stdio(this.options.stdin),
      stdout: stdio(this.options.stdout),
      stderr: stdio(this.options.stderr),
    });
    return new RuntimeChild(process);
  }

  async output(): Promise<CommandOutput> {
    return await this.spawn().output();
  }
}

export class RuntimeChild {
  readonly #process: Subprocess;

  constructor(process: Subprocess) {
    this.#process = process;
  }

  get stdout(): ReadableStream<Uint8Array> | number | undefined {
    return this.#process.stdout;
  }

  get stderr(): ReadableStream<Uint8Array> | number | undefined {
    return this.#process.stderr;
  }

  get stdin(): {
    getWriter(): {
      write(data: Uint8Array): Promise<void>;
      close(): Promise<void>;
    };
  } {
    const sink = this.#process.stdin;
    if (typeof sink === "number" || sink === undefined) throw new Error("stdin is not piped");
    return {
      getWriter: () => ({
        write: async (data) => {
          sink.write(data);
          await sink.flush();
        },
        close: async () => {
          await sink.end();
        },
      }),
    };
  }

  get status(): Promise<{
    code: number;
    success: boolean;
    signal: string | null;
  }> {
    return this.#process.exited.then((code) => ({
      code,
      success: code === 0,
      signal: this.#process.signalCode,
    }));
  }

  kill(signal: NodeJS.Signals | number = "SIGTERM"): void {
    this.#process.kill(signal);
  }

  async output(): Promise<CommandOutput> {
    const stdoutPromise = bytes(this.#process.stdout);
    const stderrPromise = bytes(this.#process.stderr);
    const [code, stdout, stderr] = await Promise.all([this.#process.exited, stdoutPromise, stderrPromise]);
    return {
      code,
      success: code === 0,
      signal: this.#process.signalCode,
      stdout,
      stderr,
    };
  }
}

export function isStdinTerminal(): boolean {
  return Boolean(process.stdin.isTTY);
}

export function isStdoutTerminal(): boolean {
  return Boolean(process.stdout.isTTY);
}
