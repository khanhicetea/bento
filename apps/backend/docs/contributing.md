# Contributing to the backend

Read [architecture.md](architecture.md) first. Repository-wide rules are in [`AGENTS.md`](../../../AGENTS.md).

## Development loop

```bash
cd apps/backend
gofmt -w . && go vet ./...
sudo go test ./...                         # root: some tests create app-owned directories
sudo go test -race ./internal/operations/  # narrow while iterating
sudo make test-integration                 # real Docker, before handing off lifecycle/runtime changes
```

Tests that need root call `t.Skip` otherwise; do not treat skipped tests as passing. Real Docker tests only run with
`BENTO_DOCKER_TESTS=1`, create a random stack name, use `t.TempDir()` roots, and remove only resources labeled with their
own stack id. Never point tests at an operator stack.

For manual end-to-end checks, build once and run a disposable stack:

```bash
go build -o /tmp/bento ./cmd/bento
sudo /tmp/bento --stack /tmp/s1 init --name s1 && sudo /tmp/bento --stack /tmp/s1 serve --listen 127.0.0.1:17780
```

## Where a change belongs

| You want to… | Change |
| --- | --- |
| Add or change an API field | `internal/api/dto` → handler/converter in `internal/api` → `make generate-types` → frontend |
| Add validation | `internal/domain/validate.go` (pure) or the acceptance function in `operations/accept.go` |
| Change what a mutation does to Docker/files | an operation handler in `internal/operations` |
| Change container shape | `internal/runtime/spec.go` (bump `specVersion`) |
| Change generated app config | `templates/config/*.tmpl` + `internal/runtime/materialize.go` |
| Change the runtime image | `templates/images/**` (the inputs hash changes; apps get a new build and roll out automatically) |
| Add a Docker capability | `docker.Engine` + `SDK` + `Fake` together |
| Change persistence | `internal/store`: bump `SchemaVersion`, add `migrations[old]` (refuse rows it cannot map), extend `schema` for new stacks, and test that a file at the old version opens with its data migrated |

## Recipe: add an API endpoint that mutates something

1. **DTOs** in `internal/api/dto/dto.go`: explicit request/response structs with camelCase JSON tags. Use pointers for
   optional request fields, `omitempty` only for genuinely optional output, and string or small-int types only.
   Enum constants must be prefixed with their type name (`type Foo string; const FooBar Foo = "bar"`) or tygo will not
   emit a union. Embedded structs need `` `tstype:",extends"` ``.
2. **Acceptance** in `internal/operations/accept.go`: validate (return `domain.ValidationErrors` for field errors,
   wrap `store.ErrNotFound`/`store.ErrConflict`/`ErrPrecondition`/`ErrConfirmation` for the rest), then call
   `c.Submit` with a `Mutate` callback that persists intent in the same transaction. Enforce any destructive
   confirmation phrase here, not only in the UI/CLI.
3. **Handler** in `internal/api/server.go`: register the route with `api("POST /api/v1/…", h)` (wraps auth + CSRF),
   `decode` the body strictly, read `idempotencyKey(r)`, call the acceptance function, respond with `s.accepted`.
4. **Types:** `make generate-types`, then use the new types in `apps/web/src/api/client.ts` and a feature hook.
5. **Tests:** at least one adversarial API test in `internal/api/api_test.go` (bad input, missing CSRF if relevant)
   and a handler test on the fake engine.
6. **CLI** (optional): a subcommand in `internal/cli/commands.go` using `Client.Mutate` (it adds an idempotency key
   and waits for the operation).

## Recipe: add an operation kind

1. Add a `Kind…` constant in `operations/apps.go` and register a handler in `registerHandlers`.
2. Write the handler as `func (c *Controller) handleX(ctx context.Context, r *Run) (any, error)`:
   - re-load state from the store (intent may have changed since acceptance);
   - call `r.Phase(ctx, "name")` before each external effect — it records progress and is where cancellation happens;
   - verify ownership (`Names.OwnedBy`, `verifyOwnedInstance`) before stopping or removing anything;
   - return `Fail(code, guidance, format, …)` for expected failures, with guidance that tells the operator what state
     things were left in and what to do next;
   - make it safe to run again after a partial failure (idempotent creates, "already done" checks).
3. Decide whether it is safe for the reconciler to trigger; if so, extend `reconcile.Pass`.
4. Add fault-injection coverage with `docker.Fake.FailOn` (see `TestFaultInjectionEachBoundaryThenRecover`).
5. Leave the kind out of `claimsFor` (`operations/claims.go`) unless it is worth running in parallel: an unlisted kind
   is *global* and runs alone, which is always safe. To make it parallel, list the exclusive and shared claims it
   needs, make sure every shared resource it touches is behind a lock (see the parallelism section of
   `architecture.md`), extend `TestOnlyReviewedKindsRunInParallel`, and add a test that it overlaps with unrelated
   work and not with related work (`parallel_test.go`).

Never put Docker calls inside `Store.Tx`, and never perform effects in an HTTP handler.

## Recipe: add a toolchain or runtime version

1. Add the version to `domain.PHPVersions` (its floating base tag, such as `php:8.6-fpm-trixie`, plus a floor in
   `domain.PHPMinVersions`) or to `domain.HTTPToolchains` (the mise tool requests, such as `node@26 pnpm@latest`,
   installed on `DebianBase`). A PHP version upstream no longer releases is instead built from deb.sury.org
   packages: set its base to `DebianBase` and add its oldest acceptable package revision to `domain.PHPSuryPackages`
   (check the Sury `Packages` index for every architecture first); the image build refuses an older package.
2. If the image needs extra packages, change `templates/images/<kind>/Dockerfile`. Keep it compatible with the classic
   builder: no `RUN --mount`, no heredocs, no `TARGETARCH` (use `dpkg --print-architecture`).
3. `BENTO_DOCKER_TESTS=1 go test ./internal/runtime -run TestIntegrationBuildRuntimeImages` builds the listed keys;
   add yours to that test while verifying, then start a real app on it.
4. Record what you actually ran in `docs/evidence.md`.

Floating inputs need no Bento change: base tags, mise tool versions, Composer (`latest-2.x`) and phpredis (newest PECL
release) resolve when an image is built, and an operator rebuild picks up new releases. Raise `domain.PHPMinVersions`
to force a fixed PHP release: the floor is a build input, so every app on that version is rebuilt and the build
refuses an older base.

Pinned artifacts (s6-overlay, minicrond, mise) live in `domain.RuntimeArtifacts` with SHA-256 checksums for amd64 and
arm64; update both checksums together (for mise, from the release's `SHASUMS256.txt`, choosing a release at least two
weeks old). After a minicrond bump, run `make vendor-minicrond-ui` to vendor that release's web UI into
`internal/schedulerui` (the scheduler gateway serves it; `TestVendoredVersionMatchesPin` fails until you do). Valkey
is pinned there too, as one source tarball (`VALKEY_VERSION`, `VALKEY_SHA256`, the same for every architecture) that
`templates/images/common/build-valkey.sh` compiles once, on `domain.ValkeyBuildBase`, for every runtime image. That
base must be the oldest Debian release among the runtime bases: a new base on an older release needs
`ValkeyBuildBase` moved to it (each final stage runs `valkey-server --version`, so a mismatch fails the build).
Toolchains are installed by `templates/images/common/build-toolchain.sh` in a `toolchain` stage that every
Dockerfile shares; a tool that links against a new Debian library gets it automatically (the stage records the
packages its binaries need).

`images/php` and `images/php-sury` start their final stage with the same steps (`php/install-tools.sh`, toolchain,
s6, Valkey) so PHP versions on one Debian release share those layers; put PHP-specific steps after them.
`TestRuntimeImagesShareLayers` fails if the shared steps drift apart, and `TestPlanArgsMatchDockerfileARGs` fails if a
plan's build args and a Dockerfile's `ARG`s differ.

## Changing the container spec

Anything that changes `AppContainerSpec` output for existing apps must bump `specVersion` in `runtime/spec.go`.
Running apps will then be replaced on their next start/update or by the reconciler — one at a time, old instance stopped
first. Say so in the change description. Keep `TestPersistentSpecSecurityInvariants` passing; extend it for new
invariants.

Secret values must never influence the fingerprint. If new secret material is mounted, bump
`App.CredentialsGeneration` when it changes.

## Frontend contract

`apps/web/src/api/generated/types.ts` is generator output: never edit it, and it is excluded from Prettier. CI fails if
`make generate-types` changes it. `api.TestWireFidelity` checks that DTO JSON keys exist in the generated file and that
embedded structs flatten; keep non-optional slices non-nil (`nonNil`) so they never serialize as `null`.

## Debugging

- `bento ops`, `bento op <id>` — phases, events, error code, guidance.
- `bento app show <slug>` — intent, observed state, reconcile budget, ingress ownership.
- Backend logs go to stderr (`journalctl -u bento@<stack>`).
- Inspect generated config under `apps/<appId>/config/` and edge generations under `edge/conf/{live,previous}`.
- Containers carry `io.bento.*` labels: `docker ps --filter label=io.bento.stack-id=<id>`.
- Exec into an app as its identity: `bento app shell <slug> --running`. Readiness by hand: run `bento-ready` there.

## Before handing off

```bash
make fmt-check vet && sudo make test-race && make check-generated && sudo make test-integration
```

Report exactly which of these ran, and never claim Docker, arm64, tunnel, or ACME behavior you did not execute.
