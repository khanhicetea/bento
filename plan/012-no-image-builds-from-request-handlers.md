# Plan 012: Opening a terminal, exec or file view never starts an image build inside an HTTP request

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/operations/tools.go apps/backend/internal/operations/runtime_images.go apps/backend/internal/operations/git.go`
> If any changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S–M
- **Risk**: MED (behaviour change for operators right after an upgrade; see "Why")
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

Rule 1 of `apps/backend/AGENTS.md`: "Handlers never cause effects. API handlers … validate, persist intent, and
Submit an operation … Only operation handlers touch Docker or the filesystem." Tool containers for the browser
terminal, one-off exec and the file browser are a documented exception — but the helper they use also **builds the
app's runtime image** when no usable build exists, and writes the app's build assignment to the database. After an
upgrade that changes image inputs, the first exec, file read or terminal on an app therefore starts a 5–25 minute
image build inside an HTTP request: outside the operation journal, outside the reconciler's backoff, without the
app's claim, and — for exec, whose request is capped at 10 minutes — aborted halfway, wasting a build slot. The
reconciler already prepares images through a proper operation (`image.prepare`). After this plan, request-path tool
sessions only *use* an existing build; when none is usable they fail fast with a clear message. The deploy
operation keeps the building behaviour, because it is an operation.

## Current state

- `apps/backend/internal/operations/tools.go`:

  ```go
  // OpenTool starts a tooling container: same image and identity, home and
  // bound data only, no daemons, no persistent-instance lock, no backups. It
  // runs outside operations (terminals, exec, file access), so it never writes
  // the app's generated config: …
  func (c *Controller) OpenTool(ctx context.Context, app domain.App, lifetime time.Duration) (*ToolSession, error) {
      return c.openTool(ctx, app, lifetime, false)
  }

  // openTool is OpenTool; writeConfig materializes the app's generated config
  // first, which only an operation holding the app may do (deploy, …).
  func (c *Controller) openTool(ctx context.Context, app domain.App, lifetime time.Duration, writeConfig bool) (*ToolSession, error) {
      if !app.Provisioned {
          return nil, Fail("not-provisioned", "Wait for provisioning to finish.", "app %s is not provisioned", app.Slug)
      }
      if err := c.verifyHome(app); err != nil {
          return nil, err
      }
      if _, err := c.EnsureNetworks(ctx); err != nil {
          return nil, err
      }
      build, err := c.ensureAppBuild(ctx, app, nil)
      imageID := build.ImageID
      if err != nil {
          return nil, err
      }
      …
  ```
- Callers: `apps/backend/internal/api/terminal.go` (near line 271), `apps/backend/internal/api/appfiles.go` (near line
  134) and `apps/backend/internal/api/stream.go` (near line 220) call `OpenTool` from HTTP handlers.
  `apps/backend/internal/operations/git.go` (near line 241, inside the `app.deploy` operation) calls
  `c.openTool(ctx, app, deployToolLifetime, true)`.
- `apps/backend/internal/operations/runtime_images.go`:
  - `plannedBuild(ctx, app) (store.RuntimeBuild, runtime.ImageSpec, bool, error)` — "resolves the build app's
    instance should run, **without building or writing anything**: its assigned build when usable, otherwise the
    current build of its runtime. ok is false when neither exists."
  - `ensureAppBuild(ctx, app, progress)` — calls `plannedBuild`; when `!ok` it builds (`ensureCurrentBuild` →
    `Images.Build`, up to 2 h) and then writes the assignment with `store.ReplaceAppRuntimeBuild`.
- The reconciler submits `image.prepare` operations for runtime keys that need a build (`prepareImage` in
  `apps/backend/internal/reconcile/reconcile.go`), so a missing build is repaired without any request.
- Error convention: expected failures return `Fail(code, guidance, format, args…)` — code in kebab-case, guidance a
  sentence telling the operator what to do (see the `not-provisioned` example above).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/operations/ ./internal/api/ -run 'Tool|Exec|Terminal|Deploy|AppFile'` | `ok` (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/operations/tools.go`
- `apps/backend/internal/operations/tools_test.go`
- `apps/backend/docs/architecture.md` (one sentence where tool containers are described, step 3)

**Out of scope**:
- `ensureAppBuild`, `plannedBuild`, `ensureCurrentBuild` — unchanged.
- The three API callers — they keep calling `OpenTool`.
- `git.go` — deploy keeps `openTool(…, true)` and its building behaviour.

## Git workflow

- Branch: `advisor/012-no-image-builds-from-request-handlers`
- One commit, plain imperative sentence, e.g. `Do not build a runtime image when a request opens a tool container`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Split the build resolution by caller

In `openTool`, replace

```go
build, err := c.ensureAppBuild(ctx, app, nil)
imageID := build.ImageID
if err != nil {
    return nil, err
}
```

with

```go
var build store.RuntimeBuild
if writeConfig {
    // An operation holding the app (deploy): it may build and assign.
    b, err := c.ensureAppBuild(ctx, app, nil)
    if err != nil {
        return nil, err
    }
    build = b
} else {
    // A request (terminal, exec, files): use an existing build only. A build
    // belongs in an operation; the reconciler prepares missing images.
    b, _, ok, err := c.plannedBuild(ctx, app)
    if err != nil {
        return nil, err
    }
    if !ok {
        return nil, Fail(
            "image-not-ready",
            "The runtime image for this app is being prepared; watch Activity for image.prepare and try again, or start the app.",
            "no runtime image is built yet for app %s",
            app.Slug,
        )
    }
    build = b
}
imageID := build.ImageID
```

Keep everything after `imageID` unchanged. Update the doc comment of `OpenTool` by adding one sentence: "It never
builds an image or records a build assignment: without a usable build it fails with image-not-ready."

Using `writeConfig` as the discriminator is correct today because it is true exactly for the operation caller. If
you find a caller that passes `writeConfig == false` from inside an operation
(`grep -rn "openTool(\|OpenTool(" apps/backend/internal --include=*.go`), STOP.

**Verify**: `cd apps/backend && gofmt -l . && go vet ./...` → no output, exit 0.

### Step 2: Check how the API reports the new failure

Find how the API maps an operations `Fail` error returned by `OpenTool` — look at what happens to the existing
`not-provisioned` failure in `apps/backend/internal/api/stream.go` near the `OpenTool` call and in `writeError`
(search `apps/backend/internal/api` for `not-provisioned` and for the type `Fail` returns). The new `image-not-ready`
failure must reach the client as a 4xx with its message, the same way. If `not-provisioned` is mapped by code in a
switch or table, add `image-not-ready` beside it (this is the only reason to touch a file in `internal/api`; if you
must, add that file to your report as an in-scope extension and keep the edit to that one mapping).

**Verify**: you can state in the report which status code `image-not-ready` produces and where that is decided.

### Step 3: Tests and doc

In `tools_test.go` add harness tests (see `lifecycle_test.go` for `newHarness`, `h.createApp`; see
`runtime_images_test.go` for `h.runningApp(slug)` and `h.planned(app)`):

1. `TestOpenToolUsesExistingBuild` — `app := h.runningApp("shop")`; `tool, err := h.c.OpenTool(ctx, app, time.Minute)`
   succeeds; close the session the way other code does (look at `ToolSession`'s close method in `tools.go`).
2. `TestOpenToolRefusesWithoutABuild` — create and provision an app, then make its build unusable the way an upgrade
   does. Look in `runtime_images_test.go` / `image_sweep_test.go` for how tests simulate a missing or superseded
   build (for example deleting the image from the fake, or clearing the `runtime_builds` rows). Then:
   - `OpenTool` returns an error whose failure code is `image-not-ready` (inspect the error the same way existing
     tests check `Fail` codes, or via `errors.AsType`);
   - the fake engine recorded **no** image build during the call (look at how `h.fake` records calls — the `record`
     method in `apps/backend/internal/docker/fake.go` — and assert no entry starting with the build method's name
     was added);
   - the app's build assignment row is unchanged (`store.GetAppRuntimeBuild` before and after).

In `apps/backend/docs/architecture.md`, find where tool containers / `OpenTool` are described (search `OpenTool` or
"tooling container") and add one sentence stating that request-path tool sessions never build an image and fail with
`image-not-ready` until `image.prepare` (or an app start) has produced one.

**Verify**: narrow test command → `ok`; new tests PASS; existing deploy tests (`git_test.go`) still pass.

## Test plan

As step 3. Existing tests that open tools through the API (`api_test.go`) and deploy tests must still pass
unchanged; if one of them relied on `OpenTool` building an image, that is a STOP condition.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] New tests pass as root; `sudo … make -C apps/backend ci` exits 0
- [ ] `grep -n "ensureAppBuild" apps/backend/internal/operations/tools.go` → one match, inside the `writeConfig` branch
- [ ] No files outside the in-scope list are modified (except the single mapping line allowed by step 2)
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- An existing test fails because it expects a tool session to build an image.
- You cannot find a way to simulate "no usable build" in the harness after reading the two named test files.
- Go or root is unavailable: report "written but not run".

## Maintenance notes

- Operator-visible change: right after an upgrade that changes image inputs, the terminal/exec/file browser of an
  app answers `image-not-ready` until the reconciler's `image.prepare` finishes (minutes on small hosts), instead of
  silently building. Mention this in the change summary and in `apps/backend/docs/evidence.md` if you ran it.
- The web UI shows the API error message as is; if operators find the message unclear, improve the guidance text,
  not the behaviour.
- Reviewer: confirm `store.ReplaceAppRuntimeBuild` is no longer reachable from any HTTP handler.
