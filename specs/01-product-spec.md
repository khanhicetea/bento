# Bento product specification

Status: current-product baseline  
Audience: product owner, operator, maintainer, systems engineer

## 1. Product definition

Bento is a self-hosted operations layer for running multiple isolated PHP applications and reverse-proxied HTTP services on one operator-owned Linux server. A local CLI converts validated desired state into Docker Compose, Nginx, PHP, database, job, and support configuration. Bento has no resident control-plane daemon.

Bento is intentionally smaller than a cloud platform and safer than an unstructured collection of Compose files. It covers the repeated host-level work around ingress, TLS, PHP versions, app identities, data services, schedules, workers, deploy webhooks, backups, diagnostics, permissions, and controlled configuration activation.

The operator owns the host, stack state, application files, certificates, database contents, and recovery process.

## 2. Problem and value proposition

Operating several PHP applications on one server requires coordinated changes across routing, certificates, process identities, filesystem permissions, runtime versions, database grants, Redis access, background work, deploy hooks, and backups. Hand-edited files drift; credentials leak into commands; broad restarts increase outages; and destructive cleanup is easy to invoke accidentally.

Bento turns those coupled concerns into one inspectable, desired-state workflow with:

- a stable application identity spanning web, CLI, jobs, deploys, and data;
- private-by-default services and explicit filesystem ownership;
- complete staged generation, validation, rollback, and scoped reload;
- durable state separated from disposable generated output;
- guarded destructive operations and operator-owned customization points;
- source and standalone-binary operation from the same TypeScript entrypoint.

## 3. Target users and fit

### Primary user

A technically capable developer or small team that:

- administers one Linux VPS or dedicated host;
- runs multiple Laravel, Symfony, WordPress, or other PHP applications;
- prefers CLI and local files over a browser control panel;
- needs reproducible operations without Kubernetes or a managed cloud platform;
- trusts the hosted applications enough to share versioned runtime containers.

### Secondary use

Bento can terminate HTTP/TLS and reverse proxy to non-PHP services reachable from Nginx. It does not provision or supervise those application runtimes.

### Poor fit

Bento is not appropriate when the workload requires multi-host availability, horizontal autoscaling, hard hostile-tenant isolation, per-app container quotas, a public management API, or a managed non-PHP runtime platform.

## 4. Core values

1. **Operator ownership.** State, data, keys, files, and operational decisions remain on infrastructure the operator controls.
2. **Comprehensibility over generality.** Optimize for one host and a finite topology instead of becoming a generic orchestrator.
3. **Safety before convenience.** Reject ambiguous or destructive shortcuts; require exact confirmation and preserve durable data by default.
4. **Explicit ownership classes.** Desired state, generated output, custom input, durable data, and ephemeral coordination MUST remain distinguishable.
5. **One app identity everywhere.** The same slug and UID/GID MUST scope requests, commands, schedules, workers, deploys, credentials, and data access.
6. **Private by default.** Nginx is the only public base service; FPM, databases, Redis, runners, and management remain private.
7. **Deterministic, validated change.** Build complete candidates, validate boundaries, and preserve the last valid generation on validation failure.
8. **Narrow blast radius.** Reload only roles affected by a change; do not restart unrelated applications or workers.
9. **Honest boundaries.** Shared containers reduce cost but are not hostile-tenant sandboxes; local backups are not disaster recovery until copied and tested.
10. **Escape hatches without forks.** Preserve supported drop-ins, templates, and overlays as operator-owned input rather than edits to generated files.

## 5. Product model

### 5.1 Stack

A stack is one independent Bento installation. It has:

- an explicit filesystem root selected by `--stack` or `BENTO_STACK_ROOT`;
- a stable Compose project name stored in `.env`;
- one desired-state document, generated configuration, custom input, homes, certificates, backups, logs, and runtime coordination;
- private network and named-volume identities derived from the Compose project name.

The stack path and stack name are independent. Production commands SHOULD name the stack path explicitly. Separate stacks on one Docker host MUST use distinct project names and non-conflicting ingress.

### 5.2 Application

An application is a stable logical identity, not merely a virtual host. Its slug binds:

- stable private UID/GID and `/home/<slug>`;
- one selected PHP version, one FPM profile, pool, and Unix socket;
- one primary domain and zero or more aliases;
- one or more database bindings;
- Redis namespace/identity;
- schedules, workers, and optional deploy queue;
- credentials, SSH key, code, logs, and app runtime state.

Slugs are effectively permanent. Changing a domain MUST preserve app identity. Bento does not provide app rename.

### 5.3 Database binding

Every app has at least one binding in `databases[]`. A binding is one of:

- MySQL on one managed versioned service;
- PostgreSQL on one managed major-version service;
- plain SQLite with local logical backup and weekly randomized maintenance;
- SQLite continuously replicated by the stack Litestream watcher.

Adding a binding MUST preserve existing bindings and data. Bento does not convert or move data between engines/services. The first binding remains the default compatibility connection written to the conventional `DB_*` fields; all bindings are also represented in indexed `BENTO_DB_*` credential entries.

### 5.4 Domain owner

Domains are stack-wide authoritative link records. Every domain MUST be normalized and uniquely owned by exactly one app or reverse proxy. Every app and proxy MUST have exactly one primary domain link.

## 6. Product goals

Bento MUST:

1. provision and operate several PHP apps on one Linux host;
2. reconstruct generated service configuration from local validated intent;
3. keep the public surface narrow and credentials out of routine output/argv;
4. support concurrent PHP and relational database versions without duplicating a whole stack per app;
5. provide app-scoped web, CLI, schedule, worker, and deploy behavior;
6. make invalid configuration recoverable and limit unrelated reloads;
7. expose guarded backup, restore, transfer, deletion, and repair workflows;
8. work from source and as a standalone Linux executable with equivalent behavior.

## 7. Functional requirements

### 7.1 Bootstrap and operator interface

Bento MUST provide scriptable CLI commands and MAY provide guided TUI flows for common operations. It MUST support initialization, render, apply, status, diagnostics, and a redacted support bundle.

Fresh initialization MUST create:

- schema-v4 desired state;
- default PHP `8.5` and MySQL `8.4` managed services;
- Redis and one Nginx ingress topology;
- stable stack identity and generated-once administrator secrets;
- private rclone configuration placeholder;
- required durable/custom/generated directory structure.

The CLI MUST support human-readable output and machine-readable JSON where advertised. Ordinary output MUST redact database, Redis, deploy, TLS, and transfer secrets.

### 7.2 Application lifecycle

The operator MUST be able to create/update, list/show, enable/disable, remove, and prune apps.

Provisioning MUST:

- validate slug, domain uniqueness, safe document root, runtime, profile, and database selection;
- allocate a stable UID/GID beginning at the product allocation range;
- create the app home, app credentials, Ed25519 deploy key, default skipped deploy hook, and placeholder entrypoint without replacing existing operator content;
- render an app-specific FPM pool/socket and Nginx vhost;
- preserve omitted runtime/database selections during updates;
- apply requested relational grants or fail without recording an explicitly requested database when its service is unavailable.

Disabling MUST remove active vhost, pool, schedules, and workers after apply while retaining state, domain claims, home, credentials, and databases. Removing desired state MUST require `delete <slug>` and retain durable data. Permanent prune MUST be separate, interactive, enumerate known retained parts, and require the literal `delete` with no bypass.

Front-controller routing MUST restrict dynamic execution to the entrypoint. Legacy routing MAY execute PHP scripts beneath the document root.

### 7.3 Ingress, domains, proxies, and TLS

One Nginx service per stack MUST be the only public service in the base topology.

Host networking is the default for direct 80/443 and HTTP/3 behavior. Additional stacks MAY use bridge mode with distinct HTTP/HTTPS publications or no host publication. When HTTP/3 is enabled, HTTPS publication MUST include matching TCP and UDP.

Nginx MUST:

- serve app domains through per-app FPM Unix sockets;
- serve reverse-proxy domains through one or more validated upstream URLs;
- read app homes without general write access;
- support shared boot TLS, stack-private CA certificates, ACME, and external certificates;
- validate candidate configuration before a running Nginx reload.

TLS changes SHOULD reload only Nginx. ACME requires operator-controlled public DNS and reachable challenge traffic. External certificate renewal remains operator-owned.

### 7.4 PHP runtime and capacity

Each managed PHP version MUST produce:

1. one persistent FPM service;
2. one persistent singleton runner;
3. one ephemeral CLI role.

The three roles MUST share the version image, extensions, app identities, and mounts. Apps on the same version share these containers while retaining separate pools, sockets, users, and homes.

Supported named FPM profiles are `tiny`, `small`, `medium`, `large`, `xlarge`, and `ondemand`. Bento SHOULD warn when enabled pool maxima exceed a version's global process cap. The runner MUST remain at one replica to avoid duplicate jobs.

### 7.5 Data services

Bento MUST support add-only managed MySQL and PostgreSQL services, one durable named volume per managed version, and one durable Redis service per stack. Managed relational service removal and automatic volume deletion MUST be blocked.

For relational bindings, Bento MUST:

- generate one persistent app user/role password and app-namespaced database records;
- keep administrator and app passwords off host process argv;
- provide add/list, database creation, shell, size, process inspection, backup, and restore operations;
- prevent automatic password rotation and automatic cross-engine/service migration;
- publish only successful non-empty dump artifacts atomically.

PostgreSQL app roles MUST remain unprivileged; app databases MUST be owned by their app role with default public database/schema access revoked.

Redis shared mode requires an app key prefix. ACL mode MUST create an app-specific user restricted to its namespace. Redis MUST not publish a base host port.

Plain SQLite MUST use a private app-owned file, stable randomized weekly `VACUUM` slot, and online `.backup` for logical artifacts. Litestream-backed SQLite MUST use an explicit binding type, one stack-wide watcher, S3-compatible policy, and non-destructive verify/export flows. A Litestream export MUST not replace the production database.

### 7.6 Commands, schedules, workers, and deploys

`app shell` and `exec` MUST run in an ephemeral PHP CLI container under the app UID/GID and selected runtime. Working directories MUST remain inside the app home.

Schedules MUST support cron expression, timezone, argv or explicit shell mode, workdir, output policy, timeout, lock, and enablement. Each app scheduler MUST run under that app identity.

Workers MUST be named app-scoped long-running commands supervised by s6 and support list/start/stop/restart/signal/inspect/remove. Reconciliation SHOULD affect only the selected worker or app scheduler.

Webhook deploy MUST:

- authenticate the exact bounded body with a constant-time compatible HMAC check;
- enqueue rather than execute deployment in the request;
- support `latest` and bounded `fifo` policies, one active job per app, timeout, history, and app-owned logs;
- run the operator-owned hook as app identity;
- treat the generated hook as skipped until replaced;
- attempt app-pool OPcache reset after a terminal run without changing the hook result;
- keep the HMAC secret in protected state/generated FastCGI input, not app-writable secret files.

Bento supplies orchestration, not a fixed Git checkout/release/rollback strategy.

### 7.7 Render and apply

Desired state and supported custom input are the source. Managed output under `generated/`, `docker/`, and `helpers/` is disposable and MUST NOT be a customization point.

A render/apply transaction MUST follow:

`exclusive lock -> abandoned-journal recovery -> complete staging -> manifest -> atomic promotion -> stale removal -> validation -> scoped reload -> finalize`

Required semantics:

- candidate-generation failure leaves live output unchanged;
- promotion or validator failure restores previous bytes and modes and sends no reload;
- reload-signal failure leaves validated new files promoted for retry;
- render writes files but never signals services;
- apply does not start stopped containers;
- full apply may conservatively target Nginx plus relevant FPM/runner roles;
- command-specific changes SHOULD use narrower reload plans.

State mutation, data-plane side effects, generated-file promotion, and service reload are not one distributed transaction. Failure guidance MUST preserve the requested intent or explain how to reverse it.

### 7.8 Backup, restore, and transfer

Logical backup MUST support one database, one app, or all apps across MySQL, PostgreSQL, and plain SQLite. Zstandard is default; gzip and uncompressed output are supported. Litestream bindings use their separate continuous-replication workflow rather than local logical dump artifacts.

Only one logical backup batch may run at once. Retention MUST run only after the requested batch succeeds. Earlier completed artifacts MAY remain if a later target fails.

A host-cron schedule MUST be stack-qualified, preserve unrelated crontab bytes, record bounded/redacted last-run status, and support register/status/run/unregister. A configured rclone target MUST upload only artifacts from the successful scheduled batch through an ephemeral sidecar with read-only backup access. Operators remain responsible for remote retention, monitoring, and restore testing.

Relational restore MUST enforce app namespace and exact replacement confirmation. It is not object-level atomic and MAY leave a partial destination on failure. PostgreSQL major upgrades SHOULD use logical dump/restore.

Stack export/import MUST:

- use an empty external transfer directory and empty destination root;
- include the stack-root archive and separate raw archives for each managed relational volume and Redis;
- stop/restart exactly the running volume-backed data services needed for raw copies;
- reject missing, corrupt, unsafe, unexpected, or conflicting archives/volumes;
- permit explicit stack-name and ingress overrides for clones;
- restore only newly created volumes on failure, re-render, then build/start the imported stack.

A raw stack archive is sensitive. Its included live SQLite bytes are not a guaranteed consistent SQLite backup.

### 7.9 Safety, diagnostics, and maintenance

Bento MUST block `compose down -v`, `--volumes`, and destructive image-removal forms. It MUST not automatically remove managed relational versions/volumes or rotate relational passwords.

Permission repair MUST offer check, dry-run, shallow, and explicit recursive modes. Recursive traversal MUST not follow symlink targets. Routine reconciliation SHOULD use shallow repair.

Status and doctor SHOULD cover stack identity, service/runtime health, ingress, storage, TLS, permissions, versions, domains, databases, overlays, and capacity. Support bundles MUST contain redacted diagnostics only.

Docker logs and app/worker/FPM logs MUST have bounded retention. Access logs are opt-in and MAY be rotated/reported without an Nginx reload. Host maintenance registration MUST preserve unrelated crontab entries.

### 7.10 Customization and distribution

Supported customization points are:

- stack `.env` settings;
- additive Nginx drop-ins under `custom/nginx/`;
- complete selected app vhost/pool templates with provenance and drift warnings;
- ordered Compose overlays.

Custom input is trusted and MAY violate invariants; it MUST be validated with the resulting service configuration. Returning to upstream templates MUST preserve custom source.

The control plane MUST run under pinned Deno with explicit permissions and compile to Linux amd64/arm64 binaries embedding immutable templates. A compiled release MUST not require Deno, Node.js, Python, npm install, or a repository checkout on the target. Source and compiled modes MUST preserve equivalent state transitions, generated content, diagnostics, exits, and safety behavior.

## 8. Critical journeys

### 8.1 First host

Initialize an explicit stack root/name, render, build/start Compose services, inspect status/doctor, and create the first app. The operator verifies DNS, firewall, backups, and host patching separately.

### 8.2 Launch a framework app

Create an app with `public` document root and front-controller routing, select PHP and one data binding, register its SSH public key, deploy code through app identity, configure the framework from protected credentials, create/migrate data, switch TLS mode, and verify HTTP/data access.

### 8.3 Add asynchronous work

Add one schedule and one queue worker. Both run under app identity in the selected singleton runner. Updating either does not interrupt unrelated web traffic or sibling apps.

### 8.4 Add another database kind

Add a managed PostgreSQL version, update the app with a PostgreSQL binding, and create its app-namespaced database. Existing MySQL/SQLite bindings and credentials remain recorded; data conversion is operator-managed.

### 8.5 Automate deploys

Enable webhook deploy, register the secret with the source-control provider, replace the skipped hook, submit a valid signed payload, observe immediate enqueue, drain under app identity, inspect result/log, and verify OPcache reset behavior.

### 8.6 Prove recovery

Run a complete logical batch, upload artifacts off-host, inspect schedule status, restore one relational dump to a new verification database, validate application invariants, and only then consider an exact-confirmed replacement. Verify Litestream through temporary restore/export separately.

### 8.7 Clone or recover a stack

Export to an external empty directory, protect all archives, import into an empty destination with compatible images/architecture, override project identity and ingress when cloning on one host, then verify state, routes, jobs, volumes, and applications.

## 9. Non-goals and limits

Bento intentionally does not provide:

- multi-host orchestration, HA, clustering, autoscaling, or Kubernetes;
- a browser admin UI, public API, remote control plane, or resident daemon;
- one container per app, hostile-tenant isolation, or per-app CPU/memory quotas in shared PHP roles;
- managed arbitrary-language application runtimes beyond reverse proxying;
- zero-downtime guarantees for deploy, apply, restore, export, or transfer;
- automatic app rename, database-engine migration, relational major upgrade, or password rotation;
- automatic relational service/volume deletion;
- a hard-coded source-control deployment strategy;
- a hosted monitoring/analytics service;
- automatic application configuration from `credentials/app.env`;
- guaranteed recovery merely from on-host dumps, remote upload, or raw live SQLite copies.

## 10. Success criteria

The product succeeds when an operator can use source or a compiled binary to create an explicit stack, run multiple PHP apps with stable identities and private data access, apply validated changes with scoped disruption, operate jobs/deploys, create and export recovery artifacts, diagnose failures without leaking secrets, and recover from invalid generated configuration without losing the prior valid generation.

Detailed structural and verification criteria are defined in [the system architecture](02-system-architecture.md) and [the reimplementation contract](03-reimplementation-contract.md).
