import type { T } from "../api/client.ts";

/** True when envFromText would not return the value unchanged: outer whitespace, or a matching outer quote pair. */
function needsQuotes(value: string): boolean {
  if (value !== value.trim()) return true;
  return value.length >= 2 && (value[0] === '"' || value[0] === "'") && value.at(-1) === value[0];
}

/** Render variables as dotenv lines that envFromText parses back to the same values. */
export function envToText(env: T.EnvVar[]): string {
  return env
    .filter((e) => e.key.trim() !== "")
    .map((e) => `${e.key}=${needsQuotes(e.value) ? `"${e.value}"` : e.value}`)
    .join("\n");
}

/** Parse dotenv-style text: blank lines and # comments are skipped, `export ` and matching quotes are stripped. */
export function envFromText(text: string): T.EnvVar[] {
  const out: T.EnvVar[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line === "" || line.startsWith("#")) continue;
    const body = line.startsWith("export ") ? line.slice(7).trimStart() : line;
    const eq = body.indexOf("=");
    const key = (eq < 0 ? body : body.slice(0, eq)).trim();
    let value = eq < 0 ? "" : body.slice(eq + 1).trim();
    if (value.length >= 2 && (value[0] === '"' || value[0] === "'") && value.at(-1) === value[0]) {
      value = value.slice(1, -1);
    }
    out.push({ key, value });
  }
  return out;
}
