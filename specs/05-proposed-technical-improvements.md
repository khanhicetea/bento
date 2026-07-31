# Proposed Bento technical fixes and enhancements

Status: proposal; no implementation authorized by this document  
Audience: architecture owner, maintainers, security/release reviewers

## 1. Purpose

This document turns the gaps and risks discovered during specification work into technical change proposals. The priorities favor correctness, safety, and release evidence before adding broader product features.

These proposals MUST preserve the baseline invariants in `01-product-spec.md`, `02-system-architecture.md`, and `03-reimplementation-contract.md` unless an approved proposal explicitly updates them.

## 2. Priority summary

| ID | Improvement | Priority | Risk addressed | Expected scope |
| --- | --- | ---: | --- | --- |
| T-01 | Enforce complete CI/release gates | P0 | Unverified release artifacts | Workflow/tests |
| T-02 | Eliminate contract/documentation drift | P0 | Operators follow stale behavior | Docs/tooling/tests |
| T-03 | Make Litestream batch-backup semantics truthful | P0 | False recovery confidence | Backup service/docs/tests |
| T-04 | Make stack transfer SQLite-consistent | P0 | Importable but inconsistent SQLite bytes | Transfer/backup format |
| T-05 | Formalize lock hierarchy and operation conflicts | P1 | Races/deadlocks across operations | Platform/services/tests |
| T-06 | Derive reload plans from semantic/output differences | P1 | Missed or excessive reloads | Domain/render/apply |
| T-07 | Add versioned machine-output/error contracts | P1 | Fragile automation | CLI/UI/contract tests |
| T-08 | Strengthen dependency/image/release supply chain | P1 | Mutable or unverifiable artifacts | Builds/releases |
| T-09 | Standardize subprocess safety | P1 | Hangs, unbounded output, secret leakage | Platform/process callers |
| T-10 | Separate secrets from structural desired state | P2 | Excessive secret exposure/copying | Schema/storage/migration |
| T-11 | Add operation journal/evidence records | P2 | Weak post-failure diagnosis | Services/storage |
| T-12 | Expand fault-injection and compatibility testing | P1 | Recovery claims lack edge proof | Tests/CI |

## 3. T-01 — Enforce complete CI and release gates

### Finding

The repository defines formatting, linting, typecheck, frozen resolution, unit/contract, integration, compile, smoke, and parity tasks. The checked-in workflow currently builds release binaries for tags/releases without running the full contract.

### Decision

Split verification and release publication into explicit jobs with publication depending on successful verification.

### Requirements

The workflow MUST:

1. run formatting, linting, typecheck, and frozen dependency resolution;
2. run unit/contract tests on every supported change path;
3. run Docker integration tests on a runner where Docker proof is required and expose skips as job summaries;
4. compile a native binary and run compiled smoke/parity;
5. compile amd64 and arm64 release binaries only after verification;
6. generate checksums and provenance metadata;
7. upload/publish artifacts only when required jobs pass;
8. use least-privilege GitHub permissions and pin third-party actions to reviewed commit SHAs for release-sensitive jobs;
9. prevent a release event from building unreviewed source different from the tagged commit.

A pull-request workflow SHOULD run the fast/static/unit contract. A protected tag/release workflow SHOULD run the full gate.

### Acceptance criteria

- Deliberately failing a unit, integration, or parity test blocks publication.
- A Docker-unavailable skip cannot be displayed as integration success without qualification.
- Published checksum/provenance identifies source commit, Deno target, Bento/asset/schema versions, architecture, and workflow run.
- Release artifacts are byte-stable where the compiler/toolchain permits; otherwise reproducibility differences are documented.

## 4. T-02 — Eliminate contract and documentation drift

### Finding

Older pages and test comments conflict with current behavior around `databases[]`, scheduled rclone upload, Litestream batch behavior, and SQLite inclusion in stack transfer. Manually maintained CLI tables and state prose are likely to drift again.

### Decision

Establish one behavior inventory and generate or verify repetitive documentation from code-owned metadata.

### Requirements

- Create a versioned capability inventory containing command, state schema, lifecycle, backup, and non-goal facts.
- Generate CLI reference tables from yargs command metadata or snapshot them in contract tests.
- Generate a redacted state-schema reference from Zod/domain metadata where feasible.
- Add repository checks for known contradictory phrases and stale version numbers.
- Add link and code-example validation for `README.md`, `docs/`, and `specs/`.
- Require a documentation impact field in change/release review.
- Keep proposals visibly separated from implemented baseline behavior.

The generated inventory MUST NOT become a second runtime source of truth. Code/schema behavior remains authoritative; generation should fail on ambiguity.

### Acceptance criteria

- A command added without CLI-reference update fails verification.
- Deno/Bento/schema/asset version references are checked against `src/version.ts`.
- The docs consistently describe multiple bindings, rclone, actual Litestream backup behavior, and transfer SQLite semantics.
- Historical tests are renamed/reworded without weakening safety assertions.

## 5. T-03 — Make Litestream batch-backup semantics truthful

### Finding

The engine-neutral backup target resolver skips Litestream bindings. Some operator prose says `bento backup --app` confirms synchronization. Silent omission can be interpreted as successful coverage even when no Litestream sync/verification occurred.

### Decision

Choose and enforce explicit semantics: a batch containing Litestream MUST either perform a confirmed sync and report evidence or report that the binding is externally covered and not verified by this batch. It MUST never disappear from results.

### Preferred behavior

For `backup --app` and `backup --all`:

1. enumerate each Litestream binding as a target;
2. require the watcher/policy to be enabled;
3. request/observe synchronization for the binding;
4. record remote generation/timestamp and latest successful integrity-verify timestamp;
5. return a non-artifact result type such as `replica-synced`;
6. fail the requested batch when synchronization cannot be confirmed, unless an explicit `--allow-unverified-litestream` policy is approved;
7. include the result in scheduled last-run status and recovery manifests.

Retention remains limited to local managed artifacts. rclone MUST NOT upload a nonexistent Litestream artifact.

### Acceptance criteria

- A mixed batch result accounts for every selected binding.
- Disabled/unreachable Litestream cannot produce an all-green backup result.
- Sync success and restore-verification success remain distinct.
- Last-run status can represent artifact and non-artifact targets without exceeding bounds or leaking S3 details.
- Documentation and tests use the same semantics.

## 6. T-04 — Make stack transfer SQLite-consistent

### Finding

`stack.tar.gz` includes the stack-root `sqlite/` tree while export stops MySQL, PostgreSQL, and Redis services only. PHP/runner or external processes may still write SQLite, so imported bytes may be inconsistent despite a successful tar.

### Decision

Move SQLite from incidental root-archive inclusion to an explicit, versioned transfer component created with SQLite-aware snapshot semantics.

### Proposed transfer format v2

```text
manifest.json
stack.tar.gz                 root state/files, excludes sqlite/ and ephemeral paths
sqlite.tar.gz                clean per-binding SQLite snapshots + metadata
<mysql-volume>.tar.gz
<postgres-volume>.tar.gz
redis-data.tar.gz
SHA256SUMS
```

### Export requirements

- Acquire the transfer lock and a compatible backup lock before snapshotting.
- Enumerate all plain SQLite and Litestream bindings from validated state.
- Pause Bento-managed app writers through a scoped maintenance/drain operation or use SQLite online `.backup` under the app identity while clearly recording the consistency point.
- Create one clean database snapshot per binding, run `integrity_check`, preserve target UID/GID/mode metadata, and exclude WAL/SHM from authoritative transfer content.
- For Litestream, optionally confirm remote sync but still create a local consistent transfer snapshot unless the operator explicitly chooses remote recovery.
- Exclude the live `sqlite/` tree from `stack.tar.gz` to avoid two competing copies.
- Generate a strict manifest and checksums only after all components succeed.
- Resume exactly the Bento-managed writers/services paused by export, even after failure.

### Import requirements

- Detect transfer format version before extracting.
- Validate manifest, checksum, expected identities, safe paths, and state-to-snapshot coverage.
- Restore SQLite snapshots only to state-derived paths with private modes/recorded UID/GID.
- Run `integrity_check` before stack startup.
- Refuse missing, duplicate, extra, or wrong-app SQLite entries.
- Retain support for v1 imports only with an explicit warning that SQLite consistency is unproven, or reject v1 when file bindings exist.

### Acceptance criteria

- Concurrent WAL writes during export produce either a verified snapshot or an explicit export failure, never a silently torn transfer.
- Every file binding has exactly one manifest entry.
- Import cannot overwrite an existing SQLite target.
- Failed export/import resumes prior services where possible and cleans only operation-owned staging.
- Transfer docs distinguish logical recovery, Litestream recovery, and full-stack transfer.

## 7. T-05 — Formalize lock hierarchy and operation conflicts

### Finding

Bento has render, backup, deploy, transfer, maintenance, and host-crontab locking, but cross-operation compatibility is implicit. Export can race with backup or state mutation; future restore/planning features increase deadlock and stale-plan risks.

### Decision

Define a lock taxonomy, acquisition order, ownership metadata, timeout policy, and conflict matrix.

### Proposed hierarchy

1. host-user global resources, such as crontab;
2. stack operation lock;
3. state mutation lock;
4. render/apply lock;
5. transfer/backup/restore data locks;
6. app-scoped deploy/maintenance locks;
7. queue/file-level locks.

Code MUST acquire locks only in declared order. Multi-lock operations MUST use one helper that releases in reverse order.

### Requirements

- Lock files include bounded diagnostic metadata: operation ID/type, PID, start time, binary version, and stack identity; no secrets.
- Normal lock contention returns a structured conflict with safe retry advice.
- Stale-lock handling MUST distinguish advisory OS lock state from stale metadata; operators MUST NOT delete a lock merely because its metadata is old.
- Transfer conflicts with state mutation, apply, database backup/restore, SQLite restore, and prune.
- Planning is read-only but applying a plan rechecks all digests after locks are acquired.
- Host-crontab operations remain serialized across different stack roots for the same user.

### Acceptance criteria

- Concurrency tests cover every conflict-matrix edge and lock acquisition order.
- Fault injection after each acquired lock proves reverse cleanup.
- Deadlock tests run competing operations repeatedly with deterministic timeouts.
- Diagnostics identify the conflicting operation without exposing command secrets.

## 8. T-06 — Derive reload plans from semantic and generated differences

### Finding

Command-specific reload plans provide a narrow blast radius but depend on each command author selecting every affected target. A new field or generator dependency can produce stale runtime if the plan is incomplete.

### Decision

Compute the final reload plan from a typed semantic diff plus the generated-file manifest; command hints may narrow only when proven safe.

### Requirements

- Define ownership metadata for each generated path: Nginx, PHP-FPM service, runner service, scheduler, worker, or no reload.
- Compare prior and candidate managed manifests by content digest and ownership.
- Merge semantic side effects that are not visible in files, such as live s6 reconciliation.
- Explain every target in preview/plan output with the changed state/path that selected it.
- Treat unknown changed generated files conservatively with a full relevant reload and warning.
- Keep stopped-service behavior unchanged.

### Acceptance criteria

- Mutation tests add/change/remove every managed file class and assert the correct minimum plan.
- No-op render produces no reload plan.
- Nginx-only changes never signal FPM/runner.
- Pool socket/path changes always target both required sides.
- Cron-only and worker-only changes remain scoped.
- Source and compiled mode compute identical plans.

## 9. T-07 — Version machine output and error contracts

### Finding

Human output, partial `--json` support, and redaction are useful interactively but insufficient as a stable automation surface.

### Decision

Implement one versioned result/error envelope in the CLI presentation layer, while services continue returning typed domain values/errors.

### Requirements

- Reserve stdout for one valid JSON value in JSON mode; diagnostics go into the envelope, not mixed text.
- Define stable error categories, exit codes, recovery field, details schema, and redaction rules.
- Distinguish validation, state, conflict, safety, not-found, service, platform, and partial-result outcomes.
- Add a schema version independent from desired-state schema.
- Keep human output free to improve without changing JSON compatibility.
- Validate subprocess JSON before embedding it.

### Acceptance criteria

- Golden tests cover every top-level command family and error category.
- Unknown internal errors return a generic redacted envelope and nonzero exit.
- No JSON field contains ANSI sequences.
- Secrets are removed structurally, not only replaced by string pattern matching.

## 10. T-08 — Strengthen the supply chain

### Finding

Deno dependencies are locked, and some in-image tools are checksum-pinned. Several base/runtime images use mutable tags, and release artifacts currently lack a complete SBOM/signature/provenance contract.

### Decision

Make every release input traceable and every published output verifiable.

### Requirements

- Pin production base images and fixed tool artifacts by digest/checksum through a reviewed lock manifest.
- Record human-readable version plus immutable digest for Nginx, PHP bases, Composer, Node, MySQL, PostgreSQL, Redis, Litestream, rclone, s6, and Supercronic where applicable.
- Add an explicit dependency/image update workflow that regenerates locks and runs full integration/parity.
- Generate SBOMs for the Bento binary and built images.
- Generate release checksums and keyless or project-key signatures with provenance tied to the Git commit/workflow identity.
- Verify downloaded archives before extraction and use TLS with failure-on-error.
- Define response policy for revoked/vulnerable images without silently changing a running stack.

### Acceptance criteria

- Rebuilding from the same lock manifest cannot pull a different image manifest unnoticed.
- Release consumers can verify checksum, signature, source commit, and architecture.
- Image update diffs are reviewable and identify every changed digest/version.
- Dynamic operator-selected database versions resolve to a recorded digest at render/build time or are explicitly marked unpinned with a warning.

## 11. T-09 — Standardize subprocess safety

### Finding

Bento invokes Docker, Compose, database clients, tar, crontab, OpenSSL, SSH tooling, and helpers. Call sites currently choose timeouts/output handling individually, increasing risk of hangs, oversized diagnostics, locale-dependent parsing, or secret leakage.

### Decision

Extend the process adapter with declared command policy and bounded execution results.

### Requirements

Every subprocess call MUST declare or inherit:

- purpose/category and safe display name;
- timeout and termination grace policy;
- maximum captured stdout/stderr bytes with truncation metadata;
- allowed environment additions and a minimal normalized locale;
- stdin sensitivity classification;
- whether network access is expected;
- expected exit-code set;
- parser/validator for machine-readable output where applicable.

The adapter MUST:

- avoid rendering sensitive argv/stdin/environment in errors;
- terminate process groups safely on timeout;
- bound memory use for captured output and stream large backup data where required;
- return structured timeout/truncation/signal information;
- support deterministic recording/fault injection in tests.

### Acceptance criteria

- Tests simulate hangs, output floods, invalid UTF-8, nonzero exits, and timeout races.
- No secret passed through stdin appears in logs/errors.
- Large backup/restore streams are not buffered entirely in the control-plane process.
- Locale-sensitive tools produce stable parseable output.

## 12. T-10 — Separate secrets from structural desired state

### Finding

Schema-v4 `state.json` contains structural intent together with database passwords and deploy HMAC secrets. This makes every state copy, diff, diagnostic path, and migration highly sensitive and complicates safe planning/version control of non-secret intent.

### Decision

For a future schema, store secret references in desired state and values in a separate private secret store under the stack root. This is exposure reduction, not protection from a host/root compromise.

### Proposed model

```text
state.json                 structural desired state + secret IDs
secrets/index.json         strict metadata, mode 0600
secrets/values/<id>        opaque bytes, mode 0600, atomic writes
```

### Requirements

- Secret IDs MUST be random, immutable, typed, and never derived from secret values.
- State validation MUST verify required references without loading unrelated values.
- Secret writes and state-reference publication require a journal that prevents dangling new references after interruption.
- Removal MUST tombstone/orphan secrets first; garbage collection is separate, dry-run capable, and retention-delayed.
- Support bundles, plans, state diffs, and exports MUST know the separate secret ownership class.
- Stack exports MUST include secrets securely; a redacted state export MAY omit values for review.
- Migration from v4 MUST preserve exact values and file modes and produce rollback material.
- Optional at-rest encryption MAY use an operator-provided key, but Bento MUST not claim encryption benefit when key and ciphertext share the same unprotected host boundary.

### Acceptance criteria

- `state.json` contains no app database password, Redis ACL password, deploy HMAC, or administrator secret value.
- Interrupted migration leaves either valid v4 or complete new schema/store.
- Secret garbage collection cannot remove a referenced or recently tombstoned value.
- Source/compiled parity includes reference behavior while excluding secret bytes from diagnostics.

## 13. T-11 — Add bounded operation journals and evidence

### Finding

Render has a recovery journal and backup schedules have last-run state, but cross-layer operations such as app provisioning, restore, transfer, and future migration lack a common evidence record. Failure recovery depends on reconstructing which steps completed.

### Decision

Introduce versioned per-operation records for complex operations. They support recovery/diagnosis but do not become desired state.

### Requirements

- Complex operations receive a random operation ID and private record containing type, stack identity, input digest, step names/statuses, timestamps, owned staging paths/resources, and redacted errors.
- Each step declares whether it is retryable, compensatable, irreversible, or operator-reviewed.
- Records are atomically updated, size-bounded, and retention-bounded.
- Recovery logic MUST validate resource identity before cleanup/retry.
- A record MUST never store plaintext credentials, request payloads, SQL dumps, or full subprocess output.
- Existing render journals remain purpose-specific but MAY share primitives/schema conventions.

### Acceptance criteria

- Fault injection after every step yields explicit recovery guidance.
- Cleanup cannot affect resources not recorded as created/owned by the operation.
- Old records can be pruned without losing desired state or durable data.
- Human and JSON status can explain a partial result consistently.

## 14. T-12 — Expand fault-injection and compatibility testing

### Finding

The test suite is broad, but the strongest product claims involve interruption, concurrency, filesystem boundaries, and cross-version recovery. Happy-path or recording-runner tests alone cannot prove them.

### Decision

Add a reusable deterministic fault-injection matrix and versioned compatibility fixtures.

### Requirements

Tests SHOULD inject failure at:

- every atomic-write temp/rename boundary;
- lock acquisition and release;
- every render promotion/journal step;
- database dump, compression, finalization, retention, and rclone upload;
- transfer stop/archive/restart/extract/volume-create/restore/start;
- SQLite prepare/checkpoint/cutover/rollback;
- state migration and secret-reference publication;
- subprocess timeout/output truncation/signal handling.

Compatibility fixtures MUST include:

- current and prior supported state/transfer/manifest/JSON-output versions;
- source and compiled execution;
- amd64 and arm64 artifact metadata;
- custom template and overlay drift cases;
- mixed MySQL/PostgreSQL/plain-SQLite/Litestream apps.

### Acceptance criteria

- Recovery tests assert final bytes, modes, locks, journals, running-service set, and owned cleanup—not only exit code.
- Property tests cover domain uniqueness, binding identity, path containment, archive entry validation, and crontab merging.
- Integration tests use disposable roots/projects and can never target the checked-in `bento/` root.
- Release reports distinguish simulated proof, Docker integration proof, and scenarios not executed.

## 15. Cross-cutting implementation rules

Any approved technical change MUST:

1. begin with a failing test or reproducible contract gap;
2. preserve exact destructive guards unless the product spec explicitly changes;
3. document state, transfer, output, and operation-record version impact;
4. state lock interactions and rollback boundary;
5. keep source and compiled behavior equivalent;
6. avoid introducing a daemon, public management port, or mandatory cloud dependency;
7. update baseline specs/docs only when implementation ships;
8. include operator migration/recovery notes before release.

## 16. Recommended execution sequence

### Phase A — Correct published truth

1. T-01 complete CI gates.
2. T-02 documentation/capability consistency.
3. T-03 explicit Litestream batch semantics.
4. T-04 transfer format v2 for SQLite consistency.

### Phase B — Strengthen control-plane correctness

1. T-05 lock hierarchy.
2. T-06 diff-derived reload planning.
3. T-09 subprocess policy.
4. T-12 fault-injection expansion.

### Phase C — Stabilize automation and release trust

1. T-07 JSON/error contracts.
2. T-08 supply-chain controls.
3. T-11 operation evidence.

### Phase D — Reduce secret exposure

1. Design/approve explicit state migration feature.
2. T-10 secret references/store.
3. Use the new migration as the first complete upgrade-path proof.
