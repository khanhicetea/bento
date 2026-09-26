/** Terminal colors backed by Bun's ANSI color conversion. */

const enabled =
  !process.env.NO_COLOR &&
  !process.argv.includes("--no-color") &&
  (Boolean(process.env.FORCE_COLOR) ||
    process.argv.includes("--color") ||
    process.platform === "win32" ||
    (Boolean(process.stdout.isTTY) && process.env.TERM !== "dumb") ||
    Boolean(process.env.CI));

function style(open: string, close: string, replace = open): (value: string) => string {
  if (!enabled) return (value) => String(value);
  return (value) => `${open}${String(value).split(close).join(replace)}${close}`;
}

function color(name: string): (value: string) => string {
  const open = Bun.color(name, "ansi");
  if (!open) throw new Error(`unsupported terminal color: ${name}`);
  return style(open, "\x1b[39m");
}

export const colors = {
  bold: style("\x1b[1m", "\x1b[22m", "\x1b[22m\x1b[1m"),
  dim: style("\x1b[2m", "\x1b[22m", "\x1b[22m\x1b[2m"),
  cyan: color("cyan"),
  green: color("green"),
  yellow: color("yellow"),
  red: color("red"),
};
