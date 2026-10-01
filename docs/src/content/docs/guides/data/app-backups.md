---
title: App backups (restic)
description: Back up a whole app (files, databases, scheduler) into an encrypted restic repository and restore it here or on another stack.
sidebar:
  order: 4
---

An app backup is a [restic](https://restic.net) snapshot of one app: its home (code, `storage/`, uploads, `.env`),
consistent copies of its SQLite files and scheduler database, a plain dump of every bound database, and a description
of the app (`app.json`). Each app has its own encrypted, deduplicated repository on any rclone remote. After the first
run, a backup only uploads what changed.

Use it to keep point-in-time history of an app, or to move an app to another Bento. The
[database backups](/guides/data/backup-restore/) stay available as the lighter option.

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
2. Copies the scheduler database and the listed SQLite files in the home with `.backup`.
3. Writes `app.json` (runtime, resources, domains, bindings, git source). Env vars are included, but values whose
   names contain `_KEY`, `KEY`, `SECRET`, `PASSWORD`, or `TOKEN` are redacted. Credentials, IDs, and UIDs are never
   included.
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

Stop the app, open **Backup → Snapshots**, choose **Restore**, pick files and/or databases, and type
`restore <slug>`.

- **Files**: the backed-up paths replace the live ones. The previous files are moved, not deleted, to
  `homes/.pre-restore-<slug>-<time>/`. Delete that directory once you've checked the restore. Restored files are
  owned by this app's UID, whatever UID they had when backed up.
- **Databases**: each dump replaces a database bound to this app with the same name, or the app's only database of
  that engine when the snapshot has only one. The service version must match exactly (for example MySQL `8.4` or
  PostgreSQL `18`).

## Move an app to another stack

1. On the source app, **Access keys → Add key** (type `export <slug>`). Copy the new key.
2. On the target Bento, create the app with the same runtime and the same database engines and versions. Add the same
   rclone remote.
3. In the new app's **Backup** tab, set the same repository, then **Connect** with the key.
4. Restore a snapshot (files and databases), then start the app.
5. Remove the handover key on the source when you're done.

Don't let both apps back up to the same repository with different retention settings, since each would expire the
other's snapshots. Point the new app at a new repository once the move is done.

## Removal

Removing an app stops its backup schedule. The repository and the key file `secrets/restic/<app id>.key` are kept.
