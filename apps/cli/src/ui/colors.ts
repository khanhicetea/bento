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

const ansiFallback = { cyan: "\x1b[36m", green: "\x1b[32m", yellow: "\x1b[33m", red: "\x1b[31m" } as const;

function color(name: keyof typeof ansiFallback): (value: string) => string {
  // Bun.color can return null in a headless container without TERM, even for a known color.
  if (!enabled) return (value) => String(value);
  return style(Bun.color(name, "ansi") ?? ansiFallback[name], "\x1b[39m");
}

export const colors = {
  bold: style("\x1b[1m", "\x1b[22m", "\x1b[22m\x1b[1m"),
  dim: style("\x1b[2m", "\x1b[22m", "\x1b[22m\x1b[2m"),
  cyan: color("cyan"),
  green: color("green"),
  yellow: color("yellow"),
  red: color("red"),
};
