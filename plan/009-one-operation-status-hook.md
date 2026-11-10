# Plan 009: Every screen that watches an operation refreshes the data that operation changed

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/web/src/features/operations apps/web/src/features/applications/CloneFromBackupDialog.tsx apps/web/src/features/applications/DeployPanel.tsx`
> If any changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S–M
- **Risk**: LOW
- **Depends on**: plan/007-web-test-baseline-and-hooks-lint.md (for the test in step 4; skip that step if 007 has not landed)
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

Four components poll an operation's status with the **same** React Query key, each with its own fetch function. Only
one of them (the toast tracker) refreshes the data the operation changed when it sees the operation finish. React
Query deduplicates fetches per key and runs the fetch function of whichever observer triggered the request. So if the
operator opens the operation's page from the toast link, that page's fetch may be the one that sees "succeeded" — and
it refreshes nothing. The toast then stops polling because the cached data is final. Hosts, services, edge, tunnel,
backups and retired data have no polling of their own, so the screen stays stale until the window regains focus.
After this plan there is one hook with one fetch function, and the refresh fires once whenever any observer first
sees a final state.

## Current state

All paths are under `apps/web/src/`.

- `features/operations/OperationTracker.tsx`:
  - `isTerminal(state)` and `invalidateFor(queryClient, op)` (lines 11–62; `invalidateFor` is not exported). The
    comment in `invalidateFor` explains it invalidates operation **lists** only, never the detail query, "which would
    cancel the fetch that observed the terminal state". Keep that property.
  - The toast's query (near line 119):

    ```tsx
    const query = useQuery({
      queryKey: keys.operations.detail(initial.id),
      queryFn: async ({ signal }) => {
        const op = await api.operations.get(initial.id, signal);
        if (isTerminal(op.state)) invalidateFor(queryClient, op);
        return op;
      },
      initialData: initial,
      refetchInterval: (q) => (isTerminal(q.state.data?.state) ? false : 1000),
    });
    ```
- `features/operations/OperationsPage.tsx`, `OperationDetail` (near line 121):

  ```tsx
  const query = useQuery({
    queryKey: keys.operations.detail(id),
    queryFn: ({ signal }) => api.operations.get(id, signal),
    refetchInterval: (current) => (isTerminal(current.state.data?.state) ? false : 1_000),
  });
  const cancel = useMutation({
    mutationFn: () => api.operations.cancel(id),
    onSuccess: (op) => queryClient.setQueryData(keys.operations.detail(id), op),
  });
  ```
- `features/applications/CloneFromBackupDialog.tsx`, `useOperationState(id)` (near line 31): same key, invalidates
  only `keys.apps.list()` on a final state, `enabled: id !== ""`.
- `features/applications/DeployPanel.tsx`, `OperationTrace` (near line 376): same key, no invalidation.
- `api/keys.ts`: `keys.operations.detail(id)` is `["operations", "detail", id]`.
- Repository rules (`AGENTS.md`): remote state goes through `@tanstack/react-query`; use key factories in
  `api/keys.ts`; "invalidate the narrowest affected keys"; no `useMemo`/`useCallback`/`React.memo`.

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
- `apps/web/src/features/operations/useOperation.ts` (create)
- `apps/web/src/features/operations/useOperation.test.ts` (create, if plan 007 landed)
- `apps/web/src/features/operations/OperationTracker.tsx`
- `apps/web/src/features/operations/OperationsPage.tsx`
- `apps/web/src/features/applications/CloneFromBackupDialog.tsx`
- `apps/web/src/features/applications/DeployPanel.tsx`

**Out of scope**:
- Which keys `invalidateFor` invalidates per target kind — move the function unchanged.
- The toast UI, the auto-dismiss timer (plan 020), `api/keys.ts`, `api/client.ts`.

## Git workflow

- Branch: `advisor/009-one-operation-status-hook`
- One commit, plain imperative sentence, e.g. `Refresh affected data whichever view sees an operation finish`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Create the shared module

Create `features/operations/useOperation.ts`:

1. Move `terminalStates`, `isTerminal` and `invalidateFor` from `OperationTracker.tsx` into it, unchanged, and export
   `isTerminal` and `invalidateFor`. In `OperationTracker.tsx` re-export so existing importers keep working:
   `export { isTerminal } from "./useOperation.ts";` (check importers with
   `grep -rn "isTerminal" apps/web/src`).
2. Add a pure function and the hook:

   ```ts
   /** True when a fetch is the first to observe the operation in a final state. */
   export function justFinished(previous: T.Operation | undefined, next: T.Operation): boolean {
     return isTerminal(next.state) && !isTerminal(previous?.state);
   }

   /**
    * Polls one operation until it is final. Every view of an operation uses
    * this hook, so whichever observer's fetch first sees the final state
    * refreshes the data the operation changed, exactly once.
    */
   export function useOperation(id: string, options: { initialData?: T.Operation } = {}) {
     const queryClient = useQueryClient();
     const queryKey = keys.operations.detail(id);
     return useQuery({
       queryKey,
       queryFn: async ({ signal }) => {
         const op = await api.operations.get(id, signal);
         if (justFinished(queryClient.getQueryData<T.Operation>(queryKey), op)) invalidateFor(queryClient, op);
         return op;
       },
       enabled: id !== "",
       initialData: options.initialData,
       refetchInterval: (query) => (isTerminal(query.state.data?.state) ? false : 1_000),
     });
   }
   ```
   Note on `initialData`: when it is `undefined`, React Query treats the query as having no initial data, which is
   what the three non-toast callers need. If TypeScript's overloads reject the optional `initialData` (the return
   type differs when it is defined), write two small exported hooks instead — `useOperation(id)` and
   `useTrackedOperation(initial: T.Operation)` — sharing one `fetchOperation(queryClient, id, signal)` function that
   contains the `justFinished` logic. Either form satisfies this plan.

**Verify**: `bun run check` → exit 0.

### Step 2: Switch the four callers

- `OperationTracker.tsx`, `TrackedOperation`: replace its `useQuery` with the hook, passing `initial` as initial data.
  `query.data` must stay non-undefined there (it is used without a null check); if the single-hook form makes it
  possibly undefined, use the two-hook form from step 1.
- `OperationsPage.tsx`, `OperationDetail`: replace its `useQuery` with `useOperation(id)`. Change the cancel mutation:

  ```tsx
  onSuccess: (op) => {
    const key = keys.operations.detail(id);
    if (justFinished(queryClient.getQueryData<T.Operation>(key), op)) invalidateFor(queryClient, op);
    queryClient.setQueryData(key, op);
  },
  ```
- `CloneFromBackupDialog.tsx`: delete the local `useOperationState` and use `useOperation(id)` at its call sites.
  (The shared `invalidateFor` already invalidates `keys.apps.all` for app-targeted operations, which covers the old
  `keys.apps.list()` invalidation. Check the `targetKind` of the clone operations in
  `apps/backend/internal/operations/restic_clone.go` and `restic_remote.go` — search for `TargetKind:`. If a clone
  operation's target kind is not `"app"`, `invalidateFor` falls to its `default:` branch, which invalidates
  everything except operations; that also covers the app list. Either way no extra code is needed; say which case
  applies in your report.)
- `DeployPanel.tsx`, `OperationTrace`: replace its `useQuery` with `useOperation(id)`.

Remove imports that became unused in each file.

**Verify**: `grep -rn "keys.operations.detail" apps/web/src --include=*.tsx --include=*.ts | grep -v test` → matches
only in `useOperation.ts` and the cancel handler in `OperationsPage.tsx`. `bun run lint && bun run check` → exit 0.

### Step 3: Build and format

**Verify**: `bun run fmt && bun run fmt:check && bun run web:build` → exit 0.

### Step 4: Tests (if plan 007 landed)

`features/operations/useOperation.test.ts` with `bun:test`:
- `justFinished`: undefined → `running` is false; undefined → `succeeded` is true; `running` → `failed` is true;
  `succeeded` → `succeeded` is false.
- `invalidateFor`: pass a fake query client object `{ invalidateQueries: (arg) => calls.push(arg) }` cast to
  `QueryClient`, and assert for an operation with `targetKind: "service"` that the recorded query keys include
  `keys.services`, `keys.operations.lists`, `keys.system` and `keys.reconcile`, and do **not** include
  `keys.operations.all` (that would cancel the observing fetch).

**Verify**: `bun run test` → all pass.

## Test plan

As step 4. The hook itself is not rendered in tests (no DOM setup); the pure decision (`justFinished`) and the
invalidation map are what is tested.

## Done criteria

- [ ] `bun run fmt:check && bun run lint && bun run check && bun run web:build` exits 0
- [ ] `grep -rn "api.operations.get" apps/web/src --include=*.tsx` → no matches (only `useOperation.ts` calls it)
- [ ] `bun run test` passes (if plan 007 landed)
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match, or a fifth component uses `keys.operations.detail` in a way not listed here.
- Typecheck cannot be satisfied with either hook form in step 1 after two attempts.
- `bun` is unavailable: report "written but not run".

## Maintenance notes

- Any new view of a single operation must use this hook; a hand-written `useQuery` on `keys.operations.detail`
  reintroduces the bug. Reviewers can grep for `keys.operations.detail`.
- Opening an old, already-finished operation in Activity now triggers one refresh of the data for its target (the
  cache had no previous state for it). That is cheap: only mounted queries refetch.
- New operation target kinds need a case in `invalidateFor`; unknown kinds fall back to refreshing everything
  except operations.
