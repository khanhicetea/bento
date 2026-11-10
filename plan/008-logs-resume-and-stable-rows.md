# Plan 008: The app log view resumes where it left off instead of silently dropping lines, and stays fast when full

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/web/src/features/applications/LogsPanel.tsx apps/web/src/api/client.ts apps/backend/internal/api/stream.go`
> If any changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: plan/007-web-test-baseline-and-hooks-lint.md (for the test runner and lint rules; if 007 has not
  landed, skip the test in step 4 and say so)
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

In an app's Logs tab the operator turns **Follow** off to read, then on again. The panel reconnects asking for zero
old lines and no starting point, so every line written while Follow was off is missing — and the view looks
continuous. During an incident that is the worst outcome: the lines that matter are silently gone. The backend
already supports resuming from a timestamp; the UI just does not use it. Separately, once the 2,000-line buffer is
full, each new line changes every row's React key (keys include the array index), so React rebuilds all 2,000 rows
per line and the tab becomes sluggish on a busy app.

## Current state

- `apps/web/src/api/client.ts` (near line 149):

  ```ts
  logsUrl: (id: string, tail: number, follow: boolean) =>
    `/api/v1/apps/${enc(id)}/logs?tail=${tail}${follow ? "&follow=1" : ""}`,
  ```
- `apps/web/src/features/applications/LogsPanel.tsx`:

  ```tsx
  type Line = { ts: string; line: string };
  const maxLines = 2000;

  export function LogsPanel({ appId }: { appId: string }) {
    const [lines, setLines] = useState<Line[]>([]);
    const [follow, setFollow] = useState(true);
    …
    useEffect(() => {
      const source = new EventSource(api.apps.logsUrl(appId, lines.length === 0 ? 300 : 0, follow));
      source.addEventListener("open", () => setStatus("streaming"));
      source.addEventListener("log", (event) => {
        const data = JSON.parse((event as MessageEvent<string>).data) as Line;
        setLines((current) => [...current, data].slice(-maxLines));
      });
      source.addEventListener("end", () => {
        setStatus("ended");
        if (!follow) source.close();
      });
      source.addEventListener("error", () => setStatus(follow ? "reconnecting" : "closed"));
      return () => source.close();
      // Existing lines deliberately survive follow reconnects.
    }, [appId, follow]);
  ```
  and the rows (near line 92):

  ```tsx
  {visible.map((line, index) => (
    <div key={`${line.ts}-${index}`}>
      <span>{line.ts.slice(11, 19)} </span>
      {line.line}
    </div>
  ))}
  ```
  The "Clear logs" button calls `setLines([])`. The copy button and the filter read `line.ts` and `line.line`.
- Backend, `apps/backend/internal/api/stream.go`, `handleAppLogs`: accepts `tail` (0 to a maximum; default 200),
  `follow=1`, and `since=<RFC 3339 timestamp>` ("Reconnects pass ?since=<RFC3339> (or Last-Event-ID) to resume").
  `since` is validated with `time.Parse(time.RFC3339Nano, since)` and passed to Docker's log API together with
  `tail`. Each `log` event's data is `{"ts": "<RFC 3339 nano timestamp>", "line": "…"}`. Do not change the backend.
- Repository React rules (`AGENTS.md`): function components; no `useMemo`, `useCallback`, `React.memo`; `useState`
  only for local UI state. A log buffer is local UI state, so `useState` here is correct.

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
- `apps/web/src/api/client.ts` (`logsUrl` only)
- `apps/web/src/features/applications/LogsPanel.tsx`
- `apps/web/src/lib/logBuffer.ts` and `apps/web/src/lib/logBuffer.test.ts` (create)
- `apps/web/src/features/applications/ApplicationDetail.tsx` (only the `key=` on `<LogsPanel>`)

**Out of scope**:
- `apps/backend/**` — the backend already supports `since`.
- The panel's layout, buttons and copy. (The floating "Latest ↓" button label is a known design nit; leave it.)

## Git workflow

- Branch: `advisor/008-logs-resume-and-stable-rows`
- One commit, plain imperative sentence, e.g. `Resume the log stream from the last line when Follow is turned back on`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Let `logsUrl` carry `since`

```ts
logsUrl: (id: string, tail: number, follow: boolean, since?: string) =>
  `/api/v1/apps/${enc(id)}/logs?tail=${tail}${follow ? "&follow=1" : ""}${since ? `&since=${enc(since)}` : ""}`,
```

**Verify**: `bun run check` → exit 0.

### Step 2: A pure buffer helper

Create `apps/web/src/lib/logBuffer.ts`:

```ts
export type LogLine = { id: number; ts: string; line: string };

/**
 * Append an incoming line to the buffer, keeping at most `max` lines.
 * A resume asks the backend for lines since the last timestamp, which is
 * inclusive: lines at exactly that timestamp arrive again and are dropped
 * here when an identical line with that timestamp is already buffered.
 * Lines older than the newest buffered line are dropped too.
 */
export function appendLine(buffer: LogLine[], incoming: { ts: string; line: string }, id: number, max: number): LogLine[] {
  const last = buffer.at(-1);
  if (last) {
    if (incoming.ts < last.ts) return buffer;
    if (incoming.ts === last.ts && buffer.some((l) => l.ts === incoming.ts && l.line === incoming.line)) return buffer;
  }
  const next = [...buffer, { id, ts: incoming.ts, line: incoming.line }];
  return next.length > max ? next.slice(-max) : next;
}
```

Timestamps are RFC 3339 strings in UTC from Docker with the same format, so string comparison orders them. If you
find while reading `stream.go` that timestamps can have differing fractional-digit counts, compare with
`Date.parse` plus a string tiebreak instead and note it in your report.

**Verify**: `bun run check` → exit 0.

### Step 3: Use it in the panel

In `LogsPanel.tsx`:

1. Replace the local `Line` type with `LogLine` from the helper.
2. Add two refs: `const seq = useRef(0);` and `const lastTs = useRef("");`.
3. In the effect, build the URL from the ref instead of `lines.length`. The backend passes `tail` and `since`
   together to Docker (`SDK.Logs` in `apps/backend/internal/docker/engine.go`), and Docker applies `tail` to the lines
   **after** `since`. So a resume must not pass `tail=0` (that would return none of the missed lines): it passes the
   backend's maximum, `logMaxTail = 5000` in `apps/backend/internal/api/stream.go`. Add next to `maxLines`:

   ```tsx
   // The backend's logMaxTail (internal/api/stream.go): on a resume, take every line since the last one seen.
   const resumeTail = 5000;
   ```
   and in the effect:

   ```tsx
   const since = lastTs.current;
   const source = new EventSource(api.apps.logsUrl(appId, since ? resumeTail : 300, follow, since || undefined));
   ```
4. In the `log` listener:

   ```tsx
   const data = JSON.parse((event as MessageEvent<string>).data) as { ts: string; line: string };
   lastTs.current = data.ts > lastTs.current ? data.ts : lastTs.current;
   setLines((current) => appendLine(current, data, ++seq.current, maxLines));
   ```
5. The effect now reads only `appId`, `follow` and refs, so its dependency array `[appId, follow]` is complete.
   Remove the comment "Existing lines deliberately survive follow reconnects." only if you replace it with one that
   says lines survive and the stream resumes from `lastTs`. Remove any `eslint-disable-next-line
   react-hooks/exhaustive-deps` comment that plan 007 added above this array.
6. "Clear logs" keeps its handler `onClick={() => setLines([])}`. Do **not** reset `lastTs` there, so cleared lines
   do not come back on the next resume.
7. Reset when the app changes by remounting the panel per app. It is rendered in
   `apps/web/src/features/applications/ApplicationDetail.tsx` (near line 88) as
   `{tab === "logs" && <LogsPanel appId={app.id} />}`; change it to `<LogsPanel key={app.id} appId={app.id} />`.
8. Rows: `key={line.id}` instead of `` key={`${line.ts}-${index}`} ``; drop the unused `index` parameter.

**Verify**: `bun run lint && bun run check` → exit 0, with no hooks-lint suppression left in `LogsPanel.tsx`
(`grep -n "eslint-disable" apps/web/src/features/applications/LogsPanel.tsx` → no matches).

### Step 4: Test the helper

`apps/web/src/lib/logBuffer.test.ts` (`bun:test`):
- appends in order and assigns the given ids;
- drops a line whose `ts` is older than the newest buffered line;
- drops an exact duplicate (same `ts`, same `line`) but keeps a different line with the same `ts`;
- trims to `max` keeping the newest, and the surviving lines keep their original ids (this is what keeps React keys
  stable);
- returns the same array instance when a line is dropped.

**Verify**: `bun run test` → all pass. Then `bun run fmt && bun run fmt:check && bun run web:build` → exit 0.

## Test plan

Unit tests for `appendLine` as above. The EventSource wiring itself is not unit-tested (no DOM test setup in this
repository); describe in your report that it was verified only by typecheck and lint, unless you were able to run the
app (`scripts/dev.sh` needs Docker).

## Done criteria

- [ ] `bun run fmt:check && bun run lint && bun run check && bun run web:build` exits 0
- [ ] `bun run test` passes including `logBuffer.test.ts` (if plan 007 landed)
- [ ] `grep -n "lines.length === 0" apps/web/src/features/applications/LogsPanel.tsx` → no matches
- [ ] `grep -n 'key={`${line.ts}' apps/web/src/features/applications/LogsPanel.tsx` → no matches
- [ ] No files outside the in-scope list are modified, except the one-line `key=` addition where `LogsPanel` is rendered
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- `logMaxTail` is no longer 5000, or `tail` and `since` are no longer passed together to Docker (read
  `handleAppLogs` and the `Logs` method in `apps/backend/internal/docker/engine.go`). Report what you found.
- `bun` is unavailable: report "written but not run".

## Maintenance notes

- The backend also honours the standard `Last-Event-ID` header, but `EventSource` only sends it on its own automatic
  reconnects, and only if the server sets event ids; the explicit `since` parameter covers the manual Follow toggle.
- If the buffer size or the backend's `logMaxTail` changes, revisit `resumeTail`: a pause long enough to exceed it
  still loses the oldest lines of the gap (the newest are kept).
