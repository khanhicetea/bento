# Proposed Bento product enhancements

Status: proposal; not part of the current product baseline  
Audience: product owner, maintainer, operator reviewers

## 1. Purpose

This document proposes product additions that follow Bento's existing values: single-host comprehensibility, operator ownership, safe change, private defaults, explicit durability, and no resident control-plane daemon.

Nothing here is implemented merely because it is specified. Each proposal requires approval, architecture review, tests, CLI/docs work, and an explicit baseline-spec update.

## 2. Prioritization

| Priority | Meaning |
| --- | --- |
| P0 | Correctness or safety gap; schedule before broad feature work |
| P1 | High operator value and strong fit with the current model |
| P2 | Valuable after P0/P1 foundations; moderate complexity |
| P3 | Optional expansion; validate demand before implementation |

| ID | Proposal | Priority | Impact | Effort | Primary value |
| --- | --- | ---: | ---: | ---: | --- |
| F-01 | Recovery readiness and backup evidence | P1 | High | Medium | Make recovery status provable |
| F-02 | Guarded SQLite production restore | P1 | High | High | Complete SQLite recovery lifecycle |
| F-03 | First-class change plans and approvals | P1 | High | Medium | Safer applies and automation |
| F-04 | Upgrade preflight and explicit state migration | P1 | High | High | Make upgrades reviewable/recoverable |
| F-05 | Database binding promotion and retirement | P2 | High | High | Finish the multi-binding lifecycle |
| F-06 | Stable automation output and event records | P2 | Medium | Medium | Reliable scripting/monitoring |
| F-07 | Capacity budgets and pressure diagnostics | P2 | Medium | Medium | Prevent shared-runtime overload |
| F-08 | Deploy queue operations and generic lifecycle hooks | P2 | Medium | Medium | Better deploy recovery without Git coupling |
| F-09 | TLS/DNS readiness and expiry checks | P2 | Medium | Low | Prevent avoidable certificate incidents |
| F-10 | Maintenance mode with scoped drain | P3 | Medium | Medium | Safer restore/migration windows |

## 3. F-01 — Recovery readiness and backup evidence

### Problem

Bento can create logical dumps, schedule them, upload artifacts with rclone, and verify Litestream restores. Operators still have to infer whether coverage is complete, remote copies exist, artifacts are intact, and restores have been tested recently.

A successful backup process is not the same as recovery readiness.

### Proposed outcome

Add a read-only recovery assessment and signed/checksummed artifact manifests, available through CLI, JSON, and TUI.

Suggested command surface:

```text
bento recovery status
bento recovery manifest [--latest|--file PATH]
bento recovery verify --artifact PATH
bento recovery drill --app APP [--engine ENGINE] --target APP_drill
```

### Requirements

The feature MUST:

- enumerate every app binding and classify its recovery method: relational logical dump, plain SQLite logical dump, or Litestream replica;
- show latest local success, artifact age/size/checksum, latest remote upload evidence, and latest restore/verification evidence;
- distinguish `healthy`, `stale`, `unverified`, `missing`, and `failed` without claiming remote durability from a local command result alone;
- produce a versioned manifest for each successful backup batch containing artifact-relative path, engine, service, app, database/file identity, byte size, SHA-256, creation time, and tool/image version;
- keep manifests private, atomic, bounded, and free of credentials;
- verify artifact checksums without modifying state or databases;
- treat an rclone upload as transport evidence, not proof of remote retention or restorability;
- make staleness thresholds configurable per stack while providing conservative defaults;
- exit nonzero when a requested policy is not met, so host cron/monitoring can alert without a daemon.

A restore drill SHOULD create a new namespaced relational database or separate SQLite output, run engine integrity checks, record bounded evidence, and never replace production.

### Acceptance criteria

- A mixed MySQL/PostgreSQL/plain-SQLite/Litestream stack reports every binding exactly once.
- Missing or corrupt artifacts cannot appear healthy.
- A successful remote upload with no restore test remains `unverified`.
- Evidence paths are relative to the stack ownership boundary and cannot traverse it.
- JSON output has a documented schema version.
- No database or remote credential appears in the report or manifest.

### Non-goals

- A hosted monitoring service.
- A guarantee that an object-store provider will retain data.
- Automatic production replacement during a drill.

## 4. F-02 — Guarded SQLite production restore

### Problem

Bento can verify and export a Litestream replica and create plain SQLite logical backups, but it does not provide a guarded in-place production restore. The manual procedure is error-prone because SQLite may have active writers and WAL/SHM sidecars.

### Proposed outcome

Provide a two-phase restore workflow that prepares and verifies a candidate before a short, explicit cutover.

Suggested command surface:

```text
bento sqlite restore prepare --app APP --file PATH
bento sqlite restore cutover --app APP --plan PLAN_ID --confirm "replace APP"
bento sqlite restore rollback --app APP --plan PLAN_ID --confirm "rollback APP"
bento sqlite restore status --app APP
```

### Requirements

`prepare` MUST:

- accept a plain SQLite backup or a separately exported Litestream database;
- refuse compressed/content mismatches, symlinks, unsafe paths, and wrong app/file identity unless explicitly importing as a new binding;
- restore to a private staging file on the same filesystem as production;
- run `PRAGMA integrity_check`, verify it is a database, and record size/checksum/schema metadata;
- leave the live database untouched;
- create a private, expiring, versioned plan bound to stack identity, app, source checksum, candidate checksum, and current live checksum.

`cutover` MUST:

- require exact confirmation and a still-current plan;
- require the app to be disabled or place it in the proposed maintenance/drain state;
- stop Bento-managed writers for the app, including FPM pool, scheduler, workers, and deploy drain;
- detect and refuse a changed live checksum since preparation unless the operator creates a new plan;
- preserve the current database and WAL/SHM as a rollback set;
- checkpoint/close managed writers, publish the candidate with same-filesystem atomic replacement, repair UID/GID/modes, remove stale sidecars safely, and rerun integrity checks;
- keep the app disabled after cutover until the operator verifies and enables it;
- never delete the rollback set automatically before a configured retention period.

`rollback` MUST use the same confirmation, writer-stop, integrity, ownership, and atomic-publication rules.

### Acceptance criteria

- A failed prepare cannot modify live bytes.
- A live database changed after plan creation cannot be replaced by the stale plan.
- Failed cutover either leaves the old database active or leaves the app disabled with an explicit recoverable rollback set; it must never claim success ambiguously.
- WAL-mode databases are covered by integration tests with concurrent writes.
- External/unmanaged writers are called out as a hard boundary and can force refusal when detectable.

### Non-goals

- Zero-downtime SQLite replacement.
- Cross-engine conversion.
- Automatic deletion of remote Litestream objects.

## 5. F-03 — First-class change plans and approvals

### Problem

`apply --preview` shows generated files and reload targets, but operators need one plan that explains state changes, durable side effects, validation, service disruption, and destructive confirmations before execution. Command-path reload plans can also be harder to audit than a state/output diff.

### Proposed outcome

Introduce immutable, expiring operation plans for significant mutations.

Suggested command surface:

```text
bento plan app update demo ...
bento plan apply
bento apply --plan PLAN_ID
bento plan show PLAN_ID --json
```

### Requirements

A plan MUST include:

- stack root and stable project identity;
- hash/version of current state, `.env` inputs relevant to generation, assets, and custom/overlay inputs;
- normalized state diff with secret values redacted;
- generated-file add/change/remove summary;
- validators and computed reload targets;
- expected container recreation/start/stop behavior, if any;
- durable side effects such as database creation, restore, prune, transfer, or credential change;
- downtime/risk classification and required confirmations;
- expiration time and a digest used to detect time-of-check/time-of-use drift.

Applying a plan MUST refuse if any bound input changed. Read-only planning MUST not write desired state, generated files, credentials, databases, host cron, or Docker resources.

Routine low-risk commands MAY continue supporting direct execution, but destructive/data-changing operations SHOULD require or internally create an equivalent plan.

### Acceptance criteria

- Plans are deterministic for identical inputs except explicit plan identity/expiry metadata.
- Secret changes appear as `changed` without revealing old/new values.
- A changed state/custom file/asset invalidates the plan.
- Reload targets are derived from actual semantic/generated differences and tested against command-specific plans.
- JSON plans are versioned and safe for code review/automation.

### Non-goals

- A remote approval server.
- Continuous reconciliation.
- Guaranteeing atomicity across state, databases, files, and containers.

## 6. F-04 — Upgrade preflight and explicit state migration

### Problem

Bento correctly rejects unsupported schema versions, but operators lack a first-class way to determine whether a new binary is compatible, preview asset/container changes, back up state, and perform an explicit migration when a future schema changes.

### Proposed outcome

Add reviewable upgrade planning and one-way, version-by-version state migration with rollback material.

Suggested command surface:

```text
bento upgrade check --binary /path/to/new/bento
bento upgrade plan --to VERSION
bento upgrade migrate --plan PLAN_ID --confirm "upgrade production"
bento upgrade verify
```

### Requirements

Upgrade preflight MUST report:

- current/target Bento, Deno target, state schema, and asset versions;
- supported source schema range and migration chain;
- changed images/assets and required container rebuild/recreation;
- custom-template drift and overlay validation warnings;
- free-space and backup prerequisites;
- expected service disruption and rollback boundary.

Migration MUST:

- be explicit, one-way, deterministic, and isolated per schema step;
- create a private immutable copy of `.env`, state, selected custom metadata, and generation metadata before changing state;
- validate both input and output schemas;
- write state atomically only after the complete migration succeeds;
- never migrate durable database contents implicitly;
- support a `--dry-run` that emits redacted before/after summaries;
- leave rollback instructions and identify where rollback becomes unsafe after data-plane changes.

### Acceptance criteria

- Routine state load still never migrates silently.
- Every migration has golden input/output fixtures and downgrade/rollback documentation.
- An interrupted migration preserves either old valid state or new valid state, never partial JSON.
- Source and compiled target binaries produce equivalent migration output.

### Non-goals

- Automatic unattended upgrades.
- Downgrading database engines or container data formats.
- Hiding incompatible custom overlays.

## 7. F-05 — Database binding promotion and retirement

### Problem

Apps can add multiple bindings, but the first binding has default credential semantics and there is no complete lifecycle for intentionally promoting another binding or retiring an unused one. Operators can migrate data externally but cannot cleanly reflect the cutover in Bento.

### Proposed outcome

Add explicit metadata-only promotion and guarded detach/retire operations without claiming to move data.

Suggested command surface:

```text
bento app database list APP
bento app database promote APP --binding BINDING_ID
bento app database retire APP --binding BINDING_ID --retain-data
bento app database prune APP --binding BINDING_ID --confirm ...
```

### Requirements

- Every binding MUST gain a stable persisted identifier independent of array position.
- Exactly one binding MUST be marked primary for conventional `DB_*` credentials.
- Promotion MUST rewrite app credentials atomically and state clearly that framework configuration/restart/migration is operator-owned.
- Promotion MUST NOT copy, transform, delete, or verify application data unless a separate verification option is requested.
- Retirement MUST remove active credentials/reference only after another primary exists and MUST retain durable data by default.
- Permanent database/file deletion MUST remain a separate prune workflow with an inventory and exact identity-specific confirmation.
- A preflight SHOULD verify target connectivity and optionally execute an operator-supplied read-only probe.

### Acceptance criteria

- Array reordering cannot change primary connection accidentally.
- Promotion preserves all binding passwords and database records.
- Removing desired reference never drops a relational database or SQLite file.
- Mixed-engine ambiguity is eliminated from shell/backup/restore commands by stable binding ID.
- Schema migration from v4 assigns deterministic binding IDs and preserves the first binding as primary.

### Non-goals

- Automatic SQL/data conversion.
- Framework `.env` editing.
- Automatic deletion after a retention timeout.

## 8. F-06 — Stable automation output and event records

### Problem

`--json` exists where supported, but automation needs documented schemas, stable error envelopes, and a bounded record of important operator actions. Human logs are not a reliable API.

### Proposed outcome

Version machine output and write a private local operation event log without introducing a service/API.

### Requirements

- Every scriptable command SHOULD support a common JSON envelope containing schema version, command, stack identity, timestamp, success, result, warnings, and structured error/recovery fields.
- Secret fields MUST be absent rather than merely visually masked where practical.
- Exit categories MUST remain stable and documented.
- Significant mutations SHOULD append a private bounded event containing actor UID, command category, affected identities, plan digest, outcome, and redacted error code.
- Event writes MUST be atomic/append-safe, rotation-bounded, and non-authoritative.
- Operators MUST be able to disable event retention or set its bound.

### Acceptance criteria

- JSON never mixes presentation text on stdout.
- Schema compatibility tests cover success, validation, safety, conflict, service, and platform failures.
- Event-log failure cannot silently convert a failed operation into success; policy defines fail-open versus fail-closed per operation.
- Support bundles include redacted event summaries only when requested.

### Non-goals

- A public management API.
- A remote audit/compliance product.
- Using events as desired state or recovery data.

## 9. F-07 — Capacity budgets and pressure diagnostics

### Problem

FPM profile sums produce warnings, but shared PHP/database/host capacity can still be exhausted without a structured budget or trend-friendly output.

### Proposed outcome

Add advisory stack/runtime budgets and snapshot diagnostics while preserving the shared-container model.

### Requirements

- Allow stack-level advisory budgets for memory, disk reserve, FPM children, worker count, backup workspace, and database volume growth.
- Compute estimated FPM memory from operator-supplied or measured per-child values; clearly label estimates.
- Report current process counts, queue/worker state, disk/volume usage, backup growth, and configured maxima.
- Refuse only operations that violate a hard safety reserve explicitly enabled by the operator; default behavior remains warnings.
- Expose snapshot JSON/Prometheus text through a CLI invocation, not a resident metrics server.

### Acceptance criteria

- Capacity output separates measured, configured, and estimated values.
- Missing Docker metrics degrade to `unknown`, not zero/healthy.
- No feature claims per-app kernel/container quotas.
- Plan output includes projected profile/process-cap impact.

## 10. F-08 — Deploy queue operations and generic lifecycle hooks

### Problem

Operators can inspect and drain deploys but have limited tools for a stuck or superseded queue and no generic pre/post orchestration contract around their hook.

### Proposed outcome

Add safe queue controls and lifecycle hooks while remaining source-control/framework neutral.

### Requirements

- Support cancel of queued jobs, retry of terminal jobs, and explicit stale-running recovery.
- Cancellation/retry MUST use queue locks, preserve immutable history, and require app/job identity.
- Running process cancellation MUST use a bounded TERM/grace/KILL policy and never target a reused PID without identity proof.
- Optional pre-deploy, deploy, post-success, and post-failure argv hooks MAY be configured; shell mode must be explicit.
- Hooks MUST run as app identity, inside app home, with individual timeouts and redacted environment.
- The terminal result model MUST distinguish hook failure, timeout, cancellation, and OPcache-reset warning.

### Acceptance criteria

- Queue corruption/interruption recovery remains deterministic.
- Retrying creates a new job linked to the source job rather than rewriting history.
- No Git provider, checkout command, release-directory scheme, or migration command is hard-coded.

## 11. F-09 — TLS and DNS readiness checks

### Problem

ACME failures and external certificate incidents often come from DNS, port reachability, SAN mismatch, key mismatch, or approaching expiry—conditions not fully proven by configuration validation.

### Proposed outcome

Extend doctor with explicit, non-mutating site readiness checks.

### Requirements

- Validate certificate/key match, SAN coverage, not-before/not-after, chain readability, and private-key mode.
- Resolve A/AAAA for every site and compare with operator-declared expected public addresses.
- Check local listeners and optionally perform an operator-enabled external HTTP challenge probe.
- Report ACME issuer/state health and renewal urgency without exposing account material.
- Produce nonzero policy exits for expiry windows configurable by the operator.

### Acceptance criteria

- Shared boot certificates are reported as non-production, not healthy public identity.
- DNS uncertainty and unavailable external probe are `unknown`, not success.
- Checks do not request/renew a certificate unless the operator runs the existing apply flow.

## 12. F-10 — Maintenance mode with scoped drain

### Problem

Disabling an app removes its runtime configuration after apply, but restore and migration windows often need a friendly response, queue drain, and explicit writer shutdown sequence.

### Proposed outcome

Add app maintenance state distinct from disabled state.

### Requirements

- Maintenance MUST retain the domain and serve a generated or operator-owned static response with configurable status and retry header.
- Entering maintenance SHOULD stop new PHP requests, pause scheduler/deploy intake, and optionally drain or stop workers with a timeout.
- Existing app data and credentials remain untouched.
- Exit MUST restore the prior enabled runtime configuration through normal validated apply.
- The state and plan MUST show whether workers were drained, stopped, or left running.

### Acceptance criteria

- Other apps on the same PHP version remain available.
- Failed drain leaves maintenance active and reports remaining writers.
- Maintenance cannot masquerade as proof that external writers are stopped.

## 13. Recommended delivery order

### Milestone 1 — Trust the release and recovery evidence

1. Complete technical remediations T-01 through T-04 in the companion technical spec.
2. Deliver F-01 recovery status/manifests.
3. Deliver F-09 TLS/DNS readiness.

### Milestone 2 — Make risky operations plan-driven

1. Deliver F-03 change plans.
2. Deliver F-02 SQLite guarded restore.
3. Deliver F-10 maintenance/drain if needed by restore implementation.

### Milestone 3 — Make evolution explicit

1. Deliver F-04 upgrade/migration.
2. Deliver F-05 binding IDs/promotion/retirement as the first schema migration.
3. Deliver F-06 stable automation output.

### Milestone 4 — Improve ongoing operations

1. Deliver F-07 capacity diagnostics.
2. Deliver F-08 deploy queue/lifecycle enhancements.

## 14. Product constraints that remain unchanged

These proposals MUST NOT quietly expand Bento into:

- a multi-host scheduler or Kubernetes layer;
- a resident daemon, public API, or browser admin product;
- a hostile multi-tenant sandbox;
- one container per app;
- a hard-coded Git/framework deployment system;
- an automatically destructive database lifecycle;
- a product that claims local or uploaded backups are recovery proof without verification.
