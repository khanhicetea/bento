---
title: Storage and recovery
description: Understand Bento ownership layers, named volumes, logical and raw backups, and failure semantics.
---

# Storage and recovery

A recoverable Bento host needs more than `state.json`: preserve operator intent, app files, certificates, and database/cache data through compatible recovery methods.

## Ownership layers

| Layer | Recovery value |
| --- | --- |
| `.env` + `state.json` | Reconstruct desired topology and credentials |
| `custom/` + `overlays/` | Reconstruct operator changes |
| `generated/`, assets, runtime | Rebuild or recover transactionally |
| `homes/`, `sqlite/`, `certs/`, logs/backups | Durable bind-mounted data |
| MySQL/PostgreSQL/Redis named volumes | Durable service data outside root tree |

## Logical versus raw

For MySQL and PostgreSQL, logical `backup` uses matching database tools, private partial files, non-empty checks, and atomic final naming. It is granular and the PostgreSQL major-upgrade path. Restore-to-new is the preferred verification flow. Restore replacement is guarded but not object-level atomic.

SQLite uses optional Litestream replication to S3-compatible storage. Verification restores a temporary copy and runs a full integrity check. Bento does not yet expose a guarded production replacement restore.

Stack export combines supported stack files with raw named-volume archives. It stops only running data services needed for consistency and restarts their prior set. It currently excludes the stack-root `sqlite/` directory. Raw transfer is broad and version/architecture sensitive; PostgreSQL requires compatible major/image.

MySQL/PostgreSQL logical backups are created on-host. A scheduled batch can upload newly created artifacts through the operator-configured rclone sidecar; verify those remote copies and test them independently. SQLite continuous backup uses its separately configured S3 replica.

## Failure semantics

- Failed/empty dump publishes no final artifact; partial is removed.
- Mid-batch failure keeps earlier successful dumps but skips retention.
- Failed restore may leave a partial destination.
- Failed import cleans only newly created destination volumes.
- Export/import archives contain secrets/private keys.
- `compose down -v` and managed database service removal are blocked.

## Recovery plan

1. Record stack name, root, binary version, architecture, and managed images.
2. Maintain encrypted off-host desired state, homes/certs, relational logical dumps, and SQLite replicas.
3. Regularly restore relational dumps to verification databases and run SQLite restore verification.
4. Test a full import on an isolated bridge-mode stack when raw recovery matters.
5. Keep source until destination health, routing, jobs, and representative data pass.

## Boundaries

Bento provides optional continuous replication only for SQLite. It does not provide MySQL/PostgreSQL continuous replication, a public SQLite replacement-restore command, object-level atomic restore, or high availability. Use database-native and infrastructure tooling when those recovery objectives are required.

## Next steps

- [Back up and restore relational databases](/guides/data/backup-restore/)
- [Operate SQLite with continuous backup](/guides/data/sqlite/)
- [Export and import](/guides/stacks/export-import/)
- [Stack layout](/reference/stack-layout/)
