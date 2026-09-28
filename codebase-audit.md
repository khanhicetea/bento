# Bento backend: codebase audit

**Date:** 2026-09-28
**Scope:** the Go backend in `apps/backend`: operations, reconcile, runtime, docker, edge, dataservices, backup, transfer, scheduler, api, store and platform. The React UI was not reviewed.
**Commit:** `0101243` (main)

> **Fix status (2026-09-28):** C1, C2, H1–H6 are fixed on `main` (merge `2ff6484`). C2 moved the scheduler UI to the utils listener (`/_bento/scheduler/*`) behind single-use tickets and grant cookies, the same way the database browser works. M1–M7 and M9 are fixed on `main`; M8, the low items, and sections 2–3 are still open. M4 moved to `<slug>_main` / `<slug>_<suffix>` names with underscore-free suffixes (no fallback for existing names). Line references below point at the pre-fix commit `0101243`.

## How this audit was done

- I read the core packages by hand and traced each finding through the code. Every finding carries one of these labels:
  - **Confirmed**: the failure follows directly from the code as written.
  - **Likely**: the code path is clear, but the outcome depends on Docker or engine behaviour I could not run here.
  - **Verify**: plausible, but it needs a real Docker test before anyone acts on it.
- Checks I ran:
  - `go vet ./...` passed.
  - `go test ./...` passed, run as root with the unit tests only.
  - A small Go program confirmed finding H1 (edge bind validation).
- Checks I did **not** run: `make test-integration` (real Docker), arm64, tunnel, ACME and rclone. No finding below claims Docker runtime evidence.

---

## 0. Summary

| # | Severity | Area | Finding |
|---|---|---|---|
| C1 | Critical (security) | platform | `ChownTree` runs as root over an app-controlled home and follows intermediate symlinks. It can chown arbitrary host paths. |
| C2 | Critical (security, design) | api/gateway | The scheduler UI is served from the management origin, and the app controls that content. A compromised app can therefore take over the control plane. |
| H1 | High | edge / accept | Invalid edge `bind` values pass validation and then make `MustParseAddr` panic. Every later edge apply fails, which blocks stop, remove and publish for **all** apps. |
| H2 | High | edge / apps | Any edge apply failure (validation, pull, foreign container) blocks **stop** and **remove** of every app. |
| H3 | High | git deploy | The tool container lives 16 minutes, but fetch plus `deploy.sh` may take up to 30. A long deploy script is killed mid-run. |
| H4 | High | runtime / apps | Scoped config changes can be written to disk without ever being reloaded. Afterwards neither the reconciler nor later updates notice. |
| H5 | High | backup | The first failing target aborts the whole batch, so one broken app (or a fresh SQLite app) disables backups for every app. |
| H6 | High | docker / network | After a `docker network prune`, a stopped app container points at a deleted network ID. Starting it then fails forever. |
| M1 | Medium | executor | `MarkRunning` ignores rows affected. An operation cancelled while queued can still run and end up marked `succeeded`. |
| M2 | Medium | dataservices / reconcile | A service whose first boot was slow stays `Initialized=false` forever. The reconciler ignores it, and a later ensure may create an empty volume. |
| M3 | Medium | redis | One failed `ACL LOAD` is never retried, so new app credentials stay inactive until Redis restarts. |
| M4 | Medium | accept | Database names collide across apps: app `shop` + extra db `blog` and app `shop-blog` both map to `shop_blog`. |
| M5 | Medium | webhook | A provider "redeliver" after a failed deploy returns `duplicate` and does not deploy again. |
| M6 | Medium | transfer | Cancelling an export leaves every app stopped, because the resume path honours the cancel flag. |
| M7 | Medium | reconcile | Service, edge, tunnel and dbadmin targets are blocked for good after 5 failures. No API resets them and no API shows their status. |
| M8 | Medium | isolation | Every stack on a host defaults to UID range 10000–19999, and an imported clone keeps its UIDs. UIDs therefore collide across stacks. |
| M9 | Medium | redis | `-@dangerous` in the app ACL breaks common libraries (`INFO`, `FLUSHDB`, `KEYS`, `SORT`). No `maxmemory` is set. |

About 15 low-severity items, 20 reliability/UX improvements and about 20 Docker edge cases follow below.

---

## 1. Bugs, critical first

### C1: Root `ChownTree` follows attacker-controlled intermediate symlinks (Confirmed)

`apps/backend/internal/platform/fs.go:165`, called from `handlePermissions` (`internal/operations/apps.go:1108`, mode `recursive`).

- **Mechanism:**
  - `filepath.WalkDir` runs as root over `homes/<slug>`. The app UID owns and writes that directory, and the app may be running.
  - `os.Lchown(path)` does not follow the *final* component, but the kernel does follow every *intermediate* component.
  - The race: the app renames `home/a` → `home/a2` and plants a symlink `home/a → /etc` (or `/var/lib/docker/volumes/...`, or another app's home). The walker then calls `Lchown("home/a/<entry>")`, which lands on a host file.
  - `WalkDir` also opens directories by path, so it can descend into the symlink target and chown the whole subtree.
- **Impact:**
  - An app compromise becomes a root-privileged recursive chown of host files to the app UID.
  - Examples: data service volumes (MySQL fails with a DoS), other apps' SQLite directories, and `bento.db`/`services/*/secrets` (unreadable to root-owned processes that check ownership, and readable by any host process running with that UID).
  - This is the classic CVE class behind "recursive chown in a user-writable tree".
- **Fix:**
  - Walk with `openat2(RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS)` or `O_NOFOLLOW` directory fds (`unix.Openat` + `Fstatat` + `Fchownat(..., AT_SYMLINK_NOFOLLOW)`), relative to the opened parent fd. Never chown by path string.
  - Alternatively, run the chown inside a throwaway container that mounts only the home, as root with `CAP_CHOWN` and nothing else. Symlinks then resolve inside the container's mount namespace.
  - `EnsureDir`'s `os.Chmod(dir, mode)` (`fs.go:29`) follows a final symlink in the same way. It is reached for app-owned subdirectories in `ensureHome`, so it needs the same `Fchmodat`/fd-based fix.

### C2: The scheduler gateway serves app-controlled content on the management origin (Confirmed; design risk)

`apps/backend/internal/api/gateway.go:26`

- **Mechanism:**
  - `/scheduler/apps/<slug>/` proxies to whatever listens on `homes/<slug>/.local/share/minicron/minicron.sock`.
  - The app owns that directory, so a compromised app can replace minicrond with its own server on that socket. `TargetSocket` only checks that the socket's owner is the app UID, which the attacker satisfies.
  - The response is served on the Bento UI origin with the session cookie in scope. A script can call `GET /api/v1/session` to read the CSRF token and then drive the whole API (terminal into other apps, dbadmin tickets, export, password change).
- **Impact:** the moment an operator opens a compromised app's scheduler page, an app-level RCE becomes full control-plane takeover. The code comment acknowledges "no XSS isolation", but the operator-facing docs should say it loudly.
- **Fix, best first:**
  - (a) Serve scheduler UIs from a **separate origin**, such as a distinct loopback port or the utils listener with ticket/grant cookies the way dbadmin already works.
  - (b) At minimum, add `Content-Security-Policy: sandbox allow-scripts allow-forms` with no `allow-same-origin`, and strip `Set-Cookie`, which is already done.
  - (c) Render the page inside a sandboxed iframe.

### H1: Invalid edge bind values pass validation, then panic on every edge apply (Confirmed, reproduced)

`apps/backend/internal/operations/accept.go:493,515` and `apps/backend/internal/operations/edge.go:61`

- `parseIP` uses `fmt.Sscanf("%d")`, which stops at the first non-digit without error. `1.2.3.4x`, `010.0.0.1`, `+1.2.3.4` and `1.2.3. 4` are all accepted; I verified this with a Go program.
- The value is persisted, and then `edgeSpec` calls `netip.MustParseAddr(s.Bind)`, which **panics**.
- The executor recovers the panic, but after that every `applyEdge` (so every stop, remove, publish, unpublish and update) fails with `internal: unexpected failure`. Because of H2, the operator cannot even stop apps. Recovery means sending a corrected `PUT /edge`, which is not obvious from the error.
- **Fix:** validate with `netip.ParseAddr` (and allow IPv6), and replace `MustParseAddr` with an error return.

### H2: Any edge apply failure blocks stop and remove of every app (Confirmed)

`apps/backend/internal/operations/apps.go:673-678` (`handleStop`) and `:893-898` (`handleRemove`)

- **Mechanism:**
  - Stop and remove begin by re-rendering the **whole** edge from intent, then validating it and reloading.
  - Unrelated problems fail the operation with `route-removal-failed`, and the app is **not** stopped. Examples:
    - a broken custom drop-in in `edge/custom/`
    - an external certificate file that was deleted or has expired
    - an edge image pull failure when the image was pruned and the host is offline
    - a foreign container with the edge's name
    - the H1 panic
- **Impact:** during an incident (a runaway app, a compromised app) the operator cannot stop it from Bento.
- **Fix:**
  - Stop or remove should try the edge first and, on failure, **warn and continue**. A 502 from a stopped upstream is better than an app that cannot be stopped.
  - Or render a "minus this route" delta that skips validation of unchanged sites.
  - Keep strict behaviour for publish.

### H3: The deploy tool container dies before the deploy budget runs out (Confirmed)

`apps/backend/internal/operations/git.go:29,221,344`

- `OpenTool(ctx, app, deployTimeout+time.Minute)` starts a container whose PID 1 is `sleep 960`.
- The fetch may take up to 15 minutes (`deployTimeout`), and `~/deploy.sh` gets up to another 15 minutes (`deployScriptTimeout`).
- When `sleep` exits the container stops, which kills the running `deploy.sh` exec in the middle of `composer install` / `npm ci` / migrations. The operator sees a confusing exec error rather than a timeout.
- A migration interrupted halfway leaves the database in an unknown state.
- **Fix:** set the lifetime to `deployTimeout + deployScriptTimeout + margin`, or run PID 1 as `sleep infinity` and rely on `tool.Close()` plus the reaper.

### H4: Scoped config changes can be absorbed without a reload (Confirmed)

`apps/backend/internal/runtime/materialize.go:188-240`, `apps/backend/internal/operations/apps.go:780-822`, `apps/backend/internal/operations/tools.go:56`, `apps/backend/internal/operations/backup.go:74`

- **Background:**
  - "Changed" means "the bytes on disk differ from the rendered bytes".
  - The files live in the directory that the **running** container mounts.
  - Several paths write these files without applying them.
- **Path 1, partial scoped-reload failure:**
  - `scopedReloads` handles frontend, then pool, then scheduler.
  - If frontend validation fails, only the frontend files are restored (`apps.go:804`). The pool and scheduler files stay written but were never reloaded.
  - The next materialize sees no diff for them, so they are **never** reloaded, even after the operator fixes the frontend problem.
  - A `reload-failed` step (as opposed to a validation failure) has the same effect for its own files.
- **Path 2, other code writes config:**
  - `OpenTool` (the terminal, `POST /exec`, deploy) and backup `ToolSpec` (every SQLite backup) call `runtime.WriteAppConfig` directly.
  - After a Bento upgrade that changes templates, or while an `app.update` is still queued, the first terminal open or backup writes the new files.
  - The queued update and `AppConfigDrift` then see no change, so the running nginx, php-fpm or minicrond keeps the old config until the next restart.
- **Fix:**
  - Record the **applied** hash per scope (for example `apps/<id>/state/applied.json`, or a container label for the boot scope), and reload when the rendered hash differs from the applied hash, not from the disk.
  - On any scoped failure, restore *all* files that were changed but not yet applied.
  - Tool containers should not write into the live config dir. Render into a tool-private dir, or only verify.

### H5: The backup batch aborts on the first failing target (Confirmed)

`apps/backend/internal/operations/backup.go:200-214`

- **Failures that abort the whole run:**
  - a data service that is not running or not ready
  - a SQLite binding whose `<slug>.db` does not exist yet, which is the normal state for a new app. **Likely** outcome: `sqlite3 .backup` of a missing file produces no snapshot or an empty one, `publish` refuses it, and the error aborts the batch.
  - a binding directory that is missing on an unprovisioned app
- Any of these aborts the scheduled `all` run, so every later app goes unbacked-up, **silently, every night**, until the operator notices.
- **Fix:**
  - Continue past per-target failures and record per-target status in `backup_runs`.
  - Treat "SQLite file does not exist" as `skipped`.
  - Run retention per target only for targets that succeeded.
  - Surface partial failure in the UI and as a notification hook.

### H6: A stale network ID after `docker network prune` stops apps from starting (Likely)

`apps/backend/internal/operations/apps.go:430-455`, `apps/backend/internal/operations/network.go:149`

- **Trigger:** with the edge disabled and every app stopped, the apps network has no attached containers, so `docker network prune` (or `docker system prune`) deletes it.
- **Recovery path:**
  - `EnsureNetworks` recreates the network by name with the persisted subnet, which gives it a **new ID**.
  - The stopped app container keeps the old network ID in its endpoint config. Its fingerprint uses network *names*, so it matches, and `ensureInstance` takes the "start existing" branch.
  - Docker fails with `network <old-id> not found`, which is reported as `start-failed` every time, including from the reconciler until the budget runs out.
- **Fix:** when start fails with a not-found network error (or when an inspected endpoint `NetworkID` ≠ the current network ID), remove and recreate the container. Also compare endpoint network IDs inside `observe`.

### M1: An operation cancelled while queued can still run (Confirmed, narrow race)

`apps/backend/internal/store/operations.go:163,177`

- `NextQueued` and `MarkRunning` are separate statements, and `MarkRunning` uses `WHERE state='queued'` without checking `RowsAffected`.
- If `RequestCancel` commits between them, the handler still runs, and `FinishOperation` overwrites `cancelled` with `succeeded`/`failed`.
- **Fix:** do an atomic `UPDATE … SET state='running' WHERE id=? AND state='queued' RETURNING …`, and skip the operation when 0 rows are affected.

### M2: A data service whose first boot was slow is orphaned (Confirmed)

`apps/backend/internal/operations/data.go:188-200`, `apps/backend/internal/reconcile/reconcile.go:216`

- `MarkServiceInitialized` runs only after readiness within 3 minutes. A MySQL first init on a slow disk or an arm SBC can take longer, so `service.create` fails and `Initialized` stays false.
- The reconciler **skips uninitialized services entirely**, so a stopped service is never restarted.
- Meanwhile the service does become ready, apps bind to it and write data. Any later `service.reconcile` runs with `allowInit = !Initialized = true`, so a missing volume would be **recreated empty**, which breaks the "never recreate durable data empty" invariant.
- **Fix:**
  - Mark the volume initialized as soon as the container has started once against it (or on the first successful grant).
  - Reconcile uninitialized services too, with `allowInit=false` once a volume exists.
  - Make the readiness deadline configurable.

### M3: A failed Redis ACL reload is never retried (Confirmed)

`apps/backend/internal/operations/data.go:92-102`

- `syncRedisACL` writes the file and then reloads only `if changed`.
- If `ACL LOAD` fails once (transient exec error, timeout), later calls see `!changed` and skip the reload. The new app's `REDIS_USERNAME` gets `WRONGPASS` until Redis restarts.
- Callers downgrade the error to a warning (`apps.go:280,723`).
- **Fix:** always `ACL LOAD` when Redis is running (it is cheap), or compare against a last-loaded hash. Also give the exec a context timeout.

### M4: Database names collide between apps (Confirmed)

`apps/backend/internal/operations/accept.go:61,97,431`

- The primary database is `dbIdent(slug)`, and extra databases are `dbIdent(slug)+"_"+name`.
- App `shop` adding database `blog` produces `shop_blog`, which is exactly the primary database of app `shop-blog`. Whichever provisions second fails with `database-retained`, and the guidance ("prune the retained app") is wrong for this case.
- **Fix:** namespace by app ID (`a1b2c3_…`) or use a separator that slugs cannot produce, such as `shop__blog`. At minimum, check collisions at accept time against every app's primary and extra database names.

### M5: Webhook "redeliver" after a failed deploy is a no-op (Confirmed)

`apps/backend/internal/operations/webhook.go:181-196`

- The idempotency key is `webhook:<app>:<provider>:<deliveryID>`.
- GitHub, Gitea and GitLab "Redeliver" reuse the delivery ID. After the operator fixes the deploy key and clicks Redeliver, Bento answers `duplicate` and returns the **failed** operation.
- **Fix:** dedupe only while the original operation is queued, running or succeeded. For failed or cancelled originals, submit a new deploy.

### M6: Cancelling an export leaves apps stopped (Confirmed)

`apps/backend/internal/operations/transfer.go:141-156`

- The resume `defer` calls `ensureInstance` and `waitReady`, and both call `r.Phase`, which returns `ErrCancelled` when the cancel flag is set.
- After a cancel, services come back (`ensureService` never calls `Phase`), but **every app stays stopped** and only a warning is logged.
- The reconciler eventually restarts them (desired=running), but only after the operation ends and through the retry budget.
- **Fix:** give the resume path a `Run` that ignores the cancel flag, or call the effect functions without `Phase`.

### M7: Non-app reconcile targets block for good and are invisible (Confirmed)

`apps/backend/internal/reconcile/reconcile.go:183,215,280-330`

- `service:<name>`, `edge`, `tunnel` and `dbadmin` targets are created with generation 0, so they never reset on a config change.
- `ResetBudget` is called only from the app lifecycle endpoints (`api/server.go:467`), and their status appears in no DTO.
- After 5 transient failures (a Docker restart storm, a port briefly busy) the edge or a database is never auto-restarted again until the backend restarts, and the UI shows nothing.
- **Fix:** reset the budget on the corresponding `PUT`/`POST` endpoints and on a successful operation of the same kind from any origin. Expose `/api/v1/reconcile` status.

### M8: UIDs collide across stacks on one host (Confirmed)

`apps/backend/internal/domain/model.go:294`, `apps/backend/internal/store/repo.go:91`

- Every `bento init` defaults to 10000–19999, and the allocator only skips IDs in `/etc/passwd` and `/etc/group`. Bento never writes there, so it does not see other stacks.
- Two stacks, or a stack and its imported clone (which keeps its UIDs; `NewUIDRange` only moves future allocations), end up running unrelated apps under the same UID.
- This breaks the "UID non-reuse" invariant at host scope. It does not matter while mounts isolate apps, but it does for anything host-level: NFS, host cron, per-UID rlimits, and audit.
- IDs from LDAP or SSSD (not in `/etc/passwd`) are also not detected.
- **Fix:** keep a host-level registry (for example `/var/lib/bento/uid-ranges`), or pick a random non-overlapping range at `init` or `import`. Use `getpwuid` via NSS or `getent` instead of parsing files.

### M9: The Redis ACL breaks common clients, and Redis memory is unbounded (Confirmed)

`apps/backend/internal/dataservices/services.go:382,390`

- `-@dangerous` removes `INFO` (BullMQ, Sidekiq and many health checks call it on connect), `FLUSHDB` (Laravel `cache:clear`), `KEYS`, `SORT` and `SWAPDB`. Apps fail with `NOPERM` in ways that are hard to diagnose.
- `maxmemory-policy noeviction` without `maxmemory` lets one app grow Redis until the host runs out of memory, taking every app down.
- **Fix:**
  - Allow `+info`, which is read-only and harmless.
  - Keep `FLUSHDB` denied, because it ignores key patterns and would wipe other apps' keys. Document the restriction (Laravel: use a tagged or prefixed cache clear instead of `cache:clear`).
  - Set `maxmemory`, either configurable or a percentage of the host.
  - Surface NOPERM guidance in the docs.

### Low-severity bugs

| # | Where | Issue |
|---|---|---|
| L1 | `store/operations.go:223` | Only the **first** 200 events are kept. Long deploys lose the tail, including the final `error` event (`controller.go:258`). Keep a ring of the last N and add a "truncated" marker. |
| L2 | `store/operations.go:163` | FIFO order is `created_at` in milliseconds and then a random `id`, so operations submitted in the same millisecond can run out of order. Order by `rowid` or a sequence instead. |
| L3 | `accept.go:535-547` | `SetTunnelToken` writes or deletes the token file **before** `Submit`. If `Submit` fails (shutdown, idempotency conflict), the file and the settings disagree. |
| L4 | `data.go:165`, `edge.go:402`, `transfer.go:174` | `ins.State` is dereferenced without a nil check. The panic is recovered, but the operation fails as `internal`. |
| L5 | `reconcile.go:359` | `collectTools` removes any tool container that is not `running`, including one in `created` state between `Create` and `Start` of an active operation (OpenTool, validateEdge). The race is narrow, and the operation fails with "no such container". |
| L6 | `docker/engine.go:283` + `apps.go:917-935` | Probe containers (role `probe`, **no stack-id**) and job/backup containers orphaned by a crash are never garbage-collected. Orphaned SQLite backup jobs keep the `app-id` label, so `handleRemove` fails with `containers-remain` forever. |
| L7 | store | `operations`, `operation_events` and `backup_runs` are never pruned, so the database grows without bound. Reconciler drift passes and webhooks add rows continuously. |
| L8 | `api/stream.go:179-256`, terminal | Exec, scheduler-command and terminal output is not redacted, although logs are. This is inconsistent with rule 6 ("Redact app secrets in streamed output"). |
| L9 | `api/stream.go:112` | One log line over 256 KiB makes `Scanner` stop, so the stream sends `end`. The client resumes from the same timestamp and hits the same line again, in a loop. |
| L10 | `store/operations.go:272` | `truncate` cuts by bytes and can split UTF-8, storing invalid text. |
| L11 | `catalog.go:78` | `mysql:8.0` is not pinned to a digest, unlike every other image. MySQL 8.0 reached EOL in April 2026. |
| L12 | `backup/backup.go:170` | The artifact name has one-second resolution, and `os.Rename` silently overwrites an artifact of the same database from the same second (a manual run right after a scheduled one). |
| L13 | `restore.go:30`, `services.go:317` | The collation `utf8mb4_0900_ai_ci` is hard-coded. It is fine for the current MySQL pins, but restore always recreates the database with it, whatever the source charset was. |
| L14 | `operations/backup.go:279-297` | `handleBackupRestore` accepts any artifact of the right engine, including another app's. MySQL dumps carry `DEFINER=<other app user>`, so the restored routines, triggers and views fail at runtime once that user is pruned. Warn, or strip `DEFINER` on dump (`--skip-definer` in 8.0.x / `sed`). |
| L15 | `apps.go:195` | The home identity sidecar is `0444 root` but lives in an app-writable directory, so the app can unlink and replace it. Only self-DoS is possible today. Do not rely on it for authorization later. |

---

## 2. Reliability and UX improvements

### Execution model

1. **Head-of-line blocking in the single executor.** One executor serializes everything, so a first-time image build, a 30-minute deploy, a large backup or an export blocks `stop`, `unpublish` and `restart` of every other app. Options:
   - an "urgent lane" for stop, unpublish and cancel-safe operations
   - per-target concurrency, with a global lock only for edge, network and service changes
2. **Docker calls have no deadlines.** Only exec, readiness and admin SQL use timeouts. A wedged `dockerd` (a common case is stopping a container stuck in D-state) hangs the executor forever, and shutdown waits 60 seconds and then abandons it. Wrap each Engine call in a per-call timeout, and add a watchdog that marks the operation `failed(docker-timeout)`.
3. **The SQLite pool has one connection shared by the API, SSE, reconciler and executor** (`store.go:106,141`). A slow query, or a future bug that holds a `rows` open, stalls everything. WAL allows concurrent readers: use a read pool (N connections, `mode=ro`) plus one writer.
4. **The reconciler holds `r.mu` for the entire pass** (Docker inspects, fingerprint planning, reading config from disk). `Status()` from the app list API blocks behind it, so the UI lags as the number of apps grows. Snapshot the state under the lock and do the I/O outside it.

### App lifecycle

5. **Replacement stops and removes the old container before creating the new one, with no rollback.** If `Create` fails (image missing, mount source missing) or the new instance crash-loops, the app is down with nothing to fall back to. Suggested order:
   1. Create the new container (not started) under a temporary name. This validates the spec.
   2. Stop the old container and **rename** it `…-prev`.
   3. Rename the new container and start it, then `waitReady`.
   4. On failure, remove the new container, rename `-prev` back, and start it.

   This still keeps the "no two schedulers at once" rule.
6. **Crash loops are not caught by fail-fast.** `waitReady` fails fast only on `Status=="exited"` (`apps.go:558-565`). With `unless-stopped` plus `bento-finish` halting, a crash-looping app shows `restarting` or `running`, and the operator waits the full 180 seconds. Also fail fast on `restarting`, a growing `RestartCount`, or health `unhealthy`, and include the log tail.
7. **Edge DNS TTL.** `resolver 127.0.0.11 valid=10s` (`edge-nginx.conf.tmpl:25`) gives up to 10 seconds of 502s after every recreation, even when the app is ready. Use `valid=1s`, or `upstream … { zone …; server app-x:port resolve; }` (supported in open-source nginx 1.27.3+).
8. **Edge recreation on a settings change stops the old edge first** (`edge.go:307`). If the new ports are busy, every site is down. Pre-check port availability (bind-test on the host), or roll back to the old spec on a create or start failure. Also, a change to only the ACME email or URL recreates the container, although a `SIGHUP` would be enough.
9. **Deploys mutate live code in place.** `git checkout --force` and `reset --hard` run while PHP-FPM is serving, so requests can see a mix of versions. Offer atomic release deploys: clone into `releases/<sha>`, run `deploy.sh`, then swap the `ReleaseSymlink` (already supported for the document root). This also enables one-click rollback to `BENTO_PREVIOUS_COMMIT`.
10. **HTTP-process reload is a hard restart** (`s6-svc -r`), which drops requests. Document this, or support a graceful signal per toolchain.
11. **Logs disappear on every recreation**, because the `local` driver deletes them with the container. Before removing the old instance, capture its tail into `apps/<id>/logs/previous.log` and show it in the UI ("previous instance").
12. **Fail early at accept time.** `PublishApp` accepts even when the edge is disabled or the app has no domains, and the operation fails later. Move those checks into `accept.go` so the UI shows an inline validation error. Removing all domains of a published app should also unpublish it or warn.
13. **Extra databases are not in the environment.** `CredentialsEnv` only exports `Databases[0]`. Add `BENTO_DB_<n>_DATABASES=a,b,c`.
14. **tmpfs sizing.** `/tmp` is 512 MiB and `/run` 64 MiB (`spec.go`). tmpfs pages count against the container memory limit, and the minimum `MemoryMB` is 64, so a small app writing to `/tmp` gets OOM-killed with no hint. Derive the tmpfs size from `MemoryMB`, or validate that `MemoryMB` is at least tmpfs plus headroom.
15. **`TZ` is reserved and forced to `UTC`.** Many PHP and cron workloads need a local timezone. Allow `TZ` as a validated per-app setting (the scheduler still uses UTC internally).

### Data, backup and restore

16. **No safety snapshot before a restore.** A relational restore does `DROP DATABASE` first, so a bad artifact leaves the database empty. Dump the current database automatically first (`pre-restore-*.sql.zst`), and warn if the app is running.
17. **Backups:**
    - Upload only the artifacts of the current batch, retrying earlier failed uploads.
    - Run retention only for the scheduled series, so manual `scope=all` runs don't rotate out scheduled ones.
    - Give rclone `Wait` a timeout.
    - Record per-target results.
18. **Export downtime covers the whole archive.** Apps and databases are stopped while every home and volume is tarred. Show an estimate (sizes), check free space at the destination, and consider per-service quiesce (dump + restart) or filesystem snapshots where available.
19. **Service image pins never roll out.** Service containers carry no generation label, so a catalog pin change is never applied to existing containers. Add a generation label and an explicit "upgrade service" operation (patch-level only, with a pre-backup).

### Observability and operator UX

20. **Operation events should keep the tail rather than the head** (L1). Expose a paginated full log for deploys (store `deploy.sh` output in a file under `apps/<id>/logs/deploys/<op>.log`).
21. **Add a `bento doctor` or System health view.** Checks worth including:
    - Docker version and API
    - userns-remap or rootless mode
    - remote `DOCKER_HOST`
    - SELinux mode
    - firewall input policy toward the apps bridge
    - subnet overlaps with host routes
    - free disk space in the stack root and the Docker root
    - blocked reconcile targets
    - orphaned `io.bento.managed` containers without a stack ID
    - image cache presence
22. **Add retention for the operation history** (L7), with a UI filter by origin (reconciler, API, webhook, schedule), so reconciler noise does not bury operator actions.

---

## 3. Edge cases between Docker components

| # | Scenario | Current behaviour | Risk | Recommendation |
|---|---|---|---|---|
| D1 | `docker network prune` or `system prune` while every app is stopped and the edge is disabled | The network is recreated by name with a new ID. Stopped containers reference the old ID (H6). | Apps cannot start | Detect a network-ID mismatch and recreate the container |
| D2 | A same-named network exists **with** Bento labels but a different subnet or `internal=false` (recreated by hand or by a script) | `EnsureNetworks` checks only the labels (`network.go:149`) | The data network may have **egress**. The edge's `.2` and the tunnel's `.3` may fall outside the subnet, so the trusted-proxy IPs are wrong. | Verify `Internal`, the subnet and the IP range; refuse on drift |
| D3 | Host routes or VPNs in `10.200.0.0/13` (WireGuard, corporate VPN, cloud VPC peering) | `UsedSubnets` (`engine.go:336`) considers only Docker networks | Apps silently lose reachability to those hosts | Include host routes (`netlink` route list) when planning, and let the operator set the pool |
| D4 | Another Docker network later takes the planned subnet while Bento's networks are pruned | The plan is persisted and never re-planned, so `EnsureNetwork` fails with "Pool overlaps" permanently | Stack cannot start | Offer an explicit "re-plan networks" operation (it recreates edge, tunnel and app containers) |
| D5 | `docker volume prune -a` removes a service volume while the service is stopped | Fails closed with `volume-missing` (good). No UI guidance beyond the error. | Data loss already happened | Surface prominently, and link to the restore flow |
| D6 | `docker image prune -a` removes `bento-runtime/*` images of stopped apps | The next start rebuilds the image: minutes long, needs network access for the base image and pinned artifacts | Offline hosts cannot start apps. The first start after a prune may exceed expectations. | Warn in the System view, and keep a `bento image cache` export and import |
| D7 | Operator runs `docker rename` or `docker stop` on a Bento container | A rename breaks the name lookup, but the label listing finds the container, so it counts as a `duplicate-instance` and blocks every operation. A manual stop gets repaired by the reconciler (fine). | Operations block, and the error is unclear | Detect "owned container with the wrong name" and offer to rename it back or remove it |
| D8 | Paused containers (`docker pause`) | `State.Running` is true while paused, so the reconciler sees the app as healthy. Readiness exec fails and stop behaviour varies. | App appears up while it is down | Treat `Paused` as drift and unpause (or report it) |
| D9 | userns-remap or rootless Docker | Not detected. Bind-mounted homes owned by the host UID do not match the in-container UID, so everything fails with EACCES, and the relay peer-UID checks mismatch. | Confusing failure | Refuse at startup based on `docker info` `SecurityOptions` (`name=userns`, `name=rootless`) |
| D10 | `DOCKER_HOST` points at a remote daemon (tcp, ssh, a context) | `client.WithHostFromEnv()` accepts it. Bind-mount paths refer to the **backend's** filesystem, and `AppsGateway` is never found. | Works partly, and dangerously | Require a local unix socket, or verify with a probe container that mounts a marker file |
| D11 | SELinux in enforcing mode (Fedora, RHEL, Rocky) | Bind mounts carry no `:z`/`:Z` relabel | Apps are denied access to their home and config | Detect the SELinux mode, relabel the stack root once (`container_file_t`), or document the requirement |
| D12 | The host firewall (UFW, firewalld) drops INPUT from Docker bridges | The edge and cloudflared cannot reach the utils listener on the apps gateway (`apps:7781`), so webhooks through the edge time out silently. Separately, the edge's published ports **bypass** UFW (Docker iptables). | Webhooks fail. The edge is exposed on `0.0.0.0` whatever UFW says. | Check this in the health view. Document it, and default `bind` to the public IP chosen explicitly |
| D13 | An app container crash-loops (`restarting`) | Every reconcile pass re-renders the edge, which toggles `Unavailable`, drifts, and triggers `edge.apply` plus a SIGHUP. Readiness waits the full 180 s (see improvement 6). | Edge reload churn and noisy operations | Debounce flapping, and do not mark a route unavailable on a transient `restarting` |
| D14 | A Docker daemon restart with `live-restore` off | Containers come back through their restart policies, and the event stream reconnect triggers a resync (good). Operations in flight get Docker errors and are not retried. | Failed operations after daemon upgrades | Classify connection errors as retryable, with one automatic retry |
| D15 | The edge static IP `.2` or tunnel IP `.3` is taken (a container attached manually with a static IP, or a network recreated externally without the `/25` range) | Edge or tunnel creation fails with "address in use" | Ingress down | Verify the IP range in D2. Report which container holds the address. |
| D16 | Adminer and the host reaching the `internal` data network | Proxying depends on the host having an address on an `internal` bridge. Docker 28's gateway-mode changes for internal networks may alter this (**Verify**). | The dbadmin gateway breaks after a Docker upgrade | Add an integration test on the minimum and latest Docker versions |
| D17 | Log driver `local` plus recreation | Old-instance logs are deleted together with the container | Debugging after a failed deploy or replacement is hard | Improvement 11 |
| D18 | An orphaned `ReadImageFile` probe container (crash between create and remove) | Labelled `managed=true`, `role=probe`, without a stack ID, so no stack's collector removes it | Clutter, and it pins the image | Add the stack ID and collect probes in `collectTools` |
| D19 | The executor is busy while the reconciler sees drift | The reconciler submits operations that queue behind a long one, and a stack-level export stops apps without reserving the app targets | Spurious queued reconcile operations after an export | Have stack-level operations reserve all targets, or pause the reconciler during an export |
| D20 | The edge only binds IPv4 (`bind` rejects IPv6, and the `udp/443` rule is v4 only) | AAAA records point at nothing | IPv6 clients fail | Support dual-stack bind (`::` plus `0.0.0.0`) |

---

## 4. Suggested fix order

1. **Security:** C1 (fd-based chown), then C2 (a separate origin or sandbox CSP for scheduler UIs).
2. **Keeping operators able to act:** H1, then H2, so that stop and remove always work.
3. **Keeping backups and deploys trustworthy:** H5, H3, M5.
4. **Config convergence:** H4 (track applied hashes), M3.
5. **Docker drift handling:** H6, D2, D9, D10, and the `bento doctor` checks.
6. **Correctness cleanups:** M1, M2, M4, M6, M7, then the low-severity items.
7. **UX track:** replacement with rollback (improvement 5), crash-loop fail-fast (6), edge DNS (7), release deploys (9), pre-restore snapshot (16), and executor lanes (1).

Each fix to runtime, Docker, edge or backup behaviour needs a `docker.Fake` fault-injection test, per `apps/backend/AGENTS.md`, and a real-Docker check with `sudo make test-integration`. Findings H6, D8, D13 and D16 should get integration tests first, because a fake cannot reproduce them.
