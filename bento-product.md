# Bento product architecture

## Product

Bento is a self-hosted operations tool for one Linux server. It runs multiple apps with Docker Compose. It targets developers and small teams that want more structure than hand-written Compose files, without Kubernetes.

Operators use a CLI, guided terminal UI, or optional web UI. They own the server, configuration, app files, and data.

## How stacks and parts fit together

A **stack** is one independent Bento installation. It has an external filesystem root and a stable Compose project name. The name identifies its containers, networks, and volumes. Multiple stacks need different names and non-conflicting ingress ports.

An **app** is a stable identity inside a stack. Its slug, Linux UID/GID, home, domains, credentials, and database bindings stay connected across operations.

```text
CLI / terminal UI / web API
  -> shared backend services
  -> desired state + configuration generation
  -> Docker Compose

Traffic -> Nginx -> app Unix socket -> PHP-FPM or Node/Bun/Python
                -> external reverse-proxy upstream

Apps -> private MySQL / PostgreSQL / Redis, or SQLite files
PHP runner -> per-app Minicrond -> jobs, workers, deploy hooks
```

- **Ingress:** One Nginx per stack handles domains, TLS, and routing. It is the only public service in the base topology.
- **PHP:** Each PHP version shares one FPM container and one background runner. Each app gets its own user, FPM pool, and socket. CLI containers are temporary.
- **Process apps:** Each Node.js, Bun, or Python app gets a dedicated private container. Bento requires an explicit runtime version and start command. New apps stay unpublished until started and healthy.
- **Data:** MySQL and PostgreSQL run by managed version. Redis is shared per stack. Apps can have multiple database bindings. Adding one does not migrate existing data.
- **Background work:** s6 supervises per-app Minicrond daemons in PHP runners. User jobs and workers live in Minicrond registries. These features and webhook deploys are not supported for process apps.

## State and change flow

`state.db` stores Bento desired state, not application database contents. `.env` stores stack settings and secrets. App homes, certificates, and backups live under the stack root. Relational data and Redis use Docker volumes.

Generated files are replaceable. Custom templates, Nginx drop-ins, and Compose overlays are operator-owned inputs.

Apply follows this sequence:

```text
lock -> recover -> stage -> promote -> validate -> scoped reload
```

Validation failure restores the previous generated files. Reload failure keeps validated new files for retry. Render writes configuration without reloading. Apply does not start stopped services. There is no continuous reconciliation loop.

## Main features

- App lifecycle, domains, aliases, reverse proxies, TLS, and optional HTTP/3.
- Multiple runtime and database versions on one host.
- PHP schedules, workers, and signed webhook deployment queues.
- Logical backups, restore, and optional rclone uploads.
- Stack export/import, health checks, logs, diagnostics, and permission repair.
- Optional Cloudflare Tunnel ingress and browser management.

## Technical decisions

- **Bun and strict TypeScript:** One backend supports source execution and standalone Linux binaries with embedded assets.
- **Shared operations:** CLI handlers and domain-specific oRPC routers call backend services. React uses contracts from `packages/shared`.
- **Validated state:** Zod checks boundaries. Bun SQLite provides transactional storage and numbered migrations.
- **Testable host access:** Platform interfaces wrap files, processes, locks, time, randomness, and assets.
- **Clear code boundaries:** `apps/cli/src/services` owns operations; `domain` owns models; `platform` owns host adapters; `server/domains` owns API handlers.

## Distinctive points and limits

Bento combines efficient shared PHP runtimes with dedicated process-app containers. One app identity connects routing, execution, files, and data access. Staged configuration and targeted reloads reduce disruption.

Removal preserves durable app data. Permanent deletion is separate and guarded. Bento blocks `compose down -v`.

This is not a cluster platform or a hostile-tenant sandbox. Builds and release strategies remain operator-owned. The web UI defaults to loopback and supports optional Basic authentication. Remote access still needs trusted authentication and encrypted transport. Backups need off-host copies and restore testing.
