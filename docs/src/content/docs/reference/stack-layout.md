---
title: Stack layout
description: Identify source-of-truth, generated, custom, durable, sensitive, and ephemeral paths in a Bento stack root.
---

# Stack layout

Paths below are relative to the selected stack root, such as `/var/lib/bento`.

| Path | Ownership/lifecycle | Sensitive or durable |
| --- | --- | --- |
| `.env` | Operator stack config/secrets | Sensitive, source-of-truth |
| `state.json` | Validated desired state | Sensitive, source-of-truth |
| `generated/` | Bento-managed complete render | Disposable; may contain generated client credentials |
| `custom/` | Operator drop-ins/templates | Durable custom input |
| `overlays/` | Ordered operator Compose input | Durable custom input |
| `homes/<app>/` | App code, credentials, SSH, logs, deploy state | Durable and sensitive |
| `certs/` | Boot, private-CA, external and ACME material | Durable; private keys sensitive |
| `backups/<service>/` | MySQL/PostgreSQL logical dumps | Durable and sensitive; on-host only |
| `sqlite/<app-slug>_<10-random-hex-chars>/<app-slug>.sqlite` | Private SQLite database and WAL/SHM sidecars | Durable and sensitive |
| `litestream-meta/` | Directory-watcher transaction metadata | Durable; required to continue replication efficiently |
| `logs/` | Nginx logs and reports | Durable operational data; potentially personal/sensitive |
| `runtime/` | FPM sockets and volatile runner locks | Ephemeral |
| `locks/` | Host render serialization | Ephemeral/recovery coordination |
| `.asset-cache/` | Digest-addressed compiled assets | Rebuildable cache |
| `docker/`, `helpers/` | Materialized immutable runtime assets | Rebuildable; do not customize |

MySQL, PostgreSQL, and Redis contents live in Compose named volumes outside the ordinary stack-root tree. SQLite files live under the stack-root `sqlite/` directory. Named-volume resources are prefixed by stable `COMPOSE_PROJECT_NAME` identity at the Docker layer. Never infer backup completeness from a filesystem copy alone.

## Generated subtrees

`generated/compose/`, `nginx/`, `php/`, `runner/`, `mysql/`, `postgres/`, `litestream/`, and `secrets/` are derived from desired state, the stack `.env`, and templates. Render uses same-filesystem staging and a journal. Edits are overwritten and can break recovery assumptions. Generated secrets remain sensitive and use mode `0600`.

## App home in containers

Host `homes/demo/` maps to `/home/demo`. Nginx sees homes read-only; PHP FPM, runner, and ephemeral CLI roles use app identity and the required writable mounts. FPM sockets appear under host `runtime/php-fpm/<php-service>/` and map differently inside Nginx and PHP.

## Backup boundary

Protect `.env`, `state.json`, `homes/`, `sqlite/`, `litestream-meta/`, `certs/`, custom input, and verified logical dumps off-host. For SQLite, use [Litestream continuous backup](/guides/data/sqlite/); do not copy a live database and its WAL/SHM files as an assumed-consistent backup. Use [stack export](/guides/stacks/export-import/) when you need compatible raw MySQL, PostgreSQL, and Redis volumes too.

## Related pages

- [Stacks and identity](/concepts/stacks/)
- [Desired state](/concepts/desired-state/)
- [Storage and recovery](/advanced/storage-recovery/)
