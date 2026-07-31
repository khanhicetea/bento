# Bento system architecture

Status: current implementation architecture  
Audience: implementer, reviewer, operator debugging internals

## 1. Architectural summary

Bento is an on-demand, host-local desired-state controller around Docker Compose.

```text
Operator
  |
  v
bento CLI (Deno/TypeScript; one process per invocation)
  |  validate intent and external boundaries
  v
state.json + .env + custom/ + overlays/
  |
  v
lock -> stage complete candidate -> promote -> validate -> scoped reload
  |
  v
Docker Compose data plane

Internet -> Nginx -> per-app PHP-FPM Unix socket -> private DB/Redis
                 \-> reverse-proxy upstream
PHP runner -> app Supercronic + deploy drain + app workers
```

There is no Bento daemon, remote API, or controller loop. Runtime converges only when the operator invokes a mutating command, `render`, or `apply`.

## 2. Product structure

The product is organized into these operator-facing areas:

| Area | Responsibility |
| --- | --- |
| Stack | Identity, root, initialization, ingress mode, render/apply, Compose, export/import |
| Applications | Stable identity, home, domain links, runtime, data bindings, lifecycle |
| Traffic | Nginx, app vhosts, reverse proxies, TLS, HTTP/3, access logs |
| Runtime | Versioned PHP FPM, singleton runners, ephemeral app CLI, capacity profiles |
| Data | MySQL, PostgreSQL, SQLite, Litestream, Redis, credentials, grants |
| Work | Schedules, s6 workers, signed deploy queue and operator hook |
| Recovery | Logical backup/restore, scheduled rclone upload, stack transfer |
| Operations | Status, doctor, support bundle, permissions, maintenance, reports |
| Customization | Nginx drop-ins, complete app templates, Compose overlays |

## 3. Control plane

### 3.1 Entrypoint and command adapters

`src/main.ts` is the source and compiled entrypoint. `src/commands/router.ts` owns global yargs parsing, help, error mapping, and command registration. `src/commands/subcommands/` groups scriptable command handlers; `src/commands/wizard/` provides interactive convenience flows; `src/ui/` handles redacted presentation.

Command adapters SHOULD parse/present and coordinate use cases. They SHOULD NOT own domain invariants or direct filesystem/process details.

### 3.2 Domain and schemas

`src/domain/` defines branded identifiers, desired-state types, errors, and reload plans. Key discriminated unions include database engine, TLS mode, domain owner, database service, and reload target.

`src/schemas/` treats JSON and CLI-derived values as untrusted. Zod schemas reject unknown fields, malformed identities, unsupported state versions, broken references, duplicate bindings, and invalid domain ownership. Persisted state is schema version 4.

Branded TypeScript values reduce accidental mixing after runtime validation; they are not a substitute for boundary checks.

### 3.3 Services

`src/services/` owns use cases and state transitions:

- app/proxy/PHP/database lifecycle;
- generation, staged render/apply, and asset materialization;
- database grants, backups, restore, SQLite/Litestream, Redis ACLs;
- schedules, workers, deploy queue, permissions, logs, TLS;
- stack environment, safe Compose assembly, transfer, diagnostics, and maintenance.

Services receive a `Platform` and SHOULD remain independent from terminal formatting.

### 3.4 Platform adapters

`src/platform/` isolates Deno and host effects behind narrow interfaces:

- `FileSystem` with atomic writes and non-following `lstat`;
- exclusive/shared `FileLock`;
- argv-based `ProcessRunner`;
- injected `Clock` and `Random` for deterministic tests;
- source/compiled `AssetResolver`;
- stack-root `PathPolicy` with app-home containment.

Domain code MUST NOT import `Deno.*` directly. Test adapters provide fixed clocks, seeded randomness, in-memory locks, and recorded subprocesses.

### 3.5 Immutable assets

`templates/` contains base Compose, Dockerfiles, Nginx/PHP templates, and in-container helpers. Source mode reads these assets from the checkout. Compiled mode embeds them and publishes them through a digest-addressed `.asset-cache/<sha256>/` under the selected stack root before exposing stable `docker/` and `helpers/` paths.

Mutable stack data MUST never be inferred from or stored beside the executable.

## 4. Data plane and cardinality

| Component | Cardinality | Lifetime | Boundary/responsibility |
| --- | ---: | --- | --- |
| Bento CLI | Per invocation | Ephemeral | Validates intent; renders and operates stack |
| Nginx | One per stack | Persistent | Only public base service; TLS, app/proxy routing |
| PHP FPM | One per managed PHP version | Persistent | One pool/socket per enabled assigned app |
| PHP runner | One per managed PHP version | Persistent singleton | s6-supervised app schedulers/workers/deploy drains |
| PHP CLI | Per command | Ephemeral profile | App UID/GID, home, selected toolchain |
| MySQL | One per managed version | Persistent | Private relational service + named volume |
| PostgreSQL | One per managed major | Persistent | Private relational service + named volume |
| Redis | One per stack | Persistent | Shared/ACL cache + named volume |
| Litestream | Zero or one per stack | Persistent when enabled | Watches explicit replicated SQLite files |
| rclone | Per invocation/artifact upload | Ephemeral profile | Backup-only egress; config + read-only backups |

Apps are not Compose services. Apps assigned to a PHP version share that version's FPM and runner containers.

## 5. Technology stack

### 5.1 Host control plane

| Concern | Technology/decision |
| --- | --- |
| Runtime/language | Deno 2.9.3, strict TypeScript |
| CLI | yargs 18 |
| Layout/colors | cliui 9, picocolors 1 |
| Runtime validation | zod 3 |
| Templates | mustache 4 |
| Cron parsing | cron-parser 5 |
| Version ordering | semver 7 |
| Standard helpers | official `@std/*` packages |
| Dependency resolution | centralized `deno.json`, committed `deno.lock` |
| Distribution | `deno compile`, embedded `templates`, Linux amd64/arm64 |
| Host orchestration | Docker Engine + Docker Compose v2 |

The docs site is a separate developer toolchain: Astro 7/Starlight on Node.js 22+. Node is not a control-plane runtime requirement.

### 5.2 Container/runtime stack

| Concern | Technology/decision |
| --- | --- |
| Ingress | Nginx stable on Debian Trixie; native ACME module; optional HTTP/3 |
| PHP image | Debian Bookworm runtime built from official versioned PHP FPM |
| PHP extensions | PDO MySQL/PostgreSQL/SQLite, mysqli/pgsql, Redis, OPcache, and common extensions |
| App tools | Composer 2, Node 24/npm, Git, OpenSSH client, SQLite, gzip/zstd |
| Supervision | s6-overlay 3.2.3.2 |
| Scheduling | Supercronic 0.2.33 |
| Relational data | official MySQL/PostgreSQL images per managed version |
| Cache | Redis 7 Alpine |
| SQLite replication | Litestream, S3-compatible object storage |
| Backup egress | rclone 1.68.2 isolated Compose profile |
| Logs | Docker `local`, logrotate, optional GoAccess reports |

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

### 6.3 Data boundary

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

### 7.3 Reverse proxies

A proxy site uses the same domain/TLS ownership model as an app and forwards to URLs interpreted inside Nginx's current network namespace. The operator MUST choose upstream addresses valid from that namespace.

## 8. Desired state and ownership layers

### 8.1 Conceptual state model

```text
DesiredState schema v4
  defaults
    phpVersion, database service, FPM profile, Redis mode
  phpVersions[]
  databaseServices[]              MySQL | PostgreSQL
  sqliteBackup?                   stack-wide Litestream policy
  apps{slug -> AppState}
    identity/runtime/TLS/log/template/deploy/Redis
    databases[]                   MySQL | PostgreSQL | SQLite | Litestream
  proxies{name -> ProxySite}
  domains{domain -> app|proxy}    authoritative ownership
  cronJobs[]
  workers[]
  timestamps
```

Derived in-memory `database`, `mainDomain`, and `aliases` fields are compatibility views and are not persisted as authority. The first database binding is the primary/default view.

State parsing MUST enforce exact schema version, strict object fields, managed-service references, one primary domain per owner, valid linked app references, and uniqueness of bindings/jobs/workers.

### 8.2 Filesystem/storage classes

| Class | Paths | Treatment |
| --- | --- | --- |
| Desired/source | `.env`, `state.json` | Sensitive; atomically written; back up |
| Operator custom | `custom/`, `overlays/` | Durable/trusted input; preserve/review |
| Generated | `generated/`, `docker/`, `helpers/` | Rebuildable; never edit |
| Durable bind data | `homes/`, `sqlite/`, `litestream-meta/`, `certs/`, `backups/`, `rclone/`, `logs/` | Sensitive; protect and back up |
| Durable volumes | versioned MySQL/PostgreSQL volumes, Redis volume | Outside stack root; explicit backup/transfer |
| Ephemeral | `runtime/`, `locks/` | Recreated/recovered |
| Rebuildable cache | `.asset-cache/` | Digest-addressed immutable assets |

Generated trees may contain client credentials despite being rebuildable and MUST remain private.

## 9. Core flows

### 9.1 Initialization

1. Resolve absolute stack root and requested stable Compose project name.
2. Refuse to initialize any stack that already has desired state.
3. Create private state/environment and directory structure.
4. Generate administrator secrets once.
5. Persist empty schema-v4 state with default PHP/MySQL services.
6. Initialize private rclone config placeholder.
7. Render/materialize when requested by the command flow.

### 9.2 App provisioning

1. Parse and normalize identity/domain/runtime/data input.
2. Validate global domain ownership and managed references.
3. Preserve UID/GID, credentials, and omitted selections for updates.
4. Add/replace only the selected binding; retain other bindings.
5. For explicit relational database creation, require live service and apply grants before recording success.
6. Atomically persist desired state.
7. Materialize app home/credentials/key/permissions without replacing operator files.
8. Render/apply and execute the pool-focused reload plan unless deferred.

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

| Mutation | Typical plan |
| --- | --- |
| Domain/proxy/TLS/access log/vhost | Nginx |
| Pool/identity/PHP assignment | Selected FPM; Nginx when route/socket changes |
| Cron/deploy scheduler | Matching runner and selected app scheduler |
| Worker definition/control | Matching runner/worker service |
| Database backup/restore | No web/runtime reload |
| Full apply | Nginx + all relevant FPM/runners |

Stopped services consume generated configuration when next started. Apply does not start them.

### 9.5 Schedules and workers

The singleton PHP runner uses s6-overlay as PID 1. Generated services include:

- one Supercronic process per app that has schedules/deploy draining;
- one flat s6 service per enabled worker;
- a root maintenance scheduler for bounded app/runtime logs.

The reconcile helper adds/removes service directories in the dynamic scan tree. Crontab-only changes send USR2 to one app scheduler. Worker controls address one app/name service.

### 9.6 Webhook deploy

1. Nginx routes the internal deploy endpoint to the read-only PHP helper.
2. Helper bounds body size, verifies HMAC against the exact bytes, locks queue state, and enqueues.
3. The app scheduler invokes the drain once per minute.
4. Drain obtains app deploy lock and executes one queued operator hook under app UID/GID with timeout/environment/payload snapshot.
5. Result and log are recorded; retention bounds queue/history.
6. Drain asks the app's FPM path to reset OPcache; failure is logged but does not rewrite hook status.

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

Sensitive assets include `.env`, `state.json`, app credentials/SSH keys, deploy HMAC, database client files, TLS/CA keys, rclone config, dumps, logs, exports, and ACME state.

Secrets SHOULD flow through mode-restricted files or subprocess stdin, not host argv. Routine output and support bundles MUST redact known secret values. Archive sharing always requires operator review.

### 10.3 Destructive safety

The architecture separates desired-state removal from durable-data deletion. It refuses destructive Compose volume flags and relational service removal. Restore replacement, app/proxy removal, and prune use specific confirmation contracts. Recursive permissions must use `lstat` and never follow symlink targets.

## 11. Failure and recovery semantics

| Failure | Required result |
| --- | --- |
| Invalid state/schema | Refuse load; preserve bytes; direct operator to restore/fix |
| Candidate generation failure | Existing generated tree unchanged |
| Interrupted promotion | Journal retained; next render/apply recovers |
| Service validator failure | Previous files/modes restored; no reload |
| Reload signal failure | Validated new generation retained; retry signal/apply |
| Explicit DB create while service down | Fail without claiming database creation |
| Backup process failure/empty output | No final artifact for that target; partial removed |
| Later batch target failure | Earlier final artifacts may remain; retention deferred |
| rclone upload failure | Local artifacts remain; scheduled run records failure |
| Restore failure | Destination may be partial; application must not use it |
| Export archive failure | Partial/final transfer artifacts removed; prior running data services restarted when possible |
| Import failure | Only newly created destination volumes eligible for cleanup |

No behavior may imply distributed atomicity or zero downtime where the architecture cannot provide it.

## 12. Architecture invariants

1. One explicit stack root and one stable Compose project identity address a stack.
2. One Nginx is the only public base service.
3. One FPM and one singleton runner exist per managed PHP version; CLI is ephemeral.
4. Apps share version containers but retain stable UID/GID, pool, socket, home, grants, and job identity.
5. Domains are globally unique authoritative links with one primary per app/proxy.
6. Every app has at least one database binding; adding another does not migrate/remove old data.
7. Database/cache ports are not published by the base model.
8. `state.json` and `.env` are sensitive source-of-truth; generated output is disposable.
9. External values are runtime-validated before becoming branded domain values.
10. State writes and generated promotion use atomic same-filesystem replacement and locks.
11. Validation precedes reload; validation failure restores prior generated files.
12. Destructive volume/service removal is not automated.
13. Runner replicas remain one.
14. Source and compiled distributions use one entrypoint and equivalent embedded assets/behavior.

See [the technical decisions and acceptance contract](03-reimplementation-contract.md) for rationale and verification.
