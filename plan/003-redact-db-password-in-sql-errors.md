# Plan 003: A failing database grant can never put a binding's password into an operation record or log

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/dataservices/`
> If the package changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

The repository's rule 6 says secrets never reach logs, errors, or API responses. When Bento creates a database user
it sends SQL containing the app's generated database password as a literal (over stdin, which is fine). But if that
SQL fails, the client's stderr is copied unredacted into the returned error. For PostgreSQL the statement is a
`DO $$ … $$` block, and when a statement inside such a block fails the server adds a `CONTEXT: SQL statement "…"` line
that repeats the statement — including the password. That error text becomes the operation's `error_message` and an
`error` event (stored in `bento.db`), is written to the backend log, and is readable through the API with the lowest
(`read`) token scope. The trigger is uncommon (disk full, server in recovery, a role-creation race), but when it
fires the password is stored permanently. After this plan the error path replaces known secrets before the text
leaves the package, and psql is told not to print statement context.

## Current state

- `apps/backend/internal/dataservices/services.go`:
  - `SQL` (near line 262) runs administrative SQL over stdin. The PostgreSQL command and the error path:

    ```go
    req.Cmd = []string{"psql", "-U", "postgres", "-d", database, "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1"}
    …
    res, err := m.Engine.Exec(cctx, containerID, req)
    if err != nil {
        return "", err
    }
    if res.ExitCode != 0 {
        return "", fmt.Errorf("%s admin SQL failed: %s", s.Name, redactLine(string(res.Stderr)))
    }
    ```
  - `redactLine` (near line 304) only trims and truncates to 500 characters; despite its name it redacts nothing.
  - `ProvisionBinding` (near line 314) embeds the password in two statements:

    ```go
    fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';\n", user, b.Password)   // MySQL
    ```
    ```go
    role := fmt.Sprintf(`DO $$ BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN
      CREATE ROLE "%s" LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
    END IF;
    END $$;
    `, user, user, b.Password)                                                                    // PostgreSQL
    ```
    Both are passed to `m.SQL(ctx, s, containerID, "", …)`.
- `apps/backend/internal/operations/data.go` (near line 69) turns a `ProvisionBinding` error into
  `Fail("grant-failed", …, "%v", err)`, which is how the text reaches the operation record. Do not change it.
- `apps/backend/internal/dataservices/services_test.go` — unit tests for this package (for example
  `TestEnsureSecretsRefusesToRegenerateEstablishedSecret` shows how a `Manager` is built in tests,
  `TestMySQLGrantIsExactAndRevokesWildcard` shows the assertion style).
- `apps/backend/internal/docker/fake.go` — `docker.NewFake()`; `Fake.ExecHook func(string, docker.ExecRequest) docker.ExecResult`
  lets a test decide the result of every `Exec`. `docker.ExecResult` has `Stdout`, `Stderr`, `ExitCode`.
  `docker.ExecRequest.Stdin` is an `io.Reader` carrying the SQL.
- Convention: redacted text is replaced by the literal `[redacted]` (see `domain.App.Redactor` in
  `apps/backend/internal/domain/model.go`).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/dataservices/` | `ok` |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/dataservices/services.go`
- `apps/backend/internal/dataservices/services_test.go`

**Out of scope**:
- `apps/backend/internal/operations/data.go` and the controller's error recording — the fix belongs at the source.
- Changing how the password is sent (for example a pre-hashed SCRAM verifier). That is a worthwhile follow-up but
  changes what the server stores; it is deliberately not part of this plan.
- `apps/backend/internal/backup/` — restore SQL is not touched here.

## Git workflow

- Branch: `advisor/003-redact-db-password-in-sql-errors`
- One commit, plain imperative sentence, e.g. `Keep a binding's password out of failed grant errors`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Find every statement that embeds a password

Run `grep -n "Password" apps/backend/internal/dataservices/services.go`. At the planned commit the only SQL that
embeds `b.Password` is the two statements shown above. If you find others (for example an `ALTER USER … PASSWORD`),
include them in step 3.

**Verify**: you have a list of call sites; expected 2.

### Step 2: Make the error path redact given secrets, and quiet psql

In `services.go`:

1. Rename the existing `SQL` body into
   `func (m *Manager) sqlRedacting(ctx context.Context, s domain.DataService, containerID, database, sql string, secrets ...string) (string, error)`.
2. Keep `SQL` with its current signature as a one-line wrapper: `return m.sqlRedacting(ctx, s, containerID, database, sql)`.
   (Other packages call `SQL`; its signature must not change.)
3. In `sqlRedacting`, on a non-zero exit code, replace every non-empty secret in the stderr text with `[redacted]`
   **before** calling `redactLine` (truncation must happen after redaction, otherwise a secret cut in half at the
   500-character boundary would survive):

   ```go
   if res.ExitCode != 0 {
       text := string(res.Stderr)
       for _, secret := range secrets {
           if secret != "" {
               text = strings.ReplaceAll(text, secret, "[redacted]")
           }
       }
       return "", fmt.Errorf("%s admin SQL failed: %s", s.Name, redactLine(text))
   }
   ```
4. Rename `redactLine` to `trimLine` (it does not redact) and update its callers in this package
   (`grep -rn "redactLine" apps/backend/internal`).
5. Add `"-v", "VERBOSITY=terse"` to the psql command slice, after `"ON_ERROR_STOP=1"`. Terse verbosity prints only
   severity, SQLSTATE-less message text — no `DETAIL`, `HINT`, `QUERY` or `CONTEXT` lines.

Before step 5, check nothing parses psql's error text:
`grep -rn "admin SQL failed\|DETAIL\|CONTEXT:" apps/backend/internal --include=*.go | grep -v _test`.
Expected: only the `fmt.Errorf` you just edited. If any code matches on detail text, STOP.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./...` → no output, exit 0.

### Step 3: Pass the password at the call sites

In `ProvisionBinding`, change the two calls that send password-bearing SQL from `m.SQL(ctx, s, containerID, "", stmt)`
to `m.sqlRedacting(ctx, s, containerID, "", stmt, b.Password)`. Leave every other `m.SQL` call unchanged.

**Verify**: `grep -n "sqlRedacting" apps/backend/internal/dataservices/services.go` → the definition, the `SQL`
wrapper, and one call per statement found in step 1.

### Step 4: Tests

Add to `services_test.go`:

`TestProvisionErrorNeverContainsThePassword`, table-driven over `domain.EngineMySQL` and `domain.EnginePostgres`:
1. Build a `Manager` with a `docker.NewFake()` engine the same way the existing tests in this file do.
2. Set `fake.ExecHook` to simulate a server that echoes the failing statement, the worst case:
   ```go
   fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
       sql, _ := io.ReadAll(req.Stdin)
       return docker.ExecResult{ExitCode: 1, Stderr: append([]byte("ERROR: boom\nCONTEXT: SQL statement \""), sql...)}
   }
   ```
   (If `req.Stdin` can be nil for some calls, guard for it.)
3. Call `ProvisionBinding` with a `domain.Binding` whose `Username` is `"ua"`, `Databases` is `[]string{"shop"}` and
   `Password` is a 32-character alphanumeric test string (a made-up value such as 32 × `"p"` followed by nothing
   else is fine — it must match `^[A-Za-z0-9]+$`).
4. Assert the returned error is non-nil, that `err.Error()` does **not** contain the password, and that it **does**
   contain `[redacted]`.

Also add `TestPsqlRunsTerse`: with an `ExecHook` that records `req.Cmd`, call `m.SQL` for a PostgreSQL service and
assert the command contains the two consecutive elements `-v`, `VERBOSITY=terse`.

**Verify**: narrow test command → `ok`; `-run 'Password|Terse' -v` shows the new tests PASS.

## Test plan

- New tests above in `services_test.go`, in the style of the existing tests there.
- `sudo … go test -race ./internal/operations/` must still pass (it exercises provisioning through the fake engine).

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] New tests PASS
- [ ] `grep -rn "redactLine" apps/backend/internal` → no matches
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- Something outside this package depends on the detail lines of psql errors (step 2 grep).
- A `Manager` cannot be built in a unit test without a real Docker engine — report what the existing tests do.
- Go is unavailable: report "written but not run".

## Maintenance notes

- Any future statement that embeds a secret must go through `sqlRedacting` with that secret. Reviewers should reject
  a new `m.SQL(…)` whose SQL text contains a password.
- The PostgreSQL server itself logs failing statements at its default `log_min_error_statement=error`; a failed
  `CREATE ROLE … PASSWORD` therefore still appears in the data-service container's own log. Sending a pre-hashed
  SCRAM-SHA-256 verifier instead of the cleartext would close that too; it is deferred because it needs a real
  PostgreSQL to verify and no Docker is available where this plan was written.
- Operation records written before this fix are not scrubbed. No production stack exists yet (see `BACKLOG.md`,
  "Decisions"), so no migration is planned.
