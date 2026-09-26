# Bento system architecture

Status: current implementation architecture  
Audience: implementer, reviewer, operator debugging internals

## 1. Architectural summary

Bento is an on-demand, host-local desired-state controller around Docker Compose.

```text
Operator
  |
  v
bento CLI (Bun/TypeScript; one process per invocation)
  |  validate intent and external boundaries
  v
state.db (Bun SQLite) + .env + custom/ + overlays/
  |
  v
lock -> stage complete candidate -> promote -> validate -> scoped reload
  |
  v
Docker Compose data plane

Internet -> Nginx -> per-app PHP-FPM Unix socket -> private DB/Redis
                 \-> per-app process Unix socket -> Node/Bun/Python loopback HTTP
                 \-> reverse-proxy upstream
Cloudflare edge -> outbound cloudflared tunnel -> Nginx network namespace
PHP runner -> app-UID minicrond (jobs/workers/deploy drain) + root maintenance minicrond
```

There is no Bento daemon, remote API, or controller loop. Runtime converges only when the operator invokes a mutating command, `render`, or `apply`.

## 2. Product structure

The product is organized into these operator-facing areas:

| Area          | Responsibility                                                                     |
| ------------- | ---------------------------------------------------------------------------------- |
| Stack         | Identity, root, initialization, ingress mode, render/apply, Compose, export/import |
| Applications  | Stable identity, home, domain links, runtime, data bindings, lifecycle             |
| Traffic       | Nginx, app vhosts, reverse proxies, TLS, HTTP/3, access logs                       |
| Runtime       | Versioned PHP roles plus dedicated Node/Bun/Python process services and app CLI    |
| Data          | MySQL, PostgreSQL, SQLite, Litestream, Redis, credentials, grants                  |
| Work          | App-owned minicrond jobs/workers, signed deploy queue and operator hook                       |
| Recovery      | Logical backup/restore, scheduled rclone upload, stack transfer                    |
| Operations    | Status, doctor, support bundle, permissions, maintenance, reports                  |
| Customization | Nginx drop-ins, complete app templates, Compose overlays                           |

## 3. Control plane

### 3.1 Entrypoint and command adapters

`apps/cli/src/main.ts` is the source and compiled entrypoint. `apps/cli/src/commands/router.ts` owns global yargs parsing, help, error mapping, and command registration. `apps/cli/src/commands/subcommands/` groups scriptable command handlers; `apps/cli/src/ui/` handles redacted CLI presentation. `bento serve` exposes typed domain operations to the loopback-default web UI.

Command adapters SHOULD parse/present and coordinate use cases. They SHOULD NOT own domain invariants or direct filesystem/process details.

### 3.2 Domain and schemas

`apps/cli/src/domain/` defines branded identifiers, desired-state types, errors, and reload plans. Key discriminated unions include database engine, TLS mode, domain owner, database service, and reload target.

`apps/cli/src/schemas/` treats JSON and CLI-derived values as untrusted. Zod schemas reject unknown fields, malformed identities, unsupported state versions, broken references, duplicate bindings, and invalid domain ownership. Persisted state is schema version 1.

Branded TypeScript values reduce accidental mixing after runtime validation; they are not a substitute for boundary checks.

### 3.3 Services

`apps/cli/src/services/` owns use cases and state transitions:

- app/proxy/PHP/database lifecycle;
- generation, staged render/apply, and asset materialization;
- database grants, backups, restore, SQLite/Litestream, Redis ACLs;
- schedules, workers, deploy queue, permissions, logs, TLS;
- stack environment, safe Compose assembly, transfer, diagnostics, and maintenance.

Services receive a `Platform` and SHOULD remain independent from terminal formatting.

### 3.4 Platform adapters

`apps/cli/src/platform/` isolates Bun and host effects behind narrow interfaces:

- `FileSystem` with atomic writes and non-following `lstat`;
- exclusive/shared `FileLock`;
- argv-based `ProcessRunner`;
- injected `Clock` and `Random` for deterministic tests;
- source/compiled `AssetResolver`;
- stack-root `PathPolicy` with app-home containment.

Domain code MUST NOT import Bun/Node runtime globals directly. Test adapters provide fixed clocks, seeded randomness, in-memory locks, and recorded subprocesses.

### 3.5 Immutable assets

`apps/cli/templates/` contains base Compose, Dockerfiles, Nginx/PHP templates, and in-container helpers. Source mode reads these assets from the checkout. Compiled mode embeds them and publishes them through a digest-addressed `.asset-cache/<sha256>/` under the selected stack root before exposing stable `docker/` and `helpers/` paths.

Mutable stack data MUST never be inferred from or stored beside the executable.

## 4. Data plane and cardinality

| Component   |                    Cardinality | Lifetime                | Boundary/responsibility                            |
| ----------- | -----------------------------: | ----------------------- | -------------------------------------------------- |
| Bento CLI   |                 Per invocation | Ephemeral               | Validates intent; renders and operates stack       |
| Nginx       |                  One per stack | Persistent              | Only public base service; TLS, app/proxy routing   |
| PHP FPM     |    One per managed PHP version | Persistent              | One pool/socket per enabled assigned app           |
| PHP runner  |    One per managed PHP version | Persistent singleton    | s6-supervised app schedulers/workers/deploy drains |
| PHP CLI     |                    Per command | Ephemeral profile       | App UID/GID, home, selected PHP toolchain          |
| Process app |    One per managed process app | Persistent when started | Node/Bun/Python HTTP process + Unix socket adapter |
| Process CLI |                    Per command | Ephemeral profile       | Same process image, app UID/GID, own home          |
| MySQL       |        One per managed version | Persistent              | Private relational service + named volume          |
| PostgreSQL  |          One per managed major | Persistent              | Private relational service + named volume          |
| Redis       |                  One per stack | Persistent              | Shared/ACL cache + named volume                    |
| Litestream  |          Zero or one per stack | Persistent when enabled | Watches explicit replicated SQLite files           |
| rclone      | Per invocation/artifact upload | Ephemeral profile       | Backup-only egress; config + read-only backups     |
| cloudflared |          Zero or one per stack | Persistent when enabled | Outbound tunnel sharing Nginx's network namespace  |

PHP apps are not Compose services: apps assigned to a PHP version share that version's FPM and runner containers. Each Node.js, Bun, or Python process app is one dedicated Compose service because those runtimes do not provide an FPM-style multi-pool master.

## 5. Technology stack

### 5.1 Host control plane

| Concern                     | Technology/decision                                                                              |
| --------------------------- | ------------------------------------------------------------------------------------------------ |
| Runtime/language            | Bun 1.4.0, strict TypeScript                                                                     |
| CLI                         | yargs 18                                                                                         |
| Layout/colors               | cliui 9, picocolors 1                                                                            |
| Runtime validation          | zod 3                                                                                            |
| Templates                   | mustache 4                                                                                       |
| Cron parsing                | cron-parser 5                                                                                    |
| Version ordering            | semver 7                                                                                         |
| Standard helpers            | Node-compatible built-ins and focused npm packages                                               |
| Dependency resolution       | centralized `package.json`, committed `bun.lock`                                                 |
| Distribution                | `bun build --compile`, embedded `templates`, Linux amd64/arm64; optional control-plane container |
| Host orchestration          | Docker Engine + Docker Compose v2 CLI                                                            |
| Containerized control plane | Compiled Bento plus pinned Docker CLI/Compose; host daemon socket and same-path stack-root bind  |

The docs site is a separate Bun workspace using Astro 7/Starlight. It is not a control-plane runtime requirement.

### 5.2 Container/runtime stack

| Concern            | Technology/decision                                                              |
| ------------------ | -------------------------------------------------------------------------------- |
| Ingress            | Nginx stable on Debian Trixie; native ACME module; optional HTTP/3               |
| PHP image          | Debian Bookworm runtime built from official versioned PHP FPM                    |
| PHP extensions     | PDO MySQL/PostgreSQL/SQLite, mysqli/pgsql, Redis, OPcache, and common extensions |
| App tools          | Composer 2, Node 24/npm, Git, OpenSSH client, SQLite, gzip/zstd                  |
| Supervision        | s6-overlay 3.2.3.2                                                               |
| Scheduling         | Minicrond 0.2.2, one socket-only daemon per enabled PHP app plus root maintenance |
| Relational data    | official MySQL/PostgreSQL images per managed version                             |
| Cache              | Redis 7 Alpine                                                                   |
| SQLite replication | Litestream, S3-compatible object storage                                         |
| Backup egress      | rclone 1.68.2 isolated Compose profile                                           |
| Logs               | Docker `local`, logrotate, optional GoAccess reports                             |

Image/runtime upgrades are product changes and SHOULD preserve the cardinality, mount, identity, and safety contracts.

## 6. Application isolation model

### 6.1 Stable identity

New apps receive a stable UID/GID beginning at 10000. FPM, CLI, scheduler, worker, and deploy commands use it consistently. The app home is host `homes/<slug>` and container `/home/<slug>`.

Important app paths:

```text
/home/<app>/code/                 application code
/home/<app>/credentials/app.env  protected connection metadata
/home/<app>/.ssh/                stable Ed25519 deploy key
/home/<app>/.bento/              hook, queue, deployment metadata
/home/<app>/logs/                app/job/worker/deploy logs
```

Nginx uses shared group `bento-web` (GID 5000) for read/traverse and FPM socket access. It receives homes read-only. Private app directories remain app-owned.

### 6.2 PHP boundary

Every enabled app gets a dedicated FPM pool and socket, with selected capacity profile and filesystem policy. The app still shares the container namespace, runtime image, private-network reachability, and global capacity with apps on that PHP version.

This boundary reduces accidental crossing; it does not contain malicious code as a VM or dedicated container would.

### 6.3 Process-app boundary

A process app receives one dedicated container built from a curated exact-version Node.js, Bun, or Python base. It mounts only its own app home, its file-database directories, a private runtime socket directory, and immutable runtime assets. Its application command runs under the stable app UID/GID with a read-only container root and no Docker socket.

A small entrypoint-owned adapter forwards the app's Unix ingress socket to its fixed private loopback HTTP port. The application service publishes no host port. Dedicated containers improve accidental filesystem/process isolation and targeted restart behavior, but still share the Docker host, kernel, and private backend network; they are not hostile-tenant sandboxes.

### 6.4 Data boundary

- MySQL uses app-namespaced users/databases and explicit grants.
- PostgreSQL uses unprivileged app roles, app-owned databases, and revoked default public access.
- Plain/Litestream SQLite files live in private app-ID directories.
- Redis shared mode relies on app prefix discipline; ACL mode additionally restricts user key/channel patterns.

All backend services share a private network, so database credentials/grants remain necessary even without published ports.

## 7. Networking

### 7.1 Base topology

PHP FPM, runner, CLI, databases, Redis, and Litestream use stack-private networking. MySQL, PostgreSQL, and Redis publish no base host ports.

Nginx has two modes:

- **Host mode (default):** direct host 80/443 and host loopback; no Compose service DNS.
- **Bridge mode:** joins the stack private network, gains service DNS, and optionally publishes chosen HTTP/HTTPS ports. `host.docker.internal` maps to the host gateway; `127.0.0.1` is the Nginx container.

Normally one host-mode stack owns 80/443. Additional stacks require bridge mode, distinct publications, or internal-only ingress.

An optional `cloudflared` service uses `network_mode: service:nginx`, publishes no ports, and receives only a generated environment file containing its tunnel token. It therefore resolves loopback and reachable origins exactly as Nginx does: host loopback in host mode and the Nginx container/private network in bridge mode. Remote tunnel ingress rules are trusted operator configuration and can target anything reachable from that namespace; Bento does not claim per-origin network isolation. Application hostnames SHOULD target Nginx on local port 80 or 443. A loopback-bound `bento serve` origin is directly reachable only when Nginx uses host mode; any control-plane hostname MUST be protected by Cloudflare Access because Bento's web server has no application authentication.

### 7.2 PHP request path

```text
Internet
  -> Nginx server selected by unique domain
  -> host socket: <stack>/runtime/php-fpm/<php-service>/<app>.sock
  -> Nginx path: /run/php-fpm/<php-service>/<app>.sock
  -> FPM path:   /run/php-fpm/<app>.sock
  -> app pool under app UID/GID
```

Socket path translation, mount alignment, and shared group ownership are architecture invariants.

### 7.3 Process request path

```text
Internet
  -> Nginx
  -> /run/bento-apps/<app>/http.sock
  -> process container socket adapter
  -> 127.0.0.1:<internal-port>
  -> explicit app start argv under app UID/GID
```

The socket bind directory is ephemeral stack runtime state. Nginx mounts the parent read-only, while each process service mounts only its own socket directory read-write. This path is identical in host and bridge ingress modes and requires no public or loopback-published TCP port.

### 7.4 Reverse proxies

A proxy site uses the same domain/TLS ownership model as an app and forwards to URLs interpreted inside Nginx's current network namespace. The operator MUST choose upstream addresses valid from that namespace.

## 8. Desired state and ownership layers

### 8.1 Conceptual state model

```text
DesiredState schema v3
  defaults
    phpVersion, database service, FPM profile, Redis mode
  phpVersions[]
  databaseServices[]              MySQL | PostgreSQL
  sqliteBackup?                   stack-wide Litestream policy
  apps{slug -> AppState}
    identity/runtimeKind/TLS/log/template/deploy/Redis
    php runtime                   version/profile/docroot/pool
      or process runtime          language/version/image/service/argv/workdir/port/health
    databases[]                   MySQL | PostgreSQL | SQLite | Litestream
  proxies{name -> ProxySite}
  domains{domain -> app|proxy}    authoritative ownership
  timestamps
```

Desired state is stored in relational tables in the private Bun SQLite database `state.db`. Managed runtimes/services, applications, database bindings, logical databases, domains, and proxies/upstreams have explicit keys, ordering columns, checks, and foreign-key relationships. Deploy argument arrays are JSON fields on their parent rows and are validated when the complete in-memory state is reconstructed. Derived in-memory `database`, `mainDomain`, and `aliases` fields are compatibility views and are not persisted as authority. The first database binding is the primary/default view.

Every load reconstructs the complete in-memory model and applies the strict domain validator. Validation MUST enforce exact domain schema version, strict fields, managed-service references, one primary domain per owner, valid linked app references, and uniqueness of bindings. Every save validates first and replaces the relational desired state in one synchronous SQLite transaction while the stack's cross-process lock is held by mutation workflows.

### 8.2 Filesystem/storage classes

| Class             | Paths                                                                             | Treatment                                    |
| ----------------- | --------------------------------------------------------------------------------- | -------------------------------------------- |
| Desired/source    | `.env`, `state.db`, `secrets/cloudflare-tunnel-token`                             | Sensitive; private modes; back up            |
| Operator custom   | `custom/`, `overlays/`                                                            | Durable/trusted input; preserve/review       |
| Generated         | `generated/`, `docker/`, `helpers/`                                               | Rebuildable; never edit                      |
| Durable bind data | `homes/`, `sqlite/`, `litestream-meta/`, `certs/`, `backups/`, `rclone/`, `logs/` | Sensitive; protect and back up               |
| Durable volumes   | versioned MySQL/PostgreSQL volumes, Redis volume                                  | Outside stack root; explicit backup/transfer |
| Ephemeral         | `runtime/`, `locks/`                                                              | Recreated/recovered                          |
| Rebuildable cache | `.asset-cache/`                                                                   | Digest-addressed immutable assets            |

Generated trees may contain client credentials despite being rebuildable and MUST remain private.

## 9. Core flows

### 9.1 Initialization

1. Resolve absolute stack root and requested stable Compose project name.
2. Acquire the stack lock and apply all pending numbered SQLite schema migrations.
3. Refuse to initialize any database that already has desired state.
4. Create private state/environment and directory structure.
5. Generate administrator secrets once.
6. Persist empty domain-schema-v3 state with default PHP/MySQL services in one transaction.
7. Initialize private rclone config placeholder.
8. Render/materialize when requested by the command flow.

`bento migrate` explicitly applies pending database migrations for scripted CLI use. `bento serve` runs the same migration gate before entering the web/API server. Migration markers and DDL commit together; future/unknown migration versions are refused. Bento never reads `state.json` as a fallback.

### 9.2 App provisioning

1. Parse and normalize identity/domain/runtime/data input.
2. Validate global domain ownership and managed references.
3. Preserve UID/GID, credentials, and omitted selections for updates.
4. Add/replace only the selected binding; retain other bindings.
5. For explicit relational database creation, require live service and apply grants before recording success.
6. Atomically persist desired state.
7. Materialize app home/credentials/key/permissions without replacing operator files.
8. Render/apply and execute the runtime-focused plan unless deferred. New process apps remain disabled until their private service is explicitly built/started and passes operator health verification.

State, files, grants, and reload are not one distributed transaction. Commands MUST provide recoverable failure semantics rather than claim atomicity across all layers.

### 9.3 Render/apply transaction

```text
acquire stack render lock
  -> recover abandoned transaction journal
  -> render complete same-filesystem staging tree
  -> create deterministic managed manifest
  -> snapshot prior files/modes
  -> atomically promote candidates
  -> remove stale managed output last
  -> validate running targets
       fail: restore snapshots; do not reload
       pass: signal scoped roles
  -> finalize/remove journal
```

Validators consume live mounted paths, so validation occurs after promotion. Reload failure is distinct from validation failure: validated new files remain active for retry.

### 9.4 Scoped reconciliation

| Mutation                          | Typical plan                                  |
| --------------------------------- | --------------------------------------------- |
| Domain/proxy/TLS/access log/vhost | Nginx                                         |
| Pool/identity/PHP assignment      | Selected FPM; Nginx when route/socket changes |
| Process command/image/mount       | Recreate only a running app service           |
| Process domain/TLS/access log     | Nginx only; stable process socket path        |
| Internal deploy/SQLite schedule   | Matching PHP runner and app minicrond service |
| User job/worker edit              | App minicrond registry; no Bento apply        |
| Database backup/restore           | No web/runtime reload                         |
| Full apply                        | Nginx + all relevant FPM/runners              |

Stopped services consume generated configuration when next started. Apply does not start them.

### 9.5 Schedules and workers

The singleton PHP runner uses s6-overlay as PID 1. It supervises one socket-only, app-UID minicrond for each enabled PHP app and a separate root maintenance minicrond. Bento renders only reserved config-owned `[[job]]` tasks (deploy drain and SQLite VACUUM for apps, logrotate for root) into each daemon's main config; minicrond 0.2.2 syncs them before scheduling and rejects registry name collisions. User jobs and workers belong in minicrond's private per-app registry. Changes to user definitions do not trigger a Bento render. The reconcile helper adds/removes app daemon services in the dynamic s6 scan tree. Bento has no user job/worker tables, CLI CRUD commands, or oRPC CRUD routes; the protected gateway and app-scoped CLI delegate directly to minicrond.

### 9.6 Webhook deploy

1. Nginx routes the internal deploy endpoint to the read-only PHP helper.
2. Helper bounds body size, verifies HMAC against the exact bytes, locks queue state, and enqueues.
3. The app scheduler invokes the drain once per minute.
4. Drain obtains app deploy lock and executes one queued operator hook under app UID/GID with timeout/environment/payload snapshot.
5. Result and log are recorded; retention bounds queue/history.
6. Drain asks the PHP app's FPM path to reset OPcache; failure is logged but does not rewrite hook status.

The initial managed process runtime deliberately does not expose this PHP-backed webhook path. Process deploy enablement is refused until a language-neutral, app-scoped queue helper is implemented.

### 9.7 Logical backup and scheduled upload

1. Acquire one stack logical-backup batch lock.
2. Resolve all selected relational/plain-SQLite bindings.
3. Run matching in-container dump or SQLite online `.backup`.
4. Write a private partial, require successful non-empty output, atomically rename final.
5. After complete batch success, apply per-database retention.
6. For scheduled runs, persist bounded status; if configured, invoke ephemeral rclone `copyto` for each newly created artifact while preserving its path below `backups/`.

Litestream bindings are protected by the watcher/verify/export workflow rather than this local artifact loop.

### 9.8 Stack transfer

Export validates all expected volumes, records which data services are running, stops only those services, archives each volume, archives the stack root excluding ephemeral/cache paths, atomically publishes transfer files, then restarts exactly the previously running data services.

Import validates paths and archive entries, extracts into an empty root, validates imported state, applies optional project/ingress changes, rejects pre-existing destination volumes, restores only newly created volumes, re-renders, and runs Compose `up -d --build`. Failure cleanup may remove only volumes created by that import.

The root archive contains SQLite bytes but export does not stop all SQLite writers; it is not a replacement for SQLite-aware backup.

## 10. Security model

### 10.1 Public and trust surfaces

Only Nginx is public in the base topology. Public routes are configured app/proxy domains, ACME challenge handling, and optional signed deploy endpoints. Docker socket/Bento CLI access is equivalent to privileged host operations and remains outside the public surface.

Compose overlays and custom templates are trusted operator input and can expose ports, mounts, capabilities, or invalid directives. Bento cannot preserve its base security model against a deliberately weakening overlay.

### 10.2 Secrets

Sensitive assets include `.env`, `state.db`, the Cloudflare tunnel token, app credentials/SSH keys, deploy HMAC, database client files, TLS/CA keys, rclone config, dumps, logs, exports, and ACME state.

Secrets SHOULD flow through mode-restricted files or subprocess stdin, not host argv. Routine output and support bundles MUST redact known secret values. Archive sharing always requires operator review.

### 10.3 Destructive safety

The architecture separates desired-state removal from durable-data deletion. It refuses destructive Compose volume flags and relational service removal. Restore replacement, app/proxy removal, and prune use specific confirmation contracts. Recursive permissions must use `lstat` and never follow symlink targets.

## 11. Failure and recovery semantics

| Failure                               | Required result                                                                               |
| ------------------------------------- | --------------------------------------------------------------------------------------------- |
| Invalid state/schema                  | Refuse load; preserve bytes; direct operator to restore/fix                                   |
| Candidate generation failure          | Existing generated tree unchanged                                                             |
| Interrupted promotion                 | Journal retained; next render/apply recovers                                                  |
| Service validator failure             | Previous files/modes restored; no reload                                                      |
| Reload signal failure                 | Validated new generation retained; retry signal/apply                                         |
| Explicit DB create while service down | Fail without claiming database creation                                                       |
| Backup process failure/empty output   | No final artifact for that target; partial removed                                            |
| Later batch target failure            | Earlier final artifacts may remain; retention deferred                                        |
| rclone upload failure                 | Local artifacts remain; scheduled run records failure                                         |
| Restore failure                       | Destination may be partial; application must not use it                                       |
| Export archive failure                | Partial/final transfer artifacts removed; prior running data services restarted when possible |
| Import failure                        | Only newly created destination volumes eligible for cleanup                                   |

No behavior may imply distributed atomicity or zero downtime where the architecture cannot provide it.

## 12. Architecture invariants

1. One explicit stack root and one stable Compose project identity address a stack.
2. One Nginx is the only public base service.
3. One FPM and one singleton runner exist per managed PHP version; PHP CLI is ephemeral.
4. PHP apps share version containers. Each process app has one dedicated private service and ephemeral CLI role. Both retain stable UID/GID, socket, home, and data grants.
5. Domains are globally unique authoritative links with one primary per app/proxy.
6. Every app has at least one database binding; adding another does not migrate/remove old data.
7. Database/cache ports are not published by the base model.
8. `state.db` and `.env` are sensitive source-of-truth; generated output is disposable.
9. External and reconstructed database values are runtime-validated before becoming branded domain values.
10. State writes use SQLite transactions; generated promotion uses atomic same-filesystem replacement; mutation and migration workflows use the stack lock.
11. Validation precedes reload; validation failure restores prior generated files.
12. Destructive volume/service removal is not automated.
13. Runner replicas remain one.
14. Source and compiled distributions use one entrypoint and equivalent embedded assets/behavior.
15. The optional Cloudflare tunnel publishes no host ports, shares Nginx's network namespace, and receives no stack secret other than its dedicated token.
16. Process apps publish no host ports, expose only an app-specific Unix socket to Nginx, mount no sibling home, and receive no Docker socket.
17. Process apps are created disabled; public enablement requires an observed running private service. Process jobs and webhook deploy fail closed until their dedicated runtime implementations ship.

See [the technical decisions and acceptance contract](03-reimplementation-contract.md) for rationale and verification.
