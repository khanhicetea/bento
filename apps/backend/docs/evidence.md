# Verification record

What has been verified, how, and what has not. Recorded 2026-09-27 on Linux x86_64 (amd64), Docker Engine 29.8.1
(API 1.56), Go 1.27.0, running as root. All live checks used disposable stack roots in a scratch directory.

**Only amd64 was executed.** The arm64 binary is cross-compiled (static ELF aarch64) but has not been run.

## Repeatable checks

```bash
cd apps/backend
make fmt-check vet
sudo go test -race ./...                                                  # unit + contract suite
make check-generated                                                      # tygo output matches the committed file
sudo BENTO_DOCKER_TESTS=1 go test -count=1 ./internal/integration/ ./internal/runtime/   # real Docker
make release                                                              # embedded UI, amd64 + arm64
```

## Runtime images and containers

- Runtime images build through the Engine API classic builder from deterministic embedded contexts:
  `bento-runtime/php:8.4-<hash>` (about 2 minutes cold) and `bento-runtime/node:24-<hash>` (about 30 seconds).
  Identical inputs produce identical tags.
- s6-overlay runs as PID 1 under `USER uid:gid` with a read-only root, all capabilities dropped, and
  `no-new-privileges`. This requires `/run` to be a uid-owned tmpfs mounted with `exec` (Docker mounts tmpfs `noexec`
  by default) and user bundles under `user-bundles.d`. Every process — s6, PHP-FPM master and workers, Nginx,
  minicrond — runs as the app UID.
- A second instance on the same home exits 75 (instance lock); minicrond also locks its data directory.
- Graceful stop takes about 3 seconds and exits 0.
- Idle footprint: PHP app 20–32 MiB and 20–23 processes; Node app 17 MiB and 22 processes; edge 20 MiB; near-zero CPU.
  Loopback latency through the edge is about 1 ms. The defaults (512 MiB, 1000m CPU, 256 processes) leave headroom.
- SQLite uses `modernc.org/sqlite` (pure Go), so builds need no C toolchain; both architectures cross-compile with
  `CGO_ENABLED=0`.

## Control plane

- The store refuses foreign, older, newer, and non-SQLite files without changing a byte or creating `-wal`/`-shm`.
- UID allocation: non-decreasing high-water mark, ledger never deleted, host collisions skipped, exhaustion reported,
  25 concurrent allocations unique.
- Operations: intent and operation saved in one transaction; idempotency keys return the original operation; an
  operation interrupted by a crash is marked `interrupted` and never replayed.
- Authentication: argon2id password, 12-hour HttpOnly `SameSite=Strict` sessions, exact Origin + CSRF token + Fetch
  Metadata on writes, login rate limit, no CORS. Control socket `0600` with peer-credential checks. The listener
  refuses non-loopback addresses; `serve` refuses missing state instead of initializing.
- REST: unknown fields, trailing data, wrong types, oversized bodies, invalid enums, and mismatched runtime variants are
  rejected with stable error codes. tygo output is checked against real DTO JSON (`TestWireFidelity`).

## App lifecycle (live)

Stack with two PHP 8.4 apps (MySQL + SQLite, PostgreSQL) and one Node 24 app (SQLite):

- Create leaves apps stopped and unpublished and provisions the app-owned code directory at `/home/<slug>/app`.
  PHP document roots and HTTP-process working directories resolve from that directory. Start waits for in-container
  readiness (scheduler, FPM, local HTTP) and a direct HTTP probe over the app network.
- Front-controller routing with `PATH_INFO`, legacy multi-file routing, dotfiles denied, symlinks escaping the document
  root refused. An app writing outside its Redis prefix is denied.
- Restarting the backend restarted no container and submitted no reconcile operations.
- A template change applied as a validated local Nginx reload; a resource change recreated only that app, stopping the
  old instance first.
- Manual `docker stop` of a running app was repaired; `docker rm -f` with intact data was recreated; with the home
  moved away the operation failed `durable-state-missing`, no empty replacement was created, and it backed off.
  Restoring the home and starting explicitly recovered.
- A duplicate container labeled for the app marked it `blocked` and nothing was touched.
- Stop sets restart policy `no`; publishing a stopped app is refused.
- Remove requires `delete <slug>` and keeps the home; creating the same slug is refused while it is retained; prune
  requires `delete`; the next incarnation received a new UID and the old one stayed `retired`.
- Scheduler: the config-owned SQLite VACUUM task is present; a user job imported with `bento app minicrond` fires while
  the app is unpublished; `daemon` is refused; a sibling app cannot see another app's home.
- On the fake engine: fault injection at image build, create, start, and exec; interrupted destructive operations not
  replayed; concurrent starts create one instance; readiness failures bounded; reconcile retry budget of 5 then
  `blocked`; stop intent never resurrected.

## Ingress (live)

- Edge on alternate host ports: HTTP, self-signed HTTPS (`HTTPS=on` only through the edge), unknown hosts 404, SSE and
  WebSocket upgrades pass through, uploads above the limit rejected (413), range requests return 206.
- Client-forged `X-Forwarded-For` is overwritten; another app forging `X-Forwarded-Proto: https` is not trusted.
- An app moved to a new IP with the edge configuration unchanged and no reload; the edge still routed to it.
- Unpublish leaves the app running; stop removes the route first. The edge mounts only its own directories.
- cloudflared with a fixture token: runs as nonroot, read-only, no ports; the token is absent from inspect output, API
  responses, state, and logs; rotation recreates only the tunnel. Tunnel connectivity itself is not verified.

## Data, backup, transfer (live)

- MySQL 8.4, PostgreSQL 17, and Redis 8.2 are initialized on first start; readiness requires a TCP answer so the
  first-boot temporary server is not mistaken for ready.
- A backup batch produced MySQL, PostgreSQL, and SQLite artifacts (zstd, `0600`, atomic). MySQL and PostgreSQL restores
  round-tripped, with restored objects owned by the app role. Wrong confirmation, another app's database, and path
  traversal were refused.
- Export stopped running apps and services, archived the root and three volumes, and restarted exactly the prior set.
- Import into a non-empty root was refused; a clone with a conflicting name left no root or volume behind; a clone with
  a new name served the exported data with all apps stopped and unpublished, on new networks with a new stack id.

## Web UI (live, headless Chromium against the release binary)

Sign-in, application list, app detail with ingress ownership, SSE logs, the scheduler view through the relay showing
the app's own job, the WebSocket tooling shell reporting uid 10000, a restart tracked to success, the data services,
backups, ingress, and operations pages, and sign-out.

minicrond 0.2.6: the scheduler UI loads as a single bundle with relative asset paths; every asset it references
(stylesheet and script) resolves under `/scheduler/apps/<slug>/` through the gateway, and the scheduler API answers
through the per-app relay.

minicrond 0.3.2 (standalone binary, unix socket, `BASE_PATH=/apps/shop/scheduler`, not yet inside a built runtime image):
the UI and `/healthz` answer under the base path, the bare path 301-redirects to the slash form, and responses carry a
strict CSP with `frame-ancestors 'none'` and `X-Frame-Options: DENY`, so the UI cannot be iframed and Bento links to it.
The gateway is covered by `TestSchedulerGateway` against a missing relay (502); a real proxied session is unverified.

## Release

The compiled binary, run from `/` with `PATH=/nonexistent`, served the API, the CLI, and the embedded UI. With the
backend stopped, edge traffic and scheduled jobs continued.

## Git deploy (live Docker, disposable root, PHP 8.4 image)

- A public HTTPS repository cloned into an empty `app/` through a tooling container as the app uid; files are owned
  by uid 10000 and the deployed commit was recorded. A redeploy of the running app reset a modified tracked file,
  kept an untracked `.env`, and reloaded only the app process: for PHP the FPM master, local Nginx, and minicrond
  kept their PIDs while FPM workers were renewed (SIGUSR2); for a Node HTTP process only the `app` s6 service was
  restarted. The container start time was unchanged in both cases and the app returned to ready.
- Switching the source to the SSH URL of the same repository repointed `origin` instead of refusing it; a checkout
  of a different repository is refused (`git-origin-mismatch`).
- Over SSH the generated ed25519 deploy key was offered, GitHub's host key was pinned (hashed) in the app's
  `~/.ssh/known_hosts`, and the unregistered key was reported as `git-access-denied` with the public key in the
  guidance. No operation record or backend log contained private key material, and no tooling container remained.

## Deploy webhook and `~/deploy.sh` (live Docker, disposable root, edge on 127.0.0.1:18080)

- With the default `--public-listen`, the backend served `127.0.0.1:7781` at start and bound the apps bridge address
  once the network existed. Docker had put that address at `10.200.2.128`, not `.1` (the dynamic range is the upper
  `/25`), so it is read from the host's interfaces.
- A bearer-authenticated `POST /_bento/webhook/deploy/<id>` queued an `app.deploy` through each of: loopback (the
  host-nginx path), the edge on a routed proxy domain (`proxy_pass http://10.200.2.128:7781`), and a throwaway
  container on the apps network calling the bridge address (the cloudflared path). `/`, `/api/v1/session` on the
  public port got 404; a bad secret got 404.
- Earlier runs of the same build through the edge: an unrouted Host got 404; a 9 MB body got 413; curl requests
  carrying GitHub-style `X-Hub-Signature-256` (computed with `openssl`), a GitLab `X-Gitlab-Token`, and a bearer token
  each queued a deploy (`origin=webhook`); a push to another branch was recorded as `ignored-ref`.
- `~/deploy.sh` ran after the fetch as uid 10000 from `/home/<slug>/app` with `BENTO_DEPLOY_TRIGGER=webhook`,
  `BENTO_COMMIT`, `BENTO_PREVIOUS_COMMIT`, and `BENTO_WEBHOOK_PROVIDER`; its output appeared in the operation
  events. A script exiting 3 failed the deploy as `deploy-script-failed` with its stderr, without a reload or record.

## Database browser (live Docker 29.8.1, disposable root, utils listener on 127.0.0.1:17781)

- `dbadmin.apply` pulled `adminer:5.5.1` (pinned digest) and started `bento-dbt-dbadmin` on the data network only;
  `<root>/dbadmin` was `0750 root:101` with `0440` files. The host reached the container on the internal data bridge.
- A ticket for a MySQL 8.4 binding redeemed once (303 + grant cookie on `/_bento/dbadmin/b/<bid>/`); a second redeem got
  401. The redirect landed on the binding's database and Adminer's `SELECT CURRENT_USER()` returned `u<appId>@%`.
  A PostgreSQL 17 binding opened its own database (`ns=public`) as the app role.
- The MySQL grant got 401 on the PostgreSQL binding path; `db=mysql` got 403; `?pgsql=…&username=postgres` and
  `?server=10.0.0.1&username=root` still showed the binding's own server and database. POSTs without Origin or with
  `Sec-Fetch-Site: cross-site` got 403. Direct requests to the container, with or without a forged
  `X-Bento-Gateway-Token`, got 403. Logging out made the grant return 401.
- Adminer static assets loaded through the gateway; a manually stopped container was restarted by `dbadmin.apply`
  from the reconciler; disabling removed the container, and grants then got 503 and new tickets 412.
- Not verified: the Browse button in a real browser (popup handling, cookies across a public base URL over HTTPS),
  large imports/exports near the 80 MB / 15 minute limits, and access through cloudflared or the edge.

## Backup schedule (live, disposable root, host TZ UTC+07:00)

- Before the fix, cron fields were read as UTC wall-clock time (the stored UTC `LastSlot` set the location for
  `cron.Next`), so on this host a slot fired 7 hours late, and `nextRun` was never filled in.
- After: `backup schedule` with `23 22 * * *` returned `nextRun` 15:23Z (22:23 local) and `timeZone` `UTC+07:00`;
  `backup.run` was submitted at 22:23:00.378 local and `nextRun` moved to the next day.

## rclone shell and upload (live Docker 29.8.1, disposable root, `rclone/rclone:1.71.1` pinned digest)

- `TestIntegrationRcloneShellAndUpload`: the shell container (read-only root, `CapDrop: ALL`, only `rclone/` mounted)
  ran `rclone config create dest local` as root and the host saw `[dest] type = local` in `rclone/rclone.conf`.
  `touch /etc/x` and `ls /upload` failed inside it. A TTY exec of `sh` showed the `rclone:` prompt and
  `rclone listremotes`.
- `Upload` copied an artifact through that remote (a local path under the config mount stood in for cloud storage);
  `TestRemote` reported an existing path as reachable and a missing one (rclone exit 3) as "will be created"; an
  unknown remote was refused before any container ran.
- Found and fixed: the upload container's `local` log driver with `max-file: 1` was rejected by Docker ("compression
  cannot be enabled when max file count is 1"), so uploads had never been able to start.
- Not verified: a real cloud backend, an OAuth backend's token refresh writing back to the config, and the rclone shell
  in a real browser.

## Parallel operation execution (2026-09-29; fake engine plus live Docker 29.8.1, disposable root)

- The executor runs operations with disjoint claims in parallel (up to `Deps.Concurrency`, default 4); see
  `architecture.md`. `go test -race` over `internal/operations` (`parallel_test.go`) and `internal/reconcile`, as root,
  against `docker.Fake`: operations on different apps overlap; operations on one app never do; the concurrency limit is
  honoured for 1 and 2; a global operation waits for running work and nothing queued behind it overtakes it; an app
  bound to a data service waits for an earlier queued operation on that service while an unrelated app does not;
  `Shutdown` waits for every running operation; parallel `EnsureNetworks` creates each network once; `applyEdge` is
  exclusive; an app being started is not routed by another operation's edge apply until it is ready; every registered
  kind not explicitly reviewed is global.
- Each guard (shared service claims, `netMu`, `edgeMu`, warming exclusion, the queue-order rule, the claim table) was
  removed in turn and its test failed.
- `TestIntegrationParallelAppOperations` (real Docker, `node:24` HTTP apps): two starts submitted together ran with
  overlapping start/finish intervals and both served; after both instances were deleted, one reconciler pass submitted
  two `app.reconcile` operations that also overlapped and both apps served again. `TestIntegrationAppLifecycle` still
  passes with the new executor.
- Follow-up (same day): a restart that overlapped another operation's edge apply left a published route "unavailable"
  until the reconciler repaired it (reproduced with the fake engine: `EdgeConfigDrift` true after the restart); the
  warming operation now restores it (`TestRestartRestoresRouteHeldDuringBoot`), and warming apps are not reported as
  drift. `app.update` and `app.deploy` became parallel kinds (`TestDeploysOfDifferentAppsRunInParallel`,
  `TestUpdatesOfDifferentAppsRunInParallel`); image builds lock per tag (`TestEnsureBuildsDifferentTagsConcurrently…`,
  `TestRemoveExcludesEnsure`); `syncRedisACL` is serialized (`TestRedisACLSyncsAreSerialized`); queued operations report
  `waitingOn` through the API (`TestQueuedOperationReportsWaitingOn`); `--op-concurrency` is range-checked. Each new
  guard was removed in turn and its test failed. Negative dispatch assertions wait for a dispatch pass instead of
  sleeping.
- Found while testing: `TestToolWriteDoesNotAbsorbScopedReload` failed about 1 in 2000 runs because the random vacuum
  slot assigned at app creation could equal the slot the test set; fixed in the test helper.
- Not verified: parallel operations with a real edge (host ports 80/443 were not used) and with real MySQL/PostgreSQL
  services; parallel deploys against a real git host; two different runtime images building concurrently on real
  Docker; behaviour under a real host reboot with many apps; throughput gain (only overlap was measured).

## PHP performance modes (2026-09-30; live Docker 29.8.1, amd64, PHP 8.4 runtime image)

- Method: disposable copies of a dev PHP app container (same image, generated nginx/FPM config, 512 MB / 1 CPU /
  256 PIDs, read-only root), one at a time, changing only the pool; a Go load generator against the app's port 8080.
  Request shapes: typical page (15 ms busy loop + 40 ms sleep), 2 s sleep, 5 MB body, 64 MB allocation.
- Old profiles: at equal `max_children`, `ondemand` and `dynamic` had the same steady-state throughput and latency;
  `ondemand` handled a burst from idle better (50 concurrent: 100 vs 62 rps at 10 workers, 154 vs 93 at 50), idled at
  19 MB / 18 PIDs regardless of size, and cost about 1 ms extra on a first request. Every profile with 10 or more
  workers (including the default `small` and `ondemand`) returned about 95/100 502s for 20 concurrent 64 MB requests
  with 92–96 cgroup OOM kills; only `tiny` (5 workers) served all 100.
- New modes, rendered by `RenderAppConfig`: `standard` (6 workers at 512 MB) served all 100 64 MB requests with no
  OOM kill; web requests saw `memory_limit` 128M, `max_execution_time` 60, `max_input_vars` 1000 while CLI saw 256M from
  `php.d/zz-app.ini`. `high-concurrency` (18 workers, 48M) stopped the 64 MB request at the PHP limit, with no OOM kill,
  and served 8.5 rps of 2 s requests against 3.0 for `standard`. `TestIntegrationAppLifecycle` passes with the new
  config.
- Found while testing: the PHP image does not set `display_errors`, so a fatal error (here the memory limit) renders
  the message and script path with HTTP 200.
- Not verified: arm64; real framework apps; true CPU saturation (the busy loop is wall-clock, so CFS throttling
  lengthens it instead of capping throughput); single run per profile on a host also running a dev stack.

## App backup hardening (2026-10-08, linux/amd64, Docker 29.8.1)

- `BENTO_DOCKER_TESTS=1 go test -run 'Restic|Clone|Restore' ./internal/integration/` as root: passed
  `TestIntegrationResticCloneSameStack` (secrets on and off), `TestIntegrationResticAppBackup`,
  `TestIntegrationResticUnreachableRemoteFailsFast` and `TestIntegrationResticRestoreFromAnotherStack`. They cover the
  clone pool and `Run.Escalate`, the `bento/` symlink refusal on real `restic restore` output, the per-app snapshot
  filter (`--tag app=<id>`), and the long-lived job container (`sleep 2147483647` on the rclone/BusyBox image).
- Only fakes cover: recovery after a backend crash in the middle of a clone (`restic.recover`), collecting running
  orphaned job containers, the data-volume free-space check, and crafted snapshots with symlinks (unit tests).
- Not run: the full `make test-integration` (lifecycle and runtime image builds), arm64.

## Ingress hosts (2026-10-09, linux/amd64, Docker 29.8.1)

- Upgrade path on a real stack: a stack created by the previous binary (schema 3) with app `shop` owning
  `shop.test` (primary) and `www.shop.test` (self-signed, access log on), a reverse proxy `grafana` on `grafana.test`,
  and an operator drop-in under `edge/custom/routes/app-shop/`. Opened with the new binary: the store migrated to
  schema 4, the reconciler re-rendered the edge into `sites/host-*.conf` + `upstreams/app-shop.conf`, real `nginx -t`
  accepted it, both app hosts served the app over HTTP and HTTPS, the `app-shop` drop-in header was still applied,
  `shop.test` stayed the display host and the app kept its access log.
- New behavior, live: re-pointing `www.shop.test` to a redirect returned `301 https://shop.test:18443/y?q=1`, and
  after unpublishing the app `http://shop.test/y` (the target is no longer served); re-pointing `grafana.test` from
  its upstream to the app served the app; `host add` on an existing name was refused with a conflict; `host remove`
  refused an inexact confirmation and then removed the route. Ingress page, host dialog and app settings were checked
  in Chrome at 1400 px and 390 px.
- `make test-integration` as root passed (runtime images were already built, so that test only checked them).
- Not verified: ACME issuance per host, external certificates per host, HTTP/3, arm64.

## Data service tuning (2026-10-11, linux/amd64)

- `TestIntegrationServiceSettings` (real Docker, root): MySQL 8.4 and PostgreSQL 18 (pglayers) were tuned through
  `service.configure`; the running servers reported the new `innodb_buffer_pool_size`/`max_connections` and
  `shared_buffers`/`work_mem`/`max_connections`, kept them across `service.restart`, and a marker row written before
  the container was replaced was still there afterwards. pglayers keeps `shared_preload_libraries` in its config
  file and its CMD is a bare `postgres`, so the appended `-c` flags do not drop the preloaded extensions.
- `make test-integration` as root passed.
- Not verified: MySQL 8.0/9.4 and PostgreSQL 14–17 flag rendering on a live server, arm64, the System page in a browser.

## Not yet verified

- A successful SSH deploy with a deploy key registered at a git host (only the rejection path ran live).
- Deliveries sent by real git hosts (GitHub/GitLab/Gitea/Bitbucket); requests were simulated with curl. A real
  cloudflared path rule to the utils listener (only a plain container on the apps network was used), and hosts whose
  firewall filters container-to-host traffic.
- arm64 execution; PHP 8.3/8.5, Bun, and Python runtime images.
- Live Cloudflare Tunnel, ACME issuance, HTTP/3, external certificates, rclone upload to a real cloud backend,
  scheduled backup firing,
  SQLite restore, and stop persistence across an actual host reboot (verified by restart-policy inspection only).
- Base images are digest-pinned only for the verified versions (PHP 8.2-8.5, Node 24, Debian, edge, MySQL 8.4,
  PostgreSQL 17, Redis, cloudflared, rclone); other catalog entries are pinned by tag.

## PHP2Shell (CVE-2026-109618, CVE-2026-109619; 2026-10-24; live Docker 29.8.1, amd64)

- PHP 8.2-8.5 base images are pinned to the Oct 21 fixed releases by index digest: 8.2.35, 8.3.36, 8.4.27, 8.5.12
  (`php -v` checked in each pulled image). Pinning changes the image hash, so every PHP app is rebuilt and replaced.
- PHP 7.4, 8.0 and 8.1 have no upstream fix. They are now built from deb.sury.org packages on the pinned Debian
  bookworm base (`templates/images/php-sury`, overlaid on `images/php`): php7.4 1:7.4.33-31, php8.0 1:8.0.30-25,
  php8.1 8.1.34-9, all built Oct 21. Sury's index was read for bookworm amd64 and arm64: all three fixed. Bullseye
  arm64 still carried the old 8.1 build, which is why these versions moved off the EOL bullseye base. The build
  fails if an installed php-cli/fpm/common package is older than `domain.PHPSuryPackages`; `php -v` cannot show a
  backport. The Sury signing key is pinned by SHA-256. Backported builds disable session upload progress in code.
- Sury images: `php -m` is a superset of the previous official 7.4 image (extras: calendar, FFI, gettext, igbinary,
  shmop, sysv*, xsl). Debian's php.ini is removed so PHP runs on compiled defaults like the official images (Debian's
  production php.ini disables session GC in favour of a cron job the container does not have). `php`, `php-fpm`,
  nginx 1.22.1, Node 24, Composer, minicrond and s6 present; `php-fpm -t` accepts the generated pool settings.
  `TestIntegrationBuildRuntimeImages` built 7.4, 8.0 and 8.1; `TestIntegrationAppLifecycle` (real app container,
  hardened spec, app UID, instance lock, reconciler recreation) passed on each with the version swapped in
  temporarily (the committed test still uses 8.4).
- `zz-bento.ini` sets `session.upload_progress.enabled=0` and `fatal_error_backtraces=0`; the FPM pool sets
  `php_admin_value[session.upload_progress.enabled]=0`. Effective values checked on 8.2.35, 8.5.12, 7.4, 8.0, 8.1.
  Upstream says no configuration fully closes either CVE; the patch is the fix and these settings are partial
  mitigation.
- Not verified: arm64 images were not built or run (package availability only); exploit resistance (no PoC run).
  Sury images track the repository at build time, not a snapshot: a rebuild picks up newer packages, but nothing
  rebuilds existing images until the floor or another build input changes. The `bullseye` handling in
  `apt-sources.sh` and `RuntimeDebianBase` is now unused.

## PHP images moved to Debian trixie (2026-10-25; live Docker 29.8.1, amd64)

- PHP 8.2-8.5 now use `php:<v>-fpm-trixie` pinned by index digest (same fixed releases: 8.2.35, 8.3.36, 8.4.27,
  8.5.12; `php -v` checked). `DebianBase` is `debian:trixie-slim` (digest-pinned), which PHP 7.4 and 8.0 (Sury
  packages) build on; nginx there is 1.26.3. Changing the base changes every PHP image hash, so PHP apps rebuild.
- PHP 8.1 stays on `DebianBookwormBase`: Sury's trixie arm64 index still has the unfixed 8.1.34-8 (amd64 and arm64
  have the fixed 7.4/8.0 and amd64 has 8.1.34-9), and the build refuses a package below `PHPSuryPackages`. Move 8.1 to
  `DebianBase` once trixie arm64 has 8.1.34-9 or newer.
- The runtime-deps scan in `templates/images/php/Dockerfile` now drops `dpkg -S` "diversion by" lines, which trixie
  emits and which broke the apt install with bogus package names.
- `TestIntegrationBuildRuntimeImages` (8.4, 7.4, 8.0, 8.1, node 24) and `TestIntegrationAppLifecycle` (8.4) passed.
  Not run: PHP 8.2, 8.3 and 8.5 builds, arm64 builds, Python/Bun/Node 20/22 (still bookworm), Redis and PostgreSQL
  (still bookworm).

## Node and edge nginx update (2026-10-25; live Docker 29.8.1, amd64)

- Node 24 is `node:24-trixie-slim` 24.22.1 and Node 22 is `node:22-trixie-slim` 22.23.4, both pinned by digest (were
  24.21.0 and an unpinned 22 tag, on bookworm). The Oct 21 Node security releases are 22.23.4, 24.22.1 and 26.12.1; the
  advisory text itself was not read (only secondary reports), so check the CVE list before quoting it. The PHP images
  copy Node and npm from the Node 24 image, so every PHP image hash changed and PHP apps rebuild.
- Node 20 is end of life (last release 20.20.2) and has no fix; it is left as `node:20-bookworm-slim`. Not removed:
  existing apps may use it.
- Edge nginx is `nginx:stable-trixie` 1.30.6 (was 1.30.5), pinned by digest.
- nginx inside the PHP runtime images is Debian's package (1.26.3 on trixie); Debian backports fixes under the old
  version number, and the package is only refreshed when an image is rebuilt.
- Passed: `TestIntegrationBuildRuntimeImages` and the `internal/integration` suite with `BENTO_DOCKER_TESTS=1`.
  Not run: arm64.

## Remaining toolchains on trixie (2026-10-25; live Docker 29.8.1, amd64)

- Node 20 (still EOL, 20.20.2), Python 3.12 and 3.13 moved to the `-trixie-slim` variants, pinned by digest. Built
  with a temporary integration test (removed): node 20, node 22, python 3.12, python 3.13.
- Superseded below: Redis, PostgreSQL 14-16 and Bun were left on bookworm at this point.

## Data services and Bun refreshed (2026-10-25; live Docker 29.8.1, amd64)

- Redis 8.2 -> `redis:8.4-trixie` 8.4.7 (`RedisVersion` is now 8.4), pinned by digest. Booted by hand with the generated
  redis.conf + ACL file as 999:999: NOAUTH liveness reply, `ACL LOAD`, `CONFIG SET maxmemory`, AOF on, and a prefix
  user refused outside its prefix and refused FLUSHALL. No automated integration test covers Redis.
- PostgreSQL 14, 15, 16 -> `postgres:<v>-trixie` (14.24, 15.19, 16.15). Existing data directories were not reindexed
  (dev phase); the glibc locale change from bookworm can leave text indexes inconsistent. pglayers 17 (17.11) and 18
  (18.6) digests refreshed; both were already trixie.
- MySQL 8.4 -> 8.4.13 (new digest). The unmaintained `9.4` pin is replaced by `9.7` (LTS, 9.7.4); 8.0 stays at 8.0.46
  (EOL upstream). The images are Oracle Linux 9, not Debian. A version key is only read when a service is created.
- Bun 1.4 added: `oven/bun:1.4-debian` 1.4.4 (trixie), pinned by digest. 1.2 (bookworm) and 1.3 are kept so existing
  apps stay valid.
- `TestIntegrationServiceSettings` extended temporarily (reverted) to create, tune, restart and verify data on
  MySQL 8.4 and 9.7 and PostgreSQL 14, 15, 16, 17 and 18: passed. A Bun 1.4 runtime image built. Not run: arm64.
