import { useState } from "react";
import { Plus, X } from "lucide-react";
import type { T } from "../../api/client.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export type EnvMode = "table" | "text";

/** Render variables as dotenv lines. */
export function envToText(env: T.EnvVar[]): string {
  return env
    .filter((e) => e.key.trim() !== "")
    .map((e) => `${e.key}=${e.value}`)
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

/** Local draft for the env editor; `value()` returns the variables in either mode. */
export function useEnvDraft(initial: T.EnvVar[]) {
  const [mode, setMode] = useState<EnvMode>("table");
  const [rows, setRows] = useState<T.EnvVar[]>(initial);
  const [text, setText] = useState("");
  return {
    mode,
    rows,
    text,
    setRows,
    setText,
    toggle() {
      if (mode === "table") {
        setText(envToText(rows));
        setMode("text");
      } else {
        setRows(envFromText(text));
        setMode("table");
      }
    },
    value(): T.EnvVar[] {
      return mode === "text" ? envFromText(text) : rows.filter((e) => e.key.trim() !== "");
    },
  };
}

export function EnvEditor({ state }: { state: ReturnType<typeof useEnvDraft> }) {
  const { mode, rows, setRows } = state;
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="note">
          Exposed to the app process. Bento-managed keys (BENTO_*, DB_*, REDIS_*) take precedence.
        </span>
        <Button type="button" size="sm" variant="ghost" onClick={state.toggle}>
          {mode === "table" ? "Edit as text" : "Edit as table"}
        </Button>
      </div>
      {mode === "text" ? (
        <textarea
          className="min-h-40 w-full rounded-md border bg-transparent p-2 font-mono text-sm"
          aria-label="Environment variables"
          spellCheck={false}
          placeholder={"APP_ENV=production\nLOG_LEVEL=info"}
          value={state.text}
          onChange={(e) => state.setText(e.target.value)}
        />
      ) : (
        <>
          {rows.length === 0 && <p className="note">No variables.</p>}
          {rows.map((row, index) => (
            <div key={index} className="flex gap-2">
              <Input
                className="w-2/5 font-mono"
                aria-label={`Key ${index + 1}`}
                placeholder="KEY"
                value={row.key}
                onChange={(e) => setRows(rows.map((r, i) => (i === index ? { ...r, key: e.target.value } : r)))}
              />
              <Input
                className="font-mono"
                aria-label={`Value ${index + 1}`}
                placeholder="value"
                value={row.value}
                onChange={(e) => setRows(rows.map((r, i) => (i === index ? { ...r, value: e.target.value } : r)))}
              />
              <Button
                type="button"
                size="icon"
                variant="ghost"
                aria-label={`Remove variable ${index + 1}`}
                onClick={() => setRows(rows.filter((_, i) => i !== index))}
              >
                <X />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="justify-self-start"
            onClick={() => setRows([...rows, { key: "", value: "" }])}
          >
            <Plus />
            Variable
          </Button>
        </>
      )}
    </div>
  );
}
