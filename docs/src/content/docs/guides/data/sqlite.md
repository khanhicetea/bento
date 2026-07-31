---
title: Operate SQLite and Litestream databases
description: Choose local SQLite backups or continuous Litestream replication, then verify recovery.
---

# Operate SQLite and Litestream databases

Bento offers two SQLite types. Choose based on the backup behavior you need:

- **`sqlite`** keeps a local database and uses scheduled logical backups. The app runner runs `VACUUM` once a week between 00:00 and 04:59 local time. Bento spreads the jobs across stable randomized slots. `bento backup` uses SQLite's `.backup` command and stores a compressed copy under `./backups/sqlite/<app>/`.
- **`litestream`** continuously replicates SQLite to an S3-compatible object store. One stack-wide Litestream watcher protects all databases of this type.

Both types keep each database in a private app-ID directory. Bento reads older bindings—created when `sqlite` meant Litestream—as `litestream`. Their files and S3 replicas stay in place.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/sqlite-backup-options.svg
Alt: Plain SQLite using scheduled local backups compared with SQLite using continuous Litestream replication and restore verification.
Show: Two parallel lanes starting from an app SQLite file. The plain lane goes through .backup to compressed on-host files and then an operator-managed off-host copy. The Litestream lane goes through the shared watcher to S3, then to temporary restore and integrity check. Mark both production replacement paths as manual and guarded.
-->

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
bento app create demo \
  --domain demo.example.com \
  --database-engine sqlite
```

Create a continuously replicated SQLite app by selecting the explicit Litestream type:

```sh
bento app create demo \
  --domain demo.example.com \
  --database-engine litestream
```

For plain SQLite, create a logical backup with either the SQLite-specific command:

```sh
bento sqlite backup local demo
bento sqlite backup local demo --gzip
```

or include plain SQLite files in the engine-neutral batch command:

```sh
bento backup --app demo
```

The default artifact ends in `.sqlite.zst`; `--gzip` produces `.sqlite.gz`. Use
`--file <sqlite-file-id>` with the SQLite-specific command when an app has more than one local file.

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
bento app show demo
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
bento sqlite backup enable demo
```

The command recreates the single watcher so it loads the stack policy. An upload alone does not count as success. Bento waits for remote synchronization, restores a temporary database from S3, runs a full integrity check, removes the temporary file, and records the verification time.

The watcher discovers new SQLite databases without another restart.

## Verify the backup

Check desired policy and whether the shared Litestream container is running:

```sh
bento sqlite backup status --app demo
```

Force current writes to reach the remote replica:

```sh
bento sqlite backup sync --app demo
```

Run another restore and full integrity check:

```sh
bento sqlite backup verify --app demo
```

Run `verify` regularly and after credential, bucket-policy, endpoint, or storage changes. Also monitor stale verification timestamps and failed Litestream service health externally.

Export the S3 replica into a new local SQLite database file:

```sh
bento sqlite backup export \
  --app demo \
  --output /safe/recovery/demo.sqlite
```

Export runs Litestream's full integrity check and publishes the file with mode `0600`. It refuses to overwrite an existing destination and never changes the live app database.

The **Manage SQLite** screen in `bento tui` also provides local compressed backups and the Litestream status, sync, verify, and export operations.

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
bento sqlite backup enable demo \
  --rpo 10s --retention 168h
```

RPO, snapshot interval, and retention are stack-wide. Running `enable` again changes the one policy and gracefully recreates the watcher. Directory discovery itself does not require runtime registration or container recreation.

### Storage and isolation

The stack mounts only `./sqlite` at `/sqlite`; Litestream cannot access app-home mounts. Each app-ID directory stays owned by the app UID with mode `0700`.

The watcher runs as root but keeps only `DAC_OVERRIDE`, `CHOWN`, and `FOWNER`. Litestream needs these capabilities to enter private directories and preserve ownership when it creates WAL, SHM, or metadata files. Transaction metadata uses the separate durable `litestream-meta/` mount. Bento does not use POSIX ACLs or a shared backup UID.

This root process can read and modify every SQLite database in the stack. Its read-only root filesystem, SQLite-only mounts, isolated egress network, lack of published ports, and capability allowlist limit—but do not remove—that blast radius. PHP apps still share containers by PHP version, so Bento is not a hostile multi-tenant sandbox.

### Removal and recovery boundary

Removing an app retains its home and SQLite directory, so the directory watcher continues protecting the retained database. The separate interactive `app prune` operation permanently deletes the local SQLite directory; Litestream detects its removal and stops managing it. Remote S3 objects remain; remove them only under your object-store retention and incident-recovery policy.

`sqlite backup verify` restores to a temporary file, while `sqlite backup export` publishes a separate recovery file. Neither stops writers nor replaces production data. A production replacement restore must stop every writer, preserve the current database and WAL/SHM files, restore to a separate path, verify integrity, repair ownership and ACLs, and replace the database deliberately. That guarded workflow is not yet exposed by the Bento CLI.

## Next steps

- [Back up and restore relational databases](/guides/data/backup-restore/)
- [Review stack files and durability](/reference/stack-layout/)
- [Understand app isolation limits](/advanced/isolation-security/)
