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
- A bearer-authenticated `POST /_webhook/deploy/<id>` queued an `app.deploy` through each of: loopback (the
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
- A ticket for a MySQL 8.4 binding redeemed once (303 + grant cookie on `/_dbadmin/b/<bid>/`); a second redeem got
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

## Not yet verified

- A successful SSH deploy with a deploy key registered at a git host (only the rejection path ran live).
- Deliveries sent by real git hosts (GitHub/GitLab/Gitea/Bitbucket); requests were simulated with curl. A real
  cloudflared path rule to the utils listener (only a plain container on the apps network was used), and hosts whose
  firewall filters container-to-host traffic.
- arm64 execution; PHP 8.3/8.5, Bun, and Python runtime images.
- Live Cloudflare Tunnel, ACME issuance, HTTP/3, external certificates, rclone upload, scheduled backup firing,
  SQLite restore, and stop persistence across an actual host reboot (verified by restart-policy inspection only).
- Base images are digest-pinned only for the verified versions (PHP 8.4, Node 24, Debian, edge, MySQL 8.4,
  PostgreSQL 17, Redis, cloudflared, rclone); other catalog entries are pinned by tag.
