# Plan 010: A browser terminal ends when the session that opened it is revoked

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/api/terminal.go apps/backend/internal/api/terminal_test.go`
> If either changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

The browser terminal (a shell as the app, or the rclone shell that holds backup-remote credentials) checks the
session, Origin and CSRF token only once, when the WebSocket is opened. After that the connection runs until 30
minutes of idleness or 4 hours of age. Logging out or changing the operator password revokes sessions in the
database, but an already-open terminal keeps working. So if a session was stolen and a shell opened, "change the
password to revoke every session" does not end that shell for up to 4 hours. The database browser already re-checks
the session on every request; this plan gives terminals the same property with a periodic check.

## Current state

- `apps/backend/internal/api/terminal.go`:
  - Constants: `terminalIdle = 30 * time.Minute`, `terminalGrace = 15 * time.Minute`, `terminalMaxAge = 4 * time.Hour`.
  - `serveTerminal` (near line 318) decides the owner:

    ```go
    p, _ := principalFrom(r.Context())
    owner := "local"
    if p.Kind == "session" {
        … // exact Origin + CSRF check
        owner = "session:" + p.Session.TokenHash
    }
    ```
    A terminal opened over the root-only Unix socket has owner `"local"` and no session.
  - After attaching, a goroutine forwards shell output and enforces the idle timer (near line 409):

    ```go
    go func() {
        defer cancel()
        idle := time.NewTimer(terminalIdle)
        defer idle.Stop()
        for {
            idle.Reset(terminalIdle)
            select {
            case <-ctx.Done():
                return
            case chunk, open := <-sub.ch:
                …
            case <-activity:
            case <-idle.C:
                _ = conn.Close(websocket.StatusPolicyViolation, "idle timeout")
                return
            }
        }
    }()
    ```
    `t` is the terminal session object; `t.kill()` ends the shell (it is what the client's `close` control message
    calls). `conn` is the WebSocket.
  - A detached shell can be reattached only by a request with the same scope **and the same owner**, so a revoked
    session can never reattach; a detached shell of a revoked session dies when `terminalGrace` ends.
- `apps/backend/internal/store/sessions.go`:
  `func GetLiveSession(ctx context.Context, q Q, tokenHash string, at time.Time) (Session, error)` returns
  `store.ErrNotFound` when the session is missing, revoked or expired.
- The pattern to copy, `apps/backend/internal/api/dbadmin.go` (near line 418):

  ```go
  if _, err := store.GetLiveSession(r.Context(), s.Store.DB(), pass.sessionHash, time.Now()); err != nil {
      s.dbadmin.revoke(c.Value)
      …
  }
  ```
- Tests: `apps/backend/internal/api/terminal_test.go` (`TestTermSessionReattachReplaysBacklog`,
  `TestTermSessionGraceKillsDetachedShell`) and WebSocket-level terminal tests in
  `apps/backend/internal/api/api_test.go` (search for `terminal`).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/api/ -run 'Term'` | `ok` |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/api/terminal.go`
- `apps/backend/internal/api/terminal_test.go`
- `apps/backend/docs/architecture.md` (one sentence in the section that describes terminals, step 3)

**Out of scope**:
- `apps/backend/internal/api/auth.go` — logout and password change stay as they are; the terminal polls instead.
- Log SSE streams (they end after at most 30 minutes) — not part of this plan.
- The web client (`apps/web/src/components/Terminal.tsx`) — it already shows a closed connection.

## Git workflow

- Branch: `advisor/010-close-terminals-on-session-revocation`
- One commit, plain imperative sentence, e.g. `End a browser terminal when its session is revoked`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: A testable liveness check

In `terminal.go` add:

```go
// terminalSessionCheck is how often an attached terminal re-checks that the
// session that opened it is still live (a variable so tests can shorten it).
var terminalSessionCheck = 30 * time.Second

// terminalOwnerLive reports whether the owner of a terminal may keep using
// it. Socket-owned terminals ("local") have no session. A session owner must
// still be live; a store error other than "not found" is not treated as a
// revocation, so a transient database error never kills a shell.
func (s *Server) terminalOwnerLive(ctx context.Context, owner string) bool {
    hash, ok := strings.CutPrefix(owner, "session:")
    if !ok {
        return true
    }
    _, err := store.GetLiveSession(ctx, s.Store.DB(), hash, time.Now())
    return !errors.Is(err, store.ErrNotFound)
}
```

Check how this file refers to the store (`s.Store.DB()` is what `dbadmin.go` uses) and add missing imports.

**Verify**: `cd apps/backend && go vet ./internal/api/` → exit 0.

### Step 2: Enforce it in the connection loop

In the goroutine shown in "Current state", add a ticker and a case:

```go
check := time.NewTicker(terminalSessionCheck)
defer check.Stop()
…
case <-check.C:
    if !s.terminalOwnerLive(ctx, owner) {
        t.kill()
        _ = conn.Close(websocket.StatusPolicyViolation, "session ended")
        return
    }
```

`owner` is the variable computed at the top of `serveTerminal`. Killing the shell (not just closing the connection)
is deliberate: a detached shell would otherwise live on for the 15-minute grace period.

Important: the `select` loop calls `idle.Reset(terminalIdle)` at the top of every iteration. A ticker firing would
therefore reset the idle timer every 30 seconds and the idle timeout would never fire. Fix that as part of this
change: move the reset so it happens only on real activity — reset the idle timer in the `chunk` and `activity`
cases (and once before the loop), not at the top of the loop. Read the whole loop first and keep its existing
behaviour otherwise.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/api/` → no output, exit 0.

### Step 3: Tests and doc

In `terminal_test.go`:

1. `TestTerminalOwnerLive` — using the same server/store setup other tests in package `api` use (look at how
   `api_test.go` creates a server and a logged-in session):
   - owner `"local"` → true;
   - owner `"session:" + <hash of a live session>` → true;
   - after revoking that session (use whatever the logout path calls in `store`, e.g. a `RevokeSession` function —
     find it with `grep -n "revoked" apps/backend/internal/store/sessions.go`) → false;
   - owner `"session:unknownhash"` → false.
2. If `api_test.go` already has a test that opens a terminal WebSocket successfully as a session (not only the
   Origin/CSRF refusal tests), add `TestTerminalClosesWhenSessionRevoked`: set `terminalSessionCheck = 20 * time.Millisecond`
   (restore with `t.Cleanup`), open the terminal, revoke the session, and assert the next read on the WebSocket
   fails within 2 seconds. If there is no such successful-open test to model on, skip this second test and say so
   in your report.

In `apps/backend/docs/architecture.md`, find the paragraph that describes the terminal (search for
`terminal.go` or "terminalMaxAge" / "reattach") and add one sentence: an attached terminal re-checks its session
every 30 s and kills the shell when the session was revoked or expired.

**Verify**: narrow test command → `ok`, new test(s) PASS.

## Test plan

As step 3. Pattern: existing tests in `terminal_test.go` and the session setup in `api_test.go`.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] `TestTerminalOwnerLive` passes
- [ ] The existing `TestTermSession…` tests still pass
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match (in particular if the loop no longer resets the idle timer at its top).
- `t.kill()` is not safe to call from this goroutine (for example it takes a lock the goroutine already holds) —
  report rather than restructure.
- Go is unavailable: report "written but not run".

## Maintenance notes

- Revocation now takes effect within `terminalSessionCheck` (30 s). If immediate effect is wanted later, logout and
  password change can additionally kill terminals whose owner hash was revoked.
- Session **expiry** now also ends an attached terminal (previously it ran on until idle/max age). That is
  intended; sessions that are extended on activity are unaffected.
- Reviewer: check the idle timeout still fires (the reset moved).
