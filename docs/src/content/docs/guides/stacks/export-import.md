---
title: Export and import a stack
description: Transfer a complete Bento stack and its raw data volumes with explicit compatibility and security checks.
---

# Export and import a stack

Export supported stack files plus MySQL, PostgreSQL, and Redis volumes, then import them into an empty stack root. Plan downtime for data services.

## Before you begin

- Ensure the export destination is empty and outside the stack root.
- Use compatible CPU architecture and database image versions on the destination. Use logical restore for PostgreSQL major upgrades.
- Allocate enough space for uncompressed data plus archives.

:::caution
Export archives contain passwords, private keys, application files, and raw database data. Encrypt them in transit and at rest, and restrict access.
:::

:::caution
Stack export does not currently include the stack-root `sqlite/` directory. Use [SQLite continuous backup](/guides/data/sqlite/) and verify its remote restore separately. Do not copy a live SQLite database and its WAL/SHM files as an assumed-consistent backup.
:::

## Export

```sh
bento --stack /var/lib/bento stack export /srv/exports/production-2026-07-28
```

Bento checks named volumes, stops only running MySQL/PostgreSQL/Redis services needed for consistent copies, writes `stack.tar.gz` and one archive per volume, then restarts exactly those services. Web requests may fail while their databases are stopped.

Verify the directory contains `stack.tar.gz`, database service archives such as `mysql84-data.tar.gz`, and `redis-data.tar.gz`. Copy the complete directory securely.

## Import

The destination selected by `--stack` must not exist or must be empty. For a same-host clone, override both identity and ingress before startup:

```sh
bento --stack /srv/bento/clone stack import \
  /srv/exports/production-2026-07-28 \
  --name clone --ingress-mode bridge \
  --http-port 18080 --https-port 18443
```

Import rejects missing, unexpected, corrupt, or unsafe archives and existing destination volumes. It restores newly created volumes, renders, and runs Compose `up -d --build`.

## Verify

```sh
bento --stack /srv/bento/clone status
bento --stack /srv/bento/clone doctor
bento --stack /srv/bento/clone stack ingress show
```

Verify representative applications and imported MySQL/PostgreSQL/Redis data before retiring the source. Recover and verify SQLite separately; the current import does not restore it.

## Troubleshooting

On failed import, Bento cleans up only volumes it created. Read the reported archive or compatibility error, correct the destination, and retry with an empty root. If export fails, confirm data services were restored to their previous running state with `compose -- ps`.

## Advanced

`runtime/`, `locks/`, and `.asset-cache/` are intentionally omitted. Raw transfer preserves volumes but is not a database upgrade mechanism. Prefer regular logical dumps for portable, granular recovery.

## Next steps

- [Back up and restore databases](/guides/data/backup-restore/)
- [Run multiple stacks](/guides/stacks/multiple-stacks/)
- [Storage and recovery internals](/advanced/storage-recovery/)
