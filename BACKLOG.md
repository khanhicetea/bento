# Backlog

Open findings from the production-readiness audit of 2026-11-01. The release blockers fixed since (the store
migration framework, the edge image pull and recreate rollback, the env loader's trailing `=`, the scheduler UI
served from the app's socket, relational restores dropping the live database first, MySQL restores creating
root-owned objects, MySQL grants whose `_` let one app use another's database, and on 2026-11-03 the login burst,
HTTPS upstream TLS, Docker deadlines and forced cancellation, one-at-a-time rollout, lost edge reloads, edge and
cloudflared pin drift, the Valkey cap and ACME backoff; on 2026-11-06 Restore DB matching the binding's service, upstream
hostnames resolved at run time, and the edge crash on its first ACME host; and from the real-VPS run of 2026-11-06,
the IPv4-only edge, edge reloads that kept Docker from restarting the edge, deploy scripts left running by a crashed
backend, the deploy summary order, webhook deliveries hiding failed deploys, the ACME challenge warning, and failed
handshakes during a host's first issuance) are recorded in `apps/backend/docs/evidence.md`. Paths are relative to `apps/backend/`
unless they start with `apps/`. Line numbers are as of commit `8a88634` and drift; the named function or template is
the stable reference.

Status: **confirmed** = the code path was read end to end (or reproduced); **plausible** = the mechanism is in the
code but the trigger was not reproduced.

## Release blockers

None open.

## Before public traffic

### Control plane

- **Forced cancellation can leave a replaced app stopped.** Since 2026-11-03 a cancellation is forced after 30 s
  (`watchCancel`); a step interrupted between stopping and recreating an instance leaves the app down until the
  reconciler repairs it. `Exec`/`Wait` and non-Docker steps (export file I/O) still have no deadline of their own.

### Images and supply chain


### Edge and ingress

- **Recreate rollback with new routes.** When routes changed and a recreate fails, the restored old edge restarts on
  the new live generation, which was validated by the new image. Low risk while nginx stays on the same stable line;
  keeping the previous generation for the rollback path would close it.

### Data and transfer


### Auth

- **The login limiter is global** (since 2026-11-03 attempts are counted before hashing and at most 2 run at once).
  Any process that can reach the loopback listener can keep browser logins rate limited; root on the control socket
  is unaffected. Fix: key the budget per client or session.

## Hardening (low)

- **Admin tokens can run code in apps.** An admin token can create a scheduler job, and an operate token runs it
  (`internal/api/scopes.go:105-121`). File PUT and git repoint plus deploy reach the same result. The docs say no
  scope reaches exec. Either document admin as code execution or narrow it. Run logs and file reads redact only
  binding passwords, not operator env values.
- **Webhook replay.** The HMAC covers only the body; the delivery id is unsigned and there is no timestamp
  (`internal/domain/webhook.go:108`, `internal/operations/webhook.go:200`). A replay only redeploys the branch head.
- **`documentRoot` is rendered unescaped.** It goes into `root {{.DocumentRoot}};`
  (`templates/config/app-nginx.conf.tmpl:65`), and `CleanRelative` (`internal/domain/validate.go:98`) allows
  newline, `;`, `{` and `$`. Impact stays within the app's own nginx; add a character allow-list.
- **Terminal CSRF check.** `internal/api/terminal.go:333` compares the token with `!=` and the token travels in the
  query string.
- **SQLite restore race.** `internal/backup/restore.go:107-161` checks for symlinks, then writes as root into the
  app-owned directory (the app is stopped). Use `O_NOFOLLOW` directory fds.
- **Tool containers act outside operations.** `OpenTool` (`internal/operations/tools.go:41-62`) runs
  `EnsureNetworks`, `Images.Ensure`, and `WriteAppConfig` with no claims. It can race an operation and rewrite config
  that `scopedReloads` just restored after a failed validation.
- **Shutdown records cancelled operations as `failed`, not `interrupted`** (`internal/cli/serve.go:287`,
  `internal/operations/controller.go:585-594`).
- **SQLite durability.** The store opens with `synchronous(NORMAL)` (`internal/store/store.go`), so the last
  acknowledged commits can roll back on power loss. `FULL` is affordable at this write rate.
- **Store and reconciler bookkeeping.**
  - A crash between `Submit` and `saveSchedule` reruns a scheduled slot (`internal/operations/schedules.go:143-150`).
  - `AppendEvent` does `MAX(seq)` and then `INSERT` outside a transaction (`internal/store/operations.go:331`).
  - The reconciler's `targets` map never forgets removed apps.
- **Edge templates.**
  - The static-cache `add_header` stops server-level headers from being inherited (`edge-site.conf.tmpl` around
    line 83), so `Alt-Svc` and any HSTS from drop-ins are lost on cached assets.
  - The body limit is hard-coded at 64 MB for HTTP apps and upstream hosts.
- **Edge container.** It has no memory or PID limit. `listen [::]` fails on hosts booted with `ipv6.disable=1`.
- **Isolation (design).** Apps share one flat network and can reach each other's listeners and the utils listener.
  App homes have no disk quota, so one app can fill the disk that holds `bento.db`.

## Never verified live

These come from `apps/backend/docs/evidence.md` and the audit:

- arm64 execution, and whether the non-PHP digest pins are multi-arch index digests.
- External certificates, a real Cloudflare Tunnel, and GitHub/GitLab/Bitbucket webhook deliveries (Gitea and the
  generic bearer trigger ran live on 2026-11-06; ACME staging issuance, HTTP/3 and IPv6 too).
- SQLite restore. Relational restores of large databases (the swap is verified on small ones with MySQL 8.0/8.4/9.7
  and PostgreSQL 16/17), and a MySQL swap while the app holds a long transaction (`lock_wait_timeout` 60s).
- Crash recovery mid-clone (`restic.recover` runs a database drop) on real Docker. Interrupted or tampered imports.
- Scheduled backup retention pruning. A large-database restore drill. Disk-full behaviour.
- The web UI has no automated tests.

## Decisions

- Runtime image tool channels float on purpose (base images, mise channels such as `bun@1.3` and `pnpm@latest`,
  Composer, phpredis from PECL): that is how their security and patch releases arrive. They resolve only when an
  operator rebuilds (`bento images rebuild`, which also bypasses the layer cache) and rolls apps out one at a time,
  and each build records what it resolved. Artifacts Bento installs itself (s6-overlay, minicrond, mise, restic,
  Valkey) are pinned with checksums.
- Wildcard MySQL grants from before 2026-11-02 are replaced only when provisioning runs for that binding (start of an
  unprovisioned app, Add binding, Add database); there is no stack-wide migration because no production stack ran the
  old grants. Databases an app created through such a grant (for example `axb_main`) are not detected.
- No stack runs in production yet, so on 2026-11-07 every schema change so far was folded into one baseline, schema 1
  (`store.SchemaVersion` and `store.MigratableFromVersion`), with no migrations. Development stacks on schemas 2–5
  are re-initialized, not migrated. The next schema change adds `migrations[1]`.
- The real-Docker suite (`make -C apps/backend test-integration`) runs on the local dev host, not in GitHub
  Actions. CI (`.github/workflows/ci.yml`) runs only on tags and releases.
- The front-proxy trusted list is not checked against the stack networks before the network plan exists (a fresh
  stack accepts `10.0.0.0/8`, then allocates the apps network inside it, so any app could spoof visitor IPs through
  the PROXY-protocol ports). Accepted as is on 2026-11-03; operators should list only their front proxy's address.
