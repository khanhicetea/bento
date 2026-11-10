# Plan 021 (investigate): Decide with evidence whether queries by operation target need an index

> **Executor instructions**: This is an investigation plan. Its most likely outcome is "no change needed", recorded
> with evidence. Follow it step by step; do not add a schema migration unless the decision rule in step 3 says so.
> If anything in the "STOP conditions" section occurs, stop and report. When done, update the status row for this
> plan in `plan/README.md` (DONE with the outcome, or REJECTED with the evidence) — unless a reviewer dispatched you
> and told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/store/`
> If the package changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S (investigation); M if a migration turns out to be justified
- **Risk**: LOW (investigation); MED for a first schema migration
- **Depends on**: none
- **Category**: perf (investigate)
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

The `operations` table has an index on `(target_kind, target_id, created_at)`, but every query that filters by
target filters on `target_id` alone, so SQLite cannot use that index for them (its leading column is not
constrained). Whether that costs anything is unproven: the hottest of those queries also filters on
`state IN ('queued','running')`, which the `(state, created_at)` index serves and which matches only a handful of
rows. History is pruned after 30 days. A new index needs the repository's first real schema migration, which is not
worth its risk without evidence. This plan gets the evidence cheaply, in a unit test, and applies a written decision
rule.

## Current state

- `apps/backend/internal/store/store.go`, in the `schema` (near line 472):

  ```sql
  CREATE INDEX operations_state ON operations(state, created_at);
  CREATE INDEX operations_target ON operations(target_kind, target_id, created_at);
  ```
  and the migration framework: `const SchemaVersion = 1`, `var migrations = map[int64]migration{}` ("migrations[v]
  upgrades schema v to v+1. Every SchemaVersion bump adds one; the baseline has none."). `BACKLOG.md`, "Decisions":
  "The next schema change adds `migrations[1]`."
- `apps/backend/internal/store/operations.go`:

  ```go
  // ActiveForTarget reports whether a queued or running operation reserves target.
  "SELECT COUNT(*) FROM operations WHERE target_id = ? AND state IN ('queued','running')"
  ```
  called by the reconciler once per app per pass, and `ListOperations` with `OpFilter.TargetID` (adds
  `AND target_id = ?`, optionally `AND state IN (…)`, then an order and limit — read the function for the exact
  SQL), used by the per-app activity API, webhook coalescing and the reconciler's recovery check.
- Tests in this package: `store_test.go`, `operations_test.go` (see how they create a store with `store.Create` on a
  temp path and insert operations with `store.InsertOperation`).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Narrow tests | `cd apps/backend && go test ./internal/store/ -run 'QueryPlan' -v` | prints the plans, PASS |
| Format / vet | `cd apps/backend && gofmt -l . && go vet ./internal/store/` | no output, exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/store/operations_test.go` (a query-plan test)
- `plan/README.md` (record the outcome)

**Out of scope unless step 3 says "add the index"**:
- `store.go` schema, `SchemaVersion`, `migrations`.

## Git workflow

- Branch: `advisor/021-investigate-operations-target-index`
- One commit for the test, e.g. `Record the query plans of operation lookups by target`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Capture the query plans in a test

Add `TestOperationTargetQueryPlans` to `operations_test.go`:

1. Create a store as other tests in the file do.
2. Insert 5,000 terminal operations spread over 50 target ids, plus 3 `queued`/`running` ones (use
   `store.InsertOperation` then the state-changing helpers the package offers, or a direct `UPDATE` in the test;
   wrap the inserts in one transaction so the test stays fast). Run `ANALYZE` afterwards so the planner has
   statistics.
3. For each of these statements, run `EXPLAIN QUERY PLAN <statement>` with a bound target id, collect the `detail`
   column of every row, and `t.Logf` it:
   - the exact SQL of `ActiveForTarget`;
   - the exact SQL `ListOperations` builds for `OpFilter{TargetID: id, Limit: 100}`;
   - the exact SQL `ListOperations` builds for `OpFilter{TargetID: id, States: []OpState{OpFailed}, Limit: 1}` (or
     whichever filter `reconcile.recovered` passes — read `apps/backend/internal/reconcile/reconcile.go`).
   Copy the SQL by reading the functions; do not approximate it.
4. Assert only that `ActiveForTarget`'s plan does **not** contain `SCAN operations` without an index (i.e. it uses
   some index). This pins today's acceptable behaviour without over-specifying the planner.

**Verify**: narrow test command → PASS and the three plans are printed.

### Step 2: Measure

In the same test, time 1,000 calls of `ActiveForTarget` and 100 calls of the per-target `ListOperations` against that
5,050-row table and log the mean per call (`t.Logf`). Do not assert on timings.

**Verify**: the log shows two mean durations.

### Step 3: Apply the decision rule

- If `ActiveForTarget` uses an index (expected: `operations_state`) **and** the per-target `ListOperations` mean is
  under 2 ms at 5,050 rows: **no index is added.** Mark this plan `REJECTED — measured, not worth a migration` in
  `plan/README.md`, and paste the three plan lines and two timings there. Keep the test (it guards against a future
  regression of the hot query). Done.
- Otherwise (a full table scan on the hot query, or a per-target list slower than 2 ms): do **not** write the
  migration under this plan. Report the plans and timings, and recommend a follow-up plan to add
  `CREATE INDEX operations_target_id ON operations(target_id, created_at)` through `migrations[1]` with a
  `SchemaVersion` bump, following "Change persistence" in `apps/backend/docs/contributing.md` ("bump `SchemaVersion`,
  add `migrations[old]` (refuse rows it cannot map), extend `schema` for new stacks, and test that a file at the old
  version opens with its data migrated"). That is the repository's first real migration and deserves its own
  review.

**Verify**: `plan/README.md` records the outcome with the evidence.

## Test plan

The query-plan test is the deliverable.

## Done criteria

- [ ] `TestOperationTargetQueryPlans` exists and passes
- [ ] `plan/README.md` row for this plan states the outcome and quotes the plans and timings
- [ ] `git diff --stat` shows no change to `store.go`
- [ ] `gofmt -l .` prints nothing; `go vet ./internal/store/` exits 0

## STOP conditions

- "Current state" excerpts do not match (for example the index already changed).
- `EXPLAIN QUERY PLAN` output cannot be read through the store's connection in a test — report the error.
- Go is unavailable: report "not run"; do not guess the planner's behaviour.

## Maintenance notes

- The existing `operations_target(target_kind, target_id, created_at)` index is usable only by queries that also
  constrain `target_kind`. If a follow-up does add an index, consider replacing this one rather than adding a third.
- Re-run this test's numbers if history retention grows beyond 30 days or webhook/schedule volume becomes large.
