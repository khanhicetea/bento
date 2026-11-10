# Plan 020: Three small UI fixes (env text mode, toast dismissal, terminal typeface)

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/web/src/features/applications/EnvEditor.tsx apps/web/src/lib/env.ts apps/web/src/features/operations/OperationTracker.tsx apps/web/src/components/Terminal.tsx`
> If any changed beyond what plans 007 and 009 describe (007 moves `envToText`/`envFromText` into
> `apps/web/src/lib/env.ts`; 009 moves the tracker's query into a shared hook), compare the "Current state" excerpts
> against the live code; on an unexplained mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: plan/007-web-test-baseline-and-hooks-lint.md (tests and lint; item A's test updates the
  "KNOWN DEFECT" case that plan adds)
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

- **A. Env text mode silently changes values.** Switching the environment editor to text mode and saving rewrites any
  value that starts and ends with the same quote character, or has leading/trailing spaces: the parser strips them,
  but the serializer never added them. A secret can change with no visible difference.
- **B. Success toasts postpone their own dismissal.** Each finished-operation toast restarts its 6-second timer
  whenever any toast is added or removed, so with several operations (for example "Rebuild all") finished toasts
  linger.
- **C. The terminal ignores the design system.** The repository's rule is "Type is JetBrains Mono only"; the browser
  terminal uses another font stack and hard-coded colours, and its production warning uses a colour outside the
  palette.

## Current state

**A.** `envToText` / `envFromText` (in `apps/web/src/features/applications/EnvEditor.tsx` at the planned commit; in
`apps/web/src/lib/env.ts` once plan 007 landed):

```ts
export function envToText(env: T.EnvVar[]): string {
  return env.filter((e) => e.key.trim() !== "").map((e) => `${e.key}=${e.value}`).join("\n");
}

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
```

The parser strips exactly one pair of matching outer quotes and does not process escapes. The backend accepts any
single-line value.

**B.** `apps/web/src/features/operations/OperationTracker.tsx`:

```tsx
{tracked.map((op, index) => (
  <TrackedOperation key={op.id} initial={op} visible={index < 5} onDismiss={() => dismiss(op.id)} />
))}
…
useEffect(() => {
  if (op.state !== "succeeded") return;
  const timer = window.setTimeout(onDismiss, 6_000);
  return () => window.clearTimeout(timer);
}, [op.state, onDismiss]);
```

`onDismiss` is a new function on every render of the provider, so the effect re-runs and the timer restarts.
`dismiss(id)` is `setTracked((current) => current.filter((op) => op.id !== id))` — it uses the functional form of
`setState`, so any version of the callback does the right thing.

**C.** `apps/web/src/components/Terminal.tsx`:

```tsx
<span className="text-xs text-amber-600">Live app container — changes affect production</span>
…
const terminal = new Terminal({
  cursorBlink: true,
  fontFamily: '"SFMono-Regular", Consolas, monospace',
  fontSize: 14,
  scrollback: 5_000,
  theme: { background: "#16140f", foreground: "#ede6d6", cursor: "#e2603f", selectionBackground: "#3a352e" },
});
const fit = new FitAddon();
terminal.loadAddon(fit);
terminal.open(element);
fit.fit();
```

Design tokens in `apps/web/src/app.css`: `--font-body: "JetBrains Mono", "Noto Sans Mono", "Noto Sans JP",
ui-monospace, monospace;` and console colours `--nori` (background), `--nori-ink` (text), `--nori-dim`; both light
and dark themes define them. Caution text classes exist: `.note--caution` and `.notice--caution`. Read
`apps/web/DESIGN.md` (sections on type, the nori "console" cell kind, and notices) before item C. Rules from
`AGENTS.md`: JetBrains Mono only, self-hosted; "Shu is the only accent"; no `useMemo`/`useCallback`/`React.memo`.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Install | `bun install --frozen-lockfile` | exit 0 |
| Format | `bun run fmt` then `bun run fmt:check` | exit 0 |
| Lint | `bun run lint` | exit 0 |
| Typecheck | `bun run check` | exit 0 |
| Tests | `bun run test` (exists once plan 007 landed) | all pass |
| Build | `bun run web:build` | exit 0 |

## Scope

**In scope**:
- `apps/web/src/lib/env.ts` and `apps/web/src/lib/env.test.ts` (or `EnvEditor.tsx` if plan 007 has not landed)
- `apps/web/src/features/operations/OperationTracker.tsx`
- `apps/web/src/components/Terminal.tsx`

**Out of scope**:
- `envFromText` — the parser stays as it is, so pasted dotenv text keeps working.
- New CSS tokens or classes. If item C seems to need one, STOP (the design system requires `DESIGN.md` to be updated
  in the same change, which is beyond this plan).
- The terminal's connection, resize and reattach logic.

## Git workflow

- Branch: `advisor/020-frontend-small-fixes`
- Three commits, one per item, plain imperative sentences, e.g. `Keep env values intact through the text editor`,
  `Dismiss a finished operation's toast after six seconds`, `Use the design system's type and colours in the terminal`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1 (A): Serialize values so the parser returns them unchanged

Change `envToText` so a value the parser would alter is wrapped in one pair of double quotes (the parser strips
exactly one outer pair and does no escape processing, so wrapping is a lossless inverse):

```ts
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
```

Tests (`env.test.ts`, if plan 007 landed): replace the "KNOWN DEFECT (plan 020)" case with round-trip assertions —
for each of these values, `envFromText(envToText([{ key: "K", value }]))` returns the same value: `plain`,
`"x"` (including the quote characters), `'*'`, ` padded `, `a "quoted" word`, `"unbalanced`, an empty string, and
`"` (a single quote character). Keep the existing parser tests unchanged.

**Verify**: `bun run test` → all pass; `bun run check` → exit 0.

### Step 2 (B): Start the dismiss timer once

In `TrackedOperation`, make the effect depend only on the state:

```tsx
useEffect(() => {
  if (op.state !== "succeeded") return;
  const timer = window.setTimeout(onDismiss, 6_000);
  return () => window.clearTimeout(timer);
  // onDismiss is a new function on every provider render; listing it would
  // restart the timer whenever any toast changes. Any version of it is
  // correct: dismissal filters by id with a functional state update.
  // eslint-disable-next-line react-hooks/exhaustive-deps
}, [op.state]);
```

(The disable comment is only needed once plan 007's lint rule exists; include it regardless — an unused disable
directive is not an error in this configuration. If lint reports it as unused, remove the comment line.)

**Verify**: `bun run lint && bun run check` → exit 0.

### Step 3 (C): Terminal type and colours from the design tokens

In `Terminal.tsx`, where the `Terminal` is constructed:

```tsx
const styles = getComputedStyle(document.documentElement);
const token = (name: string, fallback: string) => styles.getPropertyValue(name).trim() || fallback;
const terminal = new Terminal({
  cursorBlink: true,
  fontFamily: token("--font-body", '"JetBrains Mono", monospace'),
  fontSize: 14,
  scrollback: 5_000,
  theme: {
    background: token("--nori", "#1b1815"),
    foreground: token("--nori-ink", "#e9e1d3"),
    cursor: <keep the existing cursor colour unless app.css defines the shu accent as a token; if it does, use that token>,
    selectionBackground: <keep the existing value>,
  },
});
```

Look in `app.css` for the accent token (search `--shu`) and use it for the cursor if present.

xterm measures character cells when it opens. If the web font is not loaded yet, the grid is measured with a
fallback font. After `terminal.open(element); fit.fit();` add:

```tsx
// Re-measure once the self-hosted font is ready; a cold load opens with the fallback's metrics.
let disposed = false;
void document.fonts.ready.then(() => {
  if (disposed) return;
  terminal.options.fontFamily = terminal.options.fontFamily; // re-assign to make xterm re-measure
  fit.fit();
});
```

and set `disposed = true` in the effect's existing cleanup function. If re-assigning the same value does not trigger
a re-measure in this xterm version (read `@xterm/xterm` 5.5.0's `options` setter if unsure), instead await
`document.fonts.load('14px "JetBrains Mono"')` inside the `then` and toggle `fontSize` to 13 and back to 14 —
choose whichever you can confirm; say which in your report.

Replace the warning span: `<span className="text-xs text-amber-600">…</span>` → use the existing caution class,
`<span className="note note--caution">Live app container — changes affect production</span>` (check in `app.css`
that `.note` and `.note--caution` produce small caution-coloured text in a toolbar; if `.note` adds unwanted block
spacing there, use `notice notice--caution` per `DESIGN.md` instead). Do not change the wording.

**Verify**: `grep -n "SFMono\|text-amber" apps/web/src/components/Terminal.tsx` → no matches.
`bun run fmt && bun run fmt:check && bun run lint && bun run check && bun run web:build` → exit 0.

## Test plan

Item A: unit tests as in step 1. Items B and C have no unit test (render behaviour, no DOM test setup); they are
verified by typecheck, lint and, where you can run the app, by eye: open a terminal (font is JetBrains Mono, colours
match the console cells in both light and dark theme) and run two operations at once (each success toast disappears
about six seconds after it finishes). If you cannot run the app (it needs Docker), say so.

## Done criteria

- [ ] `bun run fmt:check && bun run lint && bun run check && bun run web:build` exits 0
- [ ] `bun run test` passes, with the round-trip cases replacing the "KNOWN DEFECT" case (if plan 007 landed)
- [ ] `grep -n "SFMono\|text-amber" apps/web/src/components/Terminal.tsx` → no matches
- [ ] `grep -n "\[op.state, onDismiss\]" apps/web/src/features/operations/OperationTracker.tsx` → no matches
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated, stating which visual checks were or were not run

## STOP conditions

- "Current state" excerpts do not match beyond the moves described in the drift check.
- Item C needs a token or class that does not exist in `app.css`.
- `bun` is unavailable: report "written but not run".

## Maintenance notes

- `needsQuotes` must stay the exact mirror of what `envFromText` strips. If the parser ever learns escapes or
  inline comments, update both together; the round-trip tests will catch a mismatch.
- The terminal reads tokens once, when it opens: switching light/dark theme while a terminal is open does not
  recolour it until it is reopened. Acceptable; note it if someone reports it.
