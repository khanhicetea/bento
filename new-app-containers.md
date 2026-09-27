# Final implementation plan: Go control plane and per-app HTTP containers

Status: **approved target architecture; not implemented**. This is the implementation handoff for the Bento rewrite. Complete the phases and evidence gates below; do not treat this document as proof of runtime behavior.

## 1. Decisions and scope

The new Bento is an **always-running, single-host Go control plane** in `apps/backend`. It manages Docker Engine directly, persists desired state in SQLite, exposes a plain REST API, and serves the React control plane. A thin Go CLI talks to the backend for normal operations.

Decisions:

1. **Remove Docker Compose completely from the target implementation.** No generated Compose files, Compose execution, overlays, Compose export, or second orchestration implementation.
2. **Use the Go Docker Engine SDK.** Do not implement ordinary Docker operations by parsing CLI output or shelling out to `docker`.
3. **One persistent container per app.** Shared managed images, stable numeric app identity, narrow mounts, and app-scoped lifecycle.
4. **Private TCP HTTP is the app ingress contract.** No app host-port publication by default; Unix sockets remain appropriate for local FPM and private scheduler control.
5. **Local Nginx only for PHP-FPM apps.** HTTP-process apps serve HTTP directly; no compulsory Nginx, socat adapter, or custom HTTP proxy in those containers.
6. **Bridge networking by default.** Shared Bento edge Nginx is optional. Cloudflared may target apps directly. An external proxy may route to Bento-owned containers, but external-platform lifecycle integration is not a goal.
7. **No webhook deployment subsystem in this rewrite.** Do not implement a receiver, deploy queue, drain, HMAC secret, deploy hook orchestration, or reserved public deploy route.
8. **Plain REST, no oRPC.** Go API DTOs are the contract source; use **tygo** to generate frontend TypeScript types. Remove shared TypeScript API/domain contracts rather than maintaining two authorities.
9. **Keep React and TanStack React Query.** Bun remains the frontend/docs toolchain, not the backend runtime.
10. **No old-state migration or compatibility layer.** Reject unsupported development state without modifying it. An explicit reset means backing up/moving the old external root aside and initializing a new one, never deleting it silently.

Preserve the existing safety contracts unless explicitly revised here: operator-owned durable data, exact destructive confirmations, add-only database bindings, private secrets, bounded diagnostics, symlink-safe permissions, consistent backups, and source/release parity.

This plan deliberately revises `specs/01-product-spec.md`, `specs/02-system-architecture.md`, `specs/03-reimplementation-contract.md`, and the topology portions of `specs/06-minicrond-migration-plan.md`. Update baseline specs with working implementation, not ahead of it. Their stale schema numbers are not implementation evidence; inspect the actual state/version code before allocating new versions.

### Non-goals

- Multi-host scheduling, Kubernetes, replicas, autoscaling, or zero-downtime replacement.
- Hostile-tenant isolation, rootless-Docker support, or arbitrary privileged/custom images.
- Automatic Git/buildpack/deployment workflows or webhook compatibility.
- Dokploy/Coolify managing the same containers as Bento.
- A generic plugin framework, arbitrary Docker API passthrough, or automatically destructive cleanup.
- An internet-exposed management API without a separately reviewed remote-access design.

## 2. Target topology

```text
React browser UI / thin Go CLI
              |
              v
Go backend: REST + auth + SQLite + operation journal + reconciler
              |
              v
Docker Engine SDK
  |-- optional edge Nginx       bridge network; host 80/443 when selected
  |-- optional cloudflared     outbound tunnel; no host listener
  |-- app-<appId>              one persistent instance per app
  |-- shared MySQL/PostgreSQL/Redis
  `-- scoped ephemeral tooling / backup containers

Ingress alternatives:
  Internet -> Bento edge Nginx -----+
  Cloudflare -> cloudflared --------+--> app-<appId>:<httpPort>
  operator-owned external proxy ---+

PHP app container (app UID):
  s6 PID 1
    |-- Nginx :8080 -> static files / private local FPM socket
    |-- PHP-FPM, one app-owned pool
    `-- minicrond, app-owned jobs/workers/internal maintenance

HTTP app container (app UID):
  s6 PID 1
    |-- explicit HTTP argv on 0.0.0.0:<httpPort>
    `-- minicrond, app-owned jobs/workers/internal maintenance
```

The backend is not in the application request path. Existing containers, ingress, and minicrond continue operating if it stops. Backend downtime suspends reconciliation, management, and any backend-triggered operation; it does not stop apps or schedules.

## 3. Repository, backend, and API boundaries

### 3.1 Go layout and ownership

Establish a Go module under `apps/backend`. Suggested responsibility layout (small packages, not a mandatory framework):

```text
apps/backend/
  cmd/bento/                   serve, bootstrap/recovery, thin client commands
  internal/api/                REST handlers, DTOs, validation, auth, streaming
  internal/domain/             identities, desired state, invariants
  internal/operations/         operation planning/execution/recovery
  internal/reconcile/          desired-versus-observed reconciliation
  internal/store/              SQLite, transactions, migrations, journals
  internal/docker/             narrow Engine SDK adapter
  internal/platform/           filesystem, locks, clock, process/identity helpers
  internal/assets/             embedded immutable templates and build assets
  templates/                   authoritative runtime/ingress assets
```

Handlers and CLI commands delegate to the same domain operations. Neither owns a separate lifecycle implementation. Use narrow interfaces around effects; avoid a large generic Docker abstraction or direct SDK calls throughout the domain.

Run one backend per explicit external stack root, with an exclusive lifetime controller lock. Preserve explicit stack selection and stable stack identity; no global current-stack state. Hold the lock across recovery and shutdown. Offline init/migration/recovery commands require the backend to be stopped and acquire the same exclusion lock. The normal CLI cannot bypass the resident controller to mutate SQLite or Docker.

The supported initial backend deployment is host-local on Linux. Provide a service-manager unit and graceful shutdown behavior. Backend-in-container packaging is deferred unless its same-path host mounts and ownership requirements are independently proven; app containers must never receive the Docker socket.

Pin the Go toolchain, module dependencies, Docker SDK, tygo, and SQLite driver; commit `go.mod` and `go.sum`. Choose and document pure-Go versus CGO SQLite during phase 1, including actual Linux amd64/arm64 build implications. Do not promise static linking merely because the backend is Go.

### 3.2 REST contract and tygo

Use versioned resource-oriented routes under `/api/v1`. Define explicit Go request/response DTOs with JSON tags; do not expose persistence structs or Docker SDK structs as the public API.

Examples of the required surface, to finalize with fixtures in phase 1:

- applications, runtime definitions, domains/ingress, data services/bindings;
- application start/stop/restart and managed-route publish/unpublish operations;
- operations/status, logs, diagnostics, backup/restore/transfer;
- authenticated app-scoped scheduler gateway and terminal access.

Use normal HTTP methods/status codes, strict bounded JSON decoding, explicit field/relationship validation, stable error codes, and redacted messages. Reject unknown request fields, invalid enum values, trailing JSON, invalid identifiers, and out-of-range numbers. Runtime validation happens in Go; generated TypeScript types are not validation.

For long-running mutations, return `202 Accepted` with a durable operation ID and status URL. Record phase, target generation, outcome, and bounded/redacted failure information. A disconnected HTTP client does not implicitly cancel an accepted operation. Cancellation is explicit and honored only at safe boundaries. Define retry/idempotency behavior so a client timeout cannot duplicate destructive work; persist operation submission before claiming acceptance.

Use **tygo** to generate a committed file such as `apps/web/src/api/generated/types.ts` from exported API DTOs. Pin the generator, document one reproducible command, mark output generated, and fail CI if regeneration changes committed output. Tygo generates types, not REST routes, a transport client, validation, or an OpenAPI specification. Do not add a parallel schema/code-generation authority.

Prove the generator mapping with fixtures for JSON names, nested DTOs, pointers/nullability, omitted versus required fields, enum constants, timestamps, lists/maps, and runtime variants. Give timestamps an explicit wire representation. Avoid sending integers above JavaScript's safe range as JSON numbers; use strings for such counters/identifiers. If a Go construct does not map faithfully, simplify the DTO or add a tested generator configuration/override; never hand-edit generated output. Model runtime variants with a validated discriminator and test variant-specific constraints even if the generated type is less expressive than a TypeScript discriminated union.

Use a small handwritten `fetch` transport with domain-specific REST functions typed by generated DTOs. Handle HTTP errors, cancellation, credentials, and streaming centrally. Continue using React Query for remote state; use explicit domain query-key factories and narrow invalidation. No oRPC imports, oRPC query-key helpers, duplicated server state in React, or manually maintained mirror DTO package.

REST replaces control-plane RPC, not streaming transports. Use bounded SSE for suitable logs/operation events and authenticated WebSockets where bidirectional terminal behavior requires them. Specify reconnect, output limits, cancellation, origin checks, and exit status. Neither surface is an unrestricted Docker proxy.

### 3.3 Management security

Docker daemon access is effectively host-administrative access whether via SDK or CLI. Restrict the backend listener to loopback by default and reject non-loopback exposure in the initial release. Do not place the management API on the public app ingress network or automatically add a tunnel route to it.

Require configured operator authentication for browser management and scheduler access. Use expiring host-only HttpOnly sessions, appropriate cookie policy, logout/revocation, exact Origin/CSRF protection for writes, and no permissive CORS. Loopback is not authentication. Define a protected local CLI credential/transport that does not put credentials in argv. Secret bootstrap must use private files or safe input, never public responses or logs. Remote TLS/proxy trust is a separate reviewed extension, not a wildcard forwarded-header setting.

Authorize every scheduler asset/API/stream request to its selected app. Preserve the scheduler gateway protections in spec 06; do not pass minicrond credentials to JavaScript. The same-origin scheduler UI remains a trusted-content mode, not XSS isolation between apps and Bento.

## 4. State, identity, and resource ownership

### 4.1 Desired state versus observations

SQLite is the authority for desired state, app identities, allocation history, and operations. Docker inspection is the authority for observed container state. Labels identify managed resources; names are convenient display/routing conventions, not evidence sufficient for adoption or deletion.

Persist app intent along these lines:

```text
appId, slug, uid, gid, home
runtimeRef
runtime:
  php-fpm: document root, routing mode, pool capacity, HTTP port
  http-process: argv, workdir, HTTP port, readiness probe
resources: memory, CPU, PID limits
desiredRuntime: stopped | running
ingress: managed | external | none
managedPublication: unpublished | published   # only for managed ingress
data bindings and routing configuration
intended image/config generation
```

Observed runtime includes absent, starting, healthy, unhealthy, stopped, and failed/blocked information. Health is observed, not a persisted boolean granting publication. Desired publication can remain published through a crash; status must show unavailable or pending activation rather than rewriting operator intent.

Use a finite runtime union. PHP, Node.js, Bun, Python, or an explicitly supported native executable are toolchains/commands, not separate lifecycle engines. Start with curated images. A future custom-image provider needs a versioned non-root/supervision/filesystem contract; do not promise arbitrary images work.

Maintain domain uniqueness and add-only database binding behavior unless a later explicit product decision changes them. Domain claims alone do not prove an externally configured route exists or is disabled.

Version the new database baseline and serialized transfer format explicitly. Refuse old/future/unknown state without rewriting it. Do not open an old Bun database and reinterpret it as the new Go baseline.

### 4.2 Immutable identities and UID allocation

Allocate a random immutable `appId` per app incarnation. Slug is human-facing and `/home/<slug>` remains stable; app rename is out of scope. Updates preserve app ID and UID/GID; removing and recreating a slug creates a new incarnation.

Keep UID == GID from a validated configured range, initially starting at 10000. Persist a nondecreasing high-water mark and an allocation ledger, including retired allocations. Allocate transactionally; skip detected host user/group collisions and reserved values; fail clearly on exhaustion. Ordinary saves and prune must not erase history or reclaim IDs. Failed provisioning may burn an allocation.

The non-reuse guarantee is within one intact stack registry lineage, not across unrelated stacks, deleted registries, or old archive restores. Multiple stacks require non-overlapping configured ranges. Export/import includes the ledger; restoring old state requires preserving newer allocation history or selecting a new unused range before allocation resumes.

Refuse to adopt or recursively chown a retained `homes/<slug>` for a new incarnation. A home sidecar records stack ID/app ID/UID/GID for consistency, not as an unforgeable credential. Adoption/restore must be explicit and validated.

### 4.3 Docker ownership metadata

Use a central naming/label builder for containers, networks, and volumes. Disjoint names distinguish apps, infrastructure, and ephemeral work. For example:

```text
io.bento.managed=true
io.bento.stack-id=<stackId>
io.bento.app-id=<appId>         # app-owned resources only
io.bento.role=runtime|tool|edge|database|cache|tunnel|backup
io.bento.generation=<non-secret configuration fingerprint>
io.bento.operation-id=<id>     # operation-owned transient resources
```

Use Docker resource IDs for actions and stable network-scoped aliases such as `app-<appId>` for routing. Verify ownership, intended role, actual mounts/configuration, and current operation before destructive actions. Labels can be forged by anyone with Docker privileges; they are not authentication. Never adopt a name collision, destroy an unknown resource, or remove a volume merely because it is unused.

Keep secret values out of labels and fingerprints; do not expose a digest of a low-entropy secret as metadata. Track secret generations separately.

## 5. Direct Docker management and reconciliation

### 5.1 SDK responsibilities

Use the Engine SDK for image pulls, inspections, container creation/start/stop/removal, exec, logs, events, networks, volumes, and narrowly scoped ephemeral jobs. Choose a supported Engine/API range, enable compatible version negotiation, and test the minimum supported version.

Pin managed images/artifacts by immutable identity and record resolved image IDs/digests. Build reusable runtime images in the release pipeline where practical. If local managed-image builds are supported, implement and test an explicit SDK build path with deterministic contexts and bounded progress/errors; do not silently depend on Docker CLI/Compose/Buildx on the operator host.

Use cancellation/timeouts, bounded output, redaction, and backpressure. Never log full SDK payloads containing credentials. Define typed capability interfaces at the domain boundary and test fake adapters plus real Docker integration; a mock alone is not runtime evidence.

### 5.2 Reconciliation policy

The backend watches Docker events and periodically inspects managed resources. Events are hints, not a durable log; reconnect/full resync must recover missed events. Debounce triggers, serialize conflicting operations, use generation checks, and avoid holding a SQLite transaction open across Docker calls. Begin with conservative mutation serialization; optimize concurrency only where independence is proven.

- Docker restart policies handle ordinary container exits while desired runtime is running.
- Bento reconciles durable operator intent, missing resources, configuration generations, and route activation.
- An intentionally stopped app stays stopped through event resync, backend restart, and host reboot. Use/update the container restart policy consistently with stop intent.
- If an app container is manually deleted while desired runtime is running, recreate it with bounded retries **only after verifying required durable homes/data/volumes and identity**. Missing durable state blocks recovery; do not silently create an empty replacement.
- Explicit initialization may create new data volumes. Reconciliation of an established database/cache service must never replace a missing volume with an empty one. Check actual volume identity and required data before starting its replacement container.
- Unhealthy application readiness does not itself trigger endless recreation. Distinguish liveness/startup failures, dependency outages, and readiness failures.
- Foreign resources, conflicting names, duplicate instances, and unexpected mounts produce visible blocked state, not automatic adoption or broad cleanup.
- Backoff and retry budgets must be bounded and observable. Do not replay non-idempotent database/restore effects after an ambiguous crash; require explicit recovery where necessary.

Manual Docker changes are trusted operator escape hatches but may be reconciled away. Document that persistent changes belong in Bento intent; operators must stop/disable reconciliation through supported controls before manual repairs.

### 5.3 Operation planning and recovery

Every mutation that changes runtime state has a durable operation record and a scoped plan:

1. Validate request, confirmations, ownership, references, and target generation.
2. Persist accepted intent/operation and reserve affected targets under controller coordination.
3. Prepare complete candidate files and required images without changing unrelated resources.
4. Validate candidates, including running-process validators where required.
5. Perform scoped runtime changes, recording effects and inspectable resource IDs.
6. Verify component readiness and intended generation.
7. Activate newly published or changed managed routes last.
8. Record completion or a diagnosable partial result with explicit retry/recovery guidance.

Retain lock/stage/manifest/promotion/journal mechanics for generated files. Validation failure restores previous bytes/modes and sends no reload. Once external effects begin, do not claim a distributed rollback: container recreation may interrupt the old instance, grants may already exist, and restore may have modified data.

Mount stable configuration directories and define how each process sees a new generation. Atomic file replacement behind a single-file bind may leave the old inode mounted; replacing a mounted parent directory has similar pitfalls. Boot-static identity changes require recreation. Test actual mounted visibility rather than relying on host bytes alone.

Replacing one app stops its previous instance before starting its replacement. No blue/green overlap of minicrond or workers. Preserve enough journal state to recover after crashes between container creation, start, readiness, and route reload. A backend shutdown stops accepting new work and checkpoints/drains operations within a bound; it does not tear down the data plane.

## 6. Networking and HTTP behavior

### 6.1 Private ports and network membership

Applications listen on `0.0.0.0:<httpPort>` inside their own network namespace; each container can reuse the same port. A Dockerfile `EXPOSE` declaration is metadata, not a firewall, host publication, or prerequisite for connectivity.

Create stack-scoped user-defined bridge networks with explicit memberships. The baseline separates the app/ingress network from the data-service network: apps join the required networks; edge/cloudflared join app ingress, not data networks; databases/cache join data networks. Backend network egress policy must be deliberate; a private network is not necessarily Docker's egress-blocking `internal` network mode. Record the initial membership matrix in tests.

This is not per-app network isolation. Apps on a shared network may reach sibling listeners; database grants and Redis ACLs remain required. External proxies gain access to all endpoints reachable on joined networks. Do not equate no host publication with no peer access.

No app, FPM, scheduler, database, or Redis host port is published by the managed default. When selected, only the edge publishes chosen HTTP/HTTPS ports, including UDP for HTTP/3. A tunnel-only stack need not publish any ingress host ports.

### 6.2 Ingress ownership

**Managed edge:** optional shared Nginx owns app/proxy domains, TLS/ACME, redirects, and public access logs. It forwards HTTP by network alias/port. It mounts edge config and TLS state, never app homes, FPM/scheduler sockets, credentials, SQLite databases, or backups. Domain changes validate/reload edge only. Ensure DNS resolution follows container replacement; configure/test supported Nginx re-resolution or an explicit scoped reload, not a permanently cached old container IP.

**Direct cloudflared:** run on a reachable bridge network with a dedicated private token file/configuration. It may target `http://app-<appId>:<port>` without Bento edge. Preserve token redaction and scoped recreation on token rotation. Cloudflare hostname/origin rules remain operator-owned in the initial release; do not implement a Cloudflare routing API controller as incidental scope.

**External proxy:** operator-owned Traefik or equivalent may join an explicitly selected app network and route to the app HTTP port. Bento remains the sole container lifecycle owner. No automatic Dokploy/Coolify deployment integration or broad external network attachment is required initially.

Publication controls apply only to Bento-owned edge routes. For direct tunnels/external routing, display external ownership and instructions; do not offer a misleading guarantee that unpublish or stop removes the external route. Stop makes the app unavailable, but a route may remain and return an error. The operator is responsible for external readiness gating and route removal.

Bypassing edge also bypasses its redirects, TLS policy, rate/request limits, and logs. State this explicitly; those features are not automatically portable with the container.

### 6.3 App-specific HTTP behavior

PHP local Nginx handles static files, front-controller and legacy multi-PHP-file routing, upload limits, and local FastCGI parameters. Deny private/dotfile paths and unsafe document-root symlink traversal; separately support an explicitly configured release-root symlink. Unix permissions alone cannot prevent an app-UID frontend from serving app-readable credentials under an unsafe routing rule.

HTTP-process apps own static serving and application-level HTTP limits. There is no mandatory Bento route interceptor and no reserved deployment path. The ordinary application may use `/_bento/`; no undocumented hidden control endpoint is injected there.

Define trusted proxy configuration per ingress mode. Managed edge overwrites security-relevant forwarding headers; apps/PHP accept original host/scheme/client address only through the intended trust chain. Do not blindly trust all `X-Forwarded-*` traffic from shared-network peers. Document operator/framework configuration for direct cloudflared and external proxies.

Test WebSocket upgrades, SSE, streaming, uploads, request limits/timeouts, redirects, static ranges, PHP `HTTPS`/host handling and `PATH_INFO`. Test direct HTTP ingress as well as PHP through local Nginx and optional edge.

## 7. Managed images, isolation, and supervision

Build each runtime/toolchain image once and reuse it across apps. Pin base images and downloaded s6/minicrond artifacts, verify digests for amd64/arm64, and keep configuration changes separate from image builds.

Require:

- numeric app UID/GID from initial process execution;
- all capabilities dropped, no-new-privileges, read-only rootfs;
- declared size-bounded tmpfs paths for `/run`, `/tmp`, supervision state, and PHP frontend caches/uploads;
- read-only generated identity files preserving required image entries; no runtime `/etc/passwd` mutation;
- app-owned single FPM pool with no root master or worker identity transition;
- explicit memory/CPU/PID limits through Engine API fields, verified against actual Docker behavior;
- no Docker socket, host network, sibling home, shared stack root, or broad generated/helper directory mounts;
- bounded Docker `local` logging and no default diagnostic capabilities such as `SYS_PTRACE`.

Persistent app mounts: its own home read-write, only its bound SQLite directories, its selected configuration/identity files read-only, and no backup archive mount. HTTP ingress requires no host socket directory or shared ingress GID.

Ephemeral tooling uses the same image/identity with a dedicated exec entrypoint, not `/init`. It starts no daemons, acquires no persistent-instance lock, and gets only its home/bound data and necessary config—not backup archives or unrelated resources. Backups use separately scoped ephemeral mounts.

s6 supervises the primary runtime, PHP-local Nginx when applicable, and minicrond. Minicrond alone supervises user workers and schedules user jobs. The main HTTP process must not also be a minicrond worker. No root minicrond remains.

Prove non-root s6 startup, writable supervision paths, signal delivery/reaping, and graceful bounded shutdown before broad implementation. Critical component startup/liveness failures use bounded backoff and an explicit failure threshold that exits the container. Docker health is an observation, not automatic restart behavior. Dependency readiness failure must not create a restart storm.

Require one persistent instance per app. Controller coordination plus an app-instance lock held for the container lifetime must prevent duplicate schedulers, including accidental starts outside the controller. Tooling may coexist without starting daemons. This does not promise exactly-once scheduled execution after a crash.

## 8. Lifecycle, scheduler, and removed deployment behavior

| Operation | Required behavior                                                                                                                                                                                                            |
| --------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Create    | Allocate identity, provision/materialize, desired stopped and unpublished. Do not start the app.                                                                                                                             |
| Start     | Persist running intent, pull/build if required, start this app privately, and wait for readiness. No implicit publication.                                                                                                   |
| Publish   | Managed ingress only. Require a running ready app of the intended generation; never implicitly start it. Activate edge route last.                                                                                           |
| Unpublish | Remove only the managed route. Runtime, jobs, and workers continue. External routes remain operator-owned.                                                                                                                   |
| Stop      | Persist stopped/unpublished intent, remove managed route first, then stop the whole app including scheduler/workers. Report partial failure honestly and prevent reconciler resurrection.                                    |
| Restart   | Explicit, app-scoped operation with bounded outage; no sibling restarts. State clearly that container restart also interrupts scheduler/workers.                                                                             |
| Remove    | Require existing exact confirmation, remove managed route, confirm all app-owned persistent/tooling containers stopped/removed, then retire identity/remove desired app state. Preserve durable data and allocation history. |
| Prune     | Separate interactive destructive confirmation; enumerate known retained artifacts. No UID reclamation, broad Docker prune, or automatic database volume removal.                                                             |

Readiness verifies the real app HTTP endpoint, primary runtime, PHP frontend/FPM where applicable, minicrond socket/config sync, and intended image/config generation. A running container or socket inode alone is insufficient. PHP may add an application-level readiness URL in addition to FPM readiness. Log actionable, bounded diagnostics on failure.

A config update recreates only affected desired-running apps when image, argv, identity, mounts, resources, or service set changes. Stopped apps remain stopped. Local PHP routing changes validate/reload only local Nginx where possible. Internal task changes sync/restart only the selected minicrond. Shared image upgrades enumerate affected apps and require an explicit operation plan, not a silent fleet rollout.

A runtime crash does not erase desired publication. Recovery rechecks readiness/generation before introducing a new route or changing its backend. Existing routes may return unavailable responses while their app is down. Unrelated edge TLS/domain edits need not wait for every already-routed app to be healthy.

`app shell`/`exec` uses app UID/GID, environment, and contained workdir. Default to scoped ephemeral tooling; explicitly requested exec into a running app is also supported. No implicit shell for configured argv. Interactive shell access is an explicit privileged operator action, not an arbitrary public API command endpoint.

`app minicrond <slug> -- <argv>` targets only the selected running app under its numeric identity. Never start a second daemon to service a command. User registry edits require no Bento render/apply and are not copied into Bento state or REST CRUD.

The scheduler proxy must preserve minicrond peer-UID checks through a minimal UID-matched relay. Do not change process-wide UID inside the concurrent Go backend or weaken daemon authentication. Design a separately scoped relay process/transport, validate ownership and path containment, and preserve SSE/download limits, per-request auth, prefix/origin/CSRF checks, and session expiry. No Docker exec or shell command per browser asset/request.

Remove deploy UI, CLI/API contracts, generated hooks, PHP deploy helpers, drain schedules, queue-status readers, OPcache-reset endpoint/client, and deploy secrets from the new baseline. Do not implement Go equivalents. Explicit runtime/container restart remains available; there is no automatic post-hook activation. Preserve old operator files when rejecting old state, rather than cleaning them up as part of a reset.

## 9. Logging, data operations, and recovery

Send runtime/FPM-master/local-Nginx/s6 diagnostics to stdout/stderr with bounded Docker logs. Honor access-log opt-in and app/domain identifiers. Minicrond owns its history/log retention. App file logs remain app-owned, with optional app-UID rotation only where needed and explicit rename/reopen behavior. No privileged rotation scheduler.

Port or explicitly retire GoAccess/file-log reporting with a documented product decision before removing its old input. Do not quietly claim the feature survived a switch to Docker logs. Diagnostics and support bundles remain bounded/redacted; app-controlled output can still contain secrets and requires protected access.

Preserve shared data-service cardinality and private networking, app grants, Redis ACLs, generated-once credentials, and add-only bindings. Keep administrator/app secrets off host argv, API output, resource labels, and routine logs; use private credential files or bounded stdin where supported. Review SDK exec/container configuration for secret persistence and inspect exposure.

Keep config-owned per-binding SQLite VACUUM tasks for both app kinds with collision-safe reserved names and busy handling. User jobs/workers stay wholly owned by minicrond. Preserve host backup/maintenance scheduling as an explicit subsystem; backend introduction must not duplicate host-cron schedules or cause missed-run replay storms. Document how scheduled invocations reach the daemon and how backend downtime is reported.

Logical backup retains one batch lock, narrow source/destination mounts, SQLite online `.backup`, private partials, successful non-empty atomic publication, and retention only after complete batch success. rclone remains a scoped ephemeral egress job with private config and read-only selected backups; upload failure retains local artifacts and records failure.

Snapshot/transfer must account for backend SQLite, app SQLite, and minicrond databases including WAL. Use supported online snapshots or stop/quiesce writers; live tar of `.db` files is not a consistency guarantee. Stop/quiesce volume-backed services for raw volume archives and restore only their prior running set afterward. Preserve archive traversal checks, destination conflict refusal, compatible image/architecture validation, and failure cleanup restricted to resources created by that import.

Imported/restored clones start with stopped app intent and no managed public routes, workers, or schedules until explicit activation. Reconciliation must not automatically honor archived running intent. Resolve interrupted operations explicitly, never replay destructive effects merely because an archive contains a pending journal. Allocation-ledger rollback constraints still apply.

A normal backend restart against an intact live stack is different from import: inspect and resume safe reconciliation without restarting healthy containers or duplicating work. Loss/corruption of state or required durable paths must fail closed, not trigger empty-stack initialization.

## 10. Implementation phases and handoff checkpoints

Implement in dependency order. Each phase includes tests and a concise handoff recording changed paths, decisions, commands actually run, failures/skips, and remaining work. Later phases must not bypass an earlier feasibility gate. Intermediate development may retain old source for reference, but must not expose two controllers or runtime paths for one stack. The final product has no old Bun backend, oRPC, shared API package, or Compose fallback.

### Phase 0 — Inventory and executable acceptance map

- Trace current CLI/web use cases, safety tests, assets, API contracts, scheduler gateway, data/transfer logic, and release scripts.
- Classify each feature as preserve, redesign here, or explicitly removed (webhook deployment, Compose, shared PHP runners, socket ingress).
- Map important legacy safety tests to replacement Go/REST/Docker tests; do not delete them merely to make the rewrite pass.
- Record current actual schema histories, identity/name rules, and all supported custom inputs. Old Compose overlays are unsupported input in the new baseline and must be rejected clearly, not silently ignored.
- Identify reporting behavior requiring preservation or an explicit retirement decision. Preserve Nginx/runtime custom-source ownership without introducing an unrestricted Docker overlay API.

**Exit gate:** a checked-in feature/test checklist with concrete source references, known intentional incompatibilities, and no ambiguous ownership of live resources. No operator stack modification.

### Phase 1 — Prove runtime, Go platform, and contract feasibility

- Create the Go module, pinned toolchain/dependencies, narrow Engine adapter, build/test entrypoints, and SQLite driver decision.
- In a disposable external root, start two PHP apps and one HTTP app with the SDK. Prove non-root s6/FPM/local Nginx/minicrond, direct HTTP for the HTTP app, tmpfs ownership, read-only rootfs, private ports, CLI identity, shutdown/reaping, and singleton locks.
- Prove bridge DNS through edge and a direct HTTP client, replacement address changes, and data-network membership. Tunnel integration can use a controlled fixture before a live token is available; label that evidence accurately.
- Prove tygo output from representative DTOs and a minimal REST/fetch round trip. Check numeric/null/time/variant semantics, strict Go decoding, and generated-file drift detection.
- Measure memory/PIDs/idle CPU/latency for PHP and HTTP containers; select initial resource profiles from evidence.

**Exit gate:** recorded runtime evidence and a small working Go/REST/tygo/Docker vertical slice on at least one supported architecture. Record which architecture was actually executed. If non-root supervision or generation fails, resolve it here; do not silently reintroduce root services, universal Nginx, or Compose.

### Phase 2 — Persistent control plane and safe API foundation

- Implement strict new-baseline SQLite state, migrations/refusal, stack lifetime lock, identities/allocation ledger, configuration generations, and durable operation records.
- Implement daemon bootstrap/shutdown, service-manager packaging, loopback listener, operator authentication/session/CSRF, and protected CLI access.
- Add REST error/idempotency conventions and operation-status handling. Separate DTOs from persistence/domain internals; generate types using the pinned tygo command.
- Create thin Go CLI bootstrap/status/client paths and source/binary external-root smoke tests.
- Add retained-home refusal and path/ownership/symlink-safe platform helpers before resource provisioning uses them.

**Exit gate:** concurrent mutation/allocator tests, corrupt/old/future state refusal without byte changes, lock exclusion, authentication/CSRF tests, accepted-operation durability, and clean restart recovery. No unauthenticated management surface.

### Phase 3 — One complete application lifecycle

- Build the central Docker spec/label/name planner and scoped generated-config materializer.
- Implement create/start/inspect/stop/restart/remove for PHP and HTTP apps using the SDK; include ephemeral tooling and app CLI.
- Add Docker event/resync reconciliation, desired-versus-observed status, bounded retries, required-storage checks, and app-instance protection.
- Implement app-local readiness, scoped replacement, mounted config visibility, and operation journal recovery.
- Port minicrond internal SQLite task configuration and CLI access; no user-job state copy or deploy drain.

**Exit gate:** both app kinds follow identical lifecycle semantics; private scheduler work runs while unpublished; stop survives backend/host restart; deleted running app recreates only with intact data; missing data blocks recovery; duplicate starts cannot run two schedulers. Fault injection covers interruption at each external-effect boundary.

### Phase 4 — Optional ingress and publication

- Implement edge bridge topology, managed HTTP/TLS/proxy routes, config validation/reload, and readiness/generation-gated publication.
- Implement optional independently networked cloudflared with private token handling and direct app origins; remove Nginx network-namespace sharing assumptions.
- Represent managed/external/no-ingress ownership in REST/status. Document external-proxy attachment as an operator procedure, not a platform integration.
- Implement/test DNS refresh after app recreation, proxy-header trust, HTTP behavior, TLS/HTTP3 port bindings, and edge mount isolation.

**Exit gate:** edge and direct-origin paths work for both app kinds; no app host port or edge app-home mounts; unpublish leaves work running; stopped apps do not implicitly start; external route ownership is never misreported as Bento-controlled. Report live tunnel/TLS skips explicitly.

### Phase 5 — Shared data, maintenance, and recovery

- Port MySQL/PostgreSQL/Redis service management, credentials/grants/bindings, and scoped SDK jobs without changing destructive restrictions.
- Port logical backup/restore, host scheduling/rclone integration, retained-data inventory, permission repair, redacted diagnostics, and bounded logging/report behavior.
- Implement consistent state/minicrond/app snapshots, raw volume transfer, staged import, interrupted-operation handling, and allocation-history validation.
- Verify established services refuse missing durable volumes rather than initialize empty replacements.

**Exit gate:** test real data round trips and failures, confirmation refusals, no secret leakage, no failed/empty final backups, retention ordering, symlink/archive attacks, WAL consistency, and import with no automatic routes/jobs. Never use production roots for these tests.

### Phase 6 — REST frontend and scheduler/terminal integration

- Replace `apps/web/src/api/client.ts` oRPC transport with the typed REST fetch layer and tygo output.
- Convert domain hooks to explicit React Query keys/options, mutation operation tracking, narrow invalidation, and desired/observed status displays.
- Remove deployment webhook pages/settings/actions and shared TypeScript server-contract dependencies. Do not generate a second hand-maintained API type layer.
- Port authenticated scheduler relay/proxy and terminal streaming. Preserve peer UID, per-app authorization, exact prefixes/origins, bounded streams, expiry, and no token handoff.
- Connect preserved operations screens, database workflows, logs, diagnostics, and explicit confirmations to the Go backend.

**Exit gate:** web typecheck/build, REST wire tests, auth/CSRF and scheduler isolation tests, browser journeys for both app kinds, SSE/WebSocket behavior, and generated-type reproducibility. No oRPC imports remain in the target web/backend paths.

### Phase 7 — Remove legacy implementation and release

- Move authoritative assets to the Go backend and delete obsolete Bun backend/CLI implementation, oRPC contracts, `packages/shared` API package, PHP version triples/runners, socat paths, webhook helpers, and Compose renderer/execution/overlays.
- Remove obsolete dependencies/workspace entries and update `bun.lock` using Bun. Trace references before deleting files; preserve useful PHP configuration, operator-owned sources, and safety test coverage.
- Update root `AGENTS.md`, scripts, specs, README/help, docs, release/container packaging, dependency inventory, and CI to match Go + REST + tygo + raw Docker. Do not leave repository instructions mandating removed oRPC/shared-contract patterns.
- Embed/version immutable assets in the Go release. Build web assets before embedding if shipped in the same binary. Prove execution outside the repository with an external stack root and without Bun/Node/Docker CLI/Compose on PATH.
- Complete parity checks for source versus release configuration, behavior, embedded assets, refusals, and redaction on Linux amd64/arm64; record architecture runtime evidence separately from cross-build success.

**Exit gate:** no live legacy/fallback controller, no Compose requirement, no oRPC/shared API authority, all preserved features accounted for, clean generated-type/asset checks, updated documentation, and the full replacement release gate passing or explicitly blocked by reported missing evidence.

## 11. Verification requirements

Add runnable Go and generation checks during phase 1; these are requirements for the implementation, not claims that the commands/scripts already exist today. The final CI must include:

- Go formatting check, `go vet ./...`, `go test ./...`, and supported race-detector tests from `apps/backend`.
- Reproducible pinned tygo generation with a clean-diff assertion.
- Bun-managed frontend install/lock checks, formatting/lint, `bun run --cwd apps/web check`, and `bun run --cwd apps/web build`.
- Go API/SQLite/operation contract tests and real Docker integration tests, including injected crash recovery.
- Browser/scheduler/terminal security and streaming tests.
- Release build and source/binary asset/state/refusal parity for Linux amd64/arm64.

While old code still exists, run its applicable current repository gates (`bun run fmt`, `bun run lint`, `bun run check`, `bun run test`, and relevant integration/parity checks) when touched. Phase 7 must replace obsolete script paths, not leave permanently failing tests pointing at deleted code. Run checks appropriate to each phase and report exactly what was run; compilation, a mock, or a skipped Docker test is not a live runtime pass.

### Required final acceptance scenarios

1. Two same-image PHP apps and one HTTP app have independent homes/data/schedulers/lifecycle. Edge sees no app files; HTTP app has no mandatory Nginx.
2. All managed app processes use app identity, no extra capabilities or writable root, no Docker socket, and no default host port. Ephemeral tools start no daemons.
3. Backend outage leaves traffic/jobs working; recovery does not restart healthy apps. Docker event loss is repaired by inspection. Stop intent persists across backend and host restart.
4. Missing running app container is safely recreated; missing durable data or ownership conflict blocks action. Duplicate instance attempts cannot duplicate workers/schedules.
5. Runtime/config/image changes affect only intended apps. Readiness failures and Docker outages produce bounded retries and visible partial operations, not restart storms or false success.
6. Publication checks real readiness/generation; unpublish preserves private work; external tunnel/proxy routing is accurately marked operator-owned. DNS follows replaced containers.
7. Strict REST validation, tygo wire fidelity, idempotent request recovery, authorization/CSRF, scheduler UID checks, stream bounds, and terminal exit behavior pass adversarial tests.
8. Identity allocation/history survives saves/removal/prune, retained-home conflicts, concurrency, export/import, and documented rollback constraints. No silent adoption/chown.
9. Database grants/ACLs, secret handling, logical backup retention, restore confirmation, archive containment, and consistent SQLite snapshots preserve their safety contracts. Ordinary removal never destroys durable data.
10. Import is staged with schedules/routes stopped. Lost/corrupt state is refused without initialization. Interrupted destructive operations are not blindly replayed.
11. Source and compiled Go distributions use the same behavior/assets with external mutable roots; release operation needs no Bun, Node, Docker CLI, or Compose install.
12. No webhook receiver/queue/drain/OPcache endpoint, Compose runtime path, oRPC transport, or shared TypeScript API contract remains in the final implementation.

## 12. Instructions to the implementing agent

Start with phase 0, then the bounded phase-1 feasibility slice. Do not begin by translating all TypeScript files or designing a generic orchestration framework. Reuse behavioral knowledge and test cases, not obsolete topology or transport abstractions.

Treat the architectural decisions above as settled. Resolve library/version/platform details with inspected source, pinned dependencies, focused prototypes, and tests. If a feasibility gate requires changing a settled decision or removing another product feature, stop and ask rather than adding an undocumented fallback.

Use disposable external stack roots for Docker tests. Never edit generated output, reuse an operator stack as a fixture, erase unsupported state, weaken destructive confirmations, or claim unexecuted integration/architecture checks passed. Hand off each completed phase with evidence and a clear next phase; only mark the rewrite complete after phase 7 and the final acceptance scenarios.
