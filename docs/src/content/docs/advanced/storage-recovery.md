---
title: Storage and recovery
description: Learn what to back up, which recovery method to use, and what can fail.
---

# Storage and recovery

`state.json` alone cannot recover a Bento host. You must also protect app files, certificates, customization, and database or cache data with the correct backup method.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/backup-coverage.svg
Alt: Bento backup methods mapped to desired state, app homes, certificates, relational databases, SQLite, and Redis.
Show: Rows for each durable data type and columns for filesystem backup, logical backup, Litestream, and stack export. Use check marks only where a method covers the data. Highlight that no single method covers every row and that off-host copies require separate verification.
-->

## Ownership layers

| Layer | Recovery value |
| --- | --- |
| `.env` + `state.json` | Reconstruct desired topology and credentials |
| `custom/` + `overlays/` | Reconstruct operator changes |
| `generated/`, assets, runtime | Rebuild or recover transactionally |
| `homes/`, `sqlite/`, `certs/`, logs/backups | Durable bind-mounted data |
| MySQL/PostgreSQL/Redis named volumes | Durable service data outside root tree |

## Logical versus raw

For MySQL and PostgreSQL, `backup` uses matching database tools. Bento writes to a private partial file, checks that it is not empty, and then publishes the final file atomically.

Logical backups let you restore one database and are the correct path for a PostgreSQL major upgrade. Verify a backup by restoring it under a new name. Replacing an existing database has safeguards, but the restore is not atomic at the object level.

SQLite uses optional Litestream replication to S3-compatible storage. Verification restores a temporary copy and runs a full integrity check. Bento does not yet expose a guarded production replacement restore.

Stack export combines supported stack files with raw archives of named volumes. It stops only the running data services required for a consistent copy, then restarts the same services.

The export currently excludes the stack root's `sqlite/` directory. Raw archives also depend on compatible CPU architectures and database images. PostgreSQL requires a compatible major version and image.

Bento creates MySQL and PostgreSQL logical backups on the same host. A scheduled job can upload new files through your configured rclone sidecar, but you must verify and test those remote copies yourself.

SQLite continuous backup uses its own configured S3 replica.

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

Bento offers optional continuous replication only for SQLite. It does not provide continuous MySQL or PostgreSQL replication, a public command that replaces a production SQLite database, atomic object-level restore, or high availability.

Use database-native or infrastructure tools when you need those recovery goals.

## Next steps

- [Back up and restore relational databases](/guides/data/backup-restore/)
- [Operate SQLite with continuous backup](/guides/data/sqlite/)
- [Export and import](/guides/stacks/export-import/)
- [Stack layout](/reference/stack-layout/)
