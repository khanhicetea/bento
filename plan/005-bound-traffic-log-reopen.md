# Plan 005: The traffic log rotation cannot hold the edge lock forever

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/operations/traffic.go apps/backend/internal/operations/traffic_test.go`
> If either changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

The periodic traffic cycle rotates the edge's access logs and then asks the edge Nginx to reopen its log files with a
`docker exec`. It does this while holding `edgeMu`, the mutex that every edge change takes (app start, stop, update,
publish, remove, reconcile). The exec runs on the backend's lifetime context, and `Engine.Exec` has no deadline of
its own by design. If the Docker daemon stalls the exec, or `nginx -s reopen` hangs, `edgeMu` is held until the
backend restarts, and every operation that touches the edge blocks in a mutex wait that forced cancellation cannot
interrupt. A 30-second deadline on this one call removes that failure mode; a missed reopen is already handled (the
next cycle signals again).

## Current state

- `apps/backend/internal/operations/traffic.go`, `rotateTrafficLogs` (near line 185):

  ```go
  func (c *Controller) rotateTrafficLogs(ctx context.Context) (reopened bool, err error) {
      c.edgeMu.Lock()
      defer c.edgeMu.Unlock()
      … // renames live logs into pending directories
      if renamed == 0 && !c.anyTrafficPending() {
          return false, nil
      }
      id, running, err := c.edgeContainer(ctx)
      if err != nil {
          return false, err
      }
      if !running {
          return false, nil
      }
      if err := c.edgeControl(ctx, id, "reopen"); err != nil {
          return false, fmt.Errorf("reopen edge logs: %w", err)
      }
      return true, nil
  }
  ```

  The comment above it explains the retry design: "Signal also when nothing was renamed but rotated files wait: an
  earlier run may have renamed them and then failed to deliver the signal".
- `apps/backend/internal/operations/edge.go` — `edgeControl` (near line 270) runs `nginx … -s <action>` through
  `c.Engine.Exec(ctx, …)`. Other callers of `edgeControl` run inside operations, whose contexts are cancelled by a
  forced cancellation; only the traffic cycle uses the backend's lifetime context.
- `apps/backend/internal/docker/deadline.go` documents: "Calls with no deadline of their own are bounded only by
  their context: Wait and Exec …".
- Tests: `apps/backend/internal/operations/traffic_test.go` (for example `TestTrafficUndeliveredReopenDefersReading`
  shows how a traffic cycle is driven and how a failed reopen is asserted). The fake engine's `Exec` ignores its
  context, so a blocking exec must be simulated with a wrapped engine: `newHarnessWith(t, func(d *Deps) { d.Engine = … })`
  (its comment says the hook exists to install "a wrapped engine").

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/operations/ -run 'Traffic'` | `ok` (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/operations/traffic.go`
- `apps/backend/internal/operations/traffic_test.go`

**Out of scope**:
- `edgeControl` and `edgeContainer` in `edge.go` — their other callers already have operation contexts.
- Making `edgeMu` context-aware — a larger change, deliberately not done here.
- `internal/docker` — do not add a deadline to `Exec` itself; long jobs rely on it having none.

## Git workflow

- Branch: `advisor/005-bound-traffic-log-reopen`
- One commit, plain imperative sentence, e.g. `Give the traffic cycle's edge log reopen a deadline`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Add the deadline

In `traffic.go`:

1. Add a package-level variable (a variable, so a test can shorten it):

   ```go
   // trafficReopenTimeout bounds the edge calls the traffic cycle makes while
   // holding edgeMu. The cycle runs on the backend's lifetime context, which
   // nothing cancels: a stalled exec would otherwise block every edge change
   // until a restart. A missed reopen is retried by the next cycle.
   var trafficReopenTimeout = 30 * time.Second
   ```
2. In `rotateTrafficLogs`, just before `c.edgeContainer(ctx)`:

   ```go
   ectx, cancel := context.WithTimeout(ctx, trafficReopenTimeout)
   defer cancel()
   ```
   and pass `ectx` to both `c.edgeContainer` and `c.edgeControl`.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/operations/` → no output, exit 0.

### Step 2: Test

Add `TestTrafficReopenTimesOutAndReleasesTheEdge` to `traffic_test.go`:

1. Define in the test file an engine wrapper that embeds `docker.Engine` and overrides `Exec` to block until its
   context ends when a flag is set:

   ```go
   type stallingEngine struct {
       docker.Engine
       stall atomic.Bool
   }

   func (e *stallingEngine) Exec(ctx context.Context, id string, req docker.ExecRequest) (docker.ExecResult, error) {
       if e.stall.Load() {
           <-ctx.Done()
           return docker.ExecResult{}, ctx.Err()
       }
       return e.Engine.Exec(ctx, id, req)
   }
   ```
2. Build the harness with it: keep a pointer, and in the `newHarnessWith` hook set
   `d.Engine = &stallingEngine{Engine: d.Engine}` (store the pointer in a variable the test can reach). Note that
   `h.fake` still refers to the inner fake.
3. Set `trafficReopenTimeout = 50 * time.Millisecond` and restore the old value with `t.Cleanup`.
4. Set up a running edge with a non-empty live access log exactly as `TestTrafficUndeliveredReopenDefersReading`
   does, set `stall` to true, and call the same traffic-cycle entry point that test calls.
5. Assert: the call returns within 2 seconds (run it in a goroutine and `select` on a result channel vs
   `time.After(2 * time.Second)`), it reports the reopen as not delivered (same assertion the existing test makes for
   a failed reopen), and afterwards `h.c.edgeMu.TryLock()` returns true (then `Unlock`).

**Verify**: narrow test command → `ok`; the new test PASSes and the existing traffic tests still pass.

## Test plan

- One new test as above, modelled on `TestTrafficUndeliveredReopenDefersReading`.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] `sudo … go test -race ./internal/operations/ -run 'Traffic' -v` → all PASS, including the new test (not SKIP)
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- The harness cannot take a wrapped engine because other code type-asserts `Deps.Engine` to `*docker.Fake` — report
  where.
- Go or root is unavailable: report "written but not run".

## Maintenance notes

- Any new call made under `edgeMu` from a non-operation context (a background loop) needs its own deadline for the
  same reason.
- If `edgeMu` is later replaced by a context-aware lock, this timeout should stay: it bounds the holder, not the
  waiters.
