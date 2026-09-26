---
title: Operate SQLite databases
description: Provision private SQLite files and create consistent logical backups.
---

# Operate SQLite databases

SQLite is an application-local database. Each binding has a private file in an app-owned directory under the stack root's `sqlite/` tree. App processes access the file through `/sqlite/<file-id>/<app>.db`.

Create an app with SQLite:

```sh
bento app add demo --domain demo.example.com --database-engine sqlite
```

An app may have multiple SQLite bindings; each has a distinct file ID. Use `bento app show demo` to inspect them. The files are owned by the app UID and have private permissions. Bento schedules a randomized weekly `VACUUM` through the app runner.

## Back up

SQLite's online `.backup` operation creates a consistent logical snapshot while the app remains online. Create a compressed artifact with:

```sh
bento sqlite backup demo
# For an app with multiple files:
bento sqlite backup demo --file <file-id>
```

`bento backup --app demo` and scheduled logical batches also include SQLite bindings. Artifacts live under the stack root's `backups/sqlite/` directory; copy them off-host and test recovery. You can use the scheduled rclone upload feature for newly created artifacts. A stack export also includes the live `sqlite/` tree, but a live filesystem archive is **not** a consistency-guaranteed SQLite backup.

## Recover

Stop application writers before replacing a production file. First restore a logical artifact to a separate location, check its integrity and application data, then plan the replacement with appropriate file ownership and permissions. Bento does not offer an automatic in-place SQLite restore. Preserve a copy of the original file and its WAL/SHM sidecars until recovery is complete.
