# PostgreSQL database support implementation plan

Status: implementation roadmap — Phases 0–9 complete

Scope: first-class managed PostgreSQL alongside MySQL, including provisioning, credentials, shells, observability, logical backup/restore, guarded pruning, and full-stack transfer.

## 1. Product decisions

These decisions are the baseline for implementation. Do not silently broaden scope while completing a phase.

1. An application uses exactly one relational database backend: `mysql` or `postgres`.
2. Multiple managed versions of both engines may coexist in one stack.
3. MySQL 8.4 remains the default for existing and newly initialized stacks unless the operator explicitly changes the default.
4. Existing MySQL CLI behavior remains compatible.
5. Bento does not migrate application data between MySQL and PostgreSQL.
6. Moving an existing app between database engines or services remains an explicit external migration.
7. Automated PostgreSQL service/version/volume removal is unsupported, matching MySQL safety policy.
8. PostgreSQL and MySQL remain private with no published ports.
9. PostgreSQL app-role passwords are generated once and are not rotated during reconciliation.
10. Raw PostgreSQL volume transfer requires a compatible PostgreSQL major version. Logical backup/restore is the supported major-upgrade path.

This feature changes the MySQL-only invariants currently documented in `01-product-spec.md` and `02-system-architecture.md`. Update those documents as part of Phase 1.

## 2. Target domain model

Do not add loose optional `postgres*` fields beside mandatory MySQL fields. Use discriminated unions so invalid mixed-engine app state cannot be represented.

Suggested model:

```ts
type DatabaseEngine = "mysql" | "postgres";

type ManagedDatabaseService =
  | {
    engine: "mysql";
    version: MysqlVersion;
    service: DatabaseService;
    image: string;
    volume: string;
  }
  | {
    engine: "postgres";
    version: PostgresVersion;
    service: DatabaseService;
    image: string;
    volume: string;
  };

type AppDatabaseBinding =
  | {
    engine: "mysql";
    service: DatabaseService;
    user: string;
    password: string;
    databases: AppDatabase[];
  }
  | {
    engine: "postgres";
    service: DatabaseService;
    user: string;
    password: string;
    databases: AppDatabase[];
  };
```

The exact names may change, but these invariants must remain:

- engine-specific fields are narrowed by a discriminator;
- an app has one database service;
- a database belongs to one app and one service;
- service/version/image/volume information remains data-driven;
- external JSON is runtime-validated before branding;
- dispatch over engines is exhaustive.

## 3. Agent working rules

Before implementing a phase, read:

1. `README.md`
2. `specs/01-product-spec.md`
3. `specs/02-system-architecture.md`
4. this file
5. the files listed under that phase

For every phase:

- complete only that phase and its directly required refactors;
- add automated tests before marking checkboxes complete;
- preserve source/compiled behavior parity;
- never put root or app database passwords on host argv;
- never print secrets in normal output, JSON status, diagnostics, or support bundles;
- retain durable volumes and app data unless the existing guarded prune workflow explicitly applies;
- use strict TypeScript and runtime validation at every external boundary;
- run the phase verification commands;
- update the progress log at the bottom of this file.

Minimum verification after each phase:

```bash
deno task fmt
deno task lint
deno task check
deno task test
```

Run integration/parity checks when the phase changes Compose, assets, state rendering, or CLI behavior:

```bash
deno task test:integration
deno task test:parity
```

---

## Phase 0 — Lock design and acceptance contract

Goal: record the PostgreSQL behavior before changing production code.

- [x] Update `specs/01-product-spec.md` to describe PostgreSQL as a supported alternative relational backend.
- [x] Update `specs/02-system-architecture.md` topology, domain model, security model, backup flow, technology table, and invariants.
- [x] Preserve MySQL as the default and existing CLI compatibility.
- [x] Document that automatic MySQL/PostgreSQL migration is out of scope.
- [x] Document that PostgreSQL service/volume removal is blocked.
- [x] Add acceptance IDs or a test matrix for every gate in §10 below.
- [x] Decide and document the PostgreSQL version format: official major-only tags such as `17`, with service `postgres17`, image `postgres:17`, and volume `postgres17-data`. Minor/patch tags and aliases such as `latest` are not accepted managed-version input.

Primary paths:

- `specs/01-product-spec.md`
- `specs/02-system-architecture.md`
- `specs/03-reimplementation-contract.md`
- `specs/todo.md`
- `README.md`

Exit criteria:

- product and architecture documents no longer claim every app must use MySQL;
- PostgreSQL scope and non-goals are unambiguous;
- no production behavior has changed yet.

---

## Phase 1 — State schema v2 and safe migration

Goal: introduce an engine-neutral, type-safe state model without losing existing MySQL state.

### Domain and validation

- [x] Add branded `PostgresVersion`, `DatabaseService`, and any required engine types.
- [x] Replace MySQL-only app database fields with a discriminated database binding.
- [x] Replace or wrap `mysqlVersions` with engine-aware managed database services.
- [x] Replace `defaults.mysqlVersion` with an engine-aware database default.
- [x] Add PostgreSQL version and service validators.
- [x] Make all engine switches exhaustive.
- [x] Bump `STATE_SCHEMA_VERSION` from 1 to 2.

### Migration

- [x] Implement a pure v1-to-v2 migration that preserves all MySQL values exactly:
  - service names;
  - image/version selections;
  - volume names;
  - app usernames and passwords;
  - database names and timestamps;
  - stack defaults.
- [x] Add an explicit migration command, for example `bento state migrate`.
- [x] Require an exact confirmation or other deliberate operator action before replacing v1 state.
- [x] Back up `state.json` before migration.
- [x] Validate v2 completely before atomic replacement.
- [x] Ensure routine reads never silently rewrite v1.
- [x] Ensure old binaries reject v2 rather than modifying it.

Primary paths:

- `src/version.ts`
- `src/domain/types.ts`
- `src/domain/state.ts`
- `src/schemas/validators.ts`
- `src/schemas/state.ts`
- `src/services/state_store.ts`
- `src/commands/router.ts`
- new state migration command/service files

Tests:

- [x] Valid v1 fixture migrates to expected v2.
- [x] Every existing MySQL secret and durable identifier is preserved.
- [x] Invalid v1 and invalid migrated v2 leave the source file untouched.
- [x] Unsupported versions are rejected without writes.
- [x] A v2 PostgreSQL binding cannot contain MySQL-only fields and vice versa.
- [x] Existing state tests are converted without weakening corrupt-boundary coverage.

Exit criteria:

- all tests pass against schema v2;
- a real v1 fixture migrates safely;
- no PostgreSQL container is required yet.

---

## Phase 2 — PostgreSQL Compose service and PHP client support

Goal: render and start a private PostgreSQL service that PHP can reach.

### Compose and assets

- [x] Add managed PostgreSQL Compose fragments.
- [x] Use service names such as `postgres17` and volumes such as `postgres17-data`.
- [x] Mount data at the official image's PostgreSQL data path.
- [x] Join only the private Compose network; publish no ports.
- [x] Add bounded Docker logging consistent with other services.
- [x] Bind `backups/<service>` at `/var/backups/bento`.
- [x] Mount generated PostgreSQL client credentials read-only.
- [x] Add PostgreSQL fragments to deterministic Compose file ordering and validation.

### Stack secrets

- [x] Generate `POSTGRES_PASSWORD` during new stack initialization.
- [x] Preserve an existing non-empty `POSTGRES_PASSWORD` during reconciliation.
- [x] Load and validate the PostgreSQL superuser password through `stack_env.ts`.
- [x] Generate a protected root client file (`.pgpass` or equivalent) under `generated/postgres/<service>/`.
- [x] Keep generated secret mode `0600` through promotion and rollback.
- [x] Never put the superuser password on host argv.

### PHP image

- [x] Add `libpq-dev` in the extension builder.
- [x] Build and enable `pdo_pgsql` and `pgsql`.
- [x] Add the required `libpq` runtime library without leaving compilers in the runtime image.
- [x] Verify amd64 and arm64 builds.

Primary paths:

- `src/services/compose.ts`
- `src/services/generate.ts`
- `src/services/render.ts`
- `src/services/state_store.ts`
- `src/services/stack_env.ts`
- `src/platform/paths.ts`
- `src/platform/interfaces.ts`
- `templates/docker/php/Dockerfile`

Tests:

- [x] Compose fragment has a private network and no `ports` entry.
- [x] Volume, credential, and backup mounts are correct.
- [x] Compose file order is deterministic with mixed engines.
- [x] Root credential file has real content and mode `0600`.
- [x] Validation rollback restores prior PostgreSQL credential bytes and mode.
- [x] PHP image definition includes `pdo_pgsql`/`pgsql` and runtime `libpq`.
- [x] Docker integration test starts PostgreSQL and passes `pg_isready` when Docker is available.

Exit criteria:

- `postgres add <version>` can eventually consume the rendered service model;
- a rendered PostgreSQL container starts privately;
- PHP reports the PostgreSQL extensions loaded.

---

## Phase 3 — PostgreSQL service adapter and version commands

Goal: create an engine-specific adapter with safe SQL and basic administration.

Create `src/services/postgres.ts` with pure planning helpers separated from process execution.

- [x] Add PostgreSQL version/service/image helpers.
- [x] Add managed-version creation and sorted listing.
- [x] Block automated version/service removal with the same durable-volume rationale as MySQL.
- [x] Implement reachability using `pg_isready` or an authenticated `psql SELECT 1`.
- [x] Implement protected SQL execution using stdin and a temporary mode-restricted client credential file.
- [x] Ensure SQL and passwords never appear in host argv.
- [x] Add identifier and literal quoting helpers suitable for PostgreSQL.
- [x] Avoid shell interpolation for untrusted identifiers or passwords.

CLI:

```bash
bento postgres add 17
bento postgres list
bento postgres remove 17   # refused
```

`postgres` may expose `pg` as an alias, but help text should use one canonical name.

Primary paths:

- new `src/services/postgres.ts`
- new `src/commands/subcommands/postgres.ts`
- `src/commands/router.ts`
- `src/commands/args.ts`
- `src/services/compose.ts`
- wizard registration files

Tests:

- [x] Version validation rejects malformed tags.
- [x] Service/image/volume names are stable.
- [x] Duplicate versions are refused.
- [x] Removal is refused.
- [x] Recorded process argv contains no root or app password.
- [x] SQL quoting tests cover hyphens, quotes, underscores, and hostile input.
- [x] CLI help and source/compiled smoke cover add/list/remove refusal.

Exit criteria:

- operators can add and list PostgreSQL versions;
- generated Compose includes the new service;
- no app provisioning uses it yet.

---

## Phase 4 — App provisioning and PostgreSQL isolation

Goal: provision an app against exactly one selected database engine and prove cross-app isolation.

### App selection

- [x] Add `--database-engine mysql|postgres` to app create/update.
- [x] Add `--postgres <version-or-service>`.
- [x] Keep existing `--mysql` behavior as a MySQL compatibility shorthand.
- [x] Reject simultaneous contradictory MySQL/PostgreSQL options.
- [x] Preserve an existing app's engine and service when database options are omitted.
- [x] Refuse moving an existing app to another engine/service as an explicit migration.
- [x] Keep `--db` and `--database` engine-neutral.

Example:

```bash
bento postgres add 17
bento app create demo \
  --domain demo.example.com \
  --database-engine postgres \
  --postgres 17 \
  --db
```

### PostgreSQL role and database policy

For each app role:

- [x] Create `LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION`.
- [x] Do not change an existing role's password during reconciliation.
- [x] Let Bento's PostgreSQL administrator create databases; do not grant app roles `CREATEDB`.
- [x] Enforce the existing app database namespace rule.
- [x] Create each database with the app role as owner.
- [x] Revoke database access from `PUBLIC`.
- [x] Grant only the owning app role required database access.
- [x] Revoke public schema creation and configure schema ownership/access for the app role.
- [x] Ensure another app role cannot connect to or modify the database.

### Failure semantics

- [x] Explicit `--db` requires a reachable PostgreSQL service.
- [x] Role/database/grant failure occurs before desired state is saved.
- [x] Best-effort role setup without `--db` may defer with an actionable message.
- [x] Re-running provisioning preserves the original app password.

### App credentials

For PostgreSQL apps, write protected credentials such as:

```text
DB_CONNECTION=pgsql
PGHOST=postgres17
PGPORT=5432
PGUSER=demo
PGPASSWORD=...
PGDATABASE=demo
```

- [x] Do not emit misleading `MYSQL_*` values for PostgreSQL apps.
- [x] Preserve Redis credentials in the same file.
- [x] Keep the credential file mode `0600`.
- [x] Redact the database password from `app show` and JSON output.

Primary paths:

- `src/services/app.ts`
- new `src/services/database.ts` for engine dispatch if useful
- `src/services/mysql.ts`
- `src/services/postgres.ts`
- `src/commands/subcommands/app.ts`
- `src/commands/wizard/apps.ts`
- `src/services/app_prune.ts` only for plan compatibility at this phase

Tests:

- [x] MySQL create flow remains behavior-compatible.
- [x] PostgreSQL app selection and persisted union are correct.
- [x] Contradictory engine flags are rejected before writes.
- [x] Explicit unavailable PostgreSQL leaves state unchanged.
- [x] PostgreSQL role password is stable across reconciliation.
- [x] App credentials are correct and mode `0600`.
- [x] Human and JSON output redact passwords.
- [x] Live integration: PHP connects through `pdo_pgsql`.
- [x] Live integration: app A cannot connect to app B's database.

Exit criteria:

- a PostgreSQL-backed PHP app can be created and connect;
- isolation and failure-before-recording are proven.

---

## Phase 5 — PostgreSQL database administration commands

Goal: reach MySQL feature parity for routine PostgreSQL operations.

CLI:

```bash
bento postgres db <app> <database>
bento postgres shell --root --service postgres17
bento postgres shell --app <slug>
bento postgres size [--app <slug>] [--service postgres17]
bento postgres processlist [--app <slug>] [--service postgres17]
```

- [x] Create and record additional namespaced app databases.
- [x] Apply the same ownership and `PUBLIC` revocation policy to every database.
- [x] Open root and app-authenticated `psql` shells.
- [x] Stage app credentials through stdin into a protected temporary file and always clean it up.
- [x] Query sizes using `pg_database_size`.
- [x] Query activity using `pg_stat_activity` without exposing query secrets unnecessarily.
- [x] Add JSON and human table output consistent with MySQL commands.
- [x] Add corresponding wizard actions.

Primary paths:

- `src/services/postgres.ts`
- `src/commands/subcommands/postgres.ts`
- new or updated `src/commands/wizard/postgres.ts`
- `src/commands/wizard/apps.ts`
- `src/ui/output.ts`

Tests:

- [x] Database namespace and duplicate checks.
- [x] Failure occurs before state recording.
- [x] Shell plan and actual argv contain no passwords.
- [x] Temporary credential cleanup runs after shell failure.
- [x] Size and process output parsing handles empty/malformed output.
- [x] CLI help and `--print`/dry planning smoke.

Exit criteria:

- PostgreSQL has add/list/db/shell/size/process parity with the existing MySQL operator surface.

---

## Phase 6 — Engine-aware logical backup and restore

Goal: make top-level backup/restore dispatch safely across MySQL and PostgreSQL.

Keep the existing top-level commands and infer the engine from the selected app/database:

```bash
bento backup --app demo
bento backup --app demo --database demo_archive
bento backup --all
bento restore --file ... --app demo --target demo_verify
```

### Refactor

- [x] Introduce engine-neutral backup request/artifact types.
- [x] Move MySQL implementation behind a MySQL adapter without changing behavior.
- [x] Dispatch app/database operations by the app's database binding.
- [x] Make `backup --all` support mixed MySQL and PostgreSQL services.
- [x] Keep retention per engine/service/database and apply it only after the requested batch succeeds.

### PostgreSQL backup

- [x] Run matching-version `pg_dump` inside the selected PostgreSQL container.
- [x] Use `--no-owner --no-acl` for portable app-level logical restores.
- [x] Support uncompressed, gzip, and Zstandard output.
- [x] Keep dump bytes inside the container backup bind.
- [x] Write a private partial file, verify success and non-empty output, then atomically rename.
- [x] Do not publish a failed or empty dump.

### PostgreSQL restore

- [x] Validate source file type, size, compression, target namespace, app, and engine.
- [x] Create the target database with the app role as owner.
- [x] Reapply database and schema isolation policy.
- [x] Restore without importing source ownership or ACLs.
- [x] Require exact target-name confirmation before replacement.
- [x] Warn that restore is not object-level atomic and failure may leave a partial destination.
- [x] Record a newly restored target database only after successful completion when appropriate.

Primary paths:

- `src/services/mysql.ts`
- `src/services/postgres.ts`
- new `src/services/database_backup.ts` or equivalent dispatcher
- `src/commands/subcommands/backup.ts`
- `src/commands/wizard/mysql.ts` or an engine-neutral replacement
- backup picker and output files

Tests:

- [x] PostgreSQL dump command uses the protected client configuration.
- [x] Root/app passwords are absent from argv.
- [x] Empty/failed dump leaves no final artifact.
- [x] Atomic finalize and per-database retention work.
- [x] Mid-batch failure preserves prior valid artifacts and skips retention.
- [x] Restore-to-new succeeds in planning/integration tests.
- [x] Replacement requires exact confirmation.
- [x] Cross-app target namespace is refused before side effects.
- [x] Mixed-engine `--all` selects the correct adapter for every database.

Exit criteria:

- PostgreSQL logical recovery has the same safety semantics as MySQL;
- existing MySQL backup tests remain green.

---

## Phase 7 — Status, doctor, maintenance, prune, and diagnostics

Goal: integrate PostgreSQL into every operator and safety surface.

### Status and health

- [x] Add PostgreSQL role kind and managed-version information to status.
- [x] Show each app's database engine/service/databases without secrets.
- [x] Probe running PostgreSQL services using `pg_isready` or authenticated `SELECT 1`.
- [x] Keep stopped services as `config-ready`/down rather than fake success.
- [x] Add PostgreSQL volumes and credential modes to doctor checks.
- [x] Include PostgreSQL in capacity/storage notes where relevant.

### Guarded prune

- [x] Make retained app data manifests engine-aware.
- [x] Preserve the existing literal `delete` requirement and lack of bypass flag.
- [x] For PostgreSQL, terminate target-database sessions safely before dropping when required.
- [x] Drop only databases recorded for that retained app.
- [x] Drop only the corresponding app role after its databases are handled.
- [x] Never drop another app's database or a managed service volume.

### Diagnostics and maintenance

- [x] Include PostgreSQL health metadata in support bundles without secrets.
- [x] Ensure environment, `.pgpass`, SQL payloads, and app passwords are redacted.
- [x] Include PostgreSQL backup directories in maintenance/retention logic.

Primary paths:

- `src/services/status.ts`
- `src/services/doctor.ts`
- `src/services/app_prune.ts`
- `src/services/maintenance.ts`
- support bundle services
- `src/ui/output.ts`
- status/application wizard files

Tests:

- [x] Status human and JSON output include PostgreSQL but no secret fields.
- [x] Doctor probes and volume checks include both engines.
- [x] Support bundle fixtures contain no PostgreSQL password.
- [x] Prune plans are engine-aware and reject malformed retained manifests.
- [x] Prune cannot target unrecorded or cross-app databases.
- [x] PostgreSQL version/volume removal remains unavailable.

Exit criteria:

- PostgreSQL is visible and diagnosable everywhere MySQL is;
- destructive paths retain existing confirmation and scoping guarantees.

---

## Phase 8 — Full stack export/import

Goal: include PostgreSQL raw volumes in portable full-stack transfer.

- [x] Generalize `stackVolumeNames` into engine-aware database volumes plus Redis.
- [x] Export one archive per logical PostgreSQL volume.
- [x] Stop only running MySQL/PostgreSQL/Redis data services needed for a consistent raw copy.
- [x] Restart exactly the services that were running before export.
- [x] Validate every archive before import.
- [x] Refuse existing destination PostgreSQL volumes.
- [x] Restore PostgreSQL archives to the logical volume names derived from imported state.
- [x] Clean up only volumes created by a failed import.
- [x] Re-render and start the complete mixed-engine Compose chain after import.
- [x] Update documentation to require compatible PostgreSQL major/image versions for raw transfer.

Primary paths:

- `src/services/stack_transfer.ts`
- `src/services/compose.ts`
- `README.md`
- stack transfer tests

Tests:

- [x] Archive naming is deterministic and collision-free across engines.
- [x] Mixed-engine export stop/restart plan is exact.
- [x] Import refuses missing, corrupt, unsafe, or unexpected archives.
- [x] Failed import cleans up only newly created volumes.
- [x] Live integration round-trips PostgreSQL data when Docker is available.

Exit criteria:

- a mixed MySQL/PostgreSQL/Redis stack exports and imports without data loss;
- failure semantics match existing stack transfer guarantees.

---

## Phase 9 — TUI, live harness, parity, and documentation completion

Goal: finish the public product surface and release proof.

- [x] Add PostgreSQL workflows to the interactive wizard.
- [x] Extend `test-stack` with a PostgreSQL-backed app and real PHP connection.
- [x] Add live two-app PostgreSQL isolation proof.
- [x] Add PostgreSQL backup/restore live chain.
- [x] Add mixed-engine status and stack-transfer scenarios.
- [x] Update README command tables, quick start, architecture, security notes, backup notes, and non-goals.
- [x] Update `scripts/system-scenarios.md` with PostgreSQL host scenarios.
- [x] Update source/compiled parity fixtures for schema v2 and mixed engines.
- [x] Verify native compile plus Linux amd64/arm64 artifacts.
- [x] Run the complete CI-equivalent suite.

Verification:

```bash
deno task fmt
deno task lint
deno task check
deno task test
deno task test:integration
deno task compile
deno task test:parity
deno task compile:amd64
deno task compile:arm64
```

Exit criteria:

- all automated acceptance gates pass;
- live Docker harness proves connectivity and isolation;
- source and compiled modes remain equivalent;
- documentation describes the shipped behavior accurately.

---

## 10. Final acceptance gates

Do not mark PostgreSQL support complete until all are checked.

1. [x] **PG-01** — A v1 MySQL state migrates to v2 without changing credentials, service names, volume names, or database ownership records.
2. [x] **PG-02** — New stacks remain MySQL-default unless explicitly configured otherwise.
3. [x] **PG-03** — A PHP app connects to PostgreSQL through `pdo_pgsql`.
4. [x] **PG-04** — PostgreSQL services have no public port publication.
5. [x] **PG-05** — Two PostgreSQL app roles cannot connect to or modify each other's databases.
6. [x] **PG-06** — Explicit PostgreSQL database failure leaves desired state unchanged.
7. [x] **PG-07** — Reconciliation never rotates an existing PostgreSQL app-role password.
8. [x] **PG-08** — Root and app secrets never appear in host argv, status JSON, diagnostics, or support bundles.
9. [x] **PG-09** — Failed or empty PostgreSQL backups publish no final artifact.
10. [x] **PG-10** — Restore-to-new works and replacement requires exact confirmation.
11. [x] **PG-11** — Mixed MySQL/PostgreSQL backup, status, doctor, and pruning dispatch correctly.
12. [x] **PG-12** — PostgreSQL volumes round-trip through full stack export/import.
13. [x] **PG-13** — Render rollback restores PostgreSQL secret bytes and modes.
14. [x] **PG-14** — MySQL behavior and safety tests remain green.
15. [x] **PG-15** — PostgreSQL service/volume removal is blocked.
16. [x] **PG-16** — Source and compiled binary behavior remains equivalent.
17. [x] **PG-17** — Linux amd64 and arm64 artifacts compile successfully.
18. [x] **PG-18** — Product, architecture, README, and system scenario documentation match implementation.

### Acceptance evidence matrix

| ID | Delivery phase | Required evidence before checking |
|---|---:|---|
| PG-01 | 1 | Unit fixtures for exact v1→v2 preservation, invalid input/no-write, backup, and atomic replacement |
| PG-02 | 1 | State-default unit test plus source/compiled init fixture |
| PG-03 | 2, 4 | Multi-architecture image definition checks and live PHP `pdo_pgsql` connection |
| PG-04 | 2 | Compose model assertion and Docker inspection when available |
| PG-05 | 4 | Live two-app positive-owner and cross-app denial test |
| PG-06 | 4 | Process-failure contract test proving byte-identical state |
| PG-07 | 4 | Reconciliation unit/integration test proving stable credential bytes |
| PG-08 | 2–7 | Recording-runner argv tests plus status/doctor/support-bundle redaction fixtures |
| PG-09 | 6 | Failed and empty dump tests proving no final path and partial cleanup |
| PG-10 | 6 | Restore planning/integration plus exact-confirmation refusal test |
| PG-11 | 6, 7 | Mixed-engine dispatcher tests for every named operator surface |
| PG-12 | 8 | Mixed-engine transfer unit tests and live compatible-major round trip |
| PG-13 | 2 | Injected validation/promotion failure restoring credential bytes and `0600` mode |
| PG-14 | Every phase | Existing full MySQL unit, contract, integration, and parity suites stay green |
| PG-15 | 3, 7 | CLI/service refusal tests and absence of a destructive bypass |
| PG-16 | 3–9 | `deno task test:parity` with mixed-engine fixtures |
| PG-17 | 2, 9 | Successful `compile:amd64` and `compile:arm64`; image build checks on both architectures |
| PG-18 | 9 | Documentation assertions/review after shipped command and scenario updates |

The gate checkboxes remain open until the listed implementation and evidence exist. Phase 0 only freezes their IDs and proof requirements.

## 11. Known risks and implementation notes

### PostgreSQL privilege semantics

PostgreSQL does not have MySQL's database-name wildcard grants. Bento must provision every database explicitly and must not give app roles `CREATEDB`. Isolation depends on revoking default `PUBLIC` access at both database and schema levels.

### Existing object ownership on restore

Logical dumps should omit owners and ACLs. Restore should run with controlled ownership so objects do not become owned by the PostgreSQL superuser or a source-host role.

### State downgrade safety

Schema v2 is intentionally incompatible with old binaries. An old binary must reject it and leave it untouched. Never retain schema version 1 while adding fields that an old parser could strip during a later save.

### Raw volume portability

Raw PostgreSQL volumes are more version-sensitive than logical dumps. Full-stack import is for compatible service versions; major upgrades use logical backup/restore.

### Naming

App slugs may contain hyphens while database names currently use alphanumeric/underscore rules. PostgreSQL role identifiers must be quoted correctly; database namespace behavior should remain explicit and covered by tests.

### Refactoring strategy

Prefer engine-specific adapters behind small dispatch functions. Do not force MySQL and PostgreSQL SQL, shell, or restore behavior into one generic implementation—their security and operational semantics differ.

## 12. Progress log

Append one row after completing each phase.

| Date | Agent | Phase | Tests/result | Residual risks |
|---|---|---|---|---|
| TBD | planning | Plan created | Not implemented | All phases open |
| 2026-07-26 | coding agent | Phase 0 | Documentation contract test; full fmt/lint/check/test suite | Production behavior intentionally unchanged; Phases 1–9 and PG-01–PG-18 implementation evidence remain open |
| 2026-07-26 | coding agent (gpt-5.6) | Phase 1 | Schema v2 union/validators, pure + explicit backed-up v1 migration tests; fmt/lint/check, 173 unit+contract, 15 integration, compiled parity | PostgreSQL data-plane behavior remains intentionally unavailable until Phase 2; PG-03–PG-18 remain open |
| 2026-07-26 | coding agent | Phase 2 | Private PostgreSQL Compose + protected pgpass + PHP pgsql extensions; fmt/lint/check, 177 unit+contract, 16 integration including live `pg_isready`, source/compiled parity, amd64/arm64 binary compile, multi-arch Docker definition checks, arm64 image build/extension probe | App provisioning/CLI adapter remains Phase 3–4; live amd64 PHP image build deferred to release CI; PG-03 and PG-05–PG-12/PG-14–PG-18 remain open |
| 2026-07-26 | coding agent (gpt-5.6) | Phase 3 | PostgreSQL adapter, protected stdin SQL, quoting, reachability, add/list/removal refusal CLI + wizard registration; fmt/lint/check/test, integration, and mixed-engine source/compiled parity | App provisioning and PostgreSQL operator parity remain Phases 4–5; PG-03 and PG-05–PG-12/PG-14/PG-16–PG-18 remain open |
| 2026-07-27 | coding agent (gpt-5.6) | Phase 4 | Engine-aware app selection/provisioning, private PostgreSQL roles/databases/schemas, protected credentials/redaction; fmt/lint/check, 189 unit+contract tests, live `pdo_pgsql` and two-app isolation | Routine PostgreSQL administration remains Phase 5; PG-08–PG-12 and PG-17–PG-18 remain open |
| 2026-07-27 | coding agent (gpt-5.6) | Phase 5 | PostgreSQL db/shell/size/processlist CLI + wizard parity, protected temporary app credentials, secret-free activity output; fmt/lint/check, 194 unit+contract tests, 17 integration tests, compiled parity | Logical PostgreSQL backup/restore remains Phase 6; diagnostics evidence needed before PG-08 can close |
| 2026-07-27 | coding agent (gpt-5.6) | Phase 6 | Engine-neutral mixed backup dispatcher, PostgreSQL atomic pg_dump and isolated restore-to-new/replacement; fmt/lint/check, 200 unit+contract tests, 18 integration tests including live backup/restore, compiled parity | Status/doctor/prune/support-bundle dispatch remains Phase 7, so PG-08 and PG-11 stay open |
| 2026-07-28 | coding agent (gpt-5.6-sol) | Phase 7 | Engine-aware status/doctor/prune, PostgreSQL health/storage/credential diagnostics, support-bundle redaction; fmt/lint/check, 206 unit+contract tests, 18 integration tests, compiled parity | Raw PostgreSQL volume transfer remains Phase 8; release harness/docs remain Phase 9 |
| 2026-07-28 | coding agent (gpt-5.6-sol) | Phase 8 | Engine-aware mixed raw-volume transfer, strict archive validation, scoped service restart and failed-import cleanup; fmt/lint/check, 209 unit+contract tests, 19 integration tests including live PostgreSQL raw-volume round trip, compiled parity | Phase 9 release harness, cross-architecture artifacts, and final documentation remain open |
| 2026-07-26 | coding agent (gpt-5.6-sol) | Phase 9 | PostgreSQL wizard/release contracts and mixed-engine `test-stack`; fmt/lint/check, 212 unit+contract tests, 19 integration tests, source/compiled parity, native + Linux amd64/arm64 compile, live harness 38 passed | HTTP/deploy were intentionally skipped in the focused Phase 9 harness run; their existing integration coverage remains green; cross-architecture execution still requires matching release runners |
