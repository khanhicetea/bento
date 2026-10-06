---
title: Backup and restore
description: Logical backups, schedule, rclone upload, and restore.
sidebar:
  order: 3
---

Bento has two independent backup methods, both on an app's **Backup** tab. Turning one on or off never changes the other.

- Files on S3 or another object store → **Database backup** (this page) is enough.
- Uploads or generated files in the app home → [**App backup**](/guides/data/app-backups/).
- Both on → database dumps for fast in-place rollback, app backups for full recovery or migration.

Database backups restore in place, per database. App backups restore only into a new app.

```bash
bento backup run [--app shop [--database shop,shop_logs]] [--compression zstd|gzip] [--upload remote:bucket/path]
bento backup list
bento backup restore --artifact shop/mysql-shop-20260101T020000.000Z.sql.zst --app shop --database shop
```

- One batch at a time. MySQL uses `mysqldump --single-transaction`, PostgreSQL `pg_dump`, SQLite the online
  `.backup` API in a scoped container that only sees that database directory.
- Each artifact is written privately and published atomically only if the dump succeeded and is non-empty.
- Artifacts are compressed with zstd (default) or gzip. Older uncompressed artifacts can still be restored.
- Retention belongs to schedules: each schedule keeps the newest N files **per database that it made** (its files
  carry a `~<schedule>` tag in the name). A database whose dump failed keeps its older files. Manual backups are never
  pruned.
- Restore asks for `replace <database>` and only targets that app's own databases. It is not atomic per object; restore
  into a scratch database first when in doubt. SQLite restores require the app to be stopped.

## Schedule and upload

A stack can have any number of backup schedules, each with its own cron expression, databases (`all`, every database
of one app, or named databases of one app), files kept per database, compression, and upload destination:

```bash
bento backup schedule list                     # enabled, next run, last run and state
bento backup schedule create --json s.json
bento backup schedule update <id> --json s.json
bento backup schedule enable|disable|delete <id>
```

with `{"name":"shop hourly","enabled":true,"cron":"0 * * * *","scope":"database","appId":"<app id>","databases":["shop"],"compression":"zstd","retain":24,"rcloneRemote":"remote:bucket/bento"}`
(`scope` `all` needs no `appId`; `app` needs no `databases`). Deleting a schedule keeps its files.
The backend evaluates schedules every 30 seconds, reading cron fields in the **server's local time zone** (as
crontab does; the Schedules tab shows it, for example `UTC+07:00`). Slots missed while it was down are **recorded, not
replayed**. Uploads run
`rclone copy` in a throwaway container of the pinned `rclone/rclone` image; the host needs no rclone install. The
container sees the `rclone/` config directory and the new artifacts (read-only), nothing else. A failed upload keeps
the local artifacts and marks the run.

Uploads use `copy`, not `sync`: local retention never deletes anything from the remote. Expire old copies with the
bucket's own lifecycle rules.

## Configuring remotes

In the UI, open **Backups → Schedules**, edit a schedule, and press **Open rclone shell** in its Upload section. It is a throwaway
container of the same rclone image that mounts only `rclone/`, so you configure and check remotes with the normal rclone
CLI:

```sh
rclone config            # add a remote
rclone lsd myremote:     # check it
```

Then set the destination (for example `myremote:bucket/bento`) as the schedule's rclone remote and press **Test**,
which lists the destination without changing it. The card shows remote names and types only; credentials never leave
the host. Without the UI, edit `<stack>/rclone/rclone.conf` directly (root-owned, `0600`).

- **Google Drive, OneDrive, Dropbox:** answer `n` to "Use web browser to automatically authenticate?", run
  `rclone authorize "<backend>"` on a computer with a browser, and paste the token. The config directory is writable
  so refreshed tokens are saved.
- **Encrypted backups:** add a `crypt` remote that wraps your storage remote and upload to it. Files are encrypted
  before they leave the host. Keep the crypt passwords somewhere else too: without them the uploads cannot be read.
- **No config password:** an encrypted `rclone.conf` cannot be unlocked by unattended uploads, so Bento refuses it.
  Remove it with `rclone config` → `s) Set configuration password` → `u) Unencrypt configuration`.
- `rclone/rclone.conf` holds credentials and is part of `bento export` archives; treat exports as secret.

Local backups are not disaster recovery until copied off the host and restore-tested.
