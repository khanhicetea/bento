---
title: Back up and restore databases
description: Create database backups, copy them off-host, and prove that you can restore them.
---

# Back up and restore databases

Create portable backups for MySQL, PostgreSQL, and plain SQLite apps. Then schedule them, copy them off the host, and test a restore before you replace production data.

Apps that use Litestream follow the [continuous backup workflow](/guides/data/sqlite/) instead.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/backup-and-restore-flow.svg
Alt: Database data flowing to an on-host dump, an encrypted off-host copy, a verification database, and finally an optional production replacement.
Show: A safe left-to-right sequence. Make the off-host copy and verification restore required checkpoints before the destructive production-replacement branch. Mark on-host-only backup as incomplete disaster recovery and mark replacement restore as non-atomic with planned downtime.
-->

## Before you begin

- Confirm the stack root and app database binding with `bento app show demo`.
- Ensure the selected MySQL or PostgreSQL service is running and healthy.
- Ensure the stack filesystem has enough free space for a new dump and restore staging.
- Install `crontab` if you plan to register scheduled backups.
- Keep an encrypted off-host destination ready. Database dumps contain application data and can contain secrets.
- Avoid application schema changes while creating a dump, and schedule replacement restores during an application outage.

:::caution
Bento writes logical dumps below the selected stack's on-host `backups/` directory. An on-host dump alone does not protect against host or disk loss. Configure and verify an off-host copy before treating the schedule as disaster recovery.
:::

## Back up one app

Back up every MySQL or PostgreSQL database recorded for `demo`:

```sh
bento backup --app demo
```

Bento uses Zstandard compression by default. It runs `mysqldump` or the matching `pg_dump` inside the selected database container. Bento publishes the final file only after the dump succeeds and contains data.

A typical path is:

```text
/var/lib/bento/backups/mysql84/demo/mysql84_demo_<timestamp>.sql.zst
```

A PostgreSQL path uses its service name, for example `postgres17`.

Back up only one recorded database when the app owns several:

```sh
bento backup \
  --app demo \
  --database demo_archive
```

Choose gzip for compatibility with a recovery environment that does not have Zstandard:

```sh
bento backup --app demo --gzip
```

Use `--none` for uncompressed plain SQL. Do not combine `--gzip` and `--none`.

The command reports each final path and byte size. Verify that the new file is non-empty and protected:

```sh
sudo find /var/lib/bento/backups -type f \
  \( -name '*.sql' -o -name '*.sql.gz' -o -name '*.sql.zst' \) \
  -printf '%m %s %p\n'
```

Bento writes managed dumps with mode `0600`. A file's existence proves dump completion, not application-level recoverability; the restore verification below provides stronger evidence.

## Back up every managed database

Run one batch across every recorded app database:

```sh
bento backup --all
```

Bento creates logical dumps for MySQL and PostgreSQL. For a plain `sqlite` binding, it uses SQLite's online `.backup` command, compresses the result, and publishes it under `backups/sqlite/<app>/`.

For a `litestream` binding, Bento waits for the stack-wide watcher to confirm synchronization instead of creating a local dump.

Bento runs only one logical backup batch at a time. If a later database or SQLite synchronization fails, earlier successful dumps remain. Bento does not apply relational retention to that failed batch.

Read the error, fix service health, storage, or SQLite replication, and run the batch again.

After a complete successful batch, Bento keeps the ten newest managed dumps for each service/database pair. It ignores unrelated files, symlinks, partial files, and names outside its managed dump pattern.

## Copy dumps off-host

Bento includes an isolated, profile-only rclone sidecar. Stack initialization creates the private `rclone/rclone.conf`; configure its remote interactively without installing rclone on the host:

```sh
bento rclone -- config
# Optional: inspect configured remotes
bento rclone -- listremotes
```

The sidecar receives only its configuration directory and a read-only `/backups` mount. `bento compose -- up` does not start it. Scheduled uploads keep each file's relative path below `backups/`.

You can also copy completed files with another encrypted backup or replication system immediately after creation. For example, with a preconfigured SSH destination:

```sh
rsync -a /var/lib/bento/backups/ \
  backupuser@backup.example.com:/srv/backups/bento/production/
```

Protect the destination as sensitive data. Verify the remote copy independently by comparing file counts, sizes, or cryptographic hashes, and test recovery from the off-host copy—not only from the original host path.

:::note
The stack's logical dumps cover managed relational databases. They do not include SQLite files, app homes, Redis data, certificates, desired state, or stack secrets. Back up those ownership layers separately.
:::

## Schedule on-host logical backups

Bento can add one stack-qualified block to the current host user's crontab. The scheduled command runs an all-database, Zstandard-compressed logical backup batch, including plain SQLite apps. Litestream remains the continuous-replication option.

Confirm the absolute installed binary path:

```sh
command -v bento
```

Register a daily run at 03:15 in the host's cron timezone:

```sh
bento backup schedule register \
  --schedule '15 3 * * *' \
  --bin /usr/local/bin/bento \
  --rclone-remote archive \
  --rclone-prefix bento/production
```

Use the absolute path returned by `command -v`. In this example, `archive` is the remote from `bento rclone -- config`, and the prefix is optional.

Each successful run uploads only its new files to `archive:bento/production/...`. Bento preserves unrelated crontab entries and keeps a separate marked block for each stack root. Omit both `--rclone-*` options if you want on-host-only scheduling.

Run the scheduled path now to test its database access, mounts, compression, and status recording:

```sh
bento backup schedule run
```

Inspect registration and the bounded last-run record:

```sh
bento backup schedule status
```

Confirm `registered: yes`, the intended schedule, `last run: succeeded`, a nonzero artifact count and byte total, and the expected on-host backup directory.

:::caution
A configured rclone destination is still only as durable as its provider, retention policy, and credentials. Monitor `backup schedule status`, verify remote file counts or checksums, and test recovery from the remote copy. SQLite synchronization uses its separately configured S3 replica.
:::

Remove only this stack's managed crontab block when needed:

```sh
bento backup schedule unregister
```

Unregistering leaves existing dumps and the last-run record in place.

## Restore to a verification database

Restore to a new database in the same app namespace before replacing the original. For app `demo`, use `demo_verify`:

:::caution
Restore is not object-level atomic. A failed import can leave a partial destination. Use a new, previously unused verification database name and do not direct application traffic to it until checks pass.
:::

```sh
bento restore \
  --file /var/lib/bento/backups/mysql84/demo/<dump>.sql.zst \
  --app demo \
  --target demo_verify
```

Replace `<dump>` with the exact finalized filename. The source may be `.sql`, `.sql.gz`, `.sql.zst`, or `.sql.zstd`. The target must equal the app slug or begin with `<slug>_`; Bento refuses names outside that namespace.

A successful restore records the new database in desired state. Bento enforces the app namespace, but a MySQL restore without `--replace` can import into an existing target.

Confirm that the verification name is unused. After a partial attempt, choose another clean name or deliberately remove the partial database before you retry.

For a MySQL-backed app, inspect it as the app account:

```sh
bento mysql shell \
  --app demo \
  --database demo_verify
```

For a PostgreSQL-backed app, use:

```sh
bento postgres shell \
  --app demo \
  --database demo_verify
```

Inside the client, verify application-specific invariants: expected tables, row counts, migration version, recent records, and representative queries. Exit without modifying the source database. A successful SQL import alone is not sufficient proof that the backup is usable by the application.

If the dump came from outside this stack, verify its engine and provenance. Bento rejects a known MySQL backup-directory path for a PostgreSQL app and vice versa, but a separately supplied plain-SQL file still requires operator validation.

## Replace an existing database

Replace only after the verification restore and application checks succeed.

:::caution
A replacement requires an application outage. Disable the app to remove its route, PHP pool, scheduler, and workers, and stop any external deployment or writer that is not supervised by Bento.
:::

```sh
bento app disable demo
```

:::danger
The next restore terminates relevant PostgreSQL sessions when applicable, drops the target database, creates it again, and imports the dump. The exact `--replace` value must match `--target`. The operation is destructive and non-atomic; failure can leave the production database partial or empty.
:::

For the original database `demo`:

```sh
bento restore \
  --file /var/lib/bento/backups/mysql84/demo/<dump>.sql.zst \
  --app demo \
  --target demo \
  --replace demo
```

Do not use a generic confirmation word. Bento requires the literal target database name.

After completion:

1. Run application-specific database checks as the app account.
2. Re-enable the app with `bento app enable demo`, then start any external writers deliberately.
3. Verify HTTP requests, background jobs, and recent logs.
4. Keep the pre-restore dump and incident notes until the recovery is accepted.

## Troubleshooting

**Backup says the database service or bind is unavailable:** render current assets and recreate the selected service so its backup bind is active:

```sh
bento render
bento compose -- up -d <service>
```

Replace `<service>` with `mysql84`, `postgres17`, or the service shown by `app show`. Inspect its logs if it does not become healthy.

**Another backup batch is running:** wait for it to finish. Do not delete the backup lock while a dump process may still be active.

**A dump fails or is empty:** Bento removes its partial file and does not publish an empty final artifact. Check database logs, free disk space, credentials, and the service's backup mount before retrying.

**Schedule registration or status cannot run `crontab`:** install and enable the host's cron utilities for the operator account. Bento does not silently replace a missing or inaccessible user crontab.

**The schedule is registered but no successful run appears:** execute `backup schedule run` interactively to expose the error. Confirm that the cron user can execute the absolute `--bin` path, access Docker, and read/write the stack root.

**Restore says the target is outside the app namespace:** choose the app slug or a name beginning with `<slug>_`, such as `demo_verify`.

**Restore rejects the replacement confirmation:** make `--replace` exactly equal to `--target`, including case.

**Restore fails after creating the destination:** treat the destination as partial. Do not point the app at it. For a verification target, choose a clean new name and retry after resolving the cause. For a failed production replacement, keep writers stopped and follow the recovery plan based on your verified dump.

**A `.zst` or `.gz` restore fails to decompress:** confirm that the extension matches the actual file encoding and that the dump is not truncated. Bento chooses decompression from the filename.

## Advanced

MySQL dumps use `--single-transaction --routines --triggers` through the local Unix socket. PostgreSQL dumps use the service's matching-major `pg_dump` with `--no-owner --no-acl`. During restore, Bento creates the target for the app role and reapplies its database isolation policy.

Use logical backups for a planned PostgreSQL major upgrade, not raw volume transfer. Moving an app to another database service remains an external migration, and the SQL must work with the target major version.

Dump and compression processes run inside the database container, writing a private `.partial` file to that service's bind-mounted backup directory. Bento requires a successful, non-empty result, sets mode `0600`, and renames it to the final path. This atomic publication prevents a failed current dump from masquerading as a complete artifact; it does not make a multi-database batch or restore atomic.

Restore accepts a source outside the service backup directory by copying it to a private temporary staging path under that service's backup tree. Bento removes that staging copy afterward. The original source remains operator-owned.

Scheduled runs record bounded, redacted status under `backups/.schedule/last-run.json` and suppress cron command output. `backup schedule status` is therefore the supported monitoring view, but external monitoring must still detect stale success timestamps and verify off-host replication.

## Next steps

- [Manage app availability before a replacement restore](/guides/apps/manage/).
- [Inspect database and service health](/guides/stacks/manage/).
- [Understand which stack layers require separate backups](/concepts/stacks/).
