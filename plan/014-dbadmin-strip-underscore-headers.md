# Plan 014: The database-browser gateway drops header names that PHP would confuse with its injected credentials

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/api/dbadmin.go apps/backend/internal/api/api_test.go apps/backend/templates/dbadmin/router.php`
> If any changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: security (hardening)
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

The database browser (Adminer) sits behind a gateway that injects one binding's connection details as request
headers (`X-Bento-Server`, `X-Bento-Username`, `X-Bento-Password`, `X-Bento-Databases`, …) after removing any such
headers the browser sent. The documented invariant is that the browser can never choose the server, user, password
or database. The removal matches names starting with `x-bento-` (with hyphens). Adminer runs under PHP's built-in
server, which exposes headers as `$_SERVER['HTTP_…']` with `-` turned into `_` — so a client header spelled
`X_Bento_Server` (underscores) maps to the **same** PHP variable as the injected `X-Bento-Server`, and it is not
removed. Which copy PHP keeps depends on header order. This could not be tested where the plan was written (no PHP,
no Docker), so treat it as a plausible gap rather than a proven one; the fix is cheap and removes the ambiguity. The
practical exposure is limited: it needs a valid, session-bound grant, i.e. the operator's own browser session.

## Current state

- `apps/backend/internal/api/dbadmin.go`, the reverse proxy's `Rewrite` (near line 318):

  ```go
  h := pr.Out.Header
  for k := range h {
      if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-bento-") || strings.HasPrefix(lk, "x-forwarded-") {
          h.Del(k)
      }
  }
  for _, k := range []string{"Authorization", "Forwarded", "Cookie", "Proxy-Authorization"} {
      h.Del(k)
  }
  …
  h.Set("X-Bento-Gateway-Token", t.token)
  h.Set("X-Bento-Driver", driver)
  h.Set("X-Bento-Server", t.binding.Service)
  h.Set("X-Bento-Username", t.binding.Username)
  h.Set("X-Bento-Password", base64.StdEncoding.EncodeToString([]byte(t.binding.Password)))
  h.Set("X-Bento-Databases", strings.Join(t.binding.Databases, ","))
  ```
- `apps/backend/templates/dbadmin/router.php` reads them:

  ```php
  function bento_header(string $name): string
  {
      $key = 'HTTP_' . strtoupper(str_replace('-', '_', $name));
      $value = $_SERVER[$key] ?? '';
      …
  ```
- Test: `apps/backend/internal/api/api_test.go`, `TestDBAdminTicketGateway` (near line 478). It redeems a ticket,
  obtains the grant cookie, and sends requests through the gateway with a helper
  `get(path, hdr map[string]string, cookies …)`. Read the whole test to see how it stands in for the Adminer upstream
  and how (or whether) it inspects the headers the upstream received.
- Note: changing files under `apps/backend/templates/` changes embedded image/config inputs. This plan does **not**
  touch `router.php`.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/api/ -run 'DBAdmin'` | `ok` (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/api/dbadmin.go` (the `Rewrite` function only)
- `apps/backend/internal/api/api_test.go` (extend `TestDBAdminTicketGateway` or add a sibling test)

**Out of scope**:
- `apps/backend/templates/dbadmin/router.php` — a second check there would be defence in depth, but it changes the
  Adminer image inputs and cannot be verified without Docker. Deferred.
- The ticket, grant and cookie logic.

## Git workflow

- Branch: `advisor/014-dbadmin-strip-underscore-headers`
- One commit, plain imperative sentence, e.g. `Drop underscore header names before proxying to the database browser`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Drop any incoming header whose name contains an underscore

Replace the first loop in `Rewrite` with:

```go
for k := range h {
    lk := strings.ToLower(k)
    // PHP's built-in server maps "-" and "_" in header names to the same
    // $_SERVER key, so X_Bento_Server would collide with the injected
    // X-Bento-Server. No legitimate browser header contains "_".
    if strings.Contains(lk, "_") || strings.HasPrefix(lk, "x-bento-") || strings.HasPrefix(lk, "x-forwarded-") {
        h.Del(k)
    }
}
```

`h.Del(k)` canonicalizes its argument; because `k` comes from ranging over the map it is already the stored key, so
deletion works for underscore names too. If your test in step 2 shows an underscore header surviving, delete with
`delete(h, k)` instead.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/api/` → no output, exit 0.

### Step 2: Test

In `TestDBAdminTicketGateway` (or a new `TestDBAdminGatewayDropsUnderscoreHeaders` reusing its setup):

1. Make the stand-in upstream record the headers of the last request it received (if the existing test's upstream
   does not already do so, wrap its handler to store `r.Header.Clone()` under a mutex).
2. Send an authorized request (with the grant cookie) carrying these extra request headers: `X_Bento_Server: evil`,
   `X_BENTO_PASSWORD: evil`, `X-Bento-Server: evil`, `X_Forwarded_For: 1.2.3.4`.
3. Assert on what the upstream received:
   - no header key containing `_` at all (iterate the recorded keys);
   - `X-Bento-Server` has exactly one value and it is the binding's service name, not `evil`.
4. Also assert an ordinary header still passes: send `Accept-Language: en` and check the upstream received it.

**Verify**: narrow test command → `ok`; test PASSes.

## Test plan

As step 2. Pattern: the existing `TestDBAdminTicketGateway`.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] The new assertions pass as root; `sudo … make -C apps/backend ci` exits 0
- [ ] `git status --porcelain` shows no change under `apps/backend/templates/`
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- The existing test has no stand-in upstream you can observe (for example it only checks status codes and the
  upstream is not under the test's control) and adding one would mean restructuring the test — report instead.
- Go or root is unavailable: report "written but not run".

## Maintenance notes

- If Adminer is ever served by something other than PHP's built-in server (php-fpm behind nginx drops underscore
  headers by default), this stays harmless.
- Follow-up worth doing when Docker is available: have `router.php` refuse a request in which any `X-Bento-*`
  value arrives more than once, and verify the whole path against a real Adminer container; record it in
  `apps/backend/docs/evidence.md`.
