# Verification record

What has been verified, how, and what has not. Recorded 2026-09-27 on Linux x86_64 (amd64), Docker Engine 29.8.1
(API 1.56), Go 1.27.0, running as root. All live checks used disposable stack roots in a scratch directory.

**Only amd64 was executed.** The arm64 binary is cross-compiled (static ELF aarch64) but has not been run.

Dated sections below are a log. Sections that mention schema versions 2–6 or store migrations before 2026-10-26
describe the pre-MVP store. The MVP restarted at schema 1; schema 2 (2026-10-31) is the production baseline, and
migrations from it onward run through `store.migrate` (2026-11-01).

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

## App backup only, stack backup password, database restore from a snapshot (2026-11-03, linux/amd64)

- Database dump backups (artifacts, `backup` schedules, `backup_runs`, rclone upload) were removed; schema 5 drops
  their table and schedules (`TestMigrateFrom4DropsDumpBackups`; schema 2/3 states still migrate to 5).
- `sudo make ci` steps fmt-check, vet, race tests and build passed; `check-generated` passes once the regenerated
  `types.ts` is committed (it compares against the index).
- Unit: `backup.EnsureStackKey` (8 concurrent callers agree on one `0400` key, no leftovers; symlinked/empty/multi-line
  keys refused), init uses the stack password for every repository, connect with an empty key adopts it and never
  creates one, `restic.restore-db` validation (exact `replace <db>`, foreign database, malformed snapshot, no
  repository), SQLite restore downloads only the `--include`d dump, refusals (`snapshot-invalid` for another app, a glob
  or `..` in the manifest path; `database-missing`; `restore-failed` for a non-SQLite file) leave the database
  untouched; API adversarial cases (CSRF, origin, unknown field, wrong confirmation, no repository, unknown app; token
  scopes refused); `bento backup password`.
- Real Docker (`BENTO_DOCKER_TESTS=1 go test -run 'Restic|Restore|Rclone' ./internal/integration/`, 30 min): passed.
  `TestIntegrationResticAppBackup` restores the SQLite binding from the latest snapshot with real restic (`--include`
  restores only that file, owned by the app UID). `TestIntegrationRestoreMySQL` (8.0, 8.4, 9.7) and
  `TestIntegrationRestorePostgres` (16, 17) now back up with restic and restore through `restic.restore-db`: swap with
  the app running, MySQL in-place rebuild with stop/restart, routines owned by the binding user; hand-made hostile dumps
  (root DEFINER, `GRANT … ON *.*`, `RESET ROLE`) through `RestoreRelational` leave the live database unchanged.
- Not verified: the web UI in a browser (Backups → Remotes, Restore DB dialog), a cloud remote, arm64.

## App backup only: live run against Cloudflare R2 (2026-11-03, live Docker 29.8.1, amd64)

Disposable `/srv/bento-e2e` stack (MySQL 8.4, PostgreSQL 18) built from the working tree, restic repositories on the
R2 test bucket under a fresh prefix (deleted afterwards). One PHP 8.4 app with a MySQL, a PostgreSQL (two databases)
and a SQLite binding, no git (code copied into the home).

- Removed: every `/api/v1/backups`, `/backups/{artifacts,runs,restore,download,delete,schedules…}` route answers 404;
  `bento backup run|list|runs|restore|schedule` exit 1 with `usage: bento backup password`.
- Stack password: `init` writes `secrets/restic/stack.key` (root, `0400`); `backup password` prints it with the stderr
  warning and refuses a non-root user; `restic/init` answers 202 with only `operation`/`statusUrl`; each app key file
  equals the stack key; `GET /backups/apps` has `stackPassword: true`. rclone test: good remote 2s, bad endpoint fails
  in 2s with rclone's DNS error.
- Connect: a random key fails `restic-key-rejected`; `{"key":""}` connects with the stack password. Add key with
  `export <slug>` returns a new 43-char key once (wrong confirmation 400).
- `restic.restore-db`: MySQL (CLI) and PostgreSQL (REST) restored with `method: swap`, rows back to the snapshot's
  counts, the app container never restarted; SQLite refused `app-running` before any download, restored when stopped
  (short snapshot id, file owned by the app UID). Wrong confirmation 400 (CLI and REST), unbound database 404, malformed
  snapshot 422, another app's snapshot in the same repository `snapshot-invalid`. A staging watcher saw only
  `data/backup/bento/db/<the one dump>` downloaded; `staging/` was empty after every run.
- Clone on stack (`restic/inspect`, `restic/clone`) and disaster restore (CLI without `--key-file`, REST with
  `"key":""`): code, untracked file, MySQL/PostgreSQL/SQLite rows restored; a random key file fails `wrong key`.
- Migration: a stack initialized by the committed binary (schema 4) with a dump schedule, one dump run and an
  app-backup schedule opened with the new binary at schema 5: `backup` schedules and `backup_runs` gone, the
  app-backup schedule kept, `backups/` files byte-identical. The pre-existing app keeps its own key (not the stack
  password); `stack.key` appears with the next repository init, which uses it.
- Finding: when an app has a MySQL and a PostgreSQL binding, both first databases are named `<slug>_main`; restore-db
  by name always picks the MySQL one, so the PostgreSQL `<slug>_main` cannot be restored (request has no engine or
  binding field). **Fixed the same day:** the request takes an optional `binding` (CLI `--binding`, sent by the UI);
  a name in more than one binding without it is refused (`TestFindDatabaseRefusesAmbiguousName`).
- Not run: the MySQL in-place path (views/routines → stop/restart) live, the web UI, arm64, Object Lock buckets.

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

## MCP and API tokens (2026-10-25; live Docker, amd64)

- Disposable stack, real backend: `bento mcp --scope read` over stdio answered `initialize`, listed 15 read tools and
  ran `list_services`; `--scope operate` ran `restart_service redis` and `wait_operation` returned `succeeded` with
  operation origin `local:operate`.
- `bento token create|list|revoke`: the token printed once; `POST /mcp` with a read token ran `get_status`; the same
  token got `403` on `POST /services/redis/restart`; `/mcp` without a token `401`; a revoked token `401`.
- Unit/contract: route-scope table matches registered routes and keeps delete/restore/exec/credential routes
  full-only; read/operate/admin enforcement, no CSRF for bearer writes, env redaction and env-change refusal,
  backup-upload refusal, expired/revoked/malformed tokens, `X-Bento-Scope` on a real Unix socket, MCP stdio framing,
  cancellation, and HTTP transport rules (Origin, protocol version, `GET` 405, sessions refused).
- Not verified: a real Claude Code / Desktop client session against either transport; the System → AI access panel
  was type-checked and built but not viewed in a browser.

## App scheduler over API tokens and MCP (2026-10-26; live Docker, amd64)

- Disposable stack (`/tmp/bms`, uids 50000+), real backend and minicrond in a running node 24 app, one job with a
  literal env value and one worker imported through `bento app minicrond -- import`.
- Tokens over HTTP: `GET …/scheduler/jobs` listed both with `envKeys: ["API_KEY"]` and no value; read token `403` on
  run; operate token ran the job (`202`), and a repeat with the same `Idempotency-Key` returned the same run;
  runs and run log (stdout and stderr lines) read back; admin token `403` on raw `scheduler/command`; a stopped app
  `409 precondition_failed`.
- `POST /mcp`: read/operate/admin tokens listed 3/6/7 scheduler tools; `get_scheduler_run_log`,
  `control_scheduler_worker` stop (worker `held`) and start, `stop_scheduler_run` on the active worker run,
  `set_scheduler_job_enabled` (admin only; operate gets unknown tool), and an unknown job returned in-band
  `not_found`.
- Found and fixed during the run: minicrond serves its API under the app's base path, so requests carry
  `runtime.SchedulerBasePath`; the unit fake now requires it. A stack root under a long path fails the relay's Unix
  socket bind (path limit) — not new, but use short roots for live checks.
- Not verified: a real Claude Code / Desktop client; arm64.
- Job definition writes (same day, new disposable stack): admin `create_scheduler_job` created a job with env and a
  worker with argv; operate does not list it; a second create of the same name returned `conflict`; a bad cron
  expression came back as minicrond's validation message. `update_scheduler_job` changed the schedule and one env
  value, removed one env key, cleared the timeout with `0`, and switched command to argv; minicrond accepted every
  round-tripped definition, and a run printed the updated env value while the untouched one survived the argv edit.
  Not exercised live: a concurrent edit losing the `If-Match` race (unit-tested against a fake).
- App code files (same day, new disposable stack, node 24 app): admin `list_app_files` showed `src` and two symlinks
  but not `.env`; operate does not list the tools. `read_app_file` of `src/main.js` returned content + sha256;
  `.env`, `../config/…`, a link to `/etc/passwd`, a link to `$HOME` and a write through a link to `/tmp` were all
  refused; a link to `$HOME` read back into `~/app` was allowed. `edit_app_file` with the read sha256 applied; a
  `write_app_file` with that now-stale sha256 got `conflict`; a create made a `0644` file owned by the app. With the
  app stopped, read and edit ran in a tool container that was removed afterwards. The scripts also ran under the
  unit tests with local bash (symlinks, hidden paths, binary refusal, UTF-8 windows, mode preserved, no temp files
  left).

## Edge front proxy (PROXY protocol) (2026-10-26; live Docker 29.8.1, amd64)

- Rendered config with the feature off is byte-identical to the previous release (diffed against `HEAD` for standard
  and custom edge ports); `nginx -t` in the pinned edge image accepted the enabled config with HTTP/3, ACME, tunnel
  and an IPv6 trusted prefix.
- Disposable stack (`/tmp/bfp-*`, uids 61000+), real backend: `bento edge set` refused a `0.0.0.0/0` trusted list,
  then enabled the front proxy on the host's LAN IP (edge on `127.0.0.1:18181/18543`, front on `:18180/18443`); the
  edge container published both pairs. An upstream host to an echo container received, through the front HTTPS port
  with `curl --haproxy-protocol --haproxy-clientip 203.0.113.9` and a forged `X-Forwarded-For: 6.6.6.6`:
  `X-Forwarded-For`/`X-Real-IP 203.0.113.9`, `Host shop.example.com`, `X-Forwarded-Proto https`,
  `X-Forwarded-Port 443`; the front HTTP port redirected to `https://shop.example.com/a`. Direct traffic on the edge
  port still carried `shop.example.com:18543` and port `18543`. A connection without a PROXY header and a container
  on the apps network sending one were both closed. Changing the trusted list reloaded the same container (the old
  peer was then closed); disabling recreated the edge without the extra ports or any `proxy_protocol` in the config.
  `Alt-Svc` was sent on the direct path and not on the front path (scratch edge with HTTP/3).
- Not verified: a real VPS over Tailscale, HAProxy as the sender, ACME issuance through the front proxy, the edge
  start retry after a reboot before `tailscale0` is up, the Ingress page in a browser.

## MVP baseline schema and fresh live e2e (2026-10-26; live Docker 29.8.1, amd64)

- The store has one baseline schema, version 1, with no migration chain; the unused `images` table is gone.
  `store.CheckCompatible` accepts only version 1 and refuses others without writing (unit tests cover foreign, older,
  newer, and non-SQLite files); `stack.Import` refuses an export with another schema version before creating the root.
- `make ci` as root and `make test-integration` (real Docker) passed. `bun run fmt:check`, `lint`, `check`, and
  `web:build` passed.
- A fresh stack initialised by the new binary has `user_version` 1, `application_id` `0x424E5431`, and 15 tables.
- The full live e2e (`docs/e2e-live.md` sections 1–10) passed on that fresh stack: five runtimes behind a real
  Cloudflare Tunnel, private SSH deploy keys, GitHub push webhooks, forged webhook calls, ingress modes, R2 database
  dumps and restore, restic app backup, clone and disaster restore, removal/prune/UID non-reuse, Laravel with
  scheduler and queue worker, Cachet, and Invoice Ninja.
- Not verified: ACME issuance, HTTP/3, arm64, scheduled backups, non-GitHub webhooks.

## Per-app Valkey and minicrond; shared Redis removed (2026-10-31; live Docker 29.8.1, amd64)

- The shared Redis service, its ACL sync and the `redis` service claim are gone. `store.SchemaVersion` is 2 (a
  version-1 `bento.db` is refused, no migration); container `specVersion` is 3. Apps join the data network only when
  bound to MySQL or PostgreSQL (`TestDataNetworkOnlyForServiceBindings`).
- Valkey 9.1.3 source pinned by SHA-256 `184e34ed…e293`, matching `valkey-io/valkey-hashes`. `TestIntegrationBuildRuntimeImages`
  built PHP 8.4, 7.4, 8.0, 8.1 (bookworm), Node 24 and Bun 1.4 with Valkey compiled on each image's own base; in the
  PHP 8.4 image `valkey-server` reports `v=9.1.3 malloc=jemalloc-5.3.0` and links only libc and libm.
- `TestIntegrationAppServices` (real Docker): a new app runs minicrond and no Valkey; turning Valkey on replaced the
  instance, readiness passed with `valkey-cli PING`, every process ran as the app UID, `REDIS_URL` was
  `redis://127.0.0.1:6379`, and `SAVE` wrote `~/.local/share/valkey/dump.rdb` (dir `0700`) in the host home. With both
  off, neither process ran and `/run/bento/failures` stayed empty after 5 s (no restart loop); turning Valkey back on
  reloaded the key from the snapshot.
- Unit/API tests: defaults (Valkey off, minicrond on), toggles persisted and replacing a running instance, app env
  winning over the `REDIS_*` defaults, port 6379 and `VALKEY_DATA` reserved, `temp-*.rdb` excluded from restic, and the
  scheduler API (409) and gateway (503) refusing without dialing when minicrond is off.
- `make ci` steps (fmt-check, vet, race tests, build) and `make test-integration` passed as root; `check-generated`
  differs only because the regenerated `types.ts` is not committed yet. `bun run fmt:check`, `lint`, `check`,
  `web:build` and `docs:build` passed.
- Tool containers (terminal tool shell, exec, file edits, deploy hooks) of a running Valkey app join the app
  container's network namespace (`ShareAppNetwork`); before this, `valkey-cli` in a tool shell was refused on a dev
  stack. `TestIntegrationAppServices` reads the key from a tool container via `valkey-cli -u "$REDIS_URL"`;
  `TestToolSharesAppNetworkForValkey` covers Valkey off and a stopped app keeping their own networking. A probe pair
  on Docker 29.8.1 showed the provider container can be stopped and removed while joined (the joiner keeps `lo` only),
  so an app replace is never blocked by an open tool shell.
- Runtime images ship `redis-cli` as a symlink to `valkey-cli` (as Valkey's own `make install` does);
  `TestIntegrationAppServices` runs `redis-cli PING` in the app container.
- Not verified: arm64 Valkey builds, PHP 8.2/8.3/8.5, Python and Node 20/22 images, the live e2e (`e2e-live.md`),
  the new UI controls in a browser, restic backup/clone of a Valkey snapshot end to end.

## Release blockers from the 2026-11-01 audit (live Docker 29.8.1, amd64)

- **Store migrations.** `store.Open` now upgrades `MigratableFromVersion` (2, the production baseline) through
  `SchemaVersion` via `store.migrate`; `migrations` is empty because 2 is current. `migrate_test.go` drives a
  synthetic step to version 3:
  - success writes a `0600` `VACUUM INTO` copy under `state-backups/` (`0700`) that still opens as schema 2, and
    turns foreign keys back on;
  - a failing step and a foreign-key violation (found by `foreign_key_check` while keys are off) both roll back,
    keep version 2 and the data, and name the copy in the error;
  - a missing step or a downgrade is refused without writing or copying;
  - opening a current-schema file creates no copy.

  `stack.Import` accepts `store.SupportedVersions()`, and export skips `state-backups`. Not verified: a real 2→3
  migration, since none exists yet.
- **Edge image and recreate.** `applyEdge` ensures the pinned image on every apply, not only when routes changed.
  A recreate now validates with the new image, renames the old edge to `-edge-previous`, creates the new edge,
  stops the old one, starts the new one, and removes the old one. A failure restores the old edge.
  - Fake engine: the fake gained `RequireImages` and per-container `FailOn` keys. On the old code,
    `TestEdgeImageBumpPullsBeforeRecreate` reproduced the outage ("No such image" after the old edge was removed).
    `TestEdgeRecreateFailureRestoresPreviousEdge` (Create and Start faults), `TestEdgeApplySettlesInterruptedRecreate`
    and `TestEdgeApplyRefusesForeignPreviousEdge` also fail on the old code and pass now.
  - Real Docker: `TestIntegrationEdgeRecreateAndRollback` moved the edge to new ports (new container, old port
    released, the new port answering 404 for an unknown host). It then tried a host port held by a listener: the
    operation failed `edge-start-failed`, and the same edge container came back running and serving on its previous
    port. No `-edge-previous` remained either time.
- **Env loader.** `env.bash` splits each line at the first `=` with parameter expansion. `IFS='=' read` dropped one
  trailing `=` (`K=abc=` loaded as `abc`); `TestEnvLoaderKeepsValuesVerbatim` fails on the old loader. The change
  under `templates/images/common` changes every runtime image tag, so every app is rebuilt and replaced on its next
  start or update, or by the reconciler.
- `make ci` and `make test-integration` passed as root (integration 264 s, runtime images 49 s). The frontend gates
  passed earlier the same day; no UI or DTO changed.
- Not verified: arm64, an edge image pin bump between two real releases (simulated by relabelling), ACME and HTTP/3
  across a recreate.

## Scheduler UI served from Bento, not the app socket (2026-11-02; live Docker 29.8.1, amd64)

Closes audit blocker 3 (a compromised app could serve its own script on the management origin by replacing minicrond
behind its socket).

- The gateway serves minicrond's page and assets from `internal/schedulerui`, vendored by `make vendor-minicrond-ui`
  from tag `0.5.5` (commit `cbb9366`). The minicrond 0.5.5 amd64 release binary (SHA-256 matching the pin) embeds
  byte-identical `index.html`, script and stylesheet; re-running the target reproduced the committed files. Only
  `/api/…` crosses the relay, and `hardenSchedulerResponse` admits JSON, plain text, event streams and TOML; it
  refuses redirects and untyped bodies (502) and adds a `sandbox` CSP.
- Unit (`go test -race`, root): `TestSchedulerGateway` runs against a hostile fake behind the relay that answers every
  path with a script. The shell for every client route is the vendored page, assets match the bundle and revalidate
  by ETag, and neither dials the relay. HTML, untyped bodies and redirects under `/api/` come back as 502 without the
  script. App cookies are dropped and credentials are not forwarded. Removing each guard in turn (proxying every
  path, the media-type allowlist, the redirect check, the sandbox CSP, the untyped-body check) made a test fail.
  `TestVendoredVersionMatchesPin` ties the bundle to `MINICROND_VERSION`.
- Creating an API token or changing the password from a browser session now requires the current password (422
  `currentPassword` otherwise; wrong guesses count against the login limiter); the control socket is exempt
  (`TestCredentialChangesNeedCurrentPassword`). The System token form asks for it.
- Live, disposable stack (`/tmp/bsx`, uids 62000+), node 24 app with the real minicrond 0.5.5 through the real relay:
  shell headers were minicrond's CSP with `frame-ancestors 'self'` and `X-Frame-Options: SAMEORIGIN`; both assets were
  byte-identical to the vendored files; `/api/v1/jobs` returned JSON with both the management and sandbox CSPs; a
  write without Origin got 403 and a same-origin trigger ran the job (202). Headless Chrome on the Processes & Tasks
  tab rendered the framed minicrond UI with the job, its schedule and the run, with no CSP violation inside the
  frame. Then, as the app UID inside the container, a Node server replaced the minicrond socket and answered every
  path with a script: the shell was still the vendored page, and `/api/v1/jobs` and `/api/v1/runs/x/log/raw` got 502
  (`content type "text/html"` in the backend log).
- Found, not changed: Bento's own `index.html` theme bootstrap is an inline script, which the management CSP
  (`default-src 'self'`) blocks; Chrome logs the violation on every page load.
- `make ci` steps fmt-check, vet, race tests and build passed as root; `check-generated` differs only because the
  regenerated `types.ts` is not committed yet. `bun run fmt:check`, `lint`, `check` and `web:build` passed.
  `make test-integration` was not run (no runtime, Docker, edge or data change).
- Not verified: arm64, Firefox and Safari, the Open in new tab path in a browser (same headers, checked with curl),
  minicrond's export/import and log-download flows in the browser, and the token form in a browser.

## minicrond 0.6.0 (2026-11-02; amd64; dev mode)

- Pin moved to 0.6.0 (`fe4651bd…` amd64, `21337b0e…` arm64; both checked against the release's `.sha256` files).
  `make vendor-minicrond-ui` now vendors the `minicrond-web-0.6.0.tar.gz` release asset after checking its published
  SHA-256 (`93693a1a…`, recorded in `internal/schedulerui/VERSION`); the three files are byte-identical to tag `0.6.0`
  `internal/api/assets` and to the bytes embedded in the 0.6.0 amd64 binary. minicrond's HTTP API, config keys, and
  CLI commands that Bento uses did not change between 0.5.5 and 0.6.0 (the OpenAPI diff is the version string).
- **Breaking:** 0.6.0 refuses a pre-0.6 data directory. Reproduced with the release binaries: a 0.5.5 data directory
  with one imported job made 0.6.0 exit 1 with `application_id 0x0, schema 12 … remove the data directory`. Bento is in
  dev mode, so there is no migration: existing stacks are re-initialized (the dev stack was).
- `go vet` and `go test -race ./...` passed as root with the new pin and bundle.
- Blocked, not verified: the host's tty layer deadlocked (tasks in `tty_open`/`ptmx_open` behind `console_lock`
  held in the DRM fbdev helper), so `apt-get` in the first runtime image build step hung in `ptmx_open` and no 0.6.0
  runtime image was built. `TestIntegrationBuildRuntimeImages`, `make test-integration`, and the live scheduler and
  hostile-socket checks have not run on 0.6.0.

## Runtime image build fixes (2026-11-02; live Docker 29.8.1, amd64; dev mode)

- Pins: phpredis 6.3.0 from `https://pecl.php.net/get/redis-6.3.0.tgz` (SHA-256 `0d5141f6…f0b5`, two downloads
  identical) replaces the unpinned `pecl install redis`. Bun 1.2 and 1.3 are pinned to their multi-arch index digests
  (`2505ef86…`, `9dba1a1b…`); the same lookup returns the existing Bun 1.4 pin. Bun 1.2 is bookworm (glibc 2.36),
  every other runtime base is trixie (2.41), so `ValkeyBuildBase` is bookworm.
- Valkey is built once, in a stage identical in every Dockerfile. The bookworm-built binary links only libc and libm,
  and `valkey-server --version` passed in every final stage, Bun 1.2 (bookworm) included.
- The PHP build stage now fails unless OPcache and redis load, and the final stage diffs `php -m` against it.
- `vim` → `vim-tiny` (`vim` alternative kept). PHP 8.4 image 819 MB → 759 MB as Docker reports it (layers
  611 → 564 MB).
- Layer sharing, measured: PHP 8.4 (official image) and 7.4 (deb.sury.org) share their first 9 layers (~466 MB:
  Debian, tools, Node.js/npm/pnpm, s6/minicrond/Composer, Valkey); their own layers are 98 MB and 128 MB. PHP 8.4,
  7.4, 8.0 and 8.1 add up to 2,361 MB but are stored in 1,429 MB (8.1 has its own bookworm prefix).
- `TestIntegrationBuildRuntimeImages` (root, real Docker) passed: PHP 8.4 3m9s (recompiled), 7.4 41s, 8.0 45s, 8.1
  1m41s, Node 24 38s, Bun 1.4 29s, Bun 1.2 30s. New unit tests `TestPlanArgsMatchDockerfileARGs` and
  `TestRuntimeImagesShareLayers` failed when one ARG was renamed and when one php-sury ENV value was changed.
- Live e2e on a fresh `/srv/bento-e2e` stack, sections 1–8 of `e2e-live.md`: all passed except new finding 21
  (deploying an app before its first start fails when the deploy script uses its Valkey).
- `make ci` (root) passed on the second run; the first run failed once in `TestScheduleUsesServerLocalTime`
  (`overlapping run not skipped`), which then passed 5/5 here and 5/5 on unchanged `HEAD`: a load-dependent flake,
  not investigated. `make test-integration` (root) passed.
- Not verified: arm64 (Valkey from bookworm, pnpm's native binary), PHP 8.2/8.3/8.5, Python 3.12, Node 20, e2e
  sections 9–10.

## Relational restore: staged swap, replayed as the binding user (2026-11-02; live Docker 29.8.1, amd64)

Fixes release blockers 1 and 2 of the 2026-11-01 audit: a restore dropped the live database before reading the
artifact, and a MySQL restore replayed the DEFINER-stripped dump as `root`, so every view, routine, trigger and event
came back as `root@localhost` with `SQL SECURITY DEFINER`.

- `TestDefinerFilterStripsDefinersAndPassesData` now feeds 1-, 7-, 13-, 64-byte and whole-line chunks. On the old
  filter, a whole `INSERT` line in one write came back with `'see DEFINER=root@localhost here'` as `'see here'` and
  `'DEFINER=`x`@`y`'` as `''`; the new filter leaves data lines alone.
- Fake engine (`internal/backup/restore_relational_test.go`): the MySQL replay logs in through a per-restore option
  file as the binding user (removed afterwards) into the staging database; the swap is one `RENAME TABLE` (live tables
  to trash first, identifiers with backticks quoted), with triggers and views dropped before it and events after it,
  and the live database is never dropped. A truncated artifact, a failing replay, and a cancellation at `swap` return
  `LiveUnchangedError` with no rename and no staging left. A staged view takes the in-place path, and `Quiesce` runs
  before the live database is dropped. A staging name that is not ours (MySQL: no grant; PostgreSQL: another owner)
  is refused, not dropped. PostgreSQL replays with `psql -U <user>` into staging, and a failed swap transaction
  re-allows connections.
- Real Docker, `TestIntegrationRestoreMySQL` on MySQL 8.0, 8.4 and 9.7 (the 8.0 and 9.7 pinned images ran for the
  first time):
  - A tables-only database with a foreign key was swapped in while the app ran (same container, still running). The
    data and the `DEFINER=`-looking row came back, and the foreign key still pointed at the live schema and was
    enforced.
  - A procedure the app user planted was restored `in-place`: the app was stopped and restarted, the procedure's
    `DEFINER` is `<user>@%`, and `CALL` runs as the binding user. A hand-made artifact with
    ``CREATE DEFINER=`root`@`localhost` PROCEDURE`` came back owned by the binding user too.
  - An artifact with `GRANT ALL PRIVILEGES ON *.*` and a truncated artifact both failed with "The live database is
    unchanged"; the user gained no global grant and the data was intact. No `bento__` database or grant remained.
- `TestIntegrationRestorePostgres` on PostgreSQL 16 (official image) and 17 (pglayers): the restore swapped, the table
  is owned by the binding role, and an artifact with `RESET ROLE; CREATE ROLE … SUPERUSER` failed with the live
  database unchanged and no role created. `psql -U <user>` over the local socket works on both images.
- Found while verifying (fixed in the next section): database-level grants were unescaped, and on MySQL 8.4.13 a
  user granted `` `a_b_main`.* `` read another tenant's `a1b_main` and created `axb_main`. The same check showed
  `log_bin=1`, so MySQL dumps with triggers or stored functions now fail to restore (an app user cannot create them
  either).
- `make ci` and `make test-integration` passed as root (integration 399 s, clone and cross-stack restore included,
  since clones restore through the same path). `bun run fmt:check` passed; no UI or DTO changed.
- Not verified: a large database, a swap while the app holds a long transaction (`lock_wait_timeout` 60s), a crash
  between the swap and dropping trash (covered only by the leftover cleanup unit test), arm64.

## Exact MySQL grants (2026-11-02; live Docker 29.8.1, amd64)

- Provisioning granted ``ALL PRIVILEGES ON `<db>`.*`` with `_` unescaped. MySQL treats `_` there as a wildcard
  (`partial_revokes` is off), so the user of app `a-b` (`a_b_main`) could read and write app `a1b`'s `a1b_main` and
  create `axb_main`. Every grant now goes through `dataservices.MySQLGrant`: `` `a\_b\_main`.* ``, followed by
  `REVOKE IF EXISTS` of the unescaped grant. The adoption check accepts either form. Restores (reset, swap-created
  live database) use the same helper.
- `TestMySQLGrantIsExactAndRevokesWildcard`: the escaped grant, the revoke of the unescaped form, and no revoke when the
  name has no wildcard character (it would remove the grant just made).
- Real Docker, `TestIntegrationMySQLGrantsAreExact` on MySQL 8.4 with apps `a-b` and `a1b`: after provisioning, a-b's
  user cannot read `a1b_main` or create `axb_main` and still uses its own database. A wildcard grant added the old way
  reproduced the leak; Add database then reprovisioned the binding, the leak was gone, and a-b's grants in `mysql.db`
  are exactly `a\_b\_extra` and `a\_b\_main`. `TestIntegrationRestoreMySQL` (8.0, 8.4, 9.7) and
  `TestIntegrationRestorePostgres` passed again.
- `make ci` and `make test-integration` passed as root (integration 404 s).
- Not migrated: existing wildcard grants change only when provisioning runs for that binding (BACKLOG, Decisions).
  The dev stack keeps its old grants until it is reinitialised or its bindings are reprovisioned.

## Production blockers 1, 4, 5, 7, 8, 9, 11 from the 2026-11-02 review (2026-11-03; live Docker 29.8.1, amd64)

- **Login (1):** an attempt now counts against the 10-per-5-minute budget when it is admitted, before argon2id runs,
  and at most 2 checks (64 MiB each) run at once; others get `429` without hashing. `TestLoginBurstIsBoundedAndCounted`
  sends 24 parallel wrong logins: at most 10 were hashed, the rest rate limited, and the right password then waits.
  Limiter unit tests cover the in-flight reservation and the concurrency cap. Not changed: the budget is still global,
  so a host-local process can keep browser logins locked (root on the control socket is unaffected).
- **HTTPS upstreams (4):** a DNS-named `https://` upstream renders `proxy_ssl_server_name on`, `proxy_ssl_name`,
  `proxy_ssl_verify on` (depth 4) against `/etc/ssl/certs/ca-certificates.crt` (present in the pinned
  `nginx:stable-trixie` edge image) and sends that name as `Host`; IP-named ones stay unverified. The rendered config
  passed `nginx -t` in the edge image, and a live edge proxied `https://example.com` (200) and refused
  `self-signed.badssl.com` with `upstream SSL certificate verify error: (18:self-signed certificate)` (502).
  Validation now requires one scheme per host and, for HTTPS, one host name.
- **Docker deadlines and forced cancellation (5):** `TestHungEngineCallsEndAtTheirDeadline` runs the real SDK against
  a Unix socket that never answers: inspect and stop end with `ErrEngineTimeout`, a pull that stalls after its first
  message ends with "made no progress". `TestCancelForcesAStuckOperationAfterTheGrace` (fake engine whose pull never
  returns) shows a cancelled `tunnel.apply` ending `cancelled` after the grace with a `cancellation forced` event, and
  the global `edge.apply` queued behind it then succeeding. Not exercised against a real wedged dockerd.
- **Rollout (7):** `TestUpgradeRollsOutOneAppAtATime` replaces exactly one of three drifted apps per pass (it fails
  with the old parallel submission: 3 after the first pass). `TestFailedReplacementPausesItsRuntimeOnly`: after
  alpha's replacement fails, beta (same runtime) keeps its instance with `rollout paused`, an app on node 22 still
  rolls out, and beta proceeds once an operator start of alpha succeeds.
- **Edge reload and image drift (8, 9):** `edge/conf/loaded` records the loaded generation.
  `TestLostEdgeReloadIsDriftAndRetried` injects a failed `SIGHUP` after promotion: `EdgeConfigDrift` reports drift,
  the retry reloads with no route change, and a further apply sends no reload. `TestEdgeImagePinChangeIsDriftAndRecreates`
  and `TestTunnelImagePinChangeIsDrift` relabel the containers as from another pin and see drift and a recreate.
  The tunnel label format changed, so every enabled tunnel is recreated once after this upgrade. Tunnel and Cloudflare
  not exercised live.
- **Valkey (11):** `TestIntegrationAppServices` on a real node 24 app (512 MB): `CONFIG GET maxmemory` is 134217728
  (a quarter) and `maxmemory-policy` is `volatile-lru`. The image template changed, so every runtime image rebuilds and
  running apps are replaced, now one at a time.
- `make ci` and `make test-integration` passed as root (integration 405 s, runtime 48 s); `bun run fmt:check`, `lint`,
  `check`, `web:build` and `docs:build` passed. No DTO changed. Not verified: arm64, a real tunnel, ACME.

## ACME backoff across reloads (2026-11-03; live Docker 29.8.1, amd64)

- Cause, from the nginx-acme source: an unissued certificate's state is copied into the new shared zone on reload
  only when it is ready; otherwise it resets to "request now", and an invalid order stops only "until the next
  configuration reload". Bento reloads the edge on most app operations, so a host that cannot validate was retried
  on each reload, against Let's Encrypt's 5 failed validations per hostname per hour.
- `edge.PlanACME` keeps per-host state in the `acme` setting: a 5-minute attempt window, then the boot certificate
  until `RetryAt` (1 h doubling to 24 h); saving the host restarts it; a valid certificate in the state directory
  (`edge.IssuedCertificates`, `<order_id>.crt` per the module source) clears it.
- `TestACMEHostWithoutCertificateBacksOffAcrossReloads` (fake engine, test clock): requested at once, held with the
  boot certificate after the window with a warning on the `edge.apply`, three applies during the backoff did not
  request it again, the next window requested it, and a certificate file cleared the state and reported `issued`.
  `TestSavingAWaitingACMEHostRetriesNow`, planner, scan and render unit tests.
- The pinned edge image (ACME module loaded) passed `nginx -t` on a render with one held and one requesting host.
- `make test-integration` passed as root (integration 417 s). `make ci` passed except the committed-types check,
  which needs the regenerated `types.ts` committed. Web checks and `docs:build` passed.
- Not verified: a real issuance (no public DNS on the dev host), so the module's real file layout is from its source;
  arm64.

## Operator password hash parameters (2026-11-03)

- New hashes use argon2id at OWASP's minimum (19 MiB, 2 iterations, 1 lane) instead of 64 MiB / 3 / 2, so a
  password check costs about a third of the memory. A stored hash keeps its own parameters: a hash from before
  verifies as before and is re-hashed at the next successful login (only if it is still the stored hash, sessions
  kept). `TestPasswordHashUsesOWASPParametersAndUpgradesOldHashes` covers both and refuses hashes with out-of-range
  parameters. bcrypt was not chosen: it is not memory-hard and truncates at 72 bytes (passwords allow 1024).

## Floating runtime bases, mise toolchains, operator rebuild and rollout (2026-11-03; live Docker 29.8.1, amd64; dev mode)

- `TestIntegrationBuildRuntimeImages` built php-8.4 (official base, 4m53s), php-7.4/8.0/8.1 (deb.sury.org),
  node-24, bun-1.4, bun-1.2 and python-3.13 from floating bases with mise 2026.10.12. Manifests recorded e.g.
  php-8.4: php 8.4.27 (≥ the 8.4.27 floor), composer 2.11.1 (latest-2.x, checksum from getcomposer.org), phpredis
  6.3.0 (newest PECL), node 24.22.2, npm 11.19.1, pnpm 12.16.1; python-3.13: 3.13.18 compiled with gcc against
  Debian's OpenSSL 3.5.7 (`ssl`, `sqlite3`, `ctypes`, `lzma`, `bz2`, `readline` import). Bun 1.2 now runs on trixie.
- In the php-8.4 image as UID 5001 with a read-only root through `bento-exec bash -l`: the home's mise shims lead
  `PATH`; `mise use -g node@22` (exit 0) switched `node` to 22.23.4 in later shells, and `mise use python@3.12` in
  `~/app` installed a precompiled 3.12.16 into `~/.local/share/mise` (persisted across containers). Using mise's
  `--system` installs for the image's tools was rejected: every later user install exited 1 trying to repair
  symlinks in the read-only system directory (also on mise 2026.10.25).
- Through `bento serve` on a disposable root: `bento images rebuild node-24` recorded the current build; a second
  rebuild (pull + no cache) resolved the same base ids, versions and Debian package digest and was discarded.
- Unit coverage (fake engine): unchanged rebuilds are discarded, changed ones leave apps on their build, new apps
  get the current one, rollout of one app/all apps, readiness confirmation, rollback, `image.rollout-stop` reverting
  only unconfirmed apps, the reconciler replacing rolled-out apps one at a time and stopping at the first failure
  (`TestOperatorRolloutStopsAtFirstFailure`), Bento-upgrade rollouts still pausing, compare-and-set assignment,
  schema 2 → 3 migration, stack-labeled builds not prunable by another stack, API refusals.
- `make test-integration` passed as root (integration 567 s, runtime 75 s). `go test -race ./...`, vet, gofmt and
  build passed; the committed-types check needs the regenerated `types.ts` committed. Web fmt/lint/typecheck/build
  and `docs:build` passed.
- Not verified: arm64 builds (mise arm64 checksum taken from the release's SHASUMS256.txt only); a rebuild that
  actually changed a version on real Docker followed by a real rollout and rollback (covered on the fake engine); the
  Runtimes UI in a browser.

## Per-app request filters: dot files, PHP probes, sensitive files (2026-11-03; live Docker 29.8.1, amd64; dev mode)

- New per-app setting `protection` (`blockDotFiles`, `blockPhpProbes`, `blockSensitiveFiles`, all on by default;
  schema 3 → 4 adds three `apps` columns, existing apps get all on). `runtime.ProtectionConfig` renders the Nginx
  location blocks once; the app-local Nginx (PHP apps) and every edge route that targets the app include the same text.
  Every block is `return 404`: dot files used to be `deny all` (403) and now answer 404 too, so a refusal never
  differs from "not found". Upstream and redirect hosts are not filtered.
- `TestIntegrationProtectionNginx` renders the blocks into real Nginx (the edge image) and requests 101 paths
  (28 legitimate, 73 hostile): all on blocks exactly the 73, each group alone blocks only its own paths, all off lets
  everything through. Legitimate paths kept reachable include `/.well-known/…`, `/sitemap.xml.gz`, `/wp-contents`,
  `/vendor/bootstrap/css/bootstrap.css`, `/info`, `/shell`, `/composer`; `/.well-known/../.env` is blocked (Nginx
  normalizes it first). The patterns use PCRE lookarounds, which Go's `regexp` cannot evaluate, so only this test
  vouches for them.
- `nginx -t` accepted the full rendered `app-nginx.conf` of a PHP app in the php-8.4 runtime image for front-controller
  and legacy routing, with protection on and off.
- `TestIntegrationAppProtection` (real Docker, edge on a free port): with the defaults, a Node app behind the edge and
  a PHP app on its own Nginx both answer 404 for `/.env`, `/wp-login.php`, `/wp-admin/`, `/composer.json` and
  `/app.js.map`, and 200 for `/about` and `/robots.txt`; `/.well-known/acme-challenge/x` reaches the app through the
  edge. After `PATCH` with all three off, `/.env` reaches the app in both layers and `/wp-admin/` reaches the PHP
  front controller. The change reloaded Nginx; the PHP instance kept its container id (no replacement).
- `specVersion` is unchanged: protection lives in `nginx.conf` (frontend scope), not in `AppContainerSpec`. A running
  app picks up the blocks with its next reload (update, start or reconcile); nothing is replaced.
- Upgrade note: schema 3 → 4 turns the PHP-probe and sensitive-file filters on for apps that already exist. A
  WordPress or legacy PHP app that serves such paths must turn **Block PHP probes** (and possibly **Block sensitive
  files**) off after upgrading.
- Unit coverage: store migration 3 → 4 and 2 → 4, protection round trip, patch semantics on create/update, backup spec
  round trip (a snapshot without the field clones with all on), edge rendering only for app hosts and before the static
  cache location, API strict decoding and CSRF for the new field.
- `make -C apps/backend ci` passed as root (gofmt, vet, race tests, generated-type drift, build); web fmt/lint/
  typecheck/build and `docs:build` passed.
- Not verified: arm64; a browser check of the Ingress settings cell (typecheck, lint, Prettier and the web build pass).

## Host traffic reports with GoAccess (2026-11-05; live Docker 29.8.1, amd64)

- GoAccess 1.12 (`allinurl/goaccess:1.12`, multi-arch index digest pinned; only amd64 executed) with the exact
  `traffic.Args`: an empty database directory with `--restore` works (the directory must exist); a second file adds
  to the totals; re-reading the same path and inode adds nothing; a copy under a new name is counted again (Bento
  never re-reads a deleted file); a restore-only run with no log and closed stdin rewrites the report; ignored panels
  are absent from the JSON. 400,000 generated lines (75 MB) took 5.3 s. The real output is the parser fixture
  `internal/traffic/testdata/goaccess-1.12.json`.
- `TestIntegrationTrafficReport` (real edge and GoAccess): an access-logged redirect host on a loopback port; of
  `/`, `/blog/post?token=…`, `/blog/post`, `/app.css`, `/img/logo.PNG`, `/.env`, `/.git/config` only the three page
  requests reach the traffic log, without query strings, and the referrer loses its query. A cycle rotates the file,
  delivers `USR1` to the hardened edge (read-only root, `CHOWN`/`DAC_OVERRIDE`), and the report holds 3 requests with
  no invalid lines. A request after the cycle lands in the reopened live file and the next cycle reports 4. The edge
  keeps running.
- Not run: arm64, the `404`/`444` filter against a real upstream (rendered and covered by config tests only), the
  hourly timer itself (cycles were called directly), and a front-proxy or tunnel client address in the traffic log.

## Restore DB service match, runtime upstream resolution, ACME module reload crash (2026-11-06; live Docker 29.8.1, amd64)

- **Restore DB.** `snapshotDump` matched a dump by engine and database name only. An app bound to two MySQL services
  has `<slug>_main` on both, so restoring one binding could replay the other service's dump. A relational dump must
  now come from the binding's service. `TestSnapshotDumpMatchesBindingService` covers it.
- **Upstream hosts.** `server host:port;` was resolved when the config loaded: with nginx 1.30.6 (the pinned edge
  image) an unresolvable name fails `nginx -t` (`host not found in upstream`), so one bad upstream blocked every edge
  apply, and an edge booting without DNS would not start. Upstream-host blocks now carry `zone` and
  `server … resolve`, through the existing `127.0.0.11` resolver. In the pinned image, `nginx -t` accepts `resolve`
  for both IP literals and names. `TestIntegrationEdgeUnresolvableUpstream` (real edge): a host whose upstream is
  `no-such-host.invalid` applies, answers 502, and a second host keeps serving. With the old template the same test
  fails at `nginx -t`.
- **ACME module on reload (found while setting up a live ACME check).** `load_module ngx_http_acme_module.so` was
  rendered only when an ACME host existed. The reload that adds the first ACME host to a running edge adds the module,
  and the nginx 1.30.6 master segfaults (exit 139), so every host goes down. Docker did not restart it: the edge had
  been up for less than 10 s. Reproduced outside Bento with Bento's rendered `previous` → `live` configs. A reload
  that drops the module, and reloads with the module always loaded (both directions), do not crash. The module is now
  loaded unconditionally. The edge shape version goes to 3, so an upgraded edge is recreated (cold start), never
  reloaded into the module. `TestIntegrationEdgeFirstACMEHostKeepsEdgeRunning` fails with the old template and passes
  with the fix.
- **ACME issuance itself is still not verified live.** A Pebble run reached the issuer only to fail TLS: the module
  ignores `SSL_CERT_FILE` (an `env` drop-in in `main.d`), and the issuer has no trusted-CA setting, so Pebble's CA
  cannot be trusted without a product change. A Let's Encrypt staging issuance on a public host is still needed.
- `make -C apps/backend ci` passed as root. `make -C apps/backend test-integration` passed on this host
  (integration 889 s, runtime 432 s).
- Not run: arm64; ACME issuance; the web checks (no frontend files changed).

## Real VPS run (2026-11-06/07; Linode, Debian 13, Docker 29.9.3, amd64, public IPv4 + IPv6)

First run on a fresh public host (`docs/real-vps.md`): install as `install.mdx` says (binary, `bento@.service`,
`init --mysql 8.4 --postgres 18`), the edge on `0.0.0.0:80/443` with HTTP/3, Let's Encrypt **staging** on nip.io
hosts, and the e2e fixtures from a private Gitea on the VPS (GitHub repository creation was not available to the
agent). `make ci` (as root) and the real-Docker suite ran on the VPS.

Verified live for the first time:

- **ACME issuance.** Seven hosts (five apps, Laravel, Cachet, two redirect hosts) issued by Let's Encrypt staging in
  10–15 s each. The module writes `edge/acme/bento_acme/<host>-<hash>.crt|.key`, which `edge.IssuedCertificates`
  matches: hosts show `certificate.state: issued` with the expiry. HTTP redirects to HTTPS keep path and query.
- **HTTP/3** over IPv4 and IPv6 (`curl --http3-only`, `HTTP/3.0` in the traffic log), `Alt-Svc: h3=":443"`.
- **IPv6** with the visitor's address in the access and traffic logs (after the fix below).
- **Gitea webhooks:** push → `deployed`, a push to another branch → `ignored-ref`, the generic bearer trigger → 202;
  no signature, a wrong `X-Gitea-Signature`, a wrong bearer, a wrong path and a GET → 404.
- **Host reboot:** `bento@prod` and Docker come back; running apps are healthy in ~40 s, a stopped app stays stopped,
  certificates persist. **Docker restart** while the backend runs, and with it stopped (after the fix below).
- **Backend SIGKILL mid-deploy:** the operation is `interrupted` in phase `deploy-script` after the systemd restart.
- Backups to an S3 remote (rclone `serve s3` on the VPS): remote test, a broken endpoint failing in 2 s, app backup,
  Restore DB (`swap`, 61 rows back, the app stayed up), restore-from-backup with the stack password and with a wrong key.
- Removal, slug reuse refused, prune, UID not reused; private ingress, unpublish/publish, legacy dotfile protection
  and every request filter; Laravel 13 (Classic recipe, MySQL queue drained by a minicrond worker, scheduler
  heartbeats, Valkey cache, push-to-deploy); Cachet 3.x on PostgreSQL 18 behind the public edge (`/dashboard`
  redirects to `https://`, Livewire URLs are `https`, no `http://` links).
- Load through the edge (wrk, 200 connections, 20 s): Node 870 req/s, PHP 190 req/s (FPM saturated: 217 client
  timeouts at 2 s, no edge errors, no non-2xx).

Fixed (one commit each):

- **IPv6 refused.** Docker 29 publishes `0.0.0.0` on IPv4 only; with only `[::]` added, IPv6 visitors arrive as the
  bridge gateway (docker-proxy). The network plan records whether the kernel has IPv6; the apps network is then
  dual-stack (Docker ULA /64) and an edge bound to all addresses publishes on `[::]` too.
- **Edge not restored by Docker.** Reloads (`ContainerKill HUP`) and hourly log reopens (`USR1`) set Docker's
  `HasBeenManuallyStopped`, so after any reload a Docker restart or reboot left the edge down until the backend
  recreated it (reproduced with a scratch container: `docker kill -s HUP` sets the flag). Reload and reopen run
  `nginx -s reload|reopen` in the container; `Engine.Signal` is gone.
- **Orphaned deploy scripts.** A running tool container (32-minute `sleep`) of a backend that died kept running
  `~/deploy.sh`; the reconciler now removes running tool and probe containers created before the backend started,
  without the grace period (gone 10 s after the SIGKILL).
- **Deploy failure summary** showed the line before the error (stdout and stderr on separate pipes): the script runs
  with stderr merged into stdout. Seen live on the Classic recipe's first deploy (`~/app/.env is missing`).
- **Webhook deliveries** read `deployed` for a deploy whose script failed: deliveries carry the operation state
  (CLI column, UI badge `deploy failed`).
- **ACME challenge warnings:** every Let's Encrypt validation request logged `using uninitialized "bento_route"`.
- **Failed handshakes during first issuance.** Until the module has a host's certificate its variables are empty, so
  every HTTPS handshake for the host failed (`cannot load certificate "/etc/bento-edge/live/"`): ~15 s on a working
  host, up to the 5-minute attempt window on a failing one, and `redirectHttps` sent port-80 visitors there. Mapping
  empty variables to the boot certificate failed live: variable certificates are loaded per handshake by the
  unprivileged worker, which cannot read the root-only boot key. A separate placeholder pair (`bento-pending`, owned
  by the nginx UID, key 0600, mounted read-only at `/etc/bento-edge-pending`; edge shape 4) is presented instead.
  Verified live: a new host answered with `bento-pending` from t+0 s, then the staging certificate; no
  `cannot load certificate`. `TestIntegrationEdgeFirstACMEHostKeepsEdgeRunning` checks the handshake on a real edge.

The manual-stop flag explains both reboots: before the first one the edge had been reloaded (not restored); before
the second it had only been started by the backend, which clears the flag (restored).

Not run: production Let's Encrypt, a real Cloudflare Tunnel, GitHub webhooks, Invoice Ninja (needs more than this
4 GB host had free), arm64, the web UI in a browser (the embedded UI, login, a wrong password and an authenticated
call were checked over loopback).

Final run on the finished branch (commit `b3fa1e1`: schema baseline 1 and the placeholder certificate), on the VPS;
the dev host only edited and built:

- `bun run fmt:check`, `lint`, `check` and `web:build` passed. `make ci` as root passed. The real-Docker suite passed: integration 17/17, runtime 28/28 including
  `TestIntegrationBuildRuntimeImages` (no skips).
- `scripts/vps/final.sh` on a fresh stack: install, edge, five apps deployed from Gitea and healthy, five staging
  certificates issued, every app 200 over IPv4 and IPv6, a Gitea push deployed (`succeeded`) and live.
- Reboot with `bento@prod` disabled, after many edge reloads: Docker alone brought back the edge and all apps; the
  sites answered 200 before the backend was started again.

## Backlog closure on the real VPS (2026-11-07; same host)

Every finding open in `BACKLOG.md` was fixed (one commit each) or recorded as a decision, then verified on the VPS
(`make ci`, the web checks, and live where a live check applies). Live checks:

- **Stack import:** the VPS stack (with a Python venv's `bin/python3 -> /usr/local/bin/python3`) exported and
  imported as a clone whose app started with its venv; before, every absolute link failed the import. Tampered
  copies are refused by checksum; an open `app exec` session is removed when the export quiesces; data services run
  only catalog images whatever the manifest names.
- **Restores:** `system id` and `source <secret file>` in a dump were checked to run as root (the secret echoed in the
  syntax error); with `--commands=OFF --skip-system-command` (MySQL 8.0.46/8.4.13/9.7.4) and `\restrict` as the
  `postgres` user (PostgreSQL 14.24–18.6) both are refused; a live PostgreSQL 18 Restore DB swapped in.
- **Patch images:** `mysql84` moved from 8.4.6 to the catalog 8.4.13 on restart with every row kept.
- **SQLite in homes:** an unlisted `database/database.sqlite` came back from a clone with a clean integrity check.
- **Login budgets:** a second local user rate limited after 10 guesses while the operator logged in.
- **Terminal CSRF:** the token travels as a WebSocket subprotocol (`bento.v1` selected); a wrong one is refused.
- **Edge:** cached assets keep `Alt-Svc` (`x-bento-cache: HIT`); a 1 MB host body limit answers 413 to 2 MB; the
  edge runs with 1 GiB / 1024 PIDs; on the VPS booted with `ipv6.disable=1` the stack plans an IPv4-only network and
  the edge starts without IPv6 listeners.
- **Webhook replay:** a signed delivery resent under a new delivery id answers `duplicate`.
- **Production Let's Encrypt (first time):** switching from staging kept serving the staging certificates (fixed: the
  ACME issuer and its state are per directory URL); after the fix production issued `CN=YE1` and `curl` without
  `-k` answers 200.
- **Invoice Ninja v5 (section 10, first run on this host):** 1536 MB app, 317 migrations on MySQL 8.4, account and
  seed, API login, client and invoice `0001`, minicrond worker running, incremental deploy in 5 s.

- **arm64, emulated:** with qemu-user and binfmt on the amd64 VPS, the whole Go unit suite compiled for arm64 passed
  (`GOARCH=arm64 go test ./...`, 19 packages), and the arm64 release binary (`ELF ... ARM aarch64`) ran `init` and
  `serve` against the real Docker until its MySQL 8.4 service was healthy. All 13 pinned image digests in
  `internal/domain` are multi-arch indexes with `linux/arm64` (adminer, goaccess, cloudflared, pglayers 17/18, MySQL
  8.0/8.4/9.7, nginx, PostgreSQL 14–16, rclone).

Not run in this pass: GitHub webhooks and a Cloudflare Tunnel (run next, below), and arm64 on arm64 hardware (runtime
image builds and app containers as arm64).

## Cloudflare Tunnel and GitHub on the real VPS (2026-11-07; same host, `85d1f29`)

The e2e tunnel (`bento-e2e`, no other connector running) attached to the VPS stack with `tunnel set-token`; GitHub
fixtures on the test account. The stack kept its public edge, so each app had both a nip.io host and an
`e2e-*.aitea.app` host routed through the tunnel to `bento-prod-edge:80`.

- **Tunnel:** `tunnel set-token` brought it to `healthy` in Bento and on Cloudflare (4 connections). Through Cloudflare,
  e2e-node, e2e-legacy, e2e-py and e2e-php answered 200 over HTTPS; the app saw `HTTPS=on`, `X-Forwarded-Proto:
  https` and the real client address, and plain `http://` through Cloudflare reached it as `http`.
- **ACME behind the tunnel:** a host with `tls: acme`, `redirectHttps: true` (e2e-php.aitea.app) answered 200 through
  the tunnel without a redirect loop, plain HTTP got one 301 to `https://`, and HTTP-01 through Cloudflare and the
  tunnel issued its staging certificate (`CN=e2e-php.aitea.app`).
- **Private GitHub repo:** `app git` with the SSH URL, the deploy key added read-only with `gh repo deploy-key add`,
  deploy cloned and ran `~/deploy.sh`.
- **Switching repository (fixed):** the app's checkout was still the Gitea repository, so the deploy refused with
  `git-origin-mismatch` and said to "move the existing code away". Moving `app/` away then made every deploy and
  `app exec` refuse with `app code directory ... is missing` (Bento will not recreate durable state empty). The
  guidance now says to empty `app/` and keep the directory, and `apps/deploy.mdx` documents switching repositories;
  done that way, the next deploy cloned the GitHub repository.
- **GitHub push to deploy, through the tunnel:** ping 200; push 202, delivery `github push deployed`, operation
  succeeded, new mark live (6 to 11 polls); push to a `feature-*` branch: `ignored-ref`; unsigned POST, a wrong
  `X-Hub-Signature-256`, a wrong bearer, a wrong path and a GET: all 404. (A hook edit without the secret made GitHub
  send unsigned pushes; Bento answered 404 to them, as it should; GitHub redelivery after restoring the secret: 202.)
- **Ingress modes:** e2e-bun `external`, routed by the tunnel to its `internalUrl`: 200 through the tunnel, 404 on the
  edge, 502 while stopped, 200 after start. e2e-py `none`: 404 through the tunnel and on the edge, app healthy.
- **Reboot:** the tunnel came back `healthy` by itself and every tunnel host answered as before. e2e-php answered 500 for
  the first seconds: Docker restarts every container at once and MySQL accepted connections 16 s after the daemon
  started; it served 200 from then on. That is the app needing its database, not a Bento fault.
- **Checks on `85d1f29`:** web checks and `make ci` on the VPS, exit 0; real-Docker suite 17 integration and 28
  runtime tests passed, 0 skipped.

## Cachet through the tunnel, Valkey snapshot failures, boot order (2026-11-07; same host, `75fd4a9`)

- **Cachet 3.x, all of section 9:** deployed from the GitHub fork (HTTPS, branch `3.x`, PostgreSQL 18, Valkey) on
  `e2e-cachet.aitea.app` through the tunnel. Admin user and Sanctum token through a script in the app's `tmp/`;
  `POST /api/components` and `/api/incidents` 201, both on `/`; scheduler and worker imported (`env_base: inherit`,
  `working_dir: ~/app`), five queued `inspire` jobs drained to 0, `failed_jobs` 0; GitHub push to deploy (ping 200,
  push 202, mark live, worker PID 63347 → 64613); a pushed migration that throws failed the deploy with
  `~/deploy.sh exited with status 1: e2e: intentional migration failure` while `/` kept 200, and the revert deployed;
  Livewire login through the tunnel redirected to `/dashboard` (200, no `http://` links). Two stale hooks on the fork
  from earlier dev-host runs were deleted.
- **Valkey stopped accepting writes after one failed snapshot (fixed).** The app-local Valkey logged the
  `vm.overcommit_memory` warning: with the default heuristic overcommit a snapshot fork can be refused, and Valkey's
  default `stop-writes-on-bgsave-error yes` then answers every write with `MISCONF` until a later snapshot succeeds.
  Reproduced on e2e-php by making `~/.local/share/valkey` unwritable (as a full home would): `BGSAVE` failed and `SET`
  returned `MISCONF`. The config now sets `stop-writes-on-bgsave-error no`; after the upgrade the backend rebuilt the
  runtime images and replaced the running apps by itself (40 s), and the same test gives `rdb_last_bgsave_status:err`
  with `SET` → `OK`, then `ok` once the directory is writable again.
- **Boot order:** the e2e-php 500s after the reboot were requests that arrived before MySQL accepted connections
  (ready 16 s after the Docker daemon). Recorded as a decision in `BACKLOG.md`: Bento does not hold routes back at
  boot, so sites come back even when the backend does not.
- **Boot order, measured (second reboot, `75fd4a9`):** a systemd unit polled each app through the local edge once a
  second from 5 s after boot. The Docker daemon was up at 11 s; PostgreSQL 18 accepted connections at about 36 s and
  MySQL 8.4 at about 38 s (every container starts at once on 2 vCPU). Until then apps answered 502 (container still
  starting), 500 (e2e-node, database refused) or held the request (Cachet, waiting on PostgreSQL); e2e-legacy (no
  query on `/`) was 200 at 15 s, e2e-node at 33 s, Cachet at 41 s, and all stayed 200. The Cachet queue worker, which
  started before PostgreSQL, was `running` with `failures: 0` afterwards, and the backend queued no operations: Docker's
  restart policies alone brought the stack back.
- **Final checks on `75fd4a9`, on the VPS:** web checks and `make ci` exit 0; real-Docker suite 17 integration and 28
  runtime tests passed (runtime images rebuilt cold for the new Valkey config), 0 skipped.

## arm64 on real hardware (2026-11-07; Hetzner, Debian 13 aarch64, 2 vCPU, Docker 29.9.3; `eff9d14`)

Native arm64, no emulation: `uname -m` is `aarch64` and Docker reports `arm64`. Steps from `real-vps.md` with
`BENTO_VPS=bento-arm`.

- **Unit suite:** `go vet ./...` and `go test -race -count=1 ./...`, built and run natively, exit 0.
- **Real-Docker suite** (test binaries cross-built with `GOARCH=arm64`): 17 integration and 28 runtime tests passed,
  0 skipped. `TestIntegrationBuildRuntimeImages` built every runtime image from cold caches in 883 s, so the mise
  toolchains, Valkey, minicrond, s6-overlay and restic artifacts all resolve and verify for arm64.
- **Smoke run (`final.sh`):** fresh `init` with MySQL 8.4 and PostgreSQL 18, public edge with HTTP/3 and Let's Encrypt
  staging, the five fixture apps from a Gitea on the host. All five `healthy`, 5 of 5 certificates issued, each app
  200 with its runtime and database versions (PHP 7.4.33 and 8.4.27 with Valkey, Node 22.23.4, Bun 1.3.14, Python
  3.13.18 with psycopg, MySQL 8.4.13, PostgreSQL 18.6) over IPv4 and IPv6; a Gitea push deployed and went live.
  Every container (apps, edge, MySQL, PostgreSQL) runs an `arm64` image.
- **From outside** (the amd64 VPS): 200 over IPv4 and IPv6, and over HTTP/3 (`--http3-only`, HTTP version 3).
- **Reboot:** all five apps answered 200 at 21 s of uptime; certificates kept, no operations needed.

No arm64-specific defects were found.

## Front proxy over Tailscale, CDN-style HTTP proxies (2026-11-07; both VPSes, Tailscale 1.104.5)

`bento-arm` (arm64) ran the Bento stack; `bento-test` (amd64) was the public front: its own Bento edge turned off,
Debian's nginx with the stream module on 80/443, configured exactly as `ingress/front-proxy.mdx` shows, reaching the
arm host over a tailnet (direct connection). Hosts `fp-*` and `l7*-*.104.64.207.10.nip.io` resolve to the front;
visitors were the arm host's public IPv4/IPv6 (over the internet) and the front itself.

Fixed (one commit each):

- **Every front-proxied request failed behind Tailscale** (`9907763`). Docker forwards the PROXY ports to the edge
  container and Tailscale masquerades forwarded traffic by default (`ts-postrouting`), so the edge saw the host's own
  apps-bridge address (`10.200.0.128`), not the VPS's tailnet IP, and closed every connection (444; the front logged
  `Connection reset by peer`, visitors got an empty reply). Its log showed only the visitor address. The edge log now
  ends with `peer=`, and the guide adds `tailscale set --snat-subnet-routes=false` and troubleshooting. Verified both
  ways: `peer=10.200.0.128` with SNAT, `peer=100.81.253.27` and 200 without.
- **An edge reload dropped in-flight ACME challenges** (`81fbbe1`). Adding a second host while the first validated:
  the same token got 200 from some workers and 301 from others (nginx-acme #26); once the module did not order again
  and the host sat in the 1-hour backoff. A window the edge reloaded in now ends in a 1-minute retry, not counted.
  Live: a host that cannot validate, with a reload in its window, went `waiting` for 1 minute with `failures: 0`, and
  the next undisturbed window counted (`failures: 1`, 1 hour).
- **No way to run behind a TLS-terminating CDN or reverse proxy** (`d0d7508`). Through an nginx `proxy_pass` front:
  plain HTTP to the edge with the HTTPS redirect looped (`301` six times), and HTTPS to the edge served the front's
  IP to the app. New edge setting `httpProxies`. Live, with the front appending `X-Forwarded-For`: both modes 200 with
  the visitor's IPv4 or IPv6, `HTTPS=on`, port 443, one hop; a visitor's forged `X-Forwarded-For` ignored (last
  address wins); an unlisted peer's `X-Forwarded-For`/`-Proto` ignored and its plain HTTP still redirected; a listed
  peer's garbage `X-Forwarded-For` falls back to the peer; no `Alt-Svc` through the proxy.
- **The edge served nothing after starting without its VPN address** (`0845af5`). `tailscale down` then an edge
  restart: Docker failed to bind the tailnet IP, and the reconciler's start left the edge running with no published
  ports, so direct visitors got nothing too, Bento said `healthy`, and it stayed that way after Tailscale came back
  (3 minutes watched). Now the front ports are published only while the address exists, and a running edge missing
  a port binding is recreated. Live: the broken edge was repaired 7 s after the backend started; with the VPN down
  the edge dropped only the front ports (6 s, warning in Activity) and direct stayed 200, even across a `docker
  restart`; with the VPN back the front path returned in 19 s.

Verified through the front proxy (PROXY protocol): ACME HTTP-01 issued for front-only hostnames; HTTPS 200 with the
visitor's real IPv4 and IPv6 (`REMOTE_ADDR`, `X-Forwarded-For`), `HTTPS=on`, `X-Forwarded-Port: 443`, no port in
`Host`; HTTP gets one 301 to `https://host/` without `:8443`; forged `X-Forwarded-For`, `X-Real-IP`,
`X-Forwarded-Proto`, `CF-Connecting-IP` ignored; a PROXY header sent by a visitor to the front: 400; an untrusted
tailnet peer on the PROXY ports: closed, with or without a header; the trusted front without a header: refused; no
`Alt-Svc` on this path; a push-to-deploy bearer trigger through it: 202 and deployed, wrong bearer 404; a reboot of
the Bento host: direct, front and HTTP-proxy paths 200 by 25 s of uptime.
