# Plan 006: `app.prune`, the operation that permanently deletes retained data, is covered by tests

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/operations/apps.go apps/backend/internal/operations/accept.go`
> If either changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: LOW (tests only)
- **Depends on**: none
- **Category**: tests
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

Removing an app in Bento keeps its data ("retained data"). `app.prune` is the only operation that then deletes it for
good: it drops the app's databases and database users, removes its SQLite directories and its home directory, and
forgets its backup settings. At the planned commit no test references it — not the acceptance function, not the
handler, not the safety guards. A regression here deletes an operator's data irreversibly, and the repository's own
rule says new operation kinds need fault-injection coverage at each external-effect boundary. This plan adds
characterization tests; it changes no production code.

## Current state

- `apps/backend/internal/operations/accept.go`, `PruneRetired` (near line 439):

  ```go
  func (c *Controller) PruneRetired(ctx context.Context, appID, confirm, idem string) (store.Operation, error) {
      ret, err := store.GetRetired(ctx, c.Store.DB(), appID)
      if err != nil {
          return store.Operation{}, err
      }
      if ret.PrunedAt != "" {
          return store.Operation{}, fmt.Errorf("%w: already pruned", store.ErrConflict)
      }
      if confirm != "delete" {
          return store.Operation{}, fmt.Errorf("%w: type exactly \"delete\" …", ErrConfirmation)
      }
      op, _, err := c.Submit(ctx, Submission{Kind: KindAppPrune, TargetKind: "retired-app", TargetID: ret.AppID, …})
      return op, err
  }
  ```
- `apps/backend/internal/operations/apps.go`, `handlePrune` (near line 1255). Its guards, in order:
  1. `ret.PrunedAt != ""` → returns `{"alreadyPruned": true}` (idempotent re-run).
  2. An active app with the same slug exists → `Fail("slug-active", …)`.
  3. Phase `drop-databases`: for each retained relational artifact, `c.Data().DropBinding(…)`.
  4. Phase `remove-files`: removes each retained SQLite directory, then the home — **only if** the home's sidecar
     file (`c.Layout.HomeSidecar(slug)`, JSON `HomeSidecar{AppID}`) names the retired app's id; otherwise it warns
     "home … belongs to another incarnation; left in place" and keeps it.
  5. Phase `remove-backup-settings`, then `store.MarkPruned`.
  Result on success: `{"pruned": true, "uidReclaimed": false}`.
- Test harness, `apps/backend/internal/operations/lifecycle_test.go`: `newHarness(t)`, `h.createApp("shop")` (creates
  and provisions an app, returns `domain.App`), `h.wait(op)` (returns the finished `store.Operation`),
  `h.mustSucceed(op, err)`, `h.fake` (`*docker.Fake`, with `FailOn map[string]error` for one-shot fault injection —
  see how `TestFaultInjectionEachBoundaryThenRecover` in the same file builds the keys), `h.layout`
  (`platform.Layout`), `h.store`.
- Example of removing an app in a test (`remove_orphans_test.go`):
  `h.mustSucceed(h.c.RemoveApp(ctx, app.ID, "delete shop", ""))` — the confirmation is `delete <slug>`.
- Harness tests need root; non-root they `t.Skip`. A skip is not a pass.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/operations/ -run 'Prune' -v` | new tests PASS (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/operations/prune_test.go` (create)

**Out of scope**:
- Every non-test file. If a test reveals a bug in `handlePrune` or `PruneRetired`, do NOT fix it and do not weaken
  the assertion: describe the defect in your report and STOP.

## Git workflow

- Branch: `advisor/006-test-app-prune`
- One commit, plain imperative sentence, e.g. `Test pruning a removed app's retained data`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Learn the harness by reading, not guessing

Read `lifecycle_test.go` (whole file), `remove_orphans_test.go`, and `app_services_test.go` (it shows how a test
gives an app a relational binding and a data service on the fake engine). Read `handleRemove` in `apps.go` (just
above `handlePrune`) to see what "retained" artifacts a removal records, and find `store.GetRetired` and
`store.MarkPruned` in `apps/backend/internal/store/` to see the `Retired` struct's fields (`PrunedAt`, `Artifacts`).

**Verify**: you can name the helper that adds a relational binding in tests, or you have confirmed there is none.

### Step 2: Write the tests

Create `prune_test.go` in package `operations` with these tests. Each starts with `h := newHarness(t)` and
`ctx := t.Context()`, creates an app with `h.createApp("shop")`, and removes it with
`h.mustSucceed(h.c.RemoveApp(ctx, app.ID, "delete shop", ""))`.

1. `TestPruneRequiresExactConfirmation` — `h.c.PruneRetired(ctx, app.ID, "Delete", "")` and `…, "", "")` and
   `…, "delete shop", "")` each return an error for which `errors.Is(err, ErrConfirmation)` is true; the home directory
   `h.layout.AppHome("shop")` still exists afterwards.
2. `TestPruneDeletesRetainedHomeAndMarksPruned` — assert the home exists before; run
   `h.mustSucceed(h.c.PruneRetired(ctx, app.ID, "delete", ""))`; assert the home no
   longer exists (`os.Stat` returns `os.ErrNotExist`); `store.GetRetired` shows a non-empty `PrunedAt`.
3. `TestPruneTwiceIsRefused` — after a successful prune, a second `PruneRetired` returns an error with
   `errors.Is(err, store.ErrConflict)`.
4. `TestPruneKeepsAHomeOfAnotherIncarnation` — after removal, overwrite the sidecar file
   `h.layout.HomeSidecar("shop")` with JSON for a different app id (marshal a `HomeSidecar{AppID: "app_other"}`; keep
   any other fields the struct has at their zero values); prune; assert the operation succeeded, the home directory
   **still exists**, and one of the operation's events contains `belongs to another incarnation` (find how other
   tests read events — `store.ListEvents` or similar; `transfer_test.go`'s `h.phases(id)` shows reading events).
5. `TestPruneRefusedWhileSlugIsActive` — after removal, create a new app with the same slug
   (`h.createApp("shop")`). If the harness refuses to create it because the old home is still present, skip this
   test with `t.Skip` and a message saying exactly what refused, and note it in your report. Otherwise prune the
   **old** app id and assert the operation finished `store.OpFailed` with `ErrorCode == "slug-active"`, and the
   new app's home still exists.
6. `TestPruneDropFailureLeavesFilesAndCanBeRetried` — only if step 1 found a way to give the app a relational
   binding in tests. Set up the app with one MySQL or PostgreSQL binding, remove the app, then make the fake fail the
   database drop: inject a one-shot failure for the `Exec` on the service container with `h.fake.FailOn` (copy the
   key format from an existing test that fails an `Exec`), prune, and assert: the operation failed, the home still
   exists (files are removed only after databases), `PrunedAt` is still empty. Then prune again (the injected
   failure is one-shot) and assert success and that the home is gone. If no binding helper exists, leave this test
   out and say so in your report.

**Verify**: narrow test command → each written test reports PASS (or the documented SKIP for test 5).

### Step 3: Full gate

**Verify**: `sudo … make -C apps/backend ci` → exit 0.

## Test plan

This plan is the test plan. Pattern files: `remove_orphans_test.go` (removal), `lifecycle_test.go`
(`TestFaultInjectionEachBoundaryThenRecover` for `FailOn`), `app_services_test.go` (bindings).

## Done criteria

- [ ] `apps/backend/internal/operations/prune_test.go` exists with at least tests 1–4
- [ ] `sudo … go test -race ./internal/operations/ -run 'Prune' -v` shows them PASS as root (not SKIP, except the
      documented case in test 5)
- [ ] `git status --porcelain` shows only `prune_test.go` (plus `plan/README.md`)
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] `plan/README.md` status row updated

## STOP conditions

- A test written as specified fails because production behaviour differs from "Current state" (for example the home
  is deleted despite a foreign sidecar). That is a real defect: report it with the failing output; do not change
  production code and do not weaken the assertion.
- "Current state" excerpts do not match.
- Go or root is unavailable: report "written but not run" — never report skipped tests as passing.

## Maintenance notes

- These tests pin the safety guards of the only destructive data path. Any change to `handlePrune` should extend
  them rather than relax them.
- Not covered here: pruning against real Docker (an integration case in
  `apps/backend/internal/integration/lifecycle_integration_test.go` would be the next step), and many other
  operation kinds that have no fault-injection test (`app.restart`, `app.update`, `binding.add`, `database.add`,
  `service.restart`, `image.prune`, `restic.recover`, `restic.unlock`, `restic.check`, `restic.key-*`); see
  `plan/README.md`.
