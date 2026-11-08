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

- **Superseded runtime builds are never removed automatically** (confirmed, both VPSes 2026-11-07/08). Every Bento
  upgrade that changes image inputs and every operator rebuild adds images of about 0.7-1.1 GB per runtime; nothing
  deletes the ones no app can use any more (inputs of an older Bento, no container). After a handful of releases the 38
  GB arm64 VPS filled: Valkey could not save on shutdown, an app stayed down until started by hand, and on 2026-11-08
  PHP apps failed their health checks until 14 GB of images were removed. Today an operator prunes them one at a time
  from the Docker inventory on **System** (typed confirmation), and **System** flags low disk. Proposed fix: after a
  rollout confirms, remove builds whose inputs differ from the current Bento's and that no container references (they
  cannot be rolled back to).

## Hardening (low)

- **App operations wait behind image builds** (confirmed, 2026-11-07). A global operation (such as `edge.apply`
  after an upgrade that changes the edge image) waits for running builds, and app operations queue behind it: 5-25
  minutes on the 2 vCPU VPSes. Apps keep serving; operator actions are delayed.
- **An operation can stay `running` after a forced cancellation on a full disk** (plausible, seen once on 2026-11-07):
  an `app.restart` whose step could not write kept its state, so that app's later operations queued behind it until
  the backend restarted.

## Never verified live

These come from `apps/backend/docs/evidence.md` and the audit:

- External certificates, and GitLab/Bitbucket webhook deliveries (Gitea and the generic bearer trigger ran live on
  2026-11-06; ACME staging and production issuance, HTTP/3, IPv6, a real Cloudflare Tunnel, GitHub deliveries, a
  front proxy over Tailscale and an nginx reverse proxy as a CDN stand-in on 2026-11-07).
- A real CDN (Cloudflare proxied DNS, BunnyCDN) in front of `httpProxies`; an HAProxy front proxy (`send-proxy-v2`);
  WebSockets and long-lived streams through a front proxy.
- SQLite restore. Relational restores of large databases (the swap is verified on small ones with MySQL 8.0/8.4/9.7
  and PostgreSQL 16/17), and a MySQL swap while the app holds a long transaction (`lock_wait_timeout` 60s).
- Crash recovery mid-clone (`restic.recover` runs a database drop) on real Docker. Interrupted or tampered imports.
- Scheduled backup retention pruning. A large-database restore drill. Disk-full behaviour.
- A minicrond job running when its app stops: services get `S6_SERVICES_GRACETIME` (8 s), stray processes 0.5 s, and
  whether minicrond waits for or signals its running jobs was not checked.
- The web UI has no automated tests.

## Decisions

- Apps share one apps network: each can reach the others' listeners and the backend's utils listener (which serves
  only self-authenticating routes: webhooks and database-browser tickets). Bento isolates apps by UID, read-only
  roots, dropped capabilities and private data credentials, not by network; Docker's no-inter-container option
  would also cut the edge off from the apps, and per-app networks (the edge, tunnel and tools joining each) are a
  redesign, not a fix. Apps that must not reach each other's listeners belong in separate stacks or behind
  authentication. App homes have no disk quota; `bento status` and **System** show the stack root's free space and
  flag it below 2 GiB or 5%.
- A forced cancellation (30 s after the request) cancels the step's context, which every Docker call, `Exec` and
  `Wait` included, honors. Plain file I/O (an export writing its archives) cannot be interrupted and ends on its own;
  the export is a global operation, so nothing else waits on a half-written file.
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
- After a host reboot Docker restarts every container at once, without ordering, and the edge routes to an app as soon
  as its container runs. An app that needs its database answers errors until the database accepts connections (on
  the 2 vCPU test VPS, measured per second on 2026-11-07: databases ready about 25 s after the Docker daemon, every
  app 200 by 41 s after boot, queue workers unharmed; see `apps/backend/docs/evidence.md`). Bento does not hold routes back at boot: the edge, apps and data services come
  back under their restart policies even when the backend is down or late, and gating them on the backend would
  trade a few seconds of app errors for an outage whenever it is. Apps that must not error at boot retry their first
  connection.
- The front-proxy trusted list is not checked against the stack networks before the network plan exists (a fresh
  stack accepts `10.0.0.0/8`, then allocates the apps network inside it, so any app could spoof visitor IPs through
  the PROXY-protocol ports). Accepted as is on 2026-11-03; operators should list only their front proxy's address.
