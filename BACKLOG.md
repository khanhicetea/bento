# Backlog

Open findings from the production-readiness audit of 2026-11-01. The release blockers fixed since (the store
migration framework, the edge image pull and recreate rollback, the env loader's trailing `=`, the scheduler UI
served from the app's socket, relational restores dropping the live database first, MySQL restores creating
root-owned objects, and MySQL grants whose `_` let one app use another's database) are recorded in `apps/backend/docs/evidence.md`. Paths are relative to `apps/backend/`
unless they start with `apps/`. Line numbers are as of commit `8a88634` and drift; the named function or template is
the stable reference.

Status: **confirmed** = the code path was read end to end (or reproduced); **plausible** = the mechanism is in the
code but the trigger was not reproduced.

## Release blockers

None open.

## Before public traffic

### Control plane

- **Docker calls have no deadlines, and running operations can't be cancelled** (confirmed).
  - `docker.NewSDK` (`internal/docker/engine.go:186`) sets no timeouts.
  - Handlers get the process context, and cancellation is honoured only at `Phase`.
  - A wedged dockerd, registry pull, or image build hangs the operation forever while it holds its claims. A global
    operation therefore stalls the executor.
  - `Reconciler.Pass` (`internal/reconcile/reconcile.go:416`) has no deadline either.
  - Fix: give each operation a context that `RequestCancel` cancels, set per-call deadlines on inspect/list/exec,
    and put bounds on pull/build time and on each pass.
- **An edge reload can be lost** (confirmed). `applyEdge` sends `SIGHUP` only when the generation changed. If
  `Signal` fails, or the backend dies between `Promote` and the HUP, `live` already equals the render, so no later
  apply reloads. The "retry to reload" hint is false. Failed promotes leave `candidate-*` directories behind.
  - Fix: persist the hash of the last generation that was signalled and HUP whenever it differs.
- **The reconciler never notices edge shape drift.** It only submits `edge.apply` when the edge is missing or stopped,
  or the rendered config differs. After an upgrade that only bumps the edge image pin, the edge stays on the old
  image until something else applies the edge. Compare the generation label in the reconciler; recreating is now
  safe because a failed recreate rolls back.
- **Upgrades replace apps 4 at a time, not one at a time** (confirmed). A `specVersion`, template, or pin bump makes
  one reconciler pass submit `app.reconcile` for every drifted app (`reconcile.go:507-510`). That kind is parallel,
  so up to `--op-concurrency` replacements run at once, contradicting `AGENTS.md`. Add a rollout limiter.
- **cloudflared is never updated in place** (confirmed). The tunnel generation label is only `token-<n>`
  (`tunnelSpec`), so a `TunnelImage` bump never recreates it. The pin `cloudflared:2025.9.1` is likely past
  Cloudflare's support window.

### Images and supply chain

- **Runtime images never get security updates** (confirmed).
  - The image tag hashes only the build context and args (`internal/runtime/images.go:36-78`), and `Ensure` builds
    only when the tag is missing. Debian packages (openssl, nginx, glibc, libcurl) stay frozen until a Bento release
    changes a build input. Nothing can force a rebuild.
  - Plausible: the classic builder without `NoCache` reuses the cached `apt-get` layer even on a rebuild.
  - Fix: a scheduled or operator-triggered rebuild with `NoCache` that rolls apps one at a time.
- **Data-service images are frozen per service.** The image is stored in the service row when the service is
  created, so MySQL/PostgreSQL patch releases never reach existing services. A same-major patch path is needed.
- **Unpinned build inputs.**
  - `templates/images/php/Dockerfile:24` runs `pecl install redis` with no version or checksum.
  - Bun images are pinned by tag only (`internal/domain/catalog.go:36-37`).
  - pnpm's native binary is checked only against the npm registry's own hash.
- **Version comment.** `internal/domain/catalog.go:133` says an in-place 8.0 → 8.4 MySQL upgrade is supported; no
  code does that.

### Edge and ingress

- **HTTPS upstream hosts:** no SNI and no certificate check. `templates/edge/edge-site.conf.tmpl` sets no
  `proxy_ssl_server_name on` or `proxy_ssl_verify`. Cloudflare/Vercel-style upstreams fail the handshake, and any
  upstream can be intercepted.
- **Upstream hostnames resolve only when the config loads.** The template renders `server host:port;` with no `zone`
  or `resolve`. If the upstream's DNS is unavailable, `nginx -t` fails every edge apply, and after a reboot without
  DNS the edge crash-loops. Changed IPs are never picked up. When upstreams of one host differ in scheme or path, the
  last one wins (upstream grouping in `renderEdge`).
- **Front proxy trust check** (confirmed). Trusted proxy CIDRs are checked against stack networks only if they
  already exist (`internal/operations/accept.go:642-651`, `network.go:118-130`). On a fresh stack, `10.0.0.0/8` is
  accepted and the network plan then allocates inside it, so any app can reach the PROXY-protocol ports and spoof
  visitor IPs. Other stacks' networks and Docker bridges are never checked.
- **Recreate rollback with new routes.** When routes changed and a recreate fails, the restored old edge restarts on
  the new live generation, which was validated by the new image. Low risk while nginx stays on the same stable line;
  keeping the previous generation for the rollback path would close it.
- **ACME has no guard rails.** Nothing refuses ACME when `httpPort != 80` and neither a tunnel nor a front proxy
  terminates HTTP-01, so issuance fails repeatedly and counts against Let's Encrypt's failed-validation limit.
- **Valkey has no memory cap.** `templates/images/common/rootfs/etc/valkey/valkey.conf` has no `maxmemory` and uses
  `save`-only persistence. A cache with no TTL, or the extra memory of a background save, hits the container limit,
  and the kernel may OOM-kill php-fpm instead. After a crash, up to an hour of data is lost.

### Data and transfer

- **Import can chmod directories outside the stack root** (confirmed with a scratch harness). Symlink containment in
  `internal/transfer/archive.go` (around 213, 236-242, 255) is lexical. A crafted archive with a symlink chain plus a
  directory entry on top of the link makes `os.Chmod` follow it.
  - `ExtractRoot` also keeps setuid/setgid bits (`&0o7777`).
  - The import runs `tar` as root in the manifest's `svc.Image` (`internal/stack/volumes.go:30`) without checking it
    against the catalog.
  - Fix: use `openat`/`O_NOFOLLOW` directory fds, mask modes to `0o777`, and accept only catalog images.
- **Client meta-commands in a crafted dump** (plausible). Restores replay dumps through `mysql` and `psql` exec'd as
  root in the service container. Both clients run shell commands from their input (`system`/`\!`), so a crafted
  artifact (a hand-placed file, or a dump in a tampered restic repository) could run commands next to the data
  volume and `/run/bento-secrets`. Bento's own dumps never contain them. Fix: pass `--skip-system-command` where the
  pinned `mysql` client supports it, put `\restrict <random>` in front of the `psql` stream, and run the exec as the
  image's database user.
- **Off-site dumps are plaintext.** `internal/backup/rclone.go:229` runs a plain `rclone copy`. Nothing requires or
  suggests a `crypt` remote, and remote copies are never pruned.
- **Export has no integrity protection.** There is no fsync of `stack.tar.zst` or the volume tars
  (`internal/operations/transfer.go:260-270`), and the manifest records no checksums. Tool shells and exec containers
  keep writing to app homes during the "quiesced" export.
- **Restic copies unlisted SQLite files raw while the app runs** (plausible). `internal/operations/restic.go:1077-1090`
  snapshots only `minicron.db` and the listed paths. Other SQLite files in the home (for example Laravel's
  `database/database.sqlite`) can be torn.

### Auth

- **Login rate limit.** `allowed()` runs before argon2 but `fail()` only after it (`internal/api/auth.go:111-135`,
  `handleLogin` around 262-298), so concurrent attempts bypass the limit.
  - Each attempt allocates 64 MiB, and there is no concurrency cap, so a burst can OOM the backend.
  - The limiter is global, so a local process can lock the operator out.
  - Fix: reserve the attempt first, put a semaphore around argon2, and key the limit per client.

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
- **Backup housekeeping.**
  - The empty-artifact check looks at the compressed size, which is never zero (`internal/backup/backup.go:169`).
  - There is no free-space check before dumps.
  - Crash leftovers are never cleaned up: `.partial-*` files, `staging/backup-*` directories, and `dumpSQLite`
    containers without an operation label.
  - There is no directory fsync after publishing or renaming.
- **Isolation (design).** Apps share one flat network and can reach each other's listeners and the utils listener.
  App homes have no disk quota, so one app can fill the disk that holds `bento.db`.

## Never verified live

These come from `apps/backend/docs/evidence.md` and the audit:

- arm64 execution, and whether the non-PHP digest pins are multi-arch index digests.
- ACME issuance, HTTP/3, external certificates, a real Cloudflare Tunnel, and real git-host webhook deliveries.
- SQLite restore. Relational restores of large databases (the swap is verified on small ones with MySQL 8.0/8.4/9.7
  and PostgreSQL 16/17), and a MySQL swap while the app holds a long transaction (`lock_wait_timeout` 60s).
- Crash recovery mid-clone (`restic.recover` runs a database drop) on real Docker. Interrupted or tampered imports.
- Scheduled backup retention pruning. A large-database restore drill. Disk-full behaviour.
- Stop persistence across a real host reboot.
- The web UI has no automated tests.

## Decisions

- Wildcard MySQL grants from before 2026-11-02 are replaced only when provisioning runs for that binding (start of an
  unprovisioned app, Add binding, Add database); there is no stack-wide migration because no production stack ran the
  old grants. Databases an app created through such a grant (for example `axb_main`) are not detected.
- No deployment runs a schema older than 2, so `store.MigratableFromVersion` is 2 and nothing older is migrated.
- The real-Docker suite (`make -C apps/backend test-integration`) runs on the local dev host, not in GitHub
  Actions. CI (`.github/workflows/ci.yml`) runs only on tags and releases.
