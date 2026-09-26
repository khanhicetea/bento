# Clean first: CLI + web, no standalone TUI

Status: proposed removal plan, **not implemented**. Do this before the enhancement/refactor roadmap in `enhance-work.md`, without using cleanup as a reason to weaken recovery or destructive-operation safety. Source inventory: `bento-questions.md`, `specs/01-product-spec.md`, `02-system-architecture.md`, `03-reimplementation-contract.md`, `06-minicrond-migration-plan.md`, and the current CLI, web, and shared contracts.

## Target shape

- Two operator surfaces: scriptable `bento` commands and the loopback-safe `bento serve` web UI. Both use typed domain/service operations, not CLI parsing from web requests. No `bento tui` or parallel full-screen wizard.
- Keep ordinary CLI human output, `--json` where supported, `app shell`, editor invocation, and **the existing direct confirmation prompt for `app prune`**. A one-question safety prompt is not a third interface; no `--yes` shortcut or relaxed confirmation.
- Keep per-app minicrond's scheduler UI/CLI and Bento's authenticated scheduler gateway: those are operational surfaces, not Bento's old TUI. Keep host crontab for backup/maintenance, and keep deploy queues/internal scheduled tasks.
- This intentionally retires a working command. Update the baseline specs and release notes when shipping; the current specs and README still describe `tui`. If supporting existing installations, decide and document the compatibility policy before deleting persisted fields.

## 1. First PR: remove the standalone TUI, not shared CLI presentation

**Delete after replacing references:** `apps/cli/src/commands/wizard.ts`, `apps/cli/src/commands/wizard/`, `apps/cli/src/ui/tui.ts`; the `tui` registration/handler/import in `apps/cli/src/commands/subcommands/core.ts`; and the TUI tip in `apps/cli/src/commands/router.ts`. `serve` already runs the state migration gate; retain `bento migrate` for scripted CLI use.

- Remove only TUI-specific tests (`apps/cli/tests/unit/tui_test.ts`, `wizard_apps_test.ts`) and replace the `tui` migration assertion in `state_database_test.ts` with a `serve` or explicit `migrate` assertion. Add CLI contract tests that `tui` is absent from help/returns a clear unknown-command error, and that `serve`, `migrate`, `app shell`, and exact prune confirmation still work.
- Audit the wizard for **unique** useful workflows before removal, especially the SQLite backup, database restore, templates, and app creation screens. If a safe operation has no scriptable CLI equivalent, either provide that equivalent in a separately scoped PR or defer the TUI deletion. Do not replace a destructive workflow with a weaker web button.
- Remove TUI instructions from `README.md`, CLI help, `docs/src/content/docs/` (including CLI/state/desired-state/SQLite pages), and TUI claims in baseline `specs/`. Document CLI and web alternatives, including `bento serve`'s loopback default and migrations. Search all references, not only filenames.
- **Do not remove** `apps/cli/src/ui/output.ts`, `cliui`, `picocolors`, `apps/cli/src/types/cliui.d.ts`, terminal/PTY web code, or `app prune` prompting just because their names or behavior look interactive: they still serve regular CLI output or web/CLI operations. Remove a dependency from `apps/cli/package.json` and update `bun.lock` only after proving it is unused.

**Done:** CLI help has no TUI; migrations and strict destructive confirmations still work; web can still start locally; source/compiled smoke and docs match. This PR should not alter state schema, job runtime, backup, or transfer behavior.

## 2. Second PR: retire Bento-owned user cron/worker surface completely

The current minicrond migration is **not** a reason to remove the actual scheduler. It already runs app-owned registries, but Bento still carries ghost `cronJobs[]`/`workers[]` and relational `cron_jobs`/`workers` tables; `services/cron.ts` and add/remove operations in `services/worker.ts` reject mutations, while old CLI and oRPC commands still advertise them.

- Remove `apps/cli/src/commands/subcommands/{cron,worker}.ts` and their router registrations once the `bento app minicrond <slug> -- <args...>` path and browser scheduler are verified. Do not remove `backup schedule` or app-scoped minicrond commands. Replace retired-command tests with absence/help tests and checks that scheduler access is still available.
- Remove dead CRUD/control/log routes from `apps/cli/src/server/domains/jobs/router.ts` and matching `packages/shared/src/domains/jobs/{contract,schema}.ts`. Retain `jobs.schedulerAccess` (used by `apps/web/src/features/jobs/SchedulerPage.tsx` and `ApplicationsPage.tsx`) or relocate it with both consumers in the same change. Keep deploy summary/history where useful, but move it to the deploy/application domain rather than deleting it with a jobs overview. No second copy of minicrond job definitions in Bento.
- Remove stale counters from `apps/cli/src/services/status.ts`, `apps/cli/src/server/domains/operations/router.ts`, `packages/shared/src/domains/operations/schema.ts`, and `apps/web/src/features/operations/OperationsPage.tsx`. Replace the misleading zero-workload display with app scheduler access/health **only if evidence is available**; otherwise omit it. Preserve deploy status and actual app/service health.
- Remove retired user-job types, schema refinements, defaults, app-delete filtering, load/save SQL and tables in `apps/cli/src/domain/state.ts`, `apps/cli/src/schemas/state.ts`, `apps/cli/src/services/{state_database,app,cron,worker}.ts` only after auditing every remaining importer and fixture. Keep minicrond's config-owned deploy drain, SQLite VACUUM, root logrotate, app-UID data directories, and independent worker supervision. Remove obsolete Supercronic references in current docs/tests; verify no runtime asset is still needed before removing an asset.
- **State compatibility decision required:** `state.db` already has numbered migrations and the desired-state schema is strict. The pre-production migration proposal permits a clean development schema with no old-row migration, but that is not permission to silently rewrite or discard a real operator's existing job definitions. Choose a clear new-schema/reset policy or a versioned migration/refusal path, with backup and recovery guidance. Never drop populated tables without an explicit operator-facing decision and tests.

**Done:** no writable Bento user-job API/CLI/state copy, but app-owned minicrond and its protected gateway still function. Test two-app scheduler isolation, internal tasks, source/compiled parity, and database version/refusal behavior. Update shared contracts, baseline specs, CLI help, guides, and web copy together.

## 3. Then simplify structure only where ownership is clearer

- Leave `apps/cli/src/commands/subcommands/` for argv parsing and human/JSON presentation; leave `apps/cli/src/server/domains/` for oRPC adapters; leave `packages/shared/src/` browser-safe. Keep actual operations in small `apps/cli/src/services/` functions behind platform abstractions. Do not route web actions through command argv or duplicate safety rules in the UI.
- After the dead paths are gone, trim orphan imports, helpers, tests, stale prose, and dependencies proven unused by reference search and checks. Deduplicate only when a concrete pair of implementations has the same behavior and tests; no catch-all service/router or universal runtime abstraction just to reduce file count.
- Do **not** remove the PHP `legacy` entrypoint mode merely because it is named legacy, or delete operator-owned custom templates, overlays, retained data, and prior transfer formats without an explicit compatibility decision. Do not edit generated stack output.

## Verification and handoff order

1. Baseline inventory and approval of the interface/state compatibility decisions; record the current CLI/web coverage and scheduler data ownership.
2. TUI PR: targeted CLI migration/confirmation tests, `bun run fmt`, `bun run lint`, `bun run check`, `bun run test`, `bun run test:parity`, plus web check/build and docs review. No Docker check claimed unless actually run.
3. Retired-jobs PR: the same gates **plus** relevant Docker integration for scheduler/internal tasks, and web check/build; assert no secrets or scheduler credentials in output.
4. Only then start `enhance-work.md`'s T-03 backup truthfulness work and the recovery/transfer improvements. Keep these PRs separate so cleanup cannot hide a changed backup or destructive-operation contract.
