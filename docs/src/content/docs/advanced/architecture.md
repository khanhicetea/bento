---
title: Architecture
description: See Bento's control plane, data plane, component cardinality, ownership, and request paths.
---

# Architecture

Bento is an on-demand host-local desired-state controller around Docker Compose. Operators run the CLI; there is no resident Bento daemon.

```text
Operator -> bento CLI -> state.json -> staged render -> validate -> targeted reload
Internet -> Nginx -> app PHP-FPM socket -> private MySQL/PostgreSQL/Redis
                    -> reverse-proxy upstream
PHP runner -> per-app Supercronic, deploy drain, and s6 workers
```

## Components and cardinality

| Component | Cardinality | Role/boundary |
| --- | ---: | --- |
| CLI control plane | Per invocation | Validates intent, state, render, Compose, safety |
| Nginx | One per stack | Only public base service; TLS, apps, proxies |
| PHP-FPM | One per PHP version | Per-app pools and Unix sockets |
| PHP runner | One per PHP version | Singleton supervisor for app background work |
| PHP CLI | Ephemeral per command | App UID/GID, home, runtime |
| MySQL/PostgreSQL | One per managed version | Private service and durable named volume |
| Redis | One per stack | Private shared/ACL cache and durable volume |

## Request and storage paths

Nginx reads app homes through a read-only mount and reaches FPM through per-app sockets. PHP roles reach databases and Redis through the private network. Host-mode Nginx does not join that network; bridge-mode Nginx does. Database and cache ports are not published by the base model.

Desired state and `.env` are local source-of-truth. Generated configuration is disposable. Operator customization is loaded from `custom/` and `overlays/`. Homes, certificates, backups, logs, and named volumes are durable.

## Validation and failure

Render is serialized and staged. Apply promotes a complete candidate, validates running targets, and reloads only affected roles. Validation failure restores previous bytes; a signal failure leaves valid new files for retry. See [render and apply](/advanced/render-apply/).

## Security boundary

Stable UID/GID, pool/socket, filesystem modes, database grants, Redis identities, and job ownership reduce accidental crossing. Shared version containers are not hostile-tenant sandboxes. See [isolation and security](/advanced/isolation-security/).

## Implementation layering

Command adapters parse and present; domain/services own state transitions and plans; schemas validate external input; platform adapters isolate filesystem, process, lock, clock, and randomness; templates are immutable runtime assets. Source and compiled distributions share this entrypoint and behavior.

## Next steps

- [Technical decisions](/advanced/technical-decisions/)
- [Networking internals](/advanced/networking/)
- [Storage and recovery](/advanced/storage-recovery/)
