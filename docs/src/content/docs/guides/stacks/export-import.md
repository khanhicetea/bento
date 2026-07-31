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
Stack export includes the stack-root `sqlite/` directory mechanically, but it does not make a live SQLite file and its WAL/SHM files consistency-safe. Use SQLite's logical `.backup` or [Litestream continuous backup](/guides/data/sqlite/) and verify recovery separately.
:::

## Export

```sh
bento stack export /srv/exports/production-2026-07-28
```

Bento checks named volumes, stops only running MySQL/PostgreSQL/Redis services needed for consistent copies, writes `stack.tar.gz` and one archive per volume, then restarts exactly those services. Web requests may fail while their databases are stopped.

Verify the directory contains `stack.tar.gz`, database service archives such as `mysql84-data.tar.gz`, and `redis-data.tar.gz`. Copy the complete directory securely.

## Import

Select a destination root that does not exist or is empty. For a same-host clone, override both identity and ingress before startup:

```sh
export BENTO_STACK_ROOT=/srv/bento/clone
bento stack import \
  /srv/exports/production-2026-07-28 \
  --name clone --ingress-mode bridge \
  --http-port 18080 --https-port 18443
```

Import rejects missing, unexpected, corrupt, or unsafe archives and existing destination volumes. It restores newly created volumes, renders, and runs Compose `up -d --build`.

## Verify

```sh
bento status
bento doctor
bento stack ingress show
```

Verify representative applications and imported MySQL/PostgreSQL/Redis data before retiring the source. Also verify every imported SQLite database from a logical backup or Litestream recovery point; raw inclusion in `stack.tar.gz` is not proof of consistency.

## Troubleshooting

On failed import, Bento cleans up only volumes it created. Read the reported archive or compatibility error, correct the destination, and retry with an empty root. If export fails, confirm data services were restored to their previous running state with `compose -- ps`.

## Advanced

`runtime/`, `locks/`, and `.asset-cache/` are intentionally omitted. Raw transfer preserves volumes but is not a database upgrade mechanism. Prefer regular logical dumps for portable, granular recovery.

## Next steps

- [Back up and restore databases](/guides/data/backup-restore/)
- [Run multiple stacks](/guides/stacks/multiple-stacks/)
- [Storage and recovery internals](/advanced/storage-recovery/)
