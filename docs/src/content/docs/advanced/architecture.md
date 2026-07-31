---
title: Architecture
description: See how Bento turns operator intent into running services, and learn who owns each part.
---

# Architecture

Bento runs only when you call the `bento` command. The CLI reads your desired state, generates configuration, and operates Docker Compose. Bento does not run a background daemon.

![Bento architecture: the CLI turns desired state into a Compose stack, with public Nginx routing to private PHP and data services.](/diagrams/bento-architecture.png)

```text
Operator -> bento CLI -> state.json -> staged render -> validate -> targeted reload
Internet -> Nginx -> app PHP-FPM socket -> private MySQL/PostgreSQL/Redis
                    -> reverse-proxy upstream
PHP runner -> per-app Supercronic, deploy drain, and s6 workers
```

## Main components

| Component | Cardinality | Role/boundary |
| --- | ---: | --- |
| CLI control plane | Once per command | Validates intent, updates state, renders files, and operates Compose |
| Nginx | One per stack | Only public base service; TLS, apps, proxies |
| PHP-FPM | One per PHP version | Per-app pools and Unix sockets |
| PHP runner | One per PHP version | Supervises app background work; must stay a singleton |
| PHP CLI | Ephemeral per command | App UID/GID, home, runtime |
| MySQL/PostgreSQL | One per managed version | Private service and durable named volume |
| Redis | One per stack | Private shared/ACL cache and durable volume |
| Litestream | Optional one per stack | Constrained-root directory watcher for every managed SQLite file |

## How requests move

Nginx reads public app files through a read-only mount. It sends PHP requests to each app's private FPM socket.

PHP services connect to databases and Redis through the stack's private network. In host mode, Nginx stays outside that network. In bridge mode, Nginx joins it. Bento does not publish database or Redis ports in the base setup.

## Who owns the files

Your desired state and `.env` are the source of truth. Bento can recreate generated configuration, so never customize generated files.

Put your changes in `custom/` and `overlays/`. Protect app homes, SQLite files, certificates, backups, logs, and named volumes as durable data.

## Validation and failure

Bento allows only one render at a time and builds the new configuration in a staging area. `apply` publishes the complete candidate, validates running services, and reloads only the affected roles.

If validation fails, Bento restores the previous files. If a reload signal fails after validation, Bento leaves the valid new files in place so you can fix the service and retry. See [Render and apply internals](/advanced/render-apply/).

## Security boundary

Bento separates apps with stable user IDs, private FPM pools and sockets, filesystem permissions, database grants, Redis identities, and job ownership. These controls reduce accidental access between apps.

Apps still share versioned containers and the host kernel. Do not use Bento as a sandbox for mutually hostile tenants. See [Isolation and security](/advanced/isolation-security/).

## Implementation layering

The code has five main layers:

- Command adapters parse input and display results.
- Domain and service code plans state changes.
- Schemas validate external data.
- Platform adapters handle files, processes, locks, time, and randomness.
- Templates provide immutable runtime assets.

Source mode and compiled releases use the same entry point and behavior.

## Next steps

- [Technical decisions](/advanced/technical-decisions/)
- [Networking internals](/advanced/networking/)
- [Storage and recovery](/advanced/storage-recovery/)
