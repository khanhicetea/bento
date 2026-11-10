# Plan 019: Three small backend and repository fixes (import history, deploy cancel message, stray bytecode file)

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/stack/import.go apps/backend/internal/operations/git.go .gitignore scripts/vps`
> If any changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug / dx
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

Three independent, low-risk defects, each a few lines:

- **A. Imported stacks keep unprunable operation rows.** `bento import` marks operations that were queued or running
  in the archive as cancelled but does not set their finish time. History pruning only deletes rows with a finish
  time, so these rows (and their events) stay forever and show no finish time in the UI.
- **B. A cancelled deploy is reported as a timeout.** When an operator force-cancels a deploy (or the backend stops)
  while `~/deploy.sh` runs, the journal says the script "did not finish within 15m" — misleading guidance.
- **C. A Python bytecode file is committed** (`scripts/vps/__pycache__/dbadmin.cpython-314.pyc`) and `.gitignore`
  does not exclude such files.

## Current state

**A.** `apps/backend/internal/stack/import.go` (near line 204) — a list of SQL statements run in a loop without
arguments:

```go
stmts := []string{
    // Archived running intent is never honored automatically.
    "UPDATE apps SET desired_runtime='stopped', publication='unpublished'",
    // Pending/interrupted work in the archive is never replayed.
    "UPDATE operations SET state='cancelled', error_code='imported', error_message='not replayed after import' WHERE state IN ('queued','running')",
    "DELETE FROM sessions",
    …
}
for _, st := range stmts {
    if _, err := q.ExecContext(ctx, st); err != nil {
        return err
    }
}
```

`apps/backend/internal/store/operations.go`, `PruneHistory`:
`const terminal = "state IN ('succeeded','failed','cancelled','interrupted') AND finished_at IS NOT NULL AND finished_at < ?"`,
with the cutoff formatted by `platform.FormatTime(time.Now().Add(-retention))`. `store.FinishOperation` writes
`finished_at` using the same time format. Tests for import: `apps/backend/internal/stack/import_test.go`.

**B.** `apps/backend/internal/operations/git.go` (near line 471), inside the deploy handler:

```go
execCtx, cancel := context.WithTimeout(ctx, deployScriptTimeout)
res, err := c.Engine.Exec(execCtx, tool.ContainerID, req)
timedOut := execCtx.Err() != nil
cancel()
…
switch {
case timedOut:
    return Fail("deploy-script-timeout", guidance, "~/%s did not finish within %s", DeployScriptName, deployScriptTimeout)
case err != nil:
    return err
```

`ctx` is the operation's context; a forced cancellation or backend shutdown cancels it, which also ends `execCtx`,
so `timedOut` is true for cancellations too. The controller already labels a force-cancelled operation correctly
when the handler returns the underlying error (it wraps it with `ErrForceCancelled`). Deploy tests:
`apps/backend/internal/operations/git_test.go`.

**C.** `git ls-files | grep pyc` → `scripts/vps/__pycache__/dbadmin.cpython-314.pyc`. `.gitignore` (whole file):

```
.bento-stack/
/bento/
/testbento/
/dist/
**/dist/
*.partial
.DS_Store
node_modules/
coverage/
mise.local.toml
```

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/stack/ ./internal/operations/ -run 'Import|Deploy'` | `ok` (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/stack/import.go`, `apps/backend/internal/stack/import_test.go`
- `apps/backend/internal/operations/git.go`, `apps/backend/internal/operations/git_test.go`
- `.gitignore`; removal of `scripts/vps/__pycache__/dbadmin.cpython-314.pyc` from the index

**Out of scope**:
- A schema migration to backfill `finished_at` on already-imported stacks — no production stack exists
  (`BACKLOG.md`, "Decisions").
- The fetch step of deploy (it already reports cancellation correctly).
- Anything else under `scripts/`.

## Git workflow

- Branch: `advisor/019-backend-small-fixes`
- Three commits, one per item, plain imperative sentences, e.g.
  `Give operations cancelled by an import a finish time`,
  `Report a cancelled deploy script as cancelled, not timed out`,
  `Stop tracking Python bytecode`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1 (A): Set `finished_at` on import

Remove the `UPDATE operations …` string from `stmts` and execute it separately, right after the loop (keeping its
comment), with the finish time as an argument:

```go
// Pending/interrupted work in the archive is never replayed. The finish
// time lets history pruning collect these rows.
if _, err := q.ExecContext(ctx,
    "UPDATE operations SET state='cancelled', error_code='imported', error_message='not replayed after import', finished_at=? WHERE state IN ('queued','running')",
    platform.FormatTime(time.Now()),
); err != nil {
    return err
}
```

Confirm `platform.FormatTime` is what `store` uses for `finished_at` (look at `now()` in
`apps/backend/internal/store/`); use the same function. Add the `platform`/`time` imports if the file lacks them.

Test: in `import_test.go`, find a test that imports an archive whose database contains operations (or the helper
that builds the source stack). Add a case where the source database has one operation in state `queued` and one in
`running` before export/import, and assert after import that both rows have state `cancelled` and a non-empty
`finished_at` (query with `store.GetOperation` and check its finished-time field, or a direct
`SELECT finished_at FROM operations`). If no existing test gives you a database with operations to import, insert
the rows into the source database with `store.InsertOperation` (see `data_test.go` in `internal/operations` for a
call example) before the archive is produced.

**Verify**: `sudo … go test -race ./internal/stack/` → `ok`.

### Step 2 (B): Distinguish timeout from cancellation

Change one line in `git.go`:

```go
// Only the script's own deadline is a timeout; a forced cancellation or a
// backend stop also ends execCtx and is reported by its own error.
timedOut := execCtx.Err() != nil && ctx.Err() == nil
```

Then read the rest of the `switch`: with `timedOut` false on a cancellation, control reaches `case err != nil:
return err`. Check what `c.Engine.Exec` returns when its context is cancelled — for the real engine it returns the
context's error. If you find that `err` can be nil on cancellation (so the code would fall through and treat the
deploy as successful), add an explicit case before the others:

```go
case ctx.Err() != nil:
    return context.Cause(ctx)
```

and keep the rest.

Test: `git_test.go` has deploy tests on the fake engine (for example the fault-injection test near line 214). Add a
test that cancels the operation while the script "runs" and asserts the finished operation's `ErrorCode` is **not**
`deploy-script-timeout`. To hold the script, use the harness with a wrapped engine whose `Exec` blocks until its
context ends for the deploy script command (`newHarnessWith(t, func(d *Deps) { d.Engine = … })`, and shorten
`d.CancelGrace`/`d.CancelPoll` to tens of milliseconds); request cancellation the way `cancel_test.go` does. If
`cancel_test.go` shows a simpler established pattern for "cancel mid-exec", use that pattern instead.

**Verify**: `sudo … go test -race ./internal/operations/ -run 'Deploy|Cancel'` → `ok`, new test PASS.

### Step 3 (C): Stop tracking bytecode

```bash
git rm --cached scripts/vps/__pycache__/dbadmin.cpython-314.pyc
```

Append to `.gitignore`:

```
__pycache__/
*.pyc
```

**Verify**: `git ls-files | grep -c '\.pyc$'` → `0`; `git status --short scripts/vps` shows the deletion staged and
no untracked `__pycache__`.

## Test plan

Items A and B each get one test as described; item C is verified by the `git ls-files` check.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] New tests for A and B pass as root; `sudo … make -C apps/backend ci` exits 0
- [ ] `grep -n "finished_at=?" apps/backend/internal/stack/import.go` → one match
- [ ] `git ls-files | grep '\.pyc$'` → no output
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- For B: the existing cancellation tests fail after the change, or you cannot build a "cancel during the script"
  test after reading `cancel_test.go` and `git_test.go` — land A and C, report B as blocked with what you found.
- Go or root is unavailable: do C, write A and B, and report them as "written but not run".

## Maintenance notes

- Stacks imported before this fix keep their unprunable rows; harmless (a few rows), and no production stack exists.
- If more one-off repository scripts in Python are added, the `.gitignore` entries already cover them.
