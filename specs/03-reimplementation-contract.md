# Bento technical decisions and reimplementation contract

Status: normative maintenance/reimplementation contract  
Baseline: Bento `0.1.0`, schema `4`, Deno `2.9.3`

## 1. Purpose

This document records why Bento is shaped as it is and defines the observable contract that a refactor, port, or replacement MUST preserve unless the product specification changes explicitly.

Implementation details MAY change. Product safety, ownership boundaries, topology cardinality, state validity, failure semantics, and source/binary behavior MUST NOT change accidentally.

## 2. Decision criteria

Technical choices are evaluated in this order:

1. protection of operator-owned state and durable data;
2. clear single-host operating and recovery behavior;
3. least public/secret exposure;
4. deterministic output and testability;
5. small operational burden and standalone distribution;
6. narrow disruption during routine changes;
7. extensibility that does not require editing generated/core output;
8. performance and convenience after the above constraints.

## 3. Technical decisions

### D-01 — Single Linux host and Docker Compose

**Context:** The target user needs a small reproducible platform, not cluster operations.  
**Decision:** Use Docker Engine and Compose v2 on one operator-owned Linux host.  
**Benefits:** Familiar packaging, named-volume durability, low control-plane overhead, inspectable topology.  
**Trade-offs:** Host failure is stack failure; no HA/autoscaling; Docker access is privileged.  
**Rejected:** Kubernetes, a remote scheduler, and hidden managed services.

### D-02 — On-demand local CLI, not a daemon

**Context:** Continuous reconciliation adds a resident privileged service and another failure mode.  
**Decision:** Run the control plane once per CLI invocation; persist intent locally; reconcile explicitly.  
**Benefits:** No management port/daemon, transparent target root, simple upgrades.  
**Trade-offs:** External drift is not continuously healed and operators must invoke apply.  
**Invariant:** Every operation resolves its stack from `BENTO_STACK_ROOT`, the `./bento` default, or a one-command `--stack` override; no process remembers a global current stack.

### D-03 — Deno, strict TypeScript, and one entrypoint

**Context:** The code handles hostile JSON/env/process boundaries but releases should not require a runtime install.  
**Decision:** Deno 2.9.3, strict TypeScript, runtime validation, `src/main.ts` for source and compiled modes.  
**Benefits:** One toolchain, typed domain, testable adapters, standalone binaries.  
**Trade-offs:** Runtime and dependencies are pinned; compiled asset resolution requires care.  
**Rejected:** Python compatibility layer, unrestricted `-A` as the documented default, separate source/binary implementations.

### D-04 — Versioned strict desired state

**Context:** Silent compatibility and hand-edited fragments cause ambiguous behavior.  
**Decision:** Keep one strict schema-v4 JSON document and reject unsupported versions/unknown fields.  
**Benefits:** Explicit model and deterministic generation.  
**Trade-offs:** No implicit migration; incompatible state requires a matching binary or deliberate conversion.  
**Invariant:** Invalid state is never overwritten during routine load.

### D-05 — Desired state is separate from generated and durable data

**Context:** Operators need to know what can be regenerated and what requires backup.  
**Decision:** Separate source (`state.json`, `.env`), custom input, generated output, durable data, and ephemeral coordination.  
**Benefits:** Safe regeneration, reviewable backup boundary, fewer upgrade forks.  
**Trade-offs:** More explicit paths and operator responsibility.  
**Invariant:** Direct generated-file edits are unsupported and replaceable.

### D-06 — Stable app identity across all execution modes

**Context:** A site identity that applies only to web traffic does not isolate jobs, CLI, deploy, or files.  
**Decision:** Bind one slug/UID/GID/home to FPM, CLI, cron, workers, deploys, domains, and data metadata.  
**Benefits:** Predictable ownership and operation.  
**Trade-offs:** Rename becomes a coordinated migration and is not supported.  
**Invariant:** App update preserves UID/GID and generated-once credentials.

### D-07 — Shared PHP containers by version

**Context:** One container/image per app duplicates toolchains and service overhead.  
**Decision:** One FPM and singleton runner per PHP version, app-specific pools/services within them, ephemeral CLI per invocation.  
**Benefits:** Multiple runtimes with efficient sharing and consistent tools.  
**Trade-offs:** Shared namespace/network/capacity; no hostile isolation or hard app quotas.  
**Rejected:** One complete container stack per app.

### D-08 — Nginx-only ingress and per-app Unix sockets

**Context:** Public surface should be narrow and app routing should align with identity.  
**Decision:** One Nginx; FPM reached through app-specific Unix sockets; databases/cache remain private.  
**Benefits:** No FPM/database public ports, straightforward app socket ownership, direct host HTTP/3.  
**Trade-offs:** Host/bridge namespace differences and typically one host-mode stack.  
**Boundary:** Non-PHP apps are reverse-proxy upstreams, not managed runtimes.

### D-09 — Host mode by default, bridge mode explicitly

**Context:** Direct 80/443/UDP favors host networking, while multi-stack operation needs isolation and selectable ports.  
**Decision:** Default Nginx to host mode; allow bridge mode with optional publications and host-gateway mapping.  
**Benefits:** Primary-stack simplicity and explicit multi-stack behavior.  
**Trade-offs:** Address meaning changes by namespace; operator must select valid upstreams.  
**Invariant:** Bridge HTTP/3 publishes both TCP and UDP on the HTTPS port.

### D-10 — Add-only heterogeneous database bindings

**Context:** Engine moves, password rotation, and volume deletion are high-risk data migrations.  
**Decision:** Persist `databases[]`; add independent MySQL/PostgreSQL/SQLite/Litestream bindings; preserve old bindings; block managed relational removal/rotation.  
**Benefits:** Mixed data use without pretending to migrate data; explicit grants and ownership.  
**Trade-offs:** The first binding has compatibility/default semantics; operators coordinate migration/cleanup.  
**Rejected:** Automatic cross-engine conversion and automatic destructive version removal.

### D-11 — Distinct plain SQLite and Litestream products

**Context:** Local maintenance/logical dumps and continuous remote replication have different guarantees and privileges.  
**Decision:** Model `sqlite` and `litestream` separately. Plain SQLite uses `.db`, online `.backup`, and randomized weekly `VACUUM`; Litestream uses watched `.sqlite` files and stack-wide S3 policy.  
**Benefits:** Backup behavior is explicit and plain files stay outside the watcher glob.  
**Trade-offs:** The watcher has stack-wide read/write authority over SQLite mounts and requires rootful Docker constraints.  
**Invariant:** Verify/export restores to separate files; no public in-place production restore.

### D-12 — Staged, journaled, scoped apply

**Context:** Partial generation and broad restarts can create outages.  
**Decision:** Lock, recover, stage all files, atomically promote, validate, reload selected roles, finalize.  
**Benefits:** Deterministic recovery and small blast radius.  
**Trade-offs:** More transaction code; service validation requires temporary promotion; not a distributed transaction.  
**Invariant:** Validation failure restores prior bytes/modes; reload failure retains validated new files.

### D-13 — s6 singleton runner with per-app schedulers

**Context:** Jobs need app identity and live reconciliation without container recycling.  
**Decision:** s6-overlay PID 1, one Supercronic process per scheduled app, flat worker services, one runner per PHP version.  
**Benefits:** Scoped controls and no sibling restarts.  
**Trade-offs:** Dynamic service-tree reconciliation and strict singleton assumption.  
**Invariant:** Scaling runners above one is unsupported because it duplicates work.

### D-14 — Signed queue orchestration, not a deployment strategy

**Context:** Webhook requests should be authenticated and quick, while deployment logic varies by app.  
**Decision:** Bound/authenticate/enqueue requests; execute an operator-owned hook asynchronously under app identity.  
**Benefits:** Safe generic orchestration, retries/history/logs, no framework coupling.  
**Trade-offs:** Operator owns Git/release/migration/rollback behavior.  
**Invariant:** Default hook skips; HMAC secret is not stored in app-writable hook config.

### D-15 — Logical backups first; raw transfer explicit

**Context:** Portable recovery and whole-stack cloning have different consistency/compatibility properties.  
**Decision:** Use engine-matching logical dumps for routine recovery and guarded per-volume tar archives for complete stack transfer.  
**Benefits:** Logical major-upgrade path plus deliberate full transfer.  
**Trade-offs:** Restore is non-atomic; raw transfer requires compatible images/architecture; live SQLite bytes in stack tar are not recovery proof.  
**Invariant:** Failed/empty dumps never become final artifacts.

### D-16 — Isolated rclone sidecar for optional backup egress

**Context:** Scheduled artifacts need an optional off-host path without installing rclone or exposing all stack mounts.  
**Decision:** Run profile-only rclone with private config, read-only `/backups`, separate egress network, read-only root, dropped capabilities.  
**Benefits:** Narrow mount/privilege boundary and provider flexibility.  
**Trade-offs:** Upload is per artifact, remote durability/retention/monitoring remains operator-owned.  
**Invariant:** Upload failure leaves local artifacts and marks scheduled execution failed.

### D-17 — Guard destructive operations in the product layer

**Context:** Compose and database tools expose shortcuts that can destroy durable data.  
**Decision:** Block volume-destructive Compose options and managed service removal; separate state removal from prune; require exact confirmations.  
**Benefits:** Accidental commands fail closed.  
**Trade-offs:** Expert cleanup is manual and intentionally inconvenient.  
**Invariant:** No ordinary app removal deletes home/database contents.

### D-18 — Operator-owned, validated escape hatches

**Context:** Real deployments need directives and services outside the core model.  
**Decision:** Preserve Nginx drop-ins, complete app templates, and ordered Compose overlays as trusted input.  
**Benefits:** Customization survives generation/upgrades without a core fork.  
**Trade-offs:** Operator input can break or weaken topology/security and needs review.  
**Invariant:** Custom sources are never silently replaced by generated output.

### D-19 — Dependency injection at host boundaries

**Context:** Filesystem, process, time, randomness, locking, and compiled assets are difficult to verify when called globally.  
**Decision:** Inject narrow platform interfaces and deterministic test adapters.  
**Benefits:** Fast unit tests, controlled failure simulation, fewer Deno leaks into domain logic.  
**Trade-offs:** Adapter ceremony and explicit plumbing.  
**Invariant:** Domain code does not directly depend on `Deno.*`.

### D-20 — Source/compiled parity is product behavior

**Context:** A standalone binary can diverge in assets, working-directory assumptions, diagnostics, or exits.  
**Decision:** Compile the same entrypoint with embedded templates and compare source/binary outputs and transitions.  
**Benefits:** Distribution confidence and no target runtime/package install.  
**Trade-offs:** Compile/parity tests are slower and exclude intentionally nondeterministic certificates/metadata from byte comparison.  
**Invariant:** Compiled mode works from an arbitrary current directory with an external stack root.

## 4. Reimplementation boundaries

A conforming reimplementation MAY change libraries, file internals, or container build mechanics only if it preserves:

- CLI intent and documented safety behavior;
- strict schema-v4 parsing/serialization, or provides an explicit product-approved migration;
- stack/project identity and durable resource naming;
- domain uniqueness and app identity allocation/preservation;
- component cardinality and private/public topology;
- staged apply, rollback, journal recovery, and reload scope;
- mode/ownership requirements for sensitive files;
- protected argv/output behavior for secrets;
- backup/restore/transfer failure semantics;
- source/standalone distribution parity.

The following require an explicit product/architecture revision rather than an implementation refactor:

- introducing a daemon/API/browser control plane;
- changing to multi-host/Kubernetes orchestration;
- one container per app or hostile tenancy claims;
- publishing backend service ports by default;
- destructive automatic database/version removal;
- implicit state migration or unknown-field acceptance;
- replacing the add-only binding model with destructive rebinding;
- scaling the runner beyond one replica.

## 5. Acceptance contract

### A-01 — Toolchain and static quality

A conforming source tree MUST pass:

```sh
deno task fmt:check
deno task lint
deno task check
deno install --frozen=true
```

Strict compiler options MUST remain enabled, including no implicit `any`, unchecked-index awareness, exhaustive returns/switches, and `unknown` catches. Imports MUST remain centralized and lockfile-resolved.

### A-02 — Unit and contract behavior

`deno task test` MUST pass. Tests MUST cover at minimum:

- validators, strict state loading, relationships, and unsupported schema refusal;
- app identity/domain/runtime/data binding transitions;
- MySQL/PostgreSQL/Redis and SQLite/Litestream policy behavior;
- render staging, rollback, interruption recovery, and reload plans;
- deploy HMAC/queue/retention/locking behavior;
- worker/cron controls and command parsing;
- backup scheduling, rclone target/upload plans, and redacted status;
- stack transfer path/archive/volume guards;
- app/proxy removal, prune, and destructive Compose refusals;
- multi-stack ingress and source CLI smoke behavior.

Safety refusals MUST return the stable safety error category/exit behavior expected by contract tests rather than generic success or silent no-op.

### A-03 — Integration behavior

`deno task test:integration` MUST run. Docker-dependent cases MAY soft-skip only when the daemon/environment is unavailable; release evidence MUST distinguish skip from proof.

A live supported host SHOULD prove:

- stack bootstrap/build/start and health;
- multi-app routing and filesystem/process identity;
- MySQL and PostgreSQL connectivity/isolation/backup/restore;
- plain SQLite and Litestream watcher behavior;
- Redis behavior;
- schedules, worker controls, deploy enqueue/drain;
- TLS modes except production ACME where test DNS is unavailable;
- validation rollback and service reload.

`deno task test:stack` provides the operator-visible real Docker harness and MUST use a disposable named stack.

### A-04 — Render/apply transaction

Tests MUST demonstrate:

1. two concurrent mutations cannot promote overlapping generations;
2. incomplete staging never changes live output;
3. stale managed files are removed only as part of a complete generation;
4. validation failure restores bytes and modes and sends no reload;
5. abandoned journals recover deterministically;
6. reload failure does not roll back a validated generation;
7. command-specific plans do not reload unrelated roles;
8. `render` does not signal services and `apply` does not start stopped services.

### A-05 — State and domain model

Round-trip tests MUST prove that:

- persisted state is strict schema v4;
- derived `database`, `mainDomain`, and `aliases` views are omitted from JSON;
- app and proxy map keys match their identities;
- domain records point to existing owners and exactly one primary exists per owner;
- linked jobs/workers point to existing apps and are unique by app/name;
- every app has one or more unique binding identities;
- relational bindings reference managed services of the same engine;
- adding a binding preserves all existing bindings and credentials;
- unsupported old/new versions fail without state modification.

### A-06 — Identity and secret safety

Tests/review MUST verify:

- stable UID/GID across update and lifecycle operations;
- app execution role/workdir selection cannot escape the app home;
- Nginx app-home mounts are read-only and socket group alignment is correct;
- recursive permission walks use `lstat` and do not follow symlinks;
- state/env/credentials/rclone/backup-result files use private modes;
- database/admin/rclone/deploy/TLS secrets are absent from routine status, support bundles, and host argv;
- generated diagnostics are redacted and bounded before persistence/sharing.

### A-07 — Data safety

Tests MUST prove:

- relational app names/grants remain within app namespace;
- PostgreSQL roles are unprivileged and public access is revoked;
- database password reconciliation preserves existing values;
- managed MySQL/PostgreSQL removal is refused;
- plain SQLite backup uses a consistent SQLite `.backup`, not a direct live copy;
- Litestream verify/export does not replace production;
- only one logical batch runs at once;
- failed/empty dump output is never finalized;
- retention waits for complete batch success;
- restore replacement requires exact target confirmation and documents partial-failure risk.

### A-08 — Backup schedule and rclone

Tests MUST prove:

- cron blocks are qualified by absolute stack root and preserve unrelated bytes;
- duplicate/malformed/reversed markers fail closed;
- executable/root paths reject control characters and cron `%`;
- cross-stack crontab writes use a host-user-scoped lock;
- last-run JSON is strict, private, atomic, bounded, and redacted;
- rclone remote/prefix input cannot create ambiguous/traversing destinations;
- the sidecar mounts only private config and read-only backups in the base topology;
- uploads preserve artifact paths below `backups/` and include only the current successful batch;
- upload failure produces failed schedule status without deleting local artifacts.

### A-09 — Transfer safety

Tests MUST prove:

- export/import directories are separate from stack roots and initially empty;
- archive names and Docker volume names are unique and deterministic;
- export checks expected volumes and restarts exactly previously running data services;
- partial transfer output is cleaned on failure;
- tar entries with absolute or `..` paths are rejected;
- missing/corrupt/unexpected archives and existing destination volumes are refused;
- failed import only removes volumes that invocation created;
- project/ingress override validation happens before startup;
- imported state is validated and regenerated with the importing Bento version.

### A-10 — Distribution parity

`deno task test:parity` MUST:

- compile a native binary;
- run version/init/render/status without a Deno/Node/Python runtime on `PATH`;
- verify embedded asset digest and digest-addressed materialization;
- compare normalized source/compiled state transitions;
- compare managed generated files and published immutable assets;
- preserve exits and safety refusals for supported command flows.

Release compilation MUST produce Linux `x86_64-unknown-linux-gnu` and `aarch64-unknown-linux-gnu` artifacts.

### A-11 — Documentation consistency

A product behavior change MUST update:

1. this specification set;
2. operator-facing `README.md` and relevant `docs/` pages;
3. CLI help when the command surface changes;
4. unit/contract/integration tests that prove the behavior;
5. state schema/version when persisted compatibility changes.

Documentation MUST NOT claim a quality gate is enforced by CI unless the checked-in workflow runs it.

## 6. Required release gate

Before a release is considered conforming, run:

```sh
deno task fmt:check
deno task lint
deno task check
deno install --frozen=true
deno task test
deno task test:integration
deno task test:parity
deno task compile:amd64
deno task compile:arm64
```

A release record SHOULD state Docker integration skips, target architectures actually executed, known recovery limitations, and any state/asset version change.

The compiled smoke MUST use a disposable external root and MUST NOT target the checked-in `bento/` stack.

## 7. Current conformance notes

These notes describe repository debt observed at the reviewed snapshot; they are not alternative product behavior.

### 7.1 Documentation drift

Several older pages still describe a single database binding, state that off-host upload is entirely manual, or say stack export excludes SQLite. Current code establishes the contract used here: `databases[]`, optional scheduled rclone upload, and a root tar that mechanically includes `sqlite/` without guaranteeing live-copy consistency.

Those pages SHOULD be reconciled with these specs.

### 7.2 Workflow enforcement gap

`deno.json` exposes the required quality, test, compile, smoke, and parity tasks. The checked-in `.github/workflows/ci.yml` currently triggers for tags/releases and compiles/uploads amd64 and arm64 binaries, but it does not execute the complete release gate in section 6.

Until the workflow is expanded, the repository cannot infer full gate success from the release workflow alone; maintainers MUST run and record the missing gates elsewhere.

### 7.3 Historical test wording

Some phase-oriented test comments still call off-host backup replication a non-goal. The tested assertion is narrower (database adapter code does not itself embed S3/rclone behavior), while the current architecture adds rclone through a separate service. Future edits SHOULD update stale wording without weakening the separation-of-concerns test.

## 8. Definition of done for changes

A change is complete only when:

- domain/state implications and ownership layer are explicit;
- every external input and subprocess output has a validation/redaction plan;
- failure and rollback semantics are documented;
- reload scope and durable-data impact are known;
- source and compiled asset behavior are considered;
- tests prove happy path, malformed boundaries, concurrency where relevant, and destructive refusal;
- product specs, CLI help, and operator docs agree;
- all applicable release gates pass.
