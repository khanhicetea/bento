# Bento

Bento is a self-hosted operations layer for running multiple isolated PHP applications and reverse-proxied services on one Linux server using Docker Compose.

![Bento logo](./bento-logo-3d.png)

This repository is a **Deno 2.9 / TypeScript** reimplementation of the Bento host control plane. It preserves the product model described in [`specs/`](specs/):

- one operator-owned Linux host
- Nginx as the only public service: host network by default, stack-private bridge mode as a multi-stack opt-in
- per-app Linux identity, home, PHP-FPM pool, and Unix socket
- version-shared PHP FPM / singleton runner / ephemeral CLI roles
- private MySQL and PostgreSQL services (per managed version), SQLite/Litestream files, and Redis, with multiple database bindings per app
- desired-state rendering with staged, validated, recoverable apply
- schedules, workers, webhook deploys, backups, and diagnostics

## Requirements

- Deno **2.9.3** (pinned 2.9.x line — see `src/version.ts` `DENO_TARGET_VERSION` and release notes)
- Linux with Docker Engine + Docker Compose v2 (data plane)
- No Python, Node.js, or `npm install` required to run the control plane (JSR/npm packages resolve through Deno + `deno.lock`)

Install or switch the runtime with the official installer / package pin, for example:

```bash
curl -fsSL https://deno.land/install.sh | sh -s v2.9.3
deno --version   # should report 2.9.3
```

## Quick start (source mode)

```bash
# pin/check runtime
deno --version   # expect 2.9.x (CI uses 2.9.3)

# format, lint, typecheck, test
deno task fmt
deno task lint
deno task check
deno task test
deno task test:integration   # soft-skips Docker-only steps when daemon is down
deno task test:stack         # real Docker stack harness (default name: testbento)

# initialize a stack root and render
deno task run --stack ./my-stack init --name my-stack
deno task run --stack ./my-stack render
deno task run --stack ./my-stack status

# create a default MySQL application
deno task run --stack ./my-stack app create demo \
  --domain demo.example.test \
  --docroot public \
  --db

# An Ed25519 deploy key is created once under homes/demo/.ssh/ (mounted as /home/demo/.ssh/).
# Register id_ed25519.pub with the Git host before cloning a private repository.

# add PostgreSQL as another database kind on the same application
deno task run --stack ./my-stack postgres add 17
deno task run --stack ./my-stack app update demo \
  --domain demo.example.test \
  --database-engine postgres \
  --postgres 17 \
  --db

# apply (validate + scoped reload when services are up)
deno task run --stack ./my-stack apply
```

Stack roots are **external** mutable state (desired state, homes, certs, backups, generated output). Immutable templates ship with the repository or compiled binary. The stable stack name is explicit and is not derived from the stack directory; it becomes `COMPOSE_PROJECT_NAME` and prefixes Compose containers, networks, and named volumes. `bento` remains the compatible default when `--name` is omitted.

### Multiple stacks and Nginx ports

Nginx uses host networking by default (`NGINX_HOST_NETWORK=1`), preserving direct host ports 80/443 and HTTP/3 behavior. Because only one host-network process can own those ports, additional stacks should opt into the stack-private bridge network and select distinct publications:

```bash
bento --stack /srv/bento/customer-b init --name customer-b
bento --stack /srv/bento/customer-b stack ingress set bridge \
  --http-port 8080 --https-port 8443
bento --stack /srv/bento/customer-b compose -- up -d --build

# Inspect effective settings
bento --stack /srv/bento/customer-b stack ingress show
```

Bridge mode keeps Nginx on that stack's private network. Blank `NGINX_HTTP_PORT` / `NGINX_HTTPS_PORT` values mean internal-only; advanced operators may instead publish addresses or ports in an operator-owned `overlays/*.yml` file. HTTPS publishes TCP and, when `HTTP3=true`, UDP on the selected port. `host.docker.internal` maps to the host gateway in bridge mode; `127.0.0.1` means the Nginx container itself.

## Compile and distribution parity

```bash
mkdir -p dist
deno task compile          # native host arch
deno task compile:amd64    # Linux x86_64 (release)
deno task compile:arm64    # Linux aarch64 (release)
```

The compiled `bento` executable needs no Deno/Python/Node on the target host. Immutable templates are embedded with `--include=templates` and materialize into a digest-addressed cache under the stack root (`.asset-cache/<digest>/`) before publishing stable Compose paths (`docker/`, `helpers/`). Mutable operator state always lives under an explicit external stack root — never next to the binary.

```bash
./dist/bento --stack /var/lib/bento init --name production
./dist/bento --stack /var/lib/bento render
./dist/bento --stack /var/lib/bento status
./dist/bento version   # reports bento version + pinned Deno target (2.9.x)
```

### Parity smoke (F-29 / F-30)

Source mode and the compiled binary must produce byte-equivalent generated files, equal state transitions/exit codes, and equivalent normalized diagnostics for identical inputs:

```bash
deno task test:parity      # compile + require binary parity suite
# or, with an existing binary:
BENTO_BIN=$PWD/dist/bento deno task test
```

Release CI (`.github/workflows/ci.yml`) runs:

1. `fmt:check`, `lint`, `check`
2. `deno install --frozen=true` (fail on lockfile / resolution drift)
3. `deno task test` (unit + contract)
4. `deno task test:integration` (soft-skips when Docker unavailable)
5. `compile` + binary smoke (`init`/`render`/`status`) + `test:parity`
6. `compile:amd64` and `compile:arm64` artifacts

Pin Deno **2.9.3** for source and compile. Documented operator paths use the explicit permission set in `deno.json` tasks (`--allow-read --allow-write --allow-env --allow-run --allow-net --allow-sys`). **Do not use unrestricted `-A` as the supported default.**

Host-level system scenarios (contract §8 fourteen scenarios) are tracked in [`scripts/system-scenarios.md`](scripts/system-scenarios.md).

## Architecture (short)

```text
Operator CLI (Deno/TS)
   -> desired state (state.json)
   -> complete candidate generation
   -> lock / stage / promote / validate / reload

Internet -> Nginx (host default / bridge opt-in) -> per-app PHP-FPM sockets
                                                   -> private MySQL / PostgreSQL services
                                                   -> private Redis

PHP runner (one per version) -> s6-overlay PID 1 -> per-app Supercronic + flat s6 workers
                             -> local deploy drain -> hook -> app FPM OPcache reset
```

Apps share containers by PHP version and isolate through UID/GID, pools, filesystem policy, DB grants, and optional Redis ACL — not one container per app.

## Command surface

| Area         | Commands                                                                                                                                                                                                                                                       |
| ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Interactive  | `tui` (wizard: apps, reverse proxies with multiple upstreams, databases, and common ops)                                                                                                                                                                       |
| Bootstrap    | `init`, `render`, `apply`, `status`                                                                                                                                                                                                                             |
| Diagnostics  | `doctor`, `support-bundle [output]` — validates runtime versions, network/storage/TLS/service health, permissions, volumes, overlays, and secret modes; bundles contain redacted diagnostics only                                                              |
| Live proof   | `test-stack [name]` (or `--test-stack [name]`, default `testbento`) — Docker harness covering MySQL and PostgreSQL PHP connectivity, PostgreSQL two-app isolation and backup/restore, mixed-engine status/raw export, app operations, and deploy; ACME skipped |
| Apps         | `app create\|list\|show\|update\|enable\|disable\|delete\|remove\|prune\|shell`; removal requires `--confirm "delete <slug>"` and retains durable data; `app prune <slug>` lists and permanently cleans retained home/database data only after typing `delete` |
| PHP          | `php add\|remove\|list`                                                                                                                                                                                                                                        |
| MySQL        | `mysql add\|list\|db\|shell\|size\|processlist` (version removal blocked; password rotation unsupported)                                                                                                                                                       |
| PostgreSQL   | `postgres add\|list\|db\|shell\|size\|processlist` (official major tags such as `17`; version/service removal blocked); apps select it with `--database-engine postgres --postgres 17`                                                                         |
| Proxy        | `proxy create\|list\|delete\|remove` (repeat `--upstream URL`; deletion requires `--confirm "delete <name>"`)                                                                                                                                                  |
| TLS          | `tls set --app\|--proxy --mode self-ca\|shared\|acme\|external`; `tls ca export --output PATH` (see TLS notes below)                                                                                                                                           |
| Background   | `cron …` (`cron reload <app>`), `worker …` (`worker signal <app> <name> --signal HUP`)                                                                                                                                                                         |
| Deploy       | `deploy enable\|disable\|rotate\|status\|drain\|instructions`                                                                                                                                                                                                  |
| Access logs  | `logs access enable\|disable\|rotate\|report --app <app>`; add `--attach` for the interactive GoAccess terminal (TUI: Applications → Access logs)                                                                                                              |
| Exec / shell | `app shell <app>`, `exec <app> [-- <cmd>]` — ephemeral PHP CLI as app UID (TUI: Applications → Open CLI shell)                                                                                                                                                 |
| Compose      | `compose files`, `compose -- <args>` (refuses `down -v`)                                                                                                                                                                                                       |
| Stack        | `stack ingress show`, `stack ingress set host\|bridge [--http-port N --https-port N]`; `stack export <directory>`, `stack import <directory> [--name NAME --ingress-mode bridge --http-port N --https-port N]`                                                 |
| Safety       | `permissions check\|repair [--shallow\|--recursive] [--dry-run]`, `backup [--all]`, `backup schedule register\|status\|unregister\|run`, `restore`                                                                                                             |

PostgreSQL is a first-class database kind alongside MySQL, SQLite, and Litestream. Managed private services can be added and listed with `postgres add <major>` / `postgres list`; calling `app update` with a new database kind links another binding without replacing existing data. Routine administration is available through `mysql db|shell|size|processlist`, `postgres db|shell|size|processlist`, and `sqlite`. Top-level backup enumerates all linked bindings; restore accepts `--engine mysql|postgres` when a mixed relational app would otherwise be ambiguous. Fresh state uses schema v4: apps persist `databases[]`, while authoritative domain records link domains to apps or proxies and mark one link primary. Older schema versions are rejected and no migration command is provided. PostgreSQL uses official major tags such as `17` (`postgres17`).

### Logical database backup and restore

```bash
# Every binding linked to the app is included; --all spans every app.
bento backup --app reports --gzip
bento backup --all

# Restore to a new namespaced database for verification.
bento restore --file /var/lib/bento/backups/postgres17/reports-....sql.gz \
  --app reports --engine postgres --target reports_verify

# Register the current stack for a daily 03:15 host-cron run.
bento --stack /var/lib/bento backup schedule register \
  --schedule "15 3 * * *" --bin /usr/local/bin/bento
bento --stack /var/lib/bento backup schedule status

# Run the same scheduled all-database operation immediately, or remove registration.
bento --stack /var/lib/bento backup schedule run
bento --stack /var/lib/bento backup schedule unregister
```

**Scheduled backups are emphatically on-host only.** They write logical dumps beneath the selected stack's `backups/` directory; Bento does **not** upload, replicate, or otherwise create an off-host copy. Configure and verify separate off-host replication before treating these dumps as a disaster-recovery backup.

Registration manages only the stack-qualified Bento block in the current user's host crontab and preserves unrelated entries. `status` reports registration plus a bounded, redacted last-run record; `unregister` leaves existing dumps and that record in place. Overlapping logical backup batches are rejected rather than queued.

MySQL uses matching `mysqldump`; PostgreSQL uses matching-major `pg_dump --no-owner --no-acl`. Dumps are written privately, checked for successful non-empty output, and atomically published. Retention runs only after the requested batch succeeds. Restore is not object-level atomic and can leave a partial destination after an import failure. Replacing an existing database requires exact target-name confirmation. Use logical backup/restore—not raw volume transfer—for PostgreSQL major upgrades.

### Full stack export and import

The stack transfer commands are intentionally CLI-only. Stack identity comes from the explicit `COMPOSE_PROJECT_NAME` in the stack `.env`, independently from the global `--stack` path. A same-machine clone should override both identity and ingress before import starts the stack:

```bash
bento --stack /srv/bento/clone stack import /srv/exports/bento-2026-07-21 \
  --name clone --ingress-mode bridge --http-port 18080 --https-port 18443
```

```bash
# Source host: destination must be empty and outside the stack root.
bento --stack /var/lib/bento stack export /srv/exports/bento-2026-07-21

# Produces:
#   stack.tar.gz          (state, homes, credentials, certificates, config, logs, backups)
#   mysql84-data.tar.gz      (one archive per managed MySQL volume)
#   mysql80-data.tar.gz      (example when another MySQL version is configured)
#   postgres17-data.tar.gz   (one archive per managed PostgreSQL volume)
#   redis-data.tar.gz        (the Redis volume)

# Destination host: --stack must be an empty/nonexistent destination root.
bento --stack /var/lib/bento stack import /srv/exports/bento-2026-07-21
```

Export verifies the named volumes, stops only the running MySQL, PostgreSQL, and Redis data services needed for a consistent raw copy, and restarts exactly those services afterward. Every volume archive uses its logical Compose volume name, and import maps it back using imported `state.json`. Ephemeral `runtime/`, `locks/`, and `.asset-cache/` are omitted. Import rejects missing, corrupt, unsafe, or unexpected archives and all existing destination data volumes, restores only newly created volumes, re-renders configuration, and runs Compose with `up -d --build`. Use matching CPU architecture and database image versions. In particular, raw PostgreSQL transfer requires a compatible PostgreSQL major/image version; use logical `backup`/`restore` for major upgrades. The archives contain secrets and private keys; encrypt and protect them when moving off-host.

### Runner service supervision

Runner containers use **s6-overlay 3.2.3.2** as PID 1. Applying cron or worker changes reconciles generated service directories into the live s6 scan tree; adding/removing a scheduler or worker does not restart the runner container or sibling services. Crontab-only changes send USR2 only to `scheduler-<app>`.

Every Compose service uses Docker's `local` logging driver with a shared 10 MiB / 3-file rotation policy. Each PHP runner also has an s6-supervised root maintenance scheduler. Supercronic runs per-app logrotate entries hourly, rotates app and captured worker logs at 10 MiB, and keeps two rotations. This is separate from each app's unprivileged crontab because PHP-FPM slow logs and captured worker logs can be root-owned. Rotation uses `copytruncate`, so Supercronic, PHP-FPM, workers, and application processes do not need reopen signals or restarts (with the usual small copy/truncate race window).

Scoped controls are available through `worker start|stop|restart|signal|inspect`. For diagnostics, the same service can be addressed inside its runner, for example:

```sh
bento compose -- exec -T php85-runner /command/s6-svstat \
  /run/bento-s6/services/worker-demo-queue
bento compose -- exec -T php85-runner /command/s6-svc -2 \
  /run/bento-s6/services/scheduler-demo
```

Migrating an existing stack requires one planned runner recreation. First run `bento render` to materialize the new image assets, then rebuild each PHP image and recreate its runner (for example, `bento compose -- build php85` followed by `bento compose -- up -d --force-recreate php85-runner`). Run `bento apply` afterward. Later cron/worker additions are live-reconciled without container restarts.

### TLS modes (F-12)

| Mode       | Behavior                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `self-ca`  | Bento manages one stack-private CA under `certs/private-ca/` and signs a separate SAN certificate for each app/proxy domain and its aliases. Certificates renew on apply when near expiry or when domains change. HTTPS redirect on. Clients must trust the exported CA.                                                                                                                                                                                     |
| `shared`   | One shared self-signed starter cert under `certs/boot.{crt,key}` (created on materialize / Nginx entrypoint). This is the default and has **no** HTTP→HTTPS redirect. It is convenient for startup but does not provide domain-name validation.                                                                                                                                                                                                              |
| `acme`     | Nginx's native `ngx_http_acme_module` automatically obtains and renews certificates using one shared `bento_acme` issuer. Configure `ACME_EMAIL` and `ACME_URL` in the stack `.env` (`ACME_URL` defaults to Let's Encrypt production). State persists under `certs/acme-state/`; no Certbot command is needed. HTTPS redirect is enabled. **DNS A/AAAA for every site domain must point at this host and public port 80 must be reachable before issuance.** |
| `external` | Operator-managed cert+key under stack `certs/` (paths validated; private key must not be world-readable, mode `0600`). HTTPS redirect on.                                                                                                                                                                                                                                                                                                                    |

TLS changes reload **Nginx only** (PHP/runners stay up).

Export and install the private CA's **public certificate** (never copy `ca.key`):

```sh
bento tls ca export --output ./bento-ca.crt

# Debian/Ubuntu target
sudo install -m 0644 bento-ca.crt /usr/local/share/ca-certificates/bento-ca.crt
sudo update-ca-certificates

# RHEL/Fedora target
sudo install -m 0644 bento-ca.crt /etc/pki/ca-trust/source/anchors/bento-ca.crt
sudo update-ca-trust
```

Applications on the target server may need their own CA bundle reload or service restart after trust-store changes. Back up `certs/private-ca/` securely: losing `ca.key` prevents renewal, while replacing the CA requires redistributing trust.

### Permissions (product §6.9)

- `permissions check <app>` — policy issues without changes
- `permissions repair <app> --dry-run` — print planned fixes
- `permissions repair <app>` / `--shallow` — fix core dirs only (default repair path)
- `permissions repair <app> --recursive` — bounded walk; **never follows symlink targets**
- App create may apply recursive policy while the home tree is still small; routine startup/apply paths use shallow repairs only.

Global flags: `--stack PATH` (or `BENTO_STACK_ROOT`), `--json`. Command parsing and help use **yargs**; table layout uses **cliui**; colorized operator output uses **picocolors**. Desired-state and CLI input validation use **zod**; cron schedules use **cron-parser**; PHP/MySQL version ordering uses **semver**; config templates use **mustache**. Standard library helpers come from official `@std/*` packages (path, yaml, encoding, assert).

## Specs and acceptance

Product, architecture, and reimplementation contract live in:

1. [`specs/01-product-spec.md`](specs/01-product-spec.md)
2. [`specs/02-system-architecture.md`](specs/02-system-architecture.md)
3. [`specs/03-reimplementation-contract.md`](specs/03-reimplementation-contract.md)

Unit, contract, and integration tests cover state validation, domain uniqueness, render rollback, compose safety, guarded app/proxy removal, app disable/enable lifecycle, deploy HMAC/queue policies, CLI smoke flows, multi-app isolation, TLS/routing modes, and corrupt-boundary rejection.

## Explicit non-goals (Phase G)

Bento intentionally does **not** provide:

- multi-host / Kubernetes / remote control plane / browser admin UI
- one container per app (apps share PHP version containers; isolation is identity-based)
- unconfirmed destructive deletion of app homes or databases (`app prune` is CLI-only, lists every known part, and requires typing the literal `delete`)
- automated MySQL or PostgreSQL version/volume deletion (`mysql remove`, `postgres remove`, and `compose down -v` are blocked)
- automatic relational-database password rotation (the operator must coordinate the database, Bento state/credentials, and dependent applications)
- automatic off-host backup replication (logical dumps stay under the stack root)
- a hard-coded Git deploy workflow (webhook orchestration + operator `deploy.sh` only)
- a Python runtime dependency
- per-app CPU/memory quotas inside shared PHP containers

See [`specs/01-product-spec.md`](specs/01-product-spec.md) §8 and `tests/unit/phase_g_test.ts`.

## Project layout

```text
src/
  main.ts                 # entrypoint
  domain/                 # branded types, state model, errors, reload plans
  schemas/                # current-state runtime validation boundary
  platform/               # Deno adapters (fs, lock, process, assets, paths)
  services/               # app, php, mysql, render, deploy, …
  commands/               # CLI router
  ui/                     # operator output + interactive TUI helpers
templates/                # immutable nginx/php/helpers assets
tests/
  unit/                   # domain + render + deploy unit suites
  contract/               # CLI smoke + source/binary parity
  integration/            # stack bootstrap / multi-app / boundary suite
scripts/system-scenarios.md  # contract §8 host checklist
specs/                    # product specifications
.github/workflows/ci.yml  # release gates
```

## Nginx performance and sideload configuration

Bento ships conservative production defaults for connection reuse, buffered access logs, gzip, static assets, cache-stampede protection, upstream failover, and WebSocket upgrades. Dynamic PHP and proxy responses remain **uncached** by default because Bento cannot infer application authentication or cache semantics.

Do not edit `generated/nginx/`; it is replaced on render. Put additive configuration under the operator-owned `custom/nginx/` tree instead. Bento creates the directory structure when needed and preserves everything placed inside it:

| Path | Nginx context and load order |
|---|---|
| `main.d/*.conf` | main context |
| `events.d/*.conf` | end of `events` |
| `http.d/*.conf` | `http`, before generated sites |
| `sites.d/*.conf` | `http`, after generated sites |
| `apps/<slug>/server.d/*.conf` | both app HTTP and HTTPS server blocks |
| `apps/<slug>/http.d/*.conf`, `https.d/*.conf` | one app protocol only |
| `proxies/<name>/upstream.d/*.conf` | the named proxy `upstream` block |
| `proxies/<name>/server.d/*.conf` | both proxy HTTP and HTTPS server blocks |
| `proxies/<name>/http.d/*.conf`, `https.d/*.conf` | one proxy protocol only |

Drop-ins are loaded lexically, so prefix files with `10-`, `20-`, and so on when order matters. They are mounted read-only into Nginx. Directives must be valid in the context shown, and redefining a generated singleton directive can make `nginx -t` fail.

For example, `custom/nginx/apps/shop/server.d/20-health.conf` can add a location without forking the generated vhost:

```nginx
location = /healthz {
  access_log off;
  return 204;
}
```

Run `bento apply` after changes; Bento validates the candidate before reloading. For changes to managed locations rather than additive directives, use `bento template select --app <slug> --kind vhost` to create a complete app-owned vhost template. Returning to the upstream template preserves that custom source.

## Security notes

- Only Nginx is public in the base topology.
- Database and webhook secrets are not printed in ordinary status output.
- MySQL and PostgreSQL administrator passwords are generated once when `init` first creates the stack `.env`; each app database password is generated once during initial provisioning.
- Set `HTTP3=true` in the stack `.env` and render to enable HTTP/3/QUIC listeners and `Alt-Svc` headers; it is disabled by default. Bridge-mode HTTPS publication includes matching UDP when enabled.
- Bento does not rotate MySQL/PostgreSQL app passwords or reset existing account passwords during reconciliation. Operators must coordinate any password change manually across the database, Bento state/credentials, and dependent applications.
- Database administrator and app passwords are not passed on host process argv for admin SQL.
- Deploy HMAC secrets live in desired state / FastCGI params, not app-writable secret files.
- Deno permissions are explicit in `deno.json` tasks (not `-A` by default).

## License

Operator-owned deployments: you own the server, state, app files, volumes, certificates, and backups.
