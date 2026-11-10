# Plan 002: `image.sweep` no longer blocks every image build and restic job behind one global lock

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/runtime/images.go apps/backend/internal/operations/image_sweep.go apps/backend/internal/operations/image_sweep_test.go`
> If any of these changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: MED (lock change; small but concurrency-sensitive)
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

`image.sweep` removes superseded runtime builds of **one** runtime key and is classified as a parallel operation that
claims only that key. But it removes images through `ImageManager.Remove`, which takes a stack-wide write lock
(`m.prune.Lock()`). Every image build holds the read side of that lock for the whole build (up to 2 hours), and every
restic job takes it briefly. Go's `sync.RWMutex` blocks new readers once a writer is waiting. So a sweep of key A that
overlaps a build of key B waits for B's build, and while it waits every restic backup, every other build and every app
operation that needs a build hangs in a plain mutex wait that forced cancellation cannot interrupt. They fill the
executor's slots (default 4), unrelated operations queue, and scheduled backups are recorded as skipped. After this
plan the sweep excludes only work on its own key.

## Current state

- `apps/backend/internal/runtime/images.go` — `ImageManager`.
  - Field (near line 218): `prune sync.RWMutex // Hold holds it shared, Remove exclusively`
  - `tagLock(tag string) *sync.Mutex` (near line 234) returns a named mutex from a map.
  - `Hold` (near line 251):

    ```go
    func (m *ImageManager) Hold(key domain.ImageKey) (release func()) {
        m.prune.RLock()
        l := m.tagLock("key:" + key.String())
        l.Lock()
        return func() {
            l.Unlock()
            m.prune.RUnlock()
        }
    }
    ```
  - `Remove` (near line 387):

    ```go
    func (m *ImageManager) Remove(ctx context.Context, id string) (docker.ImageSummary, error) {
        m.prune.Lock()
        defer m.prune.Unlock()
        containers, err := m.Engine.List(ctx, nil)
        …
        images, err := m.Engine.ListImages(ctx)
        …
        img, ok := PrunableImage(containers, images, id, m.Names.StackID)
        if !ok {
            return img, ErrImageNotPrunable
        }
        return img, m.Engine.RemoveImage(ctx, img.ID, img.Tags)
    }
    ```
  - `EnsureRestic` (near line 456) takes `m.prune.RLock()`.
- `apps/backend/internal/operations/image_sweep.go` — `handleImageSweep` (near line 138). `key := r.Op.TargetID` is the
  runtime key as a **string** (the same text `domain.ImageKey.String()` produces, e.g. `php-8.4`). It calls
  `c.Images.Remove(ctx, id)` in a loop (near line 161).
- `apps/backend/internal/operations/images.go:41` — `handleImagePrune` (the operator's manual image delete, a *global*
  operation that runs alone) also calls `c.Images.Remove`. It must keep the global lock.
- `apps/backend/internal/operations/claims.go` — `KindImageSweep` claims `excl: imageClaim(op.TargetID)`: it never runs
  together with `image.prepare`/`image.rebuild`/`image.rollout` of the same key, but app operations that call
  `Hold(key)` are not excluded by claims, which is why a per-key lock is still required.
- Tests: `apps/backend/internal/operations/image_sweep_test.go` has `TestSweepRemovesBuildsReleasedForTheGracePeriod`
  and helpers `h.runningApp(slug)`, `h.planned(app)`, `h.rebuild(key)`, `h.current(key)`, `h.rollTo(appID, key, buildID)`,
  `h.mark()`. Harness tests need root.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/operations/ ./internal/runtime/ -run 'Sweep|Image|Remove'` | `ok` (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/runtime/images.go`
- `apps/backend/internal/operations/image_sweep.go`
- `apps/backend/internal/operations/image_sweep_test.go`
- `apps/backend/docs/architecture.md` (only if it describes the lock; see step 4)

**Out of scope**:
- `apps/backend/internal/operations/images.go` (`handleImagePrune`) — keeps calling `Remove` with the global lock.
- `apps/backend/internal/operations/claims.go` — do not change the sweep's claims.
- `Hold` and `EnsureRestic` — unchanged.

## Git workflow

- Branch: `advisor/002-image-sweep-per-key-lock`
- One commit, plain imperative sentence, e.g. `Let an image sweep wait only for builds of its own runtime`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Split `Remove` into a locked wrapper and an unlocked body

In `images.go`, move the body of `Remove` after the lock lines into a new unexported method
`removeLocked(ctx context.Context, id string) (docker.ImageSummary, error)`. `Remove` becomes:

```go
func (m *ImageManager) Remove(ctx context.Context, id string) (docker.ImageSummary, error) {
    m.prune.Lock()
    defer m.prune.Unlock()
    return m.removeLocked(ctx, id)
}
```

**Verify**: `cd apps/backend && go vet ./internal/runtime/` → exit 0.

### Step 2: Add `RemoveForKey`

```go
// RemoveForKey is Remove for a superseded build of one runtime key (the
// String form of a domain.ImageKey). It excludes only Hold of that key, so a
// sweep never waits for a build of another runtime, and a waiting sweep never
// stalls restic jobs or other builds behind the stack-wide lock. The Engine
// still refuses to remove an image a container references.
func (m *ImageManager) RemoveForKey(ctx context.Context, key, id string) (docker.ImageSummary, error) {
    l := m.tagLock("key:" + key)
    l.Lock()
    defer l.Unlock()
    return m.removeLocked(ctx, id)
}
```

Update the comment on the `prune` field to: `// Hold holds it shared, Remove exclusively; RemoveForKey takes only the key lock`.

Lock-order check (state this in the commit body): `Hold` takes `prune.RLock` then the key lock; `RemoveForKey` takes
only the key lock; `Remove` takes only `prune.Lock`. No path takes the key lock and then `prune`, so no deadlock.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/runtime/` → no output, exit 0.

### Step 3: Use it in the sweep

In `image_sweep.go`, in `handleImageSweep`, change `c.Images.Remove(ctx, id)` to `c.Images.RemoveForKey(ctx, key, id)`.
Nothing else in the handler changes.

**Verify**: `grep -n "Images.Remove" apps/backend/internal/operations/*.go | grep -v _test` → exactly one `Images.Remove(`
(in `images.go`) and one `Images.RemoveForKey(` (in `image_sweep.go`).

### Step 4: Tests and doc

Add to `image_sweep_test.go`:

`TestSweepDoesNotWaitForAnotherRuntimesBuild` — reuse the setup of `TestSweepRemovesBuildsReleasedForTheGracePeriod`
up to the point where a build is due for removal, then:
1. Take a hold on a *different* key and keep it for the test:
   `release := h.c.Images.Hold(domain.ImageKey{Kind: alpha.Runtime.ImageKey().Kind, Toolchain: "other", Version: "0"})`
   and `defer release()`. (The key only has to differ; it is never built.)
2. Advance the clock past `BuildReleaseGrace`, submit the sweep the same way the existing test does, and `h.wait(op)`.
   `h.wait` fails the test after 10 s, so a sweep that blocks on the held key fails here.
3. Assert the operation succeeded and the due image is gone (same assertion as the existing test).

`TestSweepWaitsForItsOwnKey` — same setup, but hold `alpha.Runtime.ImageKey()` itself, submit the sweep, sleep 200 ms,
assert the operation is **not** terminal (`store.GetOperation`), then `release()` and `h.wait(op)` succeeds.

Then run `grep -n "prune" apps/backend/docs/architecture.md | grep -i "lock\|sweep"`; if the architecture document
describes the sweep taking the image lock, correct that sentence to say it takes only its key's lock. If there is no
such sentence, change nothing.

**Verify**: narrow test command → `ok`, both new tests PASS with `-v` (not SKIP).

## Test plan

- Two new harness tests as above, modelled on `TestSweepRemovesBuildsReleasedForTheGracePeriod`.
- The existing sweep and image tests must keep passing unchanged.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] Both new tests PASS as root (not SKIP)
- [ ] `sudo … make -C apps/backend ci` exits 0
- [ ] `grep -n "m.prune.Lock()" apps/backend/internal/runtime/images.go` → one match, inside `Remove`
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match (for example `Remove` has gained other callers:
  `grep -rn "Images.Remove(" apps/backend/internal --include=*.go`).
- The existing sweep test cannot be adapted because the sweep is submitted in a way you cannot find — report what
  you found instead of inventing a submission path.
- Any test deadlocks (a test hanging until the Go test timeout). Report the goroutine dump.
- Go or root is unavailable: report "written but not run".

## Maintenance notes

- `PrunableImage` still decides from a full container list, so an image in use by any container is never removed;
  the per-key lock only replaces the exclusion against concurrent `Hold` of the same key.
- If shared build images (`bento-build/valkey`, `bento-backup/restic`) are ever added to the sweep (an open backlog
  item), they are not keyed by runtime: use `Remove` (global lock) for those, from a global operation.
- Reviewer: confirm no code path acquires the key lock before `prune`.
