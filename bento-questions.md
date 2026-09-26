# Bento: architecture questions

These are recommendations, not changes to the current product contract. They assume a small team building a single-host tool for trusted operators and apps.

## My short answer

| Question                  | Recommendation                                                                  |
| ------------------------- | ------------------------------------------------------------------------------- |
| Port Bun to Go?           | Not now. Improve reliability before changing language.                          |
| One container per app?    | Favor app-scoped isolation. Do not require web and jobs to share one container. |
| Replace Docker Compose?   | Keep Compose for the single-host product.                                       |
| Replace Nginx with Caddy? | Keep Nginx now. Test Caddy only against a clear maintenance goal.               |
| Which interfaces?         | CLI + web. Keep terminal prompts small, not a third full UI.                    |
| Improve process apps?     | Make lifecycle, deployments, and background work runtime-neutral.               |

## 1. Should the backend move from Bun to Go?

**My view: no full rewrite without evidence of a Bun-specific problem.** Go is more mature, but language maturity does not automatically make Bento safer.

### Pros of Go

- Mature standard library, tooling, profiling, and operational ecosystem.
- Good support for long-running services, cancellation, and concurrent work.
- Straightforward native distribution for many workloads.
- Potentially lower idle memory use. Measure it with Bento's workload.

### Cons

- Bento already ships standalone binaries. Distribution alone is not a strong reason.
- A rewrite must reproduce migrations, locks, rollback, secrets, and destructive-operation guards.
- Direct TypeScript/oRPC contract sharing would need a replacement, such as generated clients.
- SQLite driver choices affect portability and CGO requirements.
- The rewrite delays features and introduces new failure modes.

**DevOps view:** Most serious risks are at Docker, filesystem, process, and recovery boundaries. Go does not remove them. Improve timeout handling, interrupted-operation recovery, and failure tests first.

**End-user view:** Reliable upgrades and recoverable failures matter more than the backend language.

**Revisit when:** Profiling or incident history shows persistent Bun-specific limits. Port one bounded component first and compare behavior against existing contract tests.

## 2. Should each app have one container with s6, FPM, and Minicrond?

**My view: app-scoped containers are a good direction. Exactly one container per app is not a useful rule.**

Bento currently shares FPM and runner containers by PHP version. Process apps already have dedicated containers.

### Pros of the proposed combined container

- Each app gets its own filesystem mounts and process namespace.
- Container CPU, memory, and PID limits become app-specific.
- App-specific extensions and runtime updates become easier.
- s6 can correctly supervise both FPM and Minicrond. Multiple processes in one container are not inherently wrong.
- Users get a simple mental model: restart one app, not a shared runtime.

### Cons

- Recreating the container interrupts HTTP traffic, schedules, and workers together.
- Web requests and jobs share one resource budget.
- Multiple replicas can duplicate scheduled work unless scheduler ownership is separate.
- More FPM masters and containers increase overhead. Shared image layers reduce disk duplication, not process memory.
- Containers still share the host kernel. This is not hostile-tenant isolation.

**Preferred shape:** One app-owned web container, plus an optional app-owned runner using the same runtime image. Keep Nginx and databases shared. Minicrond owns jobs and workers; s6 supervises the daemon. Keep one active scheduler per app.

A combined container is reasonable for small apps whose web and background work intentionally share a lifecycle. It should be a deliberate simplicity trade-off.

**DevOps view:** Benchmark shared PHP versus app-scoped PHP on a representative small VPS. Test worker draining, OOM behavior, and scoped upgrades before choosing the default.

**End-user view:** Independent restarts and understandable resource limits are valuable. Avoid exposing several competing deployment modes without a clear need.

## 3. Should Bento replace Docker Compose with another OCI-compatible toolchain?

**My view: keep Compose. Add OCI image support before replacing orchestration.**

OCI defines image and runtime standards. It does not define Compose services, networking, secrets, or lifecycle orchestration. Docker already supports OCI images.

### Pros of alternatives

- **Podman + Quadlet:** Attractive for systemd-native, daemonless operation. Rootless operation can reduce some host privileges.
- **Nomad:** Useful when real multi-host scheduling becomes a requirement.
- **Kubernetes:** Strong ecosystem for clusters, policies, and service orchestration.

### Cons

- Quadlet is not a drop-in Compose replacement. Rootless networking, UID mapping, mounts, and sockets need testing.
- Nomad and Kubernetes add control-plane operations beyond Bento's single-host scope.
- OCI compatibility does not guarantee equivalent volumes, networking, health checks, or secret behavior.
- Supporting multiple backends multiplies the release and recovery test matrix.

**DevOps view:** Keep runtime calls behind focused service operations. Do not build a universal orchestrator abstraction before a second backend is required.

**End-user view:** Familiar Docker commands make diagnosis and recovery easier. Users should not need a cluster education to run a few apps.

**Revisit when:** Target users require rootless Podman, prohibit Docker, or genuinely need multiple hosts. A migration must explicitly preserve data, identities, and deletion guards.

## 4. Should Nginx become Caddy?

**My view: Caddy is appealing for a new product. Replacing working Bento ingress is not a priority.**

### Pros of Caddy

- Automatic HTTPS is central to its design.
- Common reverse-proxy and PHP configurations can be shorter.
- Built-in WebSocket proxying and graceful configuration loading simplify common operations.
- Potentially less certificate and routing configuration for maintainers to own.

### Cons

- Bento already has native Nginx ACME support. This is not a move from manual TLS to automatic TLS.
- Existing Nginx drop-ins and custom templates cannot transfer unchanged.
- PHP front-controller rules, legacy routing, Unix sockets, logs, and deploy endpoints need equivalent tests.
- External certificates, private CA behavior, HTTP/3, and Cloudflare namespace assumptions need verification.
- Nginx expertise is widespread. Performance improvements cannot be assumed without measurements.

**DevOps view:** Prototype Caddy against the existing ingress acceptance tests. Measure configuration size and maintenance burden, not just benchmark throughput. Keep its administration endpoint private and preserve certificate storage across restarts.

**End-user view:** Fewer TLS failures would justify a change. Different configuration syntax alone would not.

Choose one supported default if the prototype wins. Avoid maintaining two full ingress implementations indefinitely. Do not give ingress the Docker socket merely for automatic discovery.

## 5. Should Bento have a TUI, CLI, web UI, or CLI + web?

**My view: CLI + web, with small CLI prompts where useful. Do not build a third complete TUI.**

| Choice          | Pros                                                | Cons                                                                              |
| --------------- | --------------------------------------------------- | --------------------------------------------------------------------------------- |
| CLI only        | Small surface. Good for SSH, scripts, and recovery. | Harder discovery and visual troubleshooting.                                      |
| TUI only        | Guided operation over SSH. No browser listener.     | Weak automation. Poor replacement for stable CLI commands.                        |
| Web only        | Easy discovery, forms, logs, and status views.      | Recovery depends on the server/UI. Adds authentication and browser security work. |
| CLI + web       | Covers automation and everyday visual operations.   | Two interfaces still need consistent contracts and safety checks.                 |
| CLI + TUI + web | Broad choice for users.                             | Repeated workflows, documentation, tests, and interaction design.                 |

**DevOps view:** The CLI must work without the web server. Both interfaces should call the same operations, not wrap each other's command parsing. Preserve JSON output and stable exit codes.

**End-user view:** Use the web UI for discovery and routine work. Keep CLI commands for repeatable actions, emergencies, and SSH-only access.

Retain useful existing wizard prompts, but freeze TUI feature expansion. Do not remove a working interface abruptly. Keep dangerous recovery or prune operations CLI-only where stronger operator interaction is appropriate.

The web UI remains a privileged administration surface. Keep loopback defaults. Authentication, trusted TLS, session handling, and request-origin checks are product work, not optional polish.

## 6. How should process apps improve?

**My view: complete the app lifecycle before adding more languages.**

The current foundation is useful: dedicated containers, app identities, private Unix-socket ingress, health-gated publication, and shared data services. The main gaps are deployments, background work, reproducibility, and operational visibility.

### Recommended changes

1. **Make the model runtime-neutral.** Keep identity, domains, secrets, storage, and database bindings common. Put PHP pools and process commands in separate runtime types. Stop carrying PHP compatibility fields through process-app behavior.
2. **Define app roles.** Support web, worker, scheduler, and one-off jobs. Allow worker-only apps without an HTTP domain. Make database bindings optional for apps that do not need data services. These are deliberate future schema changes.
3. **Offer two packaging paths.** Keep curated Node.js/Bun/Python environments for simple projects. Add operator-supplied OCI images, preferably built in CI and pinned by digest. Do not infer arbitrary build commands.
4. **Strengthen lifecycle handling.** Test PID 1 behavior, child reaping, signal forwarding, and TERM/grace/KILL shutdown. Define what happens if the socket adapter dies. Separate startup readiness from ongoing health reporting. Docker health status alone does not restart an unhealthy process.
5. **Add app resource controls and diagnostics.** Expose CPU, memory, PID, and stop-timeout settings. Show OOM events, exit reasons, restart counts, health failures, and bounded logs.
6. **Extend Minicrond through an app-scoped runner.** Provide the app's required runtime tools. Keep exactly one scheduler active. Leave user definitions in Minicrond rather than copying them into Bento state.
7. **Make deployments explicit.** Separate build, release, migration, health verification, and activation. Record the previous image or release. Begin with operator-triggered deployments and documented downtime. Code rollback does not undo database migrations.
8. **Remove PHP-only webhook coupling later.** Use bounded signed requests, a durable queue, and app-identity hooks. Container replacement belongs to a privileged Bento operation, never an app hook with Docker socket access. Automated replacement needs an explicit executor design.

### Pros

- PHP and process apps gain a consistent operator experience.
- Immutable releases are easier to reproduce and roll back.
- Background workers become first-class workloads.
- Clear health and resource information makes failures easier to diagnose.

### Cons

- More roles increase state, UI, and integration-test complexity.
- Arbitrary images need a documented contract for ports, users, writable paths, and secrets.
- Separate runners may require additional image packaging.
- Safe rollout and migration handling are much harder than starting a container.

**DevOps view:** Prioritize graceful shutdown, isolation, secret handling, and restore tests. Test Node.js, Bun, and Python, including crashes and interrupted deployments.

**End-user view:** The desired workflow is simple: choose a runtime or image, configure commands and secrets, start privately, inspect health, publish, and recover a previous release when safe.

## Suggested order

1. Strengthen recovery, SQLite-consistent transfer, process handling, and failure tests.
2. Focus interface investment on CLI + web.
3. Improve process lifecycle, diagnostics, and reproducible releases.
4. Add runtime-neutral background work and deployment support.
5. Pilot app-scoped PHP containers and measure the cost.
6. Reconsider Go, Caddy, or another runtime backend only against measured needs.

Do not change language, container topology, orchestration, and ingress together. Keep each change independently testable and reversible.

### Code and specifications behind this review

- `apps/cli/src/services/{render,state_database,compose,process_app,deploy,minicrond}.ts`
- `apps/cli/templates/docker/process/entrypoint.sh`
- `apps/cli/src/server/` and `packages/shared/src/`
- `specs/01-product-spec.md`, `specs/02-system-architecture.md`, and `specs/03-reimplementation-contract.md`
- Proposal documents `specs/05-proposed-technical-improvements.md` and `specs/06-minicrond-migration-plan.md`
