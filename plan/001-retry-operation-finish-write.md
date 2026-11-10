# Plan 001: An operation whose final state cannot be written keeps retrying instead of staying `running` forever

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/operations/controller.go apps/backend/internal/operations/controller_test.go`
> If either file changed since this plan was written, compare the "Current state" excerpts against the live code before
> proceeding; on a mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S–M
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

When an operation ends, the executor writes its final state (`succeeded`, `failed`, …) to SQLite exactly once. If that
write fails (full disk, I/O error), the error is only logged and the row stays `running` for as long as the backend
lives. Nothing retries it. The consequences, all traced in code: the reconciler treats the app as busy forever and
never repairs it (`store.ActiveForTarget` counts `running` rows), a rollout can freeze, scheduled backups for that app
are recorded as `skipped`, and the operator sees an operation that cannot be cancelled. Only a backend restart clears
it (`store.InterruptRunning`). `BACKLOG.md` lists this symptom as "An operation can stay `running` after a forced
cancellation on a full disk". After this plan the write is retried with backoff until it succeeds or the backend
stops, and the operation's claims stay held meanwhile so nothing runs against a target whose state is unrecorded.

## Current state

- `apps/backend/internal/operations/controller.go` — the operation executor.
  - `Deps` struct (starts near line 40) holds tunables such as `CancelPoll` with defaults applied in `NewController`.
  - `dispatch` starts each operation like this (near line 452):

    ```go
    c.workers.Go(func() {
        defer c.Wake() // a finished operation may unblock queued ones
        defer c.end(op.ID)
        c.execute(ctx, op)
    })
    ```

    `c.end` releases the operation's in-memory claims. It runs after `execute` returns.
  - `execute` ends with the single write (near line 664):

    ```go
    if ferr := store.FinishOperation(context.WithoutCancel(ctx), db, op.ID, state, raw, code, msg, guidance); ferr != nil {
        c.Log.Error("finish operation", "op", op.ID, "err", ferr)
    }
    c.notify(op.ID)
    ```

    `ctx` here is the backend's lifetime context (cancelled only when the backend stops).
- `apps/backend/internal/store/operations.go` — `FinishOperation` (near line 248) is one `UPDATE operations SET state=?, …
  finished_at=? WHERE id=?`. It is idempotent: running it again with the same arguments is safe.
- `apps/backend/internal/operations/lifecycle_test.go` — test harness: `newHarness(t)`, `newHarnessWith(t, func(d *Deps){…})`,
  `h.createApp("shop")`, `h.wait(op)`, `h.mustSucceed(op, err)`. Harness tests need root; they call `t.Skip` otherwise.
- Convention: tunables live on `Deps` with a doc comment and a default in `NewController` (see `CancelPoll`: "how often
  a running operation's cancellation flag is read (default 1s)"). Match that.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/operations/ -run 'Finish'` | `ok` (must be root; non-root silently skips) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope** (the only files you should modify):
- `apps/backend/internal/operations/controller.go`
- `apps/backend/internal/operations/controller_test.go` (add a test)
- `BACKLOG.md` (one bullet, step 4)

**Out of scope**:
- `apps/backend/internal/store/operations.go` — `FinishOperation` itself is correct.
- `store.InterruptRunning` / `Controller.Recover` — startup recovery stays as it is.
- Any change to how claims are computed (`claims.go`).

## Git workflow

- Branch: `advisor/001-retry-operation-finish-write`
- One commit; message style is a plain imperative sentence, as in the repo's log, e.g.
  `Keep app Valkey writable when a snapshot fails`. Suggested: `Retry recording an operation's final state until it is written`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Add the tunable and an injectable finish function

In `controller.go`:

1. Add to `Deps`, next to `CancelPoll`, with a doc comment:
   `FinishRetry time.Duration` — "the first delay between attempts to record an operation's final state when the write
   fails (default 250ms); it doubles up to 5s."
2. In `NewController`, where other defaults are applied (look for `if d.Now == nil`), default `FinishRetry` to
   `250 * time.Millisecond` when it is zero.
3. Add an unexported field on `Controller`:
   `finishOp func(ctx context.Context, q store.Q, id string, state store.OpState, result json.RawMessage, code, message, guidance string) error`
   and set it to `store.FinishOperation` in the `c := &Controller{…}` literal. (Check the exact parameter types of
   `store.FinishOperation` and copy them.)

**Verify**: `cd apps/backend && go vet ./internal/operations/` → exit 0.

### Step 2: Retry the write

Replace the single `store.FinishOperation` call in `execute` with a call to a new method:

```go
// recordFinish writes the operation's final state, retrying with backoff
// while the write fails: a row left running blocks its target's
// reconciliation and schedules until the next start. The caller still holds
// the operation's claims, so nothing else runs on the target meanwhile. It
// gives up only when the backend stops; Recover marks the row interrupted on
// the next start.
func (c *Controller) recordFinish(ctx context.Context, id string, state store.OpState, raw json.RawMessage, code, msg, guidance string) {
    delay := c.FinishRetry
    for attempt := 1; ; attempt++ {
        err := c.finishOp(context.WithoutCancel(ctx), c.Store.DB(), id, state, raw, code, msg, guidance)
        if err == nil {
            if attempt > 1 {
                c.Log.Info("finish operation recorded after retries", "op", id, "attempts", attempt)
            }
            return
        }
        // Log the first failure and then every tenth, not every attempt.
        if attempt == 1 || attempt%10 == 0 {
            c.Log.Error("finish operation", "op", id, "attempt", attempt, "err", err)
        }
        select {
        case <-ctx.Done():
            return
        case <-time.After(delay):
        }
        delay = min(delay*2, 5*time.Second)
    }
}
```

Keep the `c.notify(op.ID)` and the reconciler wake that follow the old call exactly where they are, after
`recordFinish` returns.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/operations/` → no output, exit 0.

### Step 3: Test

Add to `controller_test.go` (package `operations`):

`TestFinishWriteIsRetriedUntilRecorded`:
1. `h := newHarnessWith(t, func(d *Deps) { d.FinishRetry = 5 * time.Millisecond })`.
2. Before submitting anything, wrap the finish function so its first 3 calls fail:
   ```go
   var calls atomic.Int32
   real := h.c.finishOp
   h.c.finishOp = func(ctx context.Context, q store.Q, id string, state store.OpState, result json.RawMessage, code, message, guidance string) error {
       if calls.Add(1) <= 3 {
           return errors.New("disk full")
       }
       return real(ctx, q, id, state, result, code, message, guidance)
   }
   ```
3. Run any operation through the harness — `app := h.createApp("shop")` already runs provisioning operations and waits
   for them; if `createApp` itself fails because of the injected errors, instead create the app first and install the
   wrapper afterwards, then submit `h.c.Submit(ctx, Submission{Kind: KindAppReconcile, TargetKind: "app", TargetID: app.ID})`.
4. `got := h.wait(op)`; assert `got.State.Terminal()` and `calls.Load() >= 4`.

`TestFinishRetryStopsWhenBackendStops`: same setup, but the wrapper always fails; submit an operation, wait until
`calls.Load() >= 2` (poll with a 5 s deadline), then call `h.cancel()` and `h.c.Shutdown(2 * time.Second)`; assert the
test returns (no hang) and the row is still `store.OpRunning` (read with `store.GetOperation`). The harness cleanup
calls `cancel`/`Shutdown` again; that is fine.

**Verify**: narrow test command above with `-run 'Finish'` → `ok`, both tests listed with `-v`.

### Step 4: Update the backlog

In `BACKLOG.md`, under "Hardening (low)", replace the bullet that starts
"**An operation can stay `running` after a forced cancellation on a full disk**" with one sentence saying the final
state write is now retried until it succeeds (the mechanism: a failed write was logged and dropped), and that the
"later operations queued behind it" part of the 2026-11-07 observation was not reproduced.

**Verify**: `grep -n "stay \`running\`" BACKLOG.md` → the bullet reflects the new wording.

## Test plan

- Two new tests in `controller_test.go` as described in Step 3, modelled on the existing tests in that file and on
  `lifecycle_test.go` for harness use.
- Verification: `sudo … go test -race ./internal/operations/` → `ok`, as root.

## Done criteria

- [ ] `cd apps/backend && gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] `sudo … go test -race ./internal/operations/ -run 'Finish' -v` shows both new tests PASS (not SKIP)
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] `grep -n "store.FinishOperation" apps/backend/internal/operations/controller.go` shows it only in the
      `Controller` literal (the `finishOp` default), not called directly in `execute`
- [ ] No files outside the in-scope list are modified (`git status`)
- [ ] `plan/README.md` status row updated

## STOP conditions

- The excerpts in "Current state" do not match the live code.
- The race detector reports a race on `finishOp` in the new tests (then the field needs to be set through `Deps`
  instead; report rather than redesign).
- Go or root is unavailable, so the tests cannot run as root. Report "written but not run" — do not claim a pass.
- Any existing test in `./internal/operations/` starts failing or hanging.

## Maintenance notes

- While the write keeps failing, the operation holds its claims and its executor slot. That is intended: the target's
  state is unrecorded. If someone later adds a cap on retries, they must also add a sweep that finishes orphaned
  `running` rows, or the original bug returns.
- The `error` event written just before (`store.AppendEvent`) is still attempted once; on a full disk the journal can
  lack the final error line even though the state is eventually recorded. Left as is on purpose.
- Reviewer: check that `c.end` still runs only after `execute` returns (the claims must be held during retries).
