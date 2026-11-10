# Plan 004: App metrics hide app secrets in process command lines, like every other app-output path

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/api/server.go apps/backend/internal/api/stream.go`
> If either changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

`GET /api/v1/apps/{id}/metrics` returns up to 200 processes of the app's container with their full argument lists. It
needs only the `read` token scope. Every other path that returns app-controlled output (logs, exec output, scheduler
commands and run logs, deploy script output) passes the text through the app's redactor, which replaces binding
passwords and secret-looking env values with `[redacted]`. This one path does not, so a process started with a
credential in its arguments (a queue worker given a DSN, a `mysqldump` job) exposes it to read-scope API tokens and
MCP clients — callers that are deliberately never shown env values.

## Current state

- `apps/backend/internal/api/server.go`, `handleAppMetrics` (near line 1398), ends with:

  ```go
  for _, p := range procs {
      out.Processes = append(out.Processes, dto.AppProcess{
          PID: p.PID, PPID: p.PPID, User: p.User, CPUPercent: p.CPUPercent, MemPercent: p.MemPercent,
          RSSBytes: int64(p.RSSKiB) * 1024, Elapsed: p.Elapsed, Command: p.Command,
      })
  }
  writeJSON(w, http.StatusOK, out)
  ```

  `app` is the `domain.App` loaded at the top of the handler by `s.loadApp(w, r)`; `procs` is `[]docker.Process`.
- `apps/backend/internal/api/stream.go:35` — the helper the other paths use:
  `func redactor(app domain.App) *strings.Replacer { return app.Redactor() }`. Example use in the same file: the log
  stream calls `.Replace(line)` on each line.
- `apps/backend/internal/domain/model.go`, `App.Redactor()` — replaces each binding's `Password` and the value of
  each env var whose key is `SensitiveEnvKey` and whose value has at least 8 characters, with `[redacted]`.
- `apps/backend/internal/api/api_test.go` — API tests for this package.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/api/ -run 'AppProcesses'` | `ok` |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/api/server.go`
- `apps/backend/internal/api/api_test.go` (or a new `apps/backend/internal/api/metrics_test.go`)

**Out of scope**:
- `internal/api/dto` — the response shape does not change (no `make generate-types` needed).
- `internal/docker` — `Top` keeps returning full arguments.
- Changing which scope may call the route.

## Git workflow

- Branch: `advisor/004-redact-process-command-lines`
- One commit, plain imperative sentence, e.g. `Redact app secrets in the process list of app metrics`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Extract and redact

In `server.go`, add a small pure function next to `handleAppMetrics`:

```go
// appProcesses converts container processes for the API. Command lines are
// app-controlled output, so the app's secrets are redacted like in logs.
func appProcesses(app domain.App, procs []docker.Process) []dto.AppProcess {
    out := make([]dto.AppProcess, 0, len(procs))
    hide := redactor(app)
    for _, p := range procs {
        out = append(out, dto.AppProcess{
            PID: p.PID, PPID: p.PPID, User: p.User, CPUPercent: p.CPUPercent, MemPercent: p.MemPercent,
            RSSBytes: int64(p.RSSKiB) * 1024, Elapsed: p.Elapsed, Command: hide.Replace(p.Command),
        })
    }
    return out
}
```

Replace the loop in `handleAppMetrics` with `out.Processes = appProcesses(app, procs)`. The slice must stay non-nil
when there are no processes (`make(…, 0, …)` guarantees it) — the repository requires non-optional slices to
serialize as `[]`, never `null`.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/api/` → no output, exit 0.

### Step 2: Test

Add `TestAppProcessesRedactsAppSecrets` (package `api`):
- Build a `domain.App` literal with one `domain.Binding{Password: "<a made-up 32-character alphanumeric test value>"}`
  in `Bindings`, and one env var in `Runtime.Env` with key `API_TOKEN` and a made-up value of at least 8 characters
  (check the element type of `Runtime.Env` in `apps/backend/internal/domain/model.go` and that
  `domain.SensitiveEnvKey("API_TOKEN")` is true; pick another key if not).
- Pass two `docker.Process` values whose `Command` contains the binding password and the env value respectively, and
  one with an ordinary command (`php-fpm: pool www`).
- Assert: neither secret appears in any returned `Command`; both redacted commands contain `[redacted]`; the ordinary
  command is unchanged; `appProcesses(app, nil)` returns a non-nil empty slice.

**Verify**: narrow test command → `ok`, test PASS.

## Test plan

- One new unit test as above. No Docker, no root needed for this test itself.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] `TestAppProcessesRedactsAppSecrets` passes
- [ ] `grep -n "Command: p.Command" apps/backend/internal/api/server.go` → no matches
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- `redactor` or `App.Redactor` no longer exists under that name.
- Go is unavailable: report "written but not run".

## Maintenance notes

- The redactor only knows binding passwords and env values with secret-looking names. A credential typed directly
  on a command line that is not also an env value is still shown. If that matters later, the stricter option is to
  return only the executable name to restricted (non-full-scope) callers.
- Reviewer: any new endpoint returning app-controlled text must use `redactor(app)`.
