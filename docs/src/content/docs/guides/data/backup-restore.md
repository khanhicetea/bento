---
title: Backup and restore
description: Logical backups, schedule, rclone upload, and restore.
sidebar:
  order: 3
---

```bash
bento backup run [--app shop] [--compression zstd|gzip|none] [--upload]
bento backup list
bento backup restore --artifact shop/mysql-shop-20260101T020000.000Z.sql.zst --app shop --database shop
```

- One batch at a time. MySQL uses `mysqldump --single-transaction`, PostgreSQL `pg_dump`, SQLite the online
  `.backup` API in a scoped container that only sees that database directory.
- Each artifact is written privately and published atomically only if the dump succeeded and is non-empty.
- Retention (keep the newest N per database) runs only after a whole batch succeeds.
- Restore asks for `replace <database>` and only targets that app's own databases. It is not atomic per object; restore
  into a scratch database first when in doubt. SQLite restores require the app to be stopped.

## Schedule and upload

`bento backup schedule --json s.json` with `{"enabled":true,"cron":"30 2 * * *","compression":"zstd","retain":7,"rcloneRemote":"remote:bucket/bento"}`.
The backend evaluates the schedule every 30 seconds, reading cron fields in the **server's local time zone** (as
crontab does; the Schedule tab shows it, for example `UTC+07:00`). Slots missed while it was down are **recorded, not
replayed**. Uploads run
`rclone copy` in a throwaway container of the pinned `rclone/rclone` image; the host needs no rclone install. The
container sees the `rclone/` config directory and the new artifacts (read-only), nothing else. A failed upload keeps
the local artifacts and marks the run. `--upload` is refused while no remote is set.

Uploads use `copy`, not `sync`: local retention never deletes anything from the remote. Expire old copies with the
bucket's own lifecycle rules.

## Configuring remotes

In the UI, open **Backups → Schedule** and press **Open rclone shell** on the rclone remotes card. It is a throwaway
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
