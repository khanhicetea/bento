---
title: Operate SQLite and Litestream databases
description: Choose local SQLite with logical backups or SQLite continuously replicated by Litestream.
---

# Operate SQLite and Litestream databases

Bento exposes two file-database types so their backup behavior is explicit:

- **`sqlite`** is a simple local SQLite database. The app runner runs `VACUUM` every Sunday at 03:30 UTC through Supercronic. `bento backup` creates a consistent copy with SQLite's `.backup` command and stores it under `./backups/sqlite/<app>/`, compressed with Zstandard by default (or gzip with `--gzip`).
- **`litestream`** is SQLite with continuous off-host replication to an S3-compatible object store. Bento runs one stack-wide Litestream watcher for these databases.

Both types keep each database in a private app-ID directory. Existing bindings from versions where `sqlite` meant Litestream are read as `litestream`; their `.sqlite` files and S3 replicas do not move.

## Before you begin

- Use the supported native-Linux, rootful Docker Engine topology. The watcher relies on a constrained-root container to traverse private app UID directories; rootless Docker and user-namespace remapping are not supported for this feature.
- Prepare a private S3 bucket, region, access key, and secret key. Restrict the credentials to the backup bucket and the required object operations.
- Ensure the host can reach the S3 endpoint from Docker.
- Plan for SQLite's single-writer behavior. Use MySQL or PostgreSQL instead when your workload requires many concurrent writers or database-server features.

:::caution
Continuous replication reduces the recovery point after host or disk loss, but it is not a substitute for restore testing. Bento can export a replica to a new local database file; it does not replace a live SQLite database from a replica.
:::

## Create a SQLite app

Create a plain local SQLite app:

```sh
bento --stack /var/lib/bento app create demo \
  --domain demo.example.com \
  --database-engine sqlite
```

Create a continuously replicated SQLite app by selecting the explicit Litestream type:

```sh
bento --stack /var/lib/bento app create demo \
  --domain demo.example.com \
  --database-engine litestream
```

For plain SQLite, create a logical backup with:

```sh
bento --stack /var/lib/bento backup --app demo
bento --stack /var/lib/bento backup --app demo --gzip
```

The default artifact ends in `.sqlite.zst`; `--gzip` produces `.sqlite.gz`.

Bento creates one private database file and writes its container path and default busy timeout to `/home/demo/credentials/app.env` as `DB_DATABASE` and `SQLITE_BUSY_TIMEOUT`. Configure your framework to load that protected file, or copy only the needed values into its protected configuration. Bento does not automatically inject the file into PHP's process environment.

After your application loads those values, initialize PDO and enable write-ahead logging:

```php
<?php
$pdo = new PDO('sqlite:' . getenv('DB_DATABASE'));
$pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
$pdo->exec('PRAGMA journal_mode=WAL');
$timeout = (int) (getenv('SQLITE_BUSY_TIMEOUT') ?: '5000');
$pdo->exec("PRAGMA busy_timeout={$timeout}");
```

`journal_mode=WAL` persists in the database. Set `busy_timeout` on each connection so a short write conflict waits instead of immediately returning `database is locked`.

## Verify local operation

Inspect the app binding:

```sh
bento --stack /var/lib/bento app show demo
```

Exercise an application endpoint that writes and reads a row. Confirm the application reports `wal` for this query:

```sql
PRAGMA journal_mode;
```

Do not edit the generated credential file or move the database manually. Bento stores the durable file under:

```text
/var/lib/bento/sqlite/<app-slug>_<10-random-hex-chars>/<app-slug>.sqlite
```

Inside PHP and Litestream containers, the same file appears at:

```text
/sqlite/<app-slug>_<10-random-hex-chars>/<app-slug>.sqlite
```

## Configure S3 replication

Edit the selected stack root's protected `.env` and add:

```text
BENTO_LITESTREAM_ENABLED=true
S3_BUCKET_NAME=<bucket-name>
S3_REGION=<region>
S3_ACCESS_KEY_ID=<access-key-id>
S3_SECRET_ACCESS_KEY=<secret-access-key>
```

For an S3-compatible service that needs a custom endpoint, also set:

```text
S3_ENDPOINT=https://<object-store-endpoint>
```

Keep `.env` at mode `0600`. Do not put credentials on the command line or in an app environment file.

Enable the stack-wide directory watcher with the default 60-second recovery point objective and seven-day snapshot retention. The app argument selects the database used for the initial upload-and-restore proof; the resulting policy covers every managed SQLite database in the stack:

```sh
bento --stack /var/lib/bento sqlite backup enable demo
```

The command gracefully recreates the one watcher process so it loads the stack policy. It does not report success after upload alone: it waits for the selected app's remote synchronization, restores a temporary database from S3, runs a full SQLite integrity check, removes the temporary file, and records the app's verification time. New SQLite databases are discovered automatically without another restart.

## Verify the backup

Check desired policy and whether the shared Litestream container is running:

```sh
bento --stack /var/lib/bento sqlite backup status --app demo
```

Force current writes to reach the remote replica:

```sh
bento --stack /var/lib/bento sqlite backup sync --app demo
```

Run another restore and full integrity check:

```sh
bento --stack /var/lib/bento sqlite backup verify --app demo
```

Run `verify` regularly and after credential, bucket-policy, endpoint, or storage changes. Also monitor stale verification timestamps and failed Litestream service health externally.

Export the S3 replica into a new local SQLite database file:

```sh
bento --stack /var/lib/bento sqlite backup export \
  --app demo \
  --output /safe/recovery/demo.sqlite
```

Export runs Litestream's full integrity check, publishes the result with mode `0600`, and refuses to overwrite an existing destination. It never changes the application's live database. The same status, sync, verification, and export operations are available under **Manage SQLite** in `bento tui`.

`bento backup --app demo` confirms remote synchronization for a `litestream` app and does not create a local logical dump. For a plain `sqlite` app it uses SQLite's online `.backup` API and publishes a compressed artifact under `backups/sqlite/demo/`.

## Troubleshooting

**`BENTO_LITESTREAM_ENABLED is false`:** set it to `true` in the selected stack's `.env`, then retry enablement.

**A required S3 variable is missing:** set all four required `S3_*` values. Set `S3_ENDPOINT` only when the provider requires it.

**The application reports `database is locked`:** keep transactions short, set a connection-level busy timeout, and check for long-running or duplicate writers. SQLite still permits only one writer at a time.

**Synchronization or verification fails:** confirm Docker can resolve and reach the endpoint, check bucket policy and credentials, inspect the `litestream` Compose service logs, then rerun `sync` and `verify`. Treat the remote backup as unverified until both succeed.

**Litestream cannot traverse a private SQLite directory:** confirm the host uses rootful Docker without user-namespace remapping. The container runs as root with only `DAC_OVERRIDE`, `CHOWN`, and `FOWNER`; Bento does not make databases world-readable and does not use POSIX ACLs.

## Advanced

### Recovery policy

`--rpo` accepts `1s`, `10s`, or `60s`; the default is `60s`. Shorter intervals reduce the expected data-loss window but increase object-store requests and cost.

```sh
bento --stack /var/lib/bento sqlite backup enable demo \
  --rpo 10s --retention 168h
```

RPO, snapshot interval, and retention are stack-wide. Running `enable` again changes the one policy and gracefully recreates the watcher. Directory discovery itself does not require runtime registration or container recreation.

### Storage and isolation

The stack mounts only `./sqlite` at `/sqlite`; Litestream does not receive app-home mounts. Each app-ID directory remains owned by its app UID at mode `0700`. The watcher runs as root with all capabilities dropped except `DAC_OVERRIDE`, `CHOWN`, and `FOWNER`, which Litestream needs to traverse those directories and preserve database ownership when it creates WAL/SHM or metadata files. Transaction metadata lives on the separate durable `litestream-meta/` mount. No POSIX ACLs or shared backup UID are used.

This root process can read and modify every SQLite database in the stack. Its read-only root filesystem, SQLite-only mounts, isolated egress network, lack of published ports, and capability allowlist limit—but do not remove—that blast radius. PHP apps still share containers by PHP version, so Bento is not a hostile multi-tenant sandbox.

### Removal and recovery boundary

Removing an app retains its home and SQLite directory, so the directory watcher continues protecting the retained database. The separate interactive `app prune` operation permanently deletes the local SQLite directory; Litestream detects its removal and stops managing it. Remote S3 objects remain; remove them only under your object-store retention and incident-recovery policy.

`sqlite backup verify` restores to a temporary file, while `sqlite backup export` publishes a separate recovery file. Neither stops writers nor replaces production data. A production replacement restore must stop every writer, preserve the current database and WAL/SHM files, restore to a separate path, verify integrity, repair ownership and ACLs, and replace the database deliberately. That guarded workflow is not yet exposed by the Bento CLI.

## Next steps

- [Back up and restore relational databases](/guides/data/backup-restore/)
- [Review stack files and durability](/reference/stack-layout/)
- [Understand app isolation limits](/advanced/isolation-security/)
