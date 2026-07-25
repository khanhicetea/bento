# Bento Web API and Web UI implementation plan

## 1. Goal

Add a long-running, host-local Bento server that exposes the existing control-plane capabilities through:

1. a versioned JSON HTTP API; and
2. a simple responsive React UI with plain CSS.

The API and UI must preserve the CLI's state model, locking, validation, secret handling, destructive confirmations, render/apply transaction, scoped reloads, and source/compiled parity. They are additional command adapters, not a second implementation of Bento.

This work deliberately changes the current baseline: the statements in `README.md`, `specs/01-product-spec.md`, `specs/02-system-architecture.md`, `specs/03-reimplementation-contract.md`, `specs/todo.md`, and `tests/unit/phase_g_test.ts` that forbid a browser UI, management API, or resident Bento process must be revised. Bento remains single-host; this does not add multi-host orchestration or a remote state database.

## 2. Scope and defaults

### In scope

- New command: `bento serve`.
- One server process controls exactly one fixed `--stack` root.
- Default listener: `127.0.0.1:8080`; use an SSH tunnel for remote administration.
- Versioned routes under `/api/v1` and same-origin static UI routes.
- Typed, allowlisted command inputs; the API never accepts a shell command string.
- Asynchronous operations with status, finite read-only event streaming, bounded output, and artifact downloads.
- A redacted snapshot endpoint for UI navigation and common read views.
- API token authentication plus short-lived browser sessions.
- Source mode and compiled binaries both include and serve the same UI assets.

### Explicitly out of scope

- `tui` and host editor sessions.
- Interactive app or MySQL shells.
- A browser terminal, PTY, WebSocket console, stdin forwarding, or container attach.
- Attached GoAccess, `compose attach`, followed logs, `compose watch`, or foreground `compose up`.
- Multi-host management, user/role administration, OAuth, Kubernetes, or a new database.
- Arbitrary filesystem browsing or arbitrary process execution.

SSE operation events are output-only, bounded, and end when an operation ends; they are not an attached console.

## 3. Command coverage contract

Every CLI command must have a canonical command ID in one shared registry. Aliases such as `app remove`, `proxy remove`, `deploy history`, and `--test-stack` map to the same canonical handlers. A contract test must fail if the CLI surface and registry coverage list drift.

| CLI area | API/UI support |
|---|---|
| Core | `version`, `init`, `render`, `apply`, `status`, `doctor`, `support-bundle`, and `test-stack` |
| Apps | `list`, `show`, `create`, `update`, `enable`, `disable`, `delete/remove`, and two-step `prune` |
| PHP | `list`, `add`, and `remove` |
| MySQL | `list`, `add`, blocked `remove`, `db`, `size`, and `processlist` |
| Proxies | `list`, `create`, and `delete/remove` |
| Deploy | `enable`, `disable`, `rotate`, `status/history`, `drain`, and `instructions` |
| Cron | `list`, `add`, `edit`, `reload`, and `remove` |
| Workers | `list`, `add`, `remove`, `start`, `stop`, `restart`, `signal`, and `inspect` |
| Safety/data | `permissions check/repair`, `backup`, and `restore` |
| TLS/logs/templates | `tls set`, `tls ca export`, all non-attached access-log operations, and all template operations |
| Stack/host | `stack export/import` and `maintenance run/register/unregister` |
| Compose | `compose files` and safe, explicitly headless Compose argv |
| Exec | `exec` only with a non-empty argv, forced non-TTY execution, captured output, timeout, and output limit |

The following variants are rejected with a stable `INTERACTIVE_UNSUPPORTED` API error:

- `tui`;
- `app shell`;
- `mysql shell` (its safe `--print` plan may be exposed as a preview result);
- `exec` with no argv;
- `logs access report --attach`;
- `template select` without supplied source/content (the UI uses a textarea/upload instead of `$EDITOR`);
- Compose arguments that attach or require a TTY, including follow/watch/attach and foreground `up`.

`app prune` remains safe but is adapted for HTTP: first return a cleanup plan and opaque plan digest, then require the literal confirmation `delete` plus that digest. The server re-plans under the exclusive lock before deletion. Other destructive actions continue to require their existing exact confirmation text. The UI confirmation dialog is not a substitute for server-side checks.

## 4. Target architecture

```text
CLI/yargs adapter ---------\
                           -> typed command registry -> domain/application services -> Platform
HTTP/API adapter ----------/                |
                                            +-> operation events/results/artifacts

Browser -> same-origin HTTP server -> React static assets + /api/v1
```

Do not implement the API by calling `runCli`, constructing CLI token strings, monkey-patching `console`, or spawning the Bento binary. Concurrent requests make global stdout capture unsafe, and doing so would duplicate parsing and weaken type boundaries.

### Shared command layer

Create a transport-neutral application command layer. Each descriptor contains:

- stable command ID and description;
- zod input schema and serializable field metadata for the UI/capabilities endpoint;
- `read`, `mutate`, or `host-exclusive` execution class;
- timeout and output-size policy;
- destructive confirmation and secret-field metadata;
- CLI/API availability policy;
- handler returning typed data, messages, optional progress, and registered artifacts.

Suggested result shape:

```ts
type CommandResult<T> = {
  data: T;
  messages: Array<{ level: "info" | "success" | "warn"; message: string }>;
  artifacts?: Array<{ id: string; name: string; mediaType: string }>;
  sensitive?: boolean;
};
```

Move orchestration out of private `cmd*` functions in `src/commands/subcommands/` into this layer. Yargs remains responsible for CLI parsing and human formatting. The HTTP adapter validates JSON and emits JSON. Both adapters call the same handlers.

### Concurrency and operations

- Every HTTP command runs as an operation with `queued`, `running`, `succeeded`, `failed`, or `cancelled` state.
- State/host mutations are FIFO and serialized per stack. Read operations may use a small bounded pool and `StateStore.withShared` where safe.
- Audit every current mutator to ensure load/change/save/apply happens under `StateStore.withExclusive`; do not rely only on the HTTP queue.
- Support cancellation only while queued in the first release. Do not kill a running render, restore, import, or backup at an unsafe point.
- Keep operation records in a bounded in-memory store. A server restart does not resume jobs. Persist only a redacted append-only audit record, not command output or secrets.
- Add optional streaming callbacks to the process adapter. Enforce per-command timeouts and a bounded stdout/stderr tail; truncation is explicit in the result.
- Add `assertHeadlessComposeArgs` beside the existing destructive Compose safety check. Explicit app `exec` always uses Compose `-T` and never inherits server stdio.
- Require `Idempotency-Key` for mutating API requests; retries return the original operation ID.

## 5. HTTP API

### Endpoints

| Method/path | Purpose |
|---|---|
| `GET /healthz` | Minimal unauthenticated liveness; no stack details |
| `POST /api/v1/session` | Exchange the API token for a browser session and CSRF token |
| `DELETE /api/v1/session` | End browser session |
| `GET /api/v1/meta` | Bento/API versions and server capabilities |
| `GET /api/v1/snapshot` | Redacted desired/live summary for the UI; supports `ETag` |
| `GET /api/v1/commands` | Command IDs, availability, risks, and input field metadata |
| `POST /api/v1/operations` | Validate and enqueue `{ command, input }`; return `202` and `Location` |
| `GET /api/v1/operations` | Recent bounded operation list |
| `GET /api/v1/operations/:id` | Status, typed result, messages, and structured error |
| `GET /api/v1/operations/:id/events` | Authenticated output-only SSE stream |
| `DELETE /api/v1/operations/:id` | Cancel a queued operation only |
| `POST /api/v1/uploads` | Stream an authenticated temporary input file with size limits |
| `GET /api/v1/artifacts/:token` | Download only an artifact registered by an operation |

Example operation request:

```json
{
  "command": "app.create",
  "input": {
    "slug": "demo",
    "domain": "demo.example.test",
    "documentRoot": "public",
    "createDatabase": true,
    "apply": true
  }
}
```

Operation responses include request/operation IDs, timestamps, progress, result, redacted messages, and a structured error with Bento error code and recovery hint. Invalid authentication/body/command IDs fail before enqueue. Once accepted, execution failures are represented on the operation resource.

Map transport errors consistently: malformed input `400`, unauthenticated `401`, forbidden/CSRF `403`, not found `404`, stale/idempotency conflict `409`, unsupported interactive variant `422`, rate/queue limit `429`, and unexpected server failure `500`. Never send stack traces to clients.

### Files and artifacts

- Upload request bodies stream to mode-`0600` temporary files; never buffer backups in memory.
- Upload tokens are random, single-purpose, expire, and cannot be converted into arbitrary paths.
- Support uploads for restore/import, external certificates, and custom templates. Trusted API clients may also use validated host paths where the underlying command requires them.
- Support registered downloads for support bundles, CA exports, HTML access reports, backups, and stack exports.
- Artifact handlers use an exact server-side path captured from a successful operation, safe `Content-Disposition`, `nosniff`, expiry, and cleanup. There is no general `GET /files` endpoint.
- One-time secrets such as `deploy rotate` results are never placed in logs, audit records, SSE, or persisted operation history. They are retained only in memory for a short TTL and cleared after the first authenticated result read.

## 6. Authentication and HTTP security

- Authentication is mandatory even on loopback, except for `/healthz` and static hashed UI assets.
- Read a high-entropy bearer token from `--auth-token-file` or `BENTO_API_TOKEN_FILE`. If absent, generate a mode-`0600` sidecar file outside the stack root (for example `<stack-root>.api-token`) and print only its path.
- API clients use `Authorization: Bearer`. The UI exchanges that token for a random, short-lived, `HttpOnly`, `SameSite=Strict` cookie and receives a separate CSRF token kept in memory.
- Cookie-authenticated mutations require the CSRF header and a valid same-origin `Origin`. Bearer requests do not use cookies.
- Do not enable permissive CORS. Validate `Host`, set CSP, `frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`.
- Rate-limit failed authentication and cap body size, upload size, queued operations, concurrent reads, and SSE clients.
- Default to loopback. A non-loopback bind requires an explicit unsafe-network acknowledgement plus configured TLS certificate/key; documentation should prefer an SSH tunnel. Never silently expose management on `0.0.0.0`.
- Redact request logs and audit fields using descriptor-level secret metadata in addition to the existing generic redactor.

## 7. React UI

Use React and React DOM only; avoid a component framework and CSS-in-JS. Use semantic HTML, a small custom router, native forms, CSS variables, and responsive grid/flex layouts.

### Main views

1. **Login** — token exchange; token is not written to local storage.
2. **Overview** — stack health, status, pending reload/apply information, recent operations, and quick actions.
3. **Apps** — list/detail/create/update, enable/disable/remove/prune, databases, TLS, access logs, deploy, cron, workers, and noninteractive exec.
4. **Runtimes and data** — PHP/MySQL versions, sizes/process list, backup and restore.
5. **Routing and TLS** — proxies, TLS modes, private CA export, and reports.
6. **Operations** — apply preview/apply, doctor, permissions, maintenance, support bundle, test stack, stack export/import, progress, errors, and downloads.
7. **Advanced** — schema-driven forms for every remaining supported command, including safe headless Compose argv.

The UI must have no terminal emulator and no raw shell-string field. Commands are argv arrays rendered as repeatable fields. Common operations receive dedicated forms; the Advanced view guarantees full registry coverage.

Use explicit destructive dialogs that display impact and require exact text. Disable duplicate submits while an idempotent operation is queued/running. Show one-time secrets once with a copy button and warning. Poll operation status with exponential backoff; optionally use SSE while the operation page is open.

Accessibility baseline: keyboard navigation, visible focus, associated labels, status live regions, sufficient contrast, reduced-motion support, and usable layouts from narrow mobile width through desktop.

## 8. Build and distribution

Suggested files:

```text
src/application/commands/
  types.ts
  registry.ts
  core.ts app.ts php.ts mysql.ts ...
src/server/
  server.ts router.ts auth.ts operations.ts artifacts.ts static.ts errors.ts
src/commands/subcommands/serve.ts
web/
  index.html
  src/main.tsx
  src/api.ts
  src/views/
  styles.css
  dist/                 # generated release assets
tests/unit/server_*.ts
tests/contract/api_*.ts
tests/integration/api_*.ts
```

- Add pinned React/React DOM imports to `deno.json`/`deno.lock`.
- Use Deno 2.9's browser bundler (`deno bundle --platform=browser`) so no Node.js runtime or global npm install is required.
- Add `web:build`, `web:watch`, `serve`, and `test:web` tasks. `check`, `fmt`, and `lint` cover both server and web sources.
- Build hashed JS/CSS assets before compile. Include `web/dist` alongside `templates` in every compiled target.
- Serve `index.html` with `no-cache`, hashed assets with immutable caching, and SPA fallback only for non-API GET routes.
- The compiled server must run from any current directory with an explicit stack root and must not look for repository files at runtime.

## 9. Implementation phases

### Phase 1 — Contract and command extraction

1. Update the existing product/architecture/non-goal documents to authorize the host-local server.
2. Define command IDs, input/result schemas, execution classes, risk metadata, and the complete coverage matrix.
3. Introduce an event-sink logger/result abstraction independent of `console`.
4. Move one vertical slice (`status`, `app list/show/create`, `apply preview/apply`) into the shared command layer, preserving CLI output and exit codes.
5. Add CLI-vs-command-layer parity tests before extracting the remaining handlers.

### Phase 2 — Complete headless command coverage

1. Move every supported command handler into the shared layer.
2. Add non-TTY exec and Compose runners with timeout/output limits.
3. Split prompt-based prune into plan/confirm application operations.
4. Split template editing into prepare/read/update/select operations usable by a textarea/upload.
5. Add artifact and upload abstractions.
6. Add the registry coverage test and explicit rejection tests for every excluded variant.

### Phase 3 — Server core and security

1. Implement `Deno.serve`, routing, JSON parsing, error mapping, request IDs, and graceful `SIGINT`/`SIGTERM` shutdown.
2. Implement token/session authentication, CSRF/origin/host checks, security headers, and limits.
3. Implement the operation scheduler, idempotency, bounded event/result storage, queued cancellation, redacted audit log, and SSE.
4. Implement snapshot, capabilities, uploads, and artifact downloads.
5. Register `bento serve` without changing normal CLI startup behavior.

### Phase 4 — React UI

1. Add the build pipeline, static serving, login/session handling, API client, and operation monitor.
2. Build Overview and Apps first, then runtimes/data, routing/TLS, operations, and Advanced forms.
3. Add exact-confirmation dialogs, upload/download flows, one-time secret handling, and responsive/accessibility polish.
4. Keep all UI state derived from redacted API DTOs; never expose raw `state.json`.

### Phase 5 — Hardening, parity, and documentation

1. Exercise every command family through HTTP, including failure and destructive-confirmation paths.
2. Add source/compiled API and static-asset parity tests.
3. Add load/concurrency, interrupted-client, process timeout, output truncation, upload cleanup, and graceful shutdown tests.
4. Document startup, token rotation, SSH tunneling, TLS/non-loopback policy, API examples, backup before upgrades, and recovery.
5. Update CI to build the UI before native/cross compilation and to run API/web tests.

## 10. Test plan and acceptance criteria

### Unit tests

- Every request/input boundary starts as `unknown` and is zod-validated.
- Auth token mode checks, bearer/session expiry, CSRF, origin/host, rate and size limits.
- Operation transitions, queue serialization, shared/exclusive access, idempotency, queued cancellation, and truncation.
- Bento error-to-HTTP mapping and redaction, including nested command fields.
- Headless Compose/exec allow and deny cases.
- Artifact path containment, expiry, single-use uploads, safe names, and cleanup.
- React reducers/components for forms, confirmations, operation status, and one-time secret display.

### Contract tests

- Every canonical CLI command is either API-supported or has the documented interactive exclusion.
- Representative CLI and API calls produce equal typed state transitions, generated files, reload plans, and Bento error codes.
- Existing CLI output/exit-code tests remain green after extraction.
- API destructive operations cannot bypass current confirmation rules.
- API responses, audit logs, SSE, snapshot DTOs, and support artifacts do not leak MySQL, Redis, deploy, or API tokens.
- Static UI and API routing work in source and compiled modes from a different current directory.

### Integration tests

- Start on an ephemeral loopback port and execute at least one read and one mutation from every command family.
- Concurrent creates/applies cannot lose state or overlap render transactions.
- Disconnecting a browser does not cancel a running operation or leave locks held.
- Backup/report/support/export artifacts download correctly; restore/import/template/certificate uploads are streamed and cleaned.
- Noninteractive exec captures exit code/stdout/stderr; every TTY/attach path is rejected before process spawn.
- Browser smoke: login, overview, create app without apply, preview/apply, exact-confirm deletion, operation failure recovery, and logout.

### Definition of done

- `bento serve --stack <path>` serves the authenticated API and UI on loopback.
- All noninteractive command semantics listed in section 3 are reachable through the API and through either a dedicated or Advanced UI form.
- No browser terminal or attach path exists.
- Existing safety, state locking, reload scoping, secret guarantees, CLI behavior, and source/compiled parity remain intact.
- `deno task fmt:check`, `lint`, `check`, unit/contract/integration/web tests, native compile smoke, parity, and amd64/arm64 builds pass.
