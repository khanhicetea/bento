# Plan 011: Editing an app during its rollout replacement no longer lets a second app be replaced at the same time

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/reconcile/`
> If the package changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

After a Bento upgrade changes a runtime image, the reconciler replaces the running apps on that image **one at a
time**, and pauses the rest if one replacement fails. It tracks this per app in an in-memory `target` (`PendingOp`,
`Rollout`). But when an app's configuration generation changes — which happens the moment an operator saves a
setting or adds a binding, even while that app's replacement is still running — the reconciler throws the target
away and starts a fresh one. The fresh target has no pending operation, so the reconciler no longer counts a rollout
as in flight and submits the next app's replacement while the first is still running. It also never learns whether
the first one failed, so a bad image is not halted. This plan keeps the in-flight fields when the generation changes
and resets only the retry budget.

Confidence note: the mechanism was traced in code but not reproduced on a running stack.

## Current state

`apps/backend/internal/reconcile/reconcile.go`:

- The per-target state (near line 27):

  ```go
  type target struct {
      Failures    int
      NextAttempt time.Time
      PendingOp   string
      LastError   string
      Generation  int64
      // OpTarget and LastFailedAt let a later successful operation on the same
      // target, from any origin, restore the budget (see recovered).
      OpTarget     string
      LastFailedAt string
      // Rollout marks an app whose pending or last failed operation replaces a
      // running instance whose image or configuration drifted (see rollout).
      Rollout bool
      // Paused marks an app whose rollout waits on another app's failed one;
      // LastError then says so.
      Paused bool
  }
  ```
- `get` (near line 299):

  ```go
  // get returns the in-flight pass's target for id, starting a fresh budget
  // when the id is new or its configuration generation changed.
  func (r *Reconciler) get(id string, gen int64) *target {
      t, ok := r.work[id]
      if !ok || (gen != 0 && t.Generation != gen) {
          t = &target{Generation: gen}
          r.work[id] = t
      }
      return t
  }
  ```
- How the fields are used in the app pass (near line 548):

  ```go
  t := r.get(app.ID, app.ConfigGeneration)
  …
  if !r.settle(ctx, t) {
      ro.inFlight = ro.inFlight || t.Rollout
      continue
  }
  ```
  `settle` returns `true` immediately when `t.PendingOp == ""`; otherwise it loads the operation, returns `false`
  while it is not terminal, and on a terminal operation clears `PendingOp` and updates `Failures`/`Rollout`.
  `rollout()` submits the next candidate only when `p.inFlight` is false.
- Tests: `apps/backend/internal/reconcile/reconcile_test.go` (for example `TestRetryBudgetIsBoundedAndObservable`).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/reconcile/` | `ok` (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/reconcile/reconcile.go` (`get` only)
- `apps/backend/internal/reconcile/reconcile_test.go`

**Out of scope**:
- `settle`, `rollout`, `recovered` and the rest of the pass.
- `apps/backend/internal/operations/` — acceptance keeps bumping `ConfigGeneration` immediately.

## Git workflow

- Branch: `advisor/011-keep-rollout-state-across-config-change`
- One commit, plain imperative sentence, e.g. `Keep a rollout in flight when its app's configuration changes`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Carry the in-flight fields over

Replace `get` with:

```go
// get returns the in-flight pass's target for id. A new id starts fresh. A
// changed configuration generation restarts the retry budget, but an
// operation still pending for the target stays tracked: dropping it would
// let a rollout submit the next app while this one's replacement still runs,
// and would lose its failure.
func (r *Reconciler) get(id string, gen int64) *target {
    t, ok := r.work[id]
    if !ok {
        t = &target{Generation: gen}
        r.work[id] = t
        return t
    }
    if gen != 0 && t.Generation != gen {
        t = &target{Generation: gen, PendingOp: t.PendingOp, OpTarget: t.OpTarget, Rollout: t.PendingOp != "" && t.Rollout}
        r.work[id] = t
    }
    return t
}
```

Why exactly these fields: `PendingOp` is what `settle` waits on; `OpTarget` belongs to the pending operation;
`Rollout` only matters while that operation is pending (a finished, failed rollout under the old configuration must
not halt others once the operator has changed the configuration — that is the "fresh budget" the old comment
promised). `Failures`, `NextAttempt`, `LastError`, `LastFailedAt` and `Paused` reset.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/reconcile/` → no output, exit 0.

### Step 2: Test

Add to `reconcile_test.go` (package `reconcile`; check the package clause of the existing file and whether tests
are in-package — if the test file uses an external `reconcile_test` package, put this test in a new in-package file
`get_internal_test.go` with `package reconcile`):

`TestGenerationChangeKeepsPendingOperation`:

```go
r := &Reconciler{work: map[string]*target{}}
r.work["app_1"] = &target{
    Generation: 1, PendingOp: "op_1", OpTarget: "app_1", Rollout: true,
    Failures: 3, LastError: "boom", NextAttempt: time.Now().Add(time.Hour), Paused: true,
}
got := r.get("app_1", 2)
// kept
if got.PendingOp != "op_1" || got.OpTarget != "app_1" || !got.Rollout || got.Generation != 2 { t.Fatalf("%+v", got) }
// reset
if got.Failures != 0 || got.LastError != "" || !got.NextAttempt.IsZero() || got.Paused { t.Fatalf("%+v", got) }
```

plus three small cases: same generation returns the same pointer unchanged; a finished target
(`PendingOp: ""`, `Rollout: true`, `Failures: 2`) with a new generation comes back with `Rollout == false` and
`Failures == 0`; `gen == 0` never replaces an existing target.

If `Reconciler` cannot be constructed as a bare literal (for example `work` has another name or is initialised
elsewhere), adapt the construction to what the code does; the assertions stay the same.

**Verify**: narrow test command → `ok`; `-run 'GenerationChange' -v` shows PASS. All existing reconcile tests pass.

## Test plan

The unit test above. An end-to-end test of "edit during rollout" needs two apps and a controllable slow replacement;
it is not required by this plan, but if `reconcile_test.go` already has a rollout test with two apps
(`grep -n "ollout" apps/backend/internal/reconcile/reconcile_test.go`), add a case that bumps the first app's
`ConfigGeneration` while its replacement is pending and asserts no second replacement is submitted.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] New test passes; existing `./internal/reconcile/` tests pass as root
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- An existing reconcile test fails after the change — it may encode the old "fresh target" behaviour on purpose.
  Report the test name and output; do not edit the existing test.
- Go or root is unavailable: report "written but not run".

## Maintenance notes

- A pending operation submitted under the old generation now counts once against the new generation's budget if it
  fails. That is intended: the failure is real and recent.
- Anyone adding a field to `target` must decide whether it describes the pending operation (carry it in `get`) or
  the retry budget (reset it).
