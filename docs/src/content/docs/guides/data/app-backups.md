---
title: App backups (restic)
description: Back up a whole app (files, databases, scheduler) into an encrypted restic repository and keep point-in-time history of it.
sidebar:
  order: 4
---

An app backup is a [restic](https://restic.net) snapshot of one app: its home (code, `storage/`, uploads, `.env`),
consistent copies of its SQLite files and scheduler database, a plain dump of every bound database, and a description
of the app (`app.json`). Each app has its own encrypted, deduplicated repository on any rclone remote. After the first
run, a backup only uploads what changed.

Use it to keep point-in-time history of an app. The
[database backups](/guides/data/backup-restore/) stay available as the lighter option.

## The Backup tab

An app's **Backup** tab has two cards, one per method, under a short chooser:

- Files on S3 or another object store → **Database backup** is enough.
- Uploads or generated files in the app home → **App backup**.
- Both on → database dumps for fast in-place rollback, app backups for full recovery or migration.

The **Database backup** card lists the schedules that cover the app (scope all, app, or database), the last dump of
each database, **Back up now**, and **Add schedule for this app** (scope app, prefilled). The **App backup** card holds
the repository, schedule, retention, inclusions, the **Include secrets** switch, the last snapshot, and **Restore into
a new app**. The methods share no settings, schedules, history or storage; disabling one leaves the other running.

## Set up

1. Add an rclone remote (**Backups → Schedules → Open rclone shell**, then `rclone config`).
2. Open the app's **Backup** tab and set the repository to `remote:path`, for example `b2:bento/apps/shop`. Use one
   path per app.
3. Choose what to back up:
   - **Paths**: relative to the app home. `.` (the whole home) is the safest choice, especially for older sites
     whose `storage/` or upload directories are ignored by git.
   - **Excludes**: restic patterns. With a `/` they start at the home (`app/storage/framework/cache`); without one
     they match any name (`node_modules`). Prefix `!` to re-include. A directory containing a `.nobackup` file is
     skipped. **Skip caches** excludes `.cache`, npm and Composer caches, and directories tagged with `CACHEDIR.TAG`.
   - **SQLite files in the home**: listed files are copied with SQLite's `.backup` instead of being read while
     they're live. The scheduler's (minicrond) database is always handled this way.
   - **Keep snapshots**: hourly, daily, weekly, and monthly counts (restic `forget --keep-*`).
   - **Schedule**: a cron expression in the server's time zone.
4. Press **Create repository**. The key is shown **once**. Store it somewhere safe: it decrypts every backup of this
   app, and if every key of the repository is lost, the backups cannot be recovered.

## What a backup does

The app keeps running. In order:

1. Dumps every bound MySQL/PostgreSQL database as plain SQL (`--single-transaction` for MySQL) and copies SQLite
   bindings with `.backup`. Databases go first, so the files captured afterwards include every upload the dumped
   rows can reference.
2. Copies the scheduler's jobs database (`minicron.db`) and the listed SQLite files in the home with `.backup`.
3. Writes `app.json` (runtime, resources, the app's Ingress host names, bindings, git source). Env vars are included, but secret-looking
   values are redacted: names containing `PASSWORD`, `PASSWD`, `PASSPHRASE`, `SECRET`, `TOKEN` or `CREDENTIAL`;
   names with a word `PASS`, `PWD`, `PW`, `AUTH`, `CREDS`, `PRIVATE`, `SALT`, `COOKIE`, `CERT`, `DSN` or `APIKEY`;
   names ending in `KEY` or `KEYS` (`APP_KEY`, not `CACHE_KEY_PREFIX`); and any value that is a URL with a password
   (`DATABASE_URL=mysql://user:pw@…`). Credentials, IDs, and UIDs are never included.
4. Runs `restic backup` of the home and those files as one snapshot, then applies retention. It prunes at most once a
   week.

restic runs in a throwaway container of a pinned image (the rclone image plus a checksum-verified restic). The
container can read the app home but not write to it, sees only the `rclone/` config, and gets the repository key as
a read-only file. The key never appears in arguments, logs, or API responses. App and tool containers never see
`rclone.conf`.

## Monitoring

**Backups → App backups** lists every app with backups set up: its schedule and next run, the last backup and its
result, the snapshot count, the data added by the last run, what is backed up, the last verification, and the
repository. A failed last run is highlighted with its error. **Backups → Runs** shows app backup runs next to
database backups: the last 50 per app, failures included, each with its trigger, duration, files, data added, and
snapshot. **Backups → Schedules** shows app backup schedules as read-only tiles; edit them in the app's **Backup** tab.

## Remote permissions

restic needs to **read, write, and delete** objects under the repository path: it deletes its lock file after every
command and deletes pruned data. Before creating a repository, Bento writes and deletes a probe object and refuses a
remote that can't do both. With S3, grant `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`, and `s3:ListBucket` on
the bucket and path. If a run warns that restic couldn't delete its lock, fix the permission, then press
**Remove stale locks** (`restic unlock`: removes only locks older than 30 minutes or left by finished processes).

A credential that can delete could also be used by an attacker on the host to delete the backups. Make deletes
reversible on the storage side: see [Protect app backups from deletion](/guides/data/protect-app-backups/).

## Restore

An app backup is never restored over the live app. It always becomes a new app (a clone) that is left stopped. Use
[database backups](/guides/data/backup-restore/) (`replace <db>`) to roll back a single database in place.

The snapshot is downloaded while other operations keep running (one clone at a time). Creating the new app, its
databases and its files then runs alone: operations submitted meanwhile wait until the clone finishes. If the backend
stops during a clone, it removes the partly created app, the downloaded snapshot and leftover job containers when it
starts again; submit the clone again.

Changing the repository of an app whose repository is set up needs the confirmation `disconnect <slug>`. The old
repository and its snapshots are not touched, and the key Bento held for it is kept on the server under
`secrets/restic/`. Pruning a removed app deletes its app backup settings and those keys, never the repository.

### From another stack

To restore on a different stack, add the repository's rclone remote to that stack's rclone config, create a key on the
source app (**Add key**) and use **Apps → From app backup**, or:

```bash
bento app restore-from-backup --repo s3:bucket/shop --key-file ./shop.key --slug shop-staging
```

The key is held in a private file while the clone runs and is deleted afterwards. Choose
`--backup-after same-repo` to keep backing up to that repository with that key instead (the new app's schedule stays
off); `new-repo` prepares a separate repository path without keeping the key. The clone needs a MySQL or PostgreSQL
service of the same engine and version on the target stack. Across stacks the source's database user is reused when it
is free (`--keep-username`).

## Include secrets

By default a snapshot holds no secrets: `app.json` redacts env values with secret-looking names, and database
passwords are not stored. Turn on **Include secrets** in **Backup settings** to also store `secrets.json` (unredacted
env values and database passwords) in the encrypted snapshot, so a restored app can keep them. Anyone with a
repository key can read them. The setting applies to snapshots taken after the change.

## Snapshot format

Each snapshot has a `bento/` directory with `manifest.json` (format 2: the source's in-container home path, each
dump's binding, database suffix and user, SQLite file ids, the scheduler database, and whether `secrets.json` is
present), `app.json`, the dumps, and optionally `secrets.json`. Snapshots are tagged `app=<id>`, `stack=<id>`, `slug`,
and `trigger`; retention only touches snapshots with both the app and stack tags, so snapshots taken before this
format (without a `stack` tag) are no longer expired by retention. Only the scheduler's `minicron.db` is backed up;
its log database and socket are skipped. If restic can't read some files (exit code 3) the run is recorded as
**partial**, not OK.

## Removal

Removing an app stops its backup schedule. The repository and the key file `secrets/restic/<app id>.key` are kept.
