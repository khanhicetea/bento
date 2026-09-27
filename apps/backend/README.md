# Bento backend

The Bento control plane: one static Go binary that runs as a resident backend (`bento serve`), performs offline stack
commands (`init`, `import`), and acts as a thin CLI client for the running backend.

It manages Docker Engine directly through the SDK, keeps desired state in SQLite, exposes a
versioned REST API on loopback, and serves the embedded React UI from `apps/web`.

- How the code works: [docs/architecture.md](docs/architecture.md)
- How to change it: [docs/contributing.md](docs/contributing.md)
- What has been verified, and what has not: [docs/evidence.md](docs/evidence.md)
- Operator documentation: [`docs/`](../../docs) (Starlight site)

## Quick start

Requirements: Linux, Go 1.27 (`mise install` at the repo root), Docker Engine API ≥ 1.44, root.

```bash
cd apps/backend
go build -o /tmp/bento ./cmd/bento

sudo /tmp/bento --stack /tmp/dev-stack init --name dev --mysql 8.4
sudo /tmp/bento --stack /tmp/dev-stack serve --listen 127.0.0.1:7780 &
echo 'a long dev password' | sudo /tmp/bento --stack /tmp/dev-stack auth set-password
sudo /tmp/bento --stack /tmp/dev-stack status
```

Use a disposable stack root outside the repository. Without `make web`, the binary serves a placeholder page instead
of the UI; run `make web build` to embed the UI.

## Make targets

| Target | What it does |
| --- | --- |
| `make build` | `CGO_ENABLED=0` build into `../../dist/bento` |
| `make web` | Builds `apps/web` and copies it into `internal/webui/dist` for embedding |
| `make release` | `web` + static `linux/amd64` and `linux/arm64` binaries + `SHA256SUMS` |
| `make generate-types` | Regenerates `apps/web/src/api/generated/types.ts` from `internal/api/dto` (tygo) |
| `make check-generated` | Fails if regeneration changes the committed types |
| `make fmt-check vet` | `gofmt` and `go vet` |
| `make test` / `make test-race` | Unit and contract tests (some need root) |
| `make test-integration` | Real Docker tests with `BENTO_DOCKER_TESTS=1`, disposable roots only |
| `make ci` | `fmt-check vet test-race check-generated build` |

## Layout

```text
cmd/bento/            main: delegates to internal/cli
templates/            embedded, immutable assets (see templates.go)
  images/{common,php,http}/   runtime image build contexts (Dockerfile + rootfs)
  config/             per-app Nginx, FastCGI, PHP-FPM, minicrond templates
  edge/               edge Nginx main config and site templates
internal/
  api/                REST handlers, auth/sessions/CSRF, SSE, WebSocket terminal, scheduler gateway, SPA serving
  api/dto/            the wire contract (source for the generated TypeScript)
  assets/             embed FS, deterministic image build contexts, template rendering
  backup/             logical dump/publish/retention/upload and restore
  cli/                command parsing, `serve`, control-socket client
  dataservices/       MySQL/PostgreSQL/Redis containers, admin SQL, grants, Redis ACL
  docker/             Engine interface, SDK implementation, in-memory Fake for tests
  domain/             model, validation, runtime catalog and pinned images
  edge/               edge config rendering, generations (stage/validate/swap), boot certificate
  integration/        real-Docker acceptance tests (gated)
  operations/         controller, operation handlers, acceptance (intent) functions
  platform/           stack layout, symlink-safe filesystem helpers, locks, ids, host id checks
  reconcile/          event watch + periodic resync, retry budgets
  runtime/            names/labels, generated app config, container specs, fingerprint, image manager
  scheduler/          UID-matched minicrond relay (parent manager + child process)
  stack/              offline init and import
  store/              SQLite schema, repositories, operations journal, sessions
  testutil/           controller-over-Fake harness for packages above operations
  transfer/           export archives, safe extraction, manifest
  webui/              embedded UI build (or placeholder)
```

## Dependencies

Pinned in `go.mod`: `github.com/moby/moby/client` + `api` (Engine SDK), `modernc.org/sqlite` (pure-Go SQLite, keeps
builds CGO-free), `github.com/coder/websocket`, `golang.org/x/crypto` (argon2id), `golang.org/x/sys` (renameat2,
peer credentials), `golang.org/x/term`, `github.com/klauspost/compress` (zstd), `github.com/robfig/cron/v3`, and
`github.com/gzuidhof/tygo` as a `go tool`.
