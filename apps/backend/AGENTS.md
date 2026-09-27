# AGENTS.md — Go backend

Rules for coding agents working in `apps/backend`. They extend the root `AGENTS.md`. Read
[docs/architecture.md](docs/architecture.md) before non-trivial changes and follow the recipes in
[docs/contributing.md](docs/contributing.md).

## What this is

One static Go binary, `bento`:

- `bento serve` — the resident backend for one stack root: SQLite intent (`bento.db`), a single operation executor, a
  reconciler, REST API + embedded UI on a loopback TCP port, and a root-only Unix control socket for the CLI.
- `bento init` / `bento import` — offline commands that take the same stack lock.
- Every other subcommand is a thin client of the running backend over `run/bento.sock`.

The backend drives Docker Engine through the SDK only. It never shells out to the `docker` CLI.

## Commands

```bash
gofmt -w . && go vet ./...
sudo go test ./...                        # root: tests create app-owned directories; non-root silently skips them
sudo go test -race ./internal/<pkg>/      # narrow while iterating
make generate-types                       # after any change in internal/api/dto
sudo make test-integration                # real Docker (BENTO_DOCKER_TESTS=1), disposable roots
make ci                                   # fmt-check, vet, race tests, generated-type drift, build
make release                              # embeds apps/web, static linux/amd64 + linux/arm64 into ../../dist
```

A skipped test is not a pass. Say which tests ran as root and whether Docker tests ran.

## Where things go

| Change | Location |
| --- | --- |
| Wire contract (request/response shapes) | `internal/api/dto` → `make generate-types` → commit `apps/web/src/api/generated/types.ts` |
| HTTP routes, decoding, error mapping, auth | `internal/api` |
| Validation of a mutation, confirmations, intent | `internal/operations/accept.go` (pure checks in `internal/domain`) |
| Docker/filesystem effects | an operation handler in `internal/operations` |
| Drift repair | `internal/reconcile` |
| Container shape, labels, names, generated app config | `internal/runtime` (+ `templates/config`) |
| Runtime images | `templates/images/{common,php,http}` and pins in `internal/domain/catalog.go` |
| Edge Nginx | `internal/edge` + `templates/edge` + `internal/operations/edge.go` |
| MySQL/PostgreSQL/Redis | `internal/dataservices` |
| Persistence | `internal/store` |
| New Docker capability | `internal/docker`: `Engine` interface + `SDK` + `Fake`, all three together |

## Non-negotiable rules

1. **Handlers never cause effects.** API handlers and CLI commands validate, persist intent, and `Submit` an operation
   in one transaction. Only operation handlers touch Docker or the filesystem.
2. **No Docker call inside `Store.Tx`.** Read, release the transaction, then act.
3. **Call `r.Phase(ctx, "…")` before each external effect** in a handler. It records progress and is the only place
   cancellation is honored. Handlers must be safe to re-run after a partial failure.
4. **Verify ownership before destroying anything.** Check `Names.OwnedBy` (labels + role, and app id where relevant)
   and, for app containers, `verifyOwnedInstance`. Never act on a resource just because its name matches. Scope label
   queries by `io.bento.stack-id` — imported clones share app ids across stacks.
5. **Never create empty replacements for durable data.** Missing homes, SQLite directories, or initialized service
   volumes must fail (`durable-state-missing`, `volume-missing`). Only explicit service creation may create a volume.
   Never remove a data volume (the sole exception is a failing import cleaning up volumes it created itself).
6. **Secrets** never go in argv, labels, the generation fingerprint, container `Env`, logs, errors, or API responses.
   Use exec stdin, `--defaults-extra-file`, private files (`0400`/`0440`), or hashes. Redact app secrets in streamed
   output.
7. **App containers stay hardened:** `User uid:gid`, read-only root, `CapDrop: ALL`, `no-new-privileges`, no host
   ports, no Docker socket, only the app's own home/SQLite/config/identity mounts. `TestPersistentSpecSecurityInvariants`
   must keep passing.
8. **Exact confirmations are enforced server-side** in `accept.go` (`delete <slug>`, `delete`, `replace <db>`,
   `export`, `delete <proxy>`). Never loosen them or move them only to the UI/CLI.
9. **Unknown state is refused, not rewritten.** `store.CheckCompatible` must stay read-only. Schema changes bump
   `store.SchemaVersion` and add an explicit migration.
10. **Loopback only.** `cli.ValidateListen` must keep rejecting non-loopback addresses. Browser writes need session +
    exact Origin + CSRF token; the Unix socket needs a root or backend-uid peer.

## Gotchas learned the hard way

- **Container spec changes:** any change to `AppContainerSpec` output must bump `specVersion` in `runtime/spec.go`.
  Every running app is then replaced (one at a time) on its next start/update or by the reconciler — mention this in
  your change summary. Secret material must influence the fingerprint only via `App.CredentialsGeneration`.
- **s6 as non-root** needs `/run` as a uid-owned tmpfs **with `exec`** (Docker's tmpfs default is `noexec`) and user
  bundles under `/etc/s6-overlay/user-bundles.d`. Don't "harden" `/run` to `noexec`.
- **Image builds use the classic builder** (`build.BuilderV1`): no `RUN --mount`, no heredocs, no automatic
  `TARGETARCH` (use `dpkg --print-architecture`). Executable bits come from `assets.executable`, not the filesystem,
  because `embed.FS` drops modes — update that rule when adding scripts in new locations.
- **Changing anything under `templates/images`** or a pinned version changes the image tag, so all apps of that
  runtime are rebuilt and replaced. Upgrading a pinned artifact (s6-overlay, minicrond, Composer) means updating the
  version **and both amd64/arm64 SHA-256s** in `domain.RuntimeArtifacts`, then running
  `BENTO_DOCKER_TESTS=1 go test ./internal/runtime -run TestIntegrationBuildRuntimeImages`.
- **Data-service readiness must use TCP.** First-boot entrypoints run a socket-only temporary server; a socket probe
  reports ready too early.
- **tygo:** enum constants must be prefixed with their type name (`ObservedStateHealthy`) to become a union; embedded
  structs need `` `tstype:",extends"` ``; keep non-optional slices non-nil (`nonNil`) so they never serialize as `null`.
  Never hand-edit or reformat the generated file (it is excluded from Prettier).
- **Edge config** must use relative includes so candidate directories validate in place; app upstreams must stay
  variable-based (`set $bento_upstream …; proxy_pass $bento_upstream;`) so Docker DNS is re-resolved.
- **Scheduler relay** execs `/proc/self/exe internal-relay` as the app UID and `fchdir`s to an inherited directory fd;
  do not replace this with `setuid` in the backend or `docker exec` per request.
- **Scoped reloads** validate with the running process and must `Changes.Restore` the previous bytes on failure
  without sending a reload.
- **Readiness failures never trigger recreation**; only missing/stopped containers or a changed fingerprint do.

## Tests

- Unit tests with effects use `docker.Fake` (fault injection via `Fake.FailOn`) through the harness in
  `internal/operations/lifecycle_test.go` or `internal/testutil`. A fake is not runtime evidence.
- Real Docker tests live in `internal/integration` and `internal/runtime/*_integration_test.go`, gated by
  `BENTO_DOCKER_TESTS=1`. They must use `t.TempDir()` roots, random stack names, and clean up only resources labeled
  with their own stack id.
- Add adversarial API tests for new endpoints in `internal/api/api_test.go` (bad input, missing CSRF/origin).
- New operation kinds need fault-injection coverage at each external-effect boundary.

## Manual end-to-end checks

```bash
go build -o /tmp/bento ./cmd/bento
sudo /tmp/bento --stack /tmp/dev init --name dev
sudo setsid /tmp/bento --stack /tmp/dev serve --listen 127.0.0.1:17780 >/tmp/dev.log 2>&1 < /dev/null &
echo 'a long dev password' | sudo /tmp/bento --stack /tmp/dev auth set-password
```

- Use disposable roots only; never touch an operator stack.
- To stop it, match the process precisely (`pkill -f '^/tmp/bento --stack /tmp/dev serve'`); a loose `pkill -f`
  pattern also matches your own shell command.
- Clean up with label filters: `docker ps -aq --filter label=io.bento.stack-id=<id>`, likewise for networks and
  volumes.
- Debug with `bento ops`, `bento op <id>`, `bento app show <slug>`, generated files under `apps/<appId>/config/`, and
  edge generations under `edge/conf/{live,previous}`.

## Before handing off

Run `make ci` (as root) and, for anything touching runtime, Docker, edge, data, or transfer behavior,
`sudo make test-integration`. If you changed DTOs, commit the regenerated types. Record newly verified behavior — and
what you could not verify (arm64, tunnel, ACME, HTTP/3, rclone) — in `docs/evidence.md`.
