# Plan 016: The app settings form keeps working after the app changes elsewhere

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/web/src/features/applications/ApplicationEditor.tsx apps/web/src/features/applications/EnvEditor.tsx`
> If either changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED (the most-used form; state handling changes)
- **Depends on**: plan/007-web-test-baseline-and-hooks-lint.md (lint rules; and it moves `envToText`/`envFromText` to
  `apps/web/src/lib/env.ts` — this plan works either way)
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

The app settings form copies the app's `configGeneration` and about ten fields into component state once, when it
mounts, and never updates them. Every Save sends `expectedGeneration`; the backend rejects a save whose generation
is not current ("app configuration generation is N+1, not N"). The generation increases whenever the app's
configuration changes — in another browser tab, through the CLI, or simply by adding a data binding on the same
page. From then on **every** Save in **every** cell of the open form fails with that raw message, and the only way
out is to leave the tab, losing unsaved edits in other cells. The repository's own rule says remote state must not
be mirrored in component state; the form needs drafts, but they must follow the server when the server changes.

## Current state

`apps/web/src/features/applications/ApplicationEditor.tsx` (384 lines). Read the whole file before changing it.

- The save helper (near line 39):

  ```tsx
  function useCellSave(app: T.App, generation: number, setGeneration: (generation: number) => void, body: () => T.UpdateAppRequest) {
    const track = useTrackOperation();
    return useMutation({
      mutationFn: () => api.apps.update(app.id, { ...body(), expectedGeneration: generation }),
      onSuccess: (accepted) => {
        setGeneration(accepted.app?.configGeneration ?? generation);
        track(accepted);
      },
    });
  }
  ```
- The component's state (near line 80):

  ```tsx
  export function ApplicationEditor({ app }: { app: T.App }) {
    const catalog = useCatalog();
    const active = useActiveOperations(app.id);
    const [generation, setGeneration] = useState(app.configGeneration);
    const kind = app.runtime.kind;
    const [php, setPhp] = useState<T.PHPRuntime>(app.runtime.php ?? { version: "8.4", documentRoot: "public", routing: "front-controller", mode: "standard", uploadLimitMb: 64 });
    const [http, setHttp] = useState<T.HTTPRuntime>(app.runtime.http ?? { toolchain: "node", version: "24", argv: ["node", "server.js"], workdir: "", port: 3000 });
    const [ingress, setIngress] = useState<T.IngressMode>(app.ingress);
    const [accessLog, setAccessLog] = useState(app.accessLog);
    const [protection, setProtection] = useState<T.Protection>(app.protection);
    const [valkey, setValkey] = useState(app.valkey);
    const [minicrond, setMinicrond] = useState(app.minicrond);
    const [hostDialog, setHostDialog] = useState(false);
    const [resources, setResources] = useState<T.Resources>(app.resources);
    const env = useEnvDraft(app.env);
  ```
- Five cells save independent slices (near line 110):

  ```tsx
  const saveRuntime = useCellSave(app, generation, setGeneration, () => ({
    runtime: kind === "php-fpm" ? { kind, php: phpWith(savedPhp, php, phpRuntimeKeys) } : { kind, http },
  }));
  const savePerformance = useCellSave(app, generation, setGeneration, () => ({
    runtime: { kind, php: phpWith(savedPhp, php, phpPerformanceKeys) },
  }));
  const saveIngress = useCellSave(app, generation, setGeneration, () => ({ ingress, accessLog, protection }));
  const saveResources = useCellSave(app, generation, setGeneration, () => ({ resources, valkey, minicrond }));
  const saveEnv = useCellSave(app, generation, setGeneration, () => ({ env: env.value() }));
  ```
  `phpRuntimeKeys` and `phpPerformanceKeys` are arrays of `keyof T.PHPRuntime` defined at the top of the file;
  `phpWith(base, draft, keys)` returns `base` with only `keys` taken from `draft`. The one `php` draft object is
  shared by the runtime cell and the performance cell.
- `apps/web/src/features/applications/EnvEditor.tsx`, `useEnvDraft(initial)` holds `mode` (`"table" | "text"`),
  `rows` (seeded from `initial`) and `text`, and returns `{ mode, rows, text, setRows, setText, toggle(), value() }`.
- Backend, `apps/backend/internal/operations/accept.go` (near line 286): a non-zero `ExpectedGeneration` that differs
  from the stored generation returns a precondition error. Do not change the backend.
- How the app prop is refreshed: when a save is accepted, `track(accepted)` invalidates the app queries, so `app`
  arrives again with the new generation shortly after.
- The accepted pattern in this codebase for "form follows saved data": remount on a key derived from the saved
  value, e.g. `apps/web/src/features/routing/RoutingPage.tsx`:
  `<EdgeConfigCell key={JSON.stringify(listeners)} status={status} />`.
- Repository rules (`AGENTS.md`): no `useMemo`/`useCallback`/`React.memo`; remote state is never fetched with
  `useEffect`. Do not use `useEffect` to copy props into state.

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
- `apps/web/src/features/applications/ApplicationEditor.tsx`
- `apps/web/src/features/applications/EnvEditor.tsx` (add a `reset` to `useEnvDraft`)
- `apps/web/src/lib/savedSlice.ts` and `apps/web/src/lib/savedSlice.test.ts` (create)

**Out of scope**:
- The form's fields, layout, labels and validation.
- `CreateApplicationPage.tsx` (it shares field markup with the editor; consolidating the two is a separate piece of
  work listed in `plan/README.md`).
- The backend generation check.

## Design

Keep one component. Two changes:

1. **Generation is derived, not frozen.** Keep the local state only as "the newest generation a save of ours
   returned", and use the larger of that and the prop:
   `const generation = Math.max(savedGeneration, app.configGeneration);`
2. **Each cell's draft is re-seeded when that cell's saved value changes.** For each of the five slices, compute a
   string key from the saved value (`JSON.stringify`). Remember the key last seen in state. During render, if the
   key differs from the remembered one, store the new key and set that slice's draft state from `app`. This is
   React's documented "adjust state when a prop changes" pattern (set state during render, guarded by a
   comparison); it needs no effect.

   Consequences, which are the intended trade-offs:
   - A cell the operator has not touched follows the server silently.
   - After the operator's own save, the saved slice equals the draft, so re-seeding changes nothing visible.
   - Unsaved edits in a cell survive changes to **other** slices (this is what remounting the whole form would
     break).
   - If the **same** slice changes elsewhere while the operator has unsaved edits in it, those edits are replaced
     by the server's values. Saving them would have overwritten the other change; the edge settings form already
     behaves this way.

## Steps

### Step 1: The slice-key helper

Create `apps/web/src/lib/savedSlice.ts`:

```ts
/** A stable string for comparing a saved value across renders. Object keys are sorted so key order never matters. */
export function sliceKey(value: unknown): string {
  return JSON.stringify(value, (_key, v) =>
    v && typeof v === "object" && !Array.isArray(v)
      ? Object.fromEntries(Object.entries(v as Record<string, unknown>).sort(([a], [b]) => a.localeCompare(b)))
      : v,
  );
}

/** Pick the listed keys of an object (a saved slice of a larger value). */
export function pick<T extends object, K extends keyof T>(value: T, keys: readonly K[]): Pick<T, K> {
  const out = {} as Pick<T, K>;
  for (const key of keys) out[key] = value[key];
  return out;
}
```

**Verify**: `bun run check` → exit 0.

### Step 2: `useEnvDraft` can be re-seeded

In `EnvEditor.tsx` add to the object `useEnvDraft` returns:

```ts
/** Replace the draft with saved variables (the saved value changed elsewhere). */
reset(saved: T.EnvVar[]) {
  setRows(saved);
  setText(envToText(saved));
},
```

**Verify**: `bun run check` → exit 0.

### Step 3: Derive the generation

In `ApplicationEditor`:

```tsx
const [savedGeneration, setGeneration] = useState(app.configGeneration);
// The newer of what our last save returned and what the app query now shows:
// the app also changes elsewhere (another tab, the CLI, an added binding).
const generation = Math.max(savedGeneration, app.configGeneration);
```

Nothing else about `useCellSave` changes.

**Verify**: `bun run check` → exit 0.

### Step 4: Re-seed each slice

Still in `ApplicationEditor`, after all the `useState` lines and `useEnvDraft`, add one block per slice. Define the
default objects that are currently inline in `useState(...)` as module-level constants (`defaultPhp`, `defaultHttp`)
so the same values seed and re-seed. Example for the ingress slice:

```tsx
const ingressSaved = sliceKey({ ingress: app.ingress, accessLog: app.accessLog, protection: app.protection });
const [ingressSeen, setIngressSeen] = useState(ingressSaved);
if (ingressSaved !== ingressSeen) {
  setIngressSeen(ingressSaved);
  setIngress(app.ingress);
  setAccessLog(app.accessLog);
  setProtection(app.protection);
}
```

The five slices and what each re-seeds:

| Slice | Key is computed from | Re-seed |
|---|---|---|
| runtime | `kind === "php-fpm" ? pick(app.runtime.php ?? defaultPhp, phpRuntimeKeys) : (app.runtime.http ?? defaultHttp)` | PHP: `setPhp((cur) => phpWith(cur, app.runtime.php ?? defaultPhp, phpRuntimeKeys))`; HTTP: `setHttp(app.runtime.http ?? defaultHttp)` |
| performance (PHP only) | `pick(app.runtime.php ?? defaultPhp, phpPerformanceKeys)` | `setPhp((cur) => phpWith(cur, app.runtime.php ?? defaultPhp, phpPerformanceKeys))` |
| ingress | `{ ingress, accessLog, protection }` of `app` | `setIngress`, `setAccessLog`, `setProtection` |
| resources | `{ resources, valkey, minicrond }` of `app` | `setResources`, `setValkey`, `setMinicrond` |
| env | `app.env` | `env.reset(app.env)` |

Notes:
- The runtime and performance slices share the `php` draft; re-seed only that slice's keys with `phpWith(current,
  saved, keys)` so the other cell's unsaved edits survive. Check `phpWith`'s parameter order in the file
  (`phpWith(base, draft, keys)` returns `base` with `keys` copied from `draft`), so the call is
  `phpWith(cur, saved, keys)`.
- All hooks (`useState`) must stay unconditional and in a fixed order; only the `if (… !== …Seen) { set… }` bodies
  are conditional.
- Do not wrap any of this in `useEffect`, `useMemo` or `useCallback`.

**Verify**: `bun run lint && bun run check` → exit 0.

### Step 5: Tests and build

`apps/web/src/lib/savedSlice.test.ts` (if plan 007 landed): `sliceKey` gives equal strings for objects with the same
content in different key order, different strings for different values, and keeps array order significant; `pick`
returns only the listed keys.

**Verify**: `bun run test` → all pass. `bun run fmt && bun run fmt:check && bun run web:build` → exit 0.

### Step 6: Manual check (only if you can run the app; needs Docker via `scripts/dev.sh`)

Open an app's Settings tab in two browser tabs. In tab A change "Access log" and save. Switch to tab B (the window
focus refetches the app): its Ingress cell shows the new value; change a Resources field in tab B and save — it is
accepted, with no "configuration generation" error. If you cannot run the app, say so in your report; do not claim
this check.

## Test plan

Unit tests for the two pure helpers. The re-seeding itself is render logic; without a DOM test setup it is verified
by typecheck, lint (hooks rules, once plan 007 landed) and the manual check in step 6 where possible.

## Done criteria

- [ ] `bun run fmt:check && bun run lint && bun run check && bun run web:build` exits 0
- [ ] `grep -n "useEffect\|useMemo\|useCallback" apps/web/src/features/applications/ApplicationEditor.tsx` → no new matches compared with the planned commit
- [ ] `grep -n "Math.max(savedGeneration, app.configGeneration)" apps/web/src/features/applications/ApplicationEditor.tsx` → one match
- [ ] `bun run test` passes (if plan 007 landed)
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated, stating whether step 6 was run

## STOP conditions

- "Current state" excerpts do not match (for example the cells have already been split into components).
- React reports "Too many re-renders" in the build or at runtime: a re-seed changes its own key. Report which slice.
- The hooks lint rule flags setting state during render in a way you cannot satisfy without an effect — report; do
  not add an effect.
- `bun` is unavailable: report "written but not run".

## Maintenance notes

- A new editable field must be added to its slice's key **and** its re-seed, or it will not follow outside changes.
  Reviewers should check both.
- If operators lose in-progress edits too often (the same cell changed elsewhere), the next step is a visible
  "Changed elsewhere — reload this cell" notice instead of a silent re-seed. Deliberately not built now.
- `RoutingPage.tsx` has a similar copy-once field (the Utils base URL form); it is not changed here.
