---
title: Backup and restore
description: Logical backups, schedule, rclone upload, and restore.
sidebar:
  order: 3
---

```bash
bento backup run [--app shop] [--compression zstd|gzip|none] [--upload]
bento backup list
bento backup restore --artifact shop/mysql-shop-20260101T020000Z.sql.zst --app shop --database shop
```

- One batch at a time. MySQL uses `mysqldump --single-transaction`, PostgreSQL `pg_dump`, SQLite the online
  `.backup` API in a scoped container that only sees that database directory.
- Each artifact is written privately and published atomically only if the dump succeeded and is non-empty.
- Retention (keep the newest N per database) runs only after a whole batch succeeds.
- Restore asks for `replace <database>` and only targets that app's own databases. It is not atomic per object; restore
  into a scratch database first when in doubt. SQLite restores require the app to be stopped.

## Schedule and upload

`bento backup schedule --json s.json` with `{"enabled":true,"cron":"30 2 * * *","compression":"zstd","retain":7,"rcloneRemote":"remote:bucket/bento"}`.
The backend evaluates the schedule. Slots missed while it was down are **recorded, not replayed**. Uploads run
`rclone copy` in a throwaway container that sees only `rclone/rclone.conf` and the new artifacts, read-only. A failed
upload keeps the local artifacts and marks the run.

Local backups are not disaster recovery until copied off the host and restore-tested.
