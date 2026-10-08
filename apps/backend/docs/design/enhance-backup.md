# Backups v2: two independent methods, app backups restore by cloning

Status: proposal, decisions confirmed 2026-10-06. Builds on `restic-backup.md` (implemented in b0c4efb). Nothing here
is built yet.

Confirmed decisions:

1. In-place restic restore is removed. App backups restore only into a new app.
2. The in-container home path is a new `apps.home_path` column (schema v3, first migration).
3. Secrets in snapshots (env values, DB passwords) are a per-app option, off by default.
4. A clone is left **stopped** so the operator can check it before starting.
5. The "All backups" database schedule stays as it is. The two methods are independent and neither changes the other.
6. SQLite bindings get a new path in the clone. The operator updates hard-coded paths. No compatibility mount.

## Goal

An operator picks the backup method that fits each app, or uses both:

| Method | For | Contains | Restores |
| --- | --- | --- | --- |
| **Database backup** (existing, unchanged) | Modern apps whose user files already live on S3 or other object storage | Compressed dumps of selected databases | In place, per database (`replace <db>`) |
| **App backup** (restic, changed) | Older apps that keep uploads, generated files and config in the home | Home + plain DB dumps + minicrond jobs DB + app spec (+ secrets when enabled), as one encrypted snapshot | Always **into a new app** (a clone), never over the live one |

The two methods share nothing: separate settings, schedules, run history, storage and restore paths. Turning one on or
off never changes the other.

The restored clone should run with as little editing as possible. Old apps hard-code paths and credentials in files
like `wp-config.php`, `config/database.php`, crontabs and minicrond jobs, so the clone keeps the source's in-container
home path, keeps the database password when the snapshot includes secrets, and maps database names in a predictable
way.

## Decisions at a glance

| Question | Decision |
| --- | --- |
| Database backup | No behaviour change. Only the app's Backup tab changes to show both methods side by side. |
| In-place restic restore | **Removed** (API, operation, UI). Database backups remain the way to roll back one database in place. |
| What app backup captures from minicrond | `minicron.db` (jobs, workers, settings) through `.backup`. `minicron-logs.db` and its `-wal`/`-shm` are excluded. |
| Restore order | Create + provision, databases, home, minicrond DB. The clone stays stopped. |
| Database names | `<src_slug>_<suffix>` becomes `<new_slug>_<suffix>` (`shop_main` → `shop_staging_main`). The dumps hold no `USE`/`\connect`, so they import into any name. |
| Database password | Copied from the snapshot when it includes secrets. Otherwise a new one is generated. |
| Database username | The source username (`u<srcAppId>`) is reused when it's free (§4d). Otherwise the clone gets a new one and the preview says which value to change. |
| In-container home path | The clone keeps the source's path (`/home/shop`). On the host it lives in `homes/<new-slug>`. Stored in `apps.home_path`. |
| Secrets in the snapshot | Per-app option **Include secrets**, off by default. When on, `bento/secrets.json` holds unredacted env values and binding passwords. |
| SQLite bindings | New file directory and name in the clone. `BENTO_DB_n_DATABASE` has the new path; hard-coded paths are changed by the operator. |
| Domains | Never moved automatically. Listed in the preview; the operator attaches them after checking the clone. |
| After restore | The clone is **stopped** and unpublished. The result lists what to check, with a **Start** button. |

## 1. Two methods, one Backup tab

The app's **Backup** tab shows two cards:

- **Database backup.** Lists the stack backup schedules that cover this app (scope `all`, `app`, or `database`), the
  last run per database, **Back up now**, and **Add schedule for this app** (prefills scope `app`). It's the existing
  data, shown per app. No new database-backup behaviour.
- **App backup.** Shows the restic repository, schedule, retention, what's included, the **Include secrets** switch,
  the last snapshot, and **Restore into a new app**.

At the top, a short chooser explains the choice:

> Files on S3 or another object store → **Database backup** is enough.
> Uploads or generated files in the app home → **App backup**.
> Both on → database dumps for fast in-place rollback, app backups for full recovery or migration.

Independence:

- `backup.*` (database) keeps its global claim and `locks/backup.lock`. `restic.*` keeps its own claims and the
  `backup-restic` pool. Settings keys and schedule kinds don't overlap (`backup` vs `app-backup`).
- Disabling app backup leaves database schedules running, and the other way round. The "All backups" scope keeps
  covering every app's databases whether or not app backup is on.
- Removing an app stops both. Retained data is listed separately (dump files, restic key).

## 2. Backup format v2 (what an app backup writes)

Changes to `restic.backup` (`operations/restic.go: resticBackup`):

1. **minicrond:** `homeSQLiteFiles` takes only `.local/share/minicron/minicron.db` from the minicrond data dir
   (plus the operator's `sqlitePaths`). The exclude file adds `minicron-logs.db`, `minicron-logs.db-wal`,
   `minicron-logs.db-shm`, `minicron.sock`. The live `minicron.db*` is excluded as today (the `.backup` copy replaces it).
2. **manifest.json** (`ResticFormatVersion` 2) adds:
   - `homePath`: the source container home (`app.ContainerHome()`, so a clone of a clone keeps the original path);
   - per dump: `binding` (index), `suffix` (`main`, `analytics`), `username`;
   - per SQLite binding: `fileId`, `fileName` (`<slug>.db`);
   - `minicron`: `{ "db": "home-sqlite/.local/share/minicron/minicron.db" }` or absent;
   - `secrets`: `true` when `secrets.json` is in the snapshot;
   - `stackId` stays; snapshots also get the tag `stack=<stackId>` and `forget` filters on `app=<id>,stack=<id>`.
3. **secrets.json**, only when the app's `includeSecrets` setting is on (`0600` in staging, inside the snapshot):
   `{ "env": [...unredacted...], "bindings": [{ "index": 0, "password": "…" }] }`. `app.json` always keeps its
   redacted env so a preview never shows secrets. The clone operation reads `secrets.json` and never returns it.
4. A run where restic exits with 3 (unreadable files) is recorded as `partial`, not OK.

### Include secrets (per-app option)

- New field `includeSecrets` (bool, default `false`) in `domain.ResticSettings`, edited on the App backup card with
  this text: "Store env values and database passwords in the encrypted backup, so a restored app keeps them. Anyone
  with a repository key can read them."
- It applies to snapshots taken after the change. The snapshot list shows a small "secrets" badge on snapshots that
  include them, and the clone preview says which values will be kept and which will be new.
- Without secrets: env values with secret-looking names come back empty and are listed as "set after restore"; DB
  passwords are generated fresh.

Format 1 snapshots (taken before this change) still restore, as snapshots without secrets. Their home path is
`/home/<manifest.slug>`, and the minicrond DB is taken from `home-sqlite/` if it's there.

## 3. In-container home path

New column `apps.home_path` (container path). NULL means `/home/<slug>`, as today.

- Schema version 3 adds the store's first migration: `ALTER TABLE apps ADD COLUMN home_path TEXT`, run in one
  transaction when opening a v2 database, then `PRAGMA user_version = 3`. `store.CheckCompatible` accepts 2 (to
  migrate) and 3. It still refuses foreign, older-than-2, and newer databases. Stack import accepts a v2 `state.db`
  and migrates it the same way.
- `domain.App.HomePath` (string). `App.ContainerHome()` returns it when set. Every current caller already goes through
  it: `runtime/spec.go` (mount target, `HOME`), `materialize.go` (open_basedir, passwd home), `api/terminal.go`
  (`HISTFILE`), `operations/git.go` (deploy script), `operations/apps.go` (mount check), `backup/restic.go` (SQLite
  job), `api/convert.go` (DTO).
- The host path stays `homes/<slug>` (`Layout.AppHome(slug)`). Only the mount target changes.
- Validation: `/home/<slug-shaped name>`. It's set only by the clone operation and can't be edited afterwards. App
  details show it: "Home inside the container: /home/shop (kept from the backup)".
- The fingerprint already hashes mounts and env, so a change replaces the container. Bump `specVersion` once for the
  new field.
- `USER` and the passwd name stay the new slug. Only the home directory differs.

Two apps on one stack may share a container home path. That's fine because each container sees only its own mount.

## 4. Restore into a new app

### 4a. Entry points

- **Same stack:** app → Backup → Snapshots → **Restore into a new app**. The repository and key are the source app's.
- **Another stack:** Apps → New → **From app backup**. The operator enters the rclone remote path and a key (from
  **Add key** on the source). The key stays in a pending file for the duration of the clone and is deleted at the end
  unless "keep backing up to this repository" is chosen.

### 4b. Inspect (`restic.inspect`, read-only, claims `restic:<repo>` shared)

`restic dump <snap> /backup/bento/manifest.json` and `app.json` → preview:

- source slug, stack, time, size, format version, whether secrets are included;
- runtime (PHP/HTTP version), resources, env keys (values hidden), which env values will be empty;
- home path that will be kept (`/home/shop`);
- database plan, one row per database: `shop_main (mysql84, 8.4)` → `shop_staging_main`, user kept or new, password
  kept or new;
- SQLite bindings with their new path, minicrond jobs DB present, domains (with "in use here" flags);
- blocking problems: no service of that engine and exact version on this stack, slug taken, a retained home with that
  slug, unknown format version.

### 4c. Clone (`app.clone-from-backup`, global claim)

Input: snapshot, new slug, options (`keepUsername`, `backupAfter: none|new-repo`). Steps:

```
1. create      new app row: new ID, new UID (ledger, never reused), runtime/resources/env from app.json
               (+ secrets.json if present), HomePath = manifest.homePath, desired stopped, unpublished
2. bindings    same engine; service = same name if it exists with the exact version, else the one the operator chose;
               databases = <new_slug>_<suffix>; password = source if in secrets.json, else new;
               username = source if free (see 4d), else u<newId>
3. provision   existing provision(): home dir, SQLite dirs, users + grants, Redis ACL. Refuse if any target database
               already exists or isn't empty.
4. download    restic restore <snap> --target /restore   (restore container, home not mounted)
5. databases   RestoreRelational per dump into the mapped name; SQLite bindings: copy into the new file dir as
               <new-slug>.db
6. home        ChownTree to the new UID (no symlink follow), replace the empty provisioned home with the restored tree,
               keep the new identity sidecar
7. scheduler   home-sqlite/.local/share/minicron/minicron.db → home, app UID, 0600; no logs DB (minicrond creates it)
8. git         copy repo URL, branch, deployed commit; generate a new deploy key (public key shown); no webhook
9. finish      image.prepare so the first start is quick; app stays stopped and unpublished
```

The operation result is a checklist for the operator: DB names that changed, a new DB user or password if any, empty
env values, the new SQLite paths, the deploy key to add, and domains to attach. The dialog ends on that checklist with
**Open app** and **Start** buttons. Nothing starts until the operator presses Start.

Failure at any step removes what this operation created: app → `remove` + `prune` (UID becomes `burned`), databases
and user dropped only if this operation created them. The source app and its repository are never written to, apart
from restic's own lock files.

### 4d. Keeping the database username

Reusing `u<srcAppId>` is allowed only if all of these hold, otherwise a new username is used and the preview says so:

- no user with that name exists on the target service;
- no binding of any app on this stack uses it;
- no `retired_apps` retained-relational record lists it (a later `prune` of that retired app would drop the user that
  now belongs to the clone).

On the same stack while the source exists, this always falls back to a new username. Across stacks it usually
succeeds. With secrets included, a hard-coded `DB_USERNAME`/`DB_PASSWORD` then keeps working and only `DB_DATABASE`
changes. `DB_HOST` is the service name (`mysql84`), so it also keeps working when the target stack names its service
the same.

### 4e. What else keeps working, and what doesn't

| Thing | Clone behaviour |
| --- | --- |
| Paths under the home (`/home/shop/app/...`) in code, configs, minicrond jobs, `deploy.sh` | Work unchanged (home path kept). |
| Env vars | Copied. Secret-looking values only when the snapshot includes secrets, otherwise left empty and listed. |
| Bento-provided `DB_*`, `BENTO_DB_n_*`, `REDIS_*` env | Regenerated for the clone, so apps reading env need no change. |
| Hard-coded DB name | Must change: `shop_main` → `shop_staging_main`. In the checklist. |
| Hard-coded DB password | Works when the snapshot includes secrets. Otherwise in the checklist. |
| Hard-coded DB user | Works when the username is kept (4d). Otherwise in the checklist. |
| SQLite binding path `/var/lib/bento/sqlite/<fileId>/<slug>.db` | New path; `BENTO_DB_n_DATABASE` has it. Hard-coded paths: in the checklist. |
| minicrond jobs and workers | Restored from `minicron.db`; log history starts empty. Config-owned `zz-bento-*` tasks are regenerated. |
| Redis keys | Not backed up (cache). The clone gets prefix `<new-slug>:`. |
| Domains, TLS, publication | Not moved; attach after checking the clone. |
| Webhook, deploy key | New ones; update the git provider. |
| App backup settings | Not copied. The clone starts with none, or a new repository if `backupAfter: new-repo`. |

## 5. API, CLI, UI

API (DTOs + `make -C apps/backend generate-types`):

- `PUT /api/v1/apps/{id}/restic` gains `includeSecrets`.
- `POST /api/v1/apps/{id}/restic/inspect` `{snapshot}` → 202 + op; result is the preview.
- `POST /api/v1/apps/{id}/restic/clone` `{snapshot, slug, keepUsername, backupAfter}` with confirmation
  `clone <new-slug>` → 202 + op.
- `POST /api/v1/apps/restore-from-backup/inspect` `{repository, key, snapshot?}` and
  `POST /api/v1/apps/restore-from-backup` (same body as clone + repository/key) for another stack.
- App DTO gains `homePath` (read-only).
- Remove `POST /api/v1/apps/{id}/restic/restore`, `ResticRestoreRequest`, and `handleResticRestore`.

CLI: `bento app clone-from-backup <src-slug> --snapshot <id|latest> --slug <new>`,
`bento app restore-from-backup --repo <remote:path> --key-file <f> --slug <new>`.

UI:

- Backup tab: two cards plus the chooser (§1), **Include secrets** switch on the App backup card.
- Snapshot list: "secrets" badge; **Restore into a new app** opens a 3-step dialog: slug + options → preview (from
  inspect) → confirm, then the checklist with **Open app** / **Start**.
- Apps → New: **From app backup** opens the same dialog, with a repository + key step first.
- App details: "Home inside the container" row when `homePath` is set; a "Cloned from shop @ 2026-10-05 03:30" note
  (stored in the operation result and app events, no new column).

## 6. Safety rules carried forward

- The clone never touches the source app, its home, its databases, or its key.
- Target databases must be new and empty; provisioning refuses an existing one (existing rule).
- Restored trees are untrusted: `ChownTree` without following symlinks, containment checks, no device nodes. `app.json`,
  `manifest.json`, `secrets.json` are decoded strictly with `DisallowUnknownFields`, and the result is validated like
  operator input.
- `secrets.json` content never reaches events, logs, API responses or argv. Values are added to the operation's
  redactor as soon as they're read.
- The cross-stack key is held in a pending file and deleted at the end unless it's adopted.
- Free disk check before download: snapshot size from `restic stats --mode restore-size` + 10 %.
- The schema migration runs in one transaction and only from version 2. A failed migration leaves the database at v2.

## 7. Files touched

| Area | Files |
| --- | --- |
| Domain | `domain/model.go` (`HomePath`, `ContainerHome`), `domain/restic.go` (excludes, `includeSecrets`, manifest v2 types), `domain/validate.go` |
| Store | `store/store.go` (schema v3, migration runner, `CheckCompatible`), `store/repo.go` (read/write `home_path`), `stack` import path |
| Runtime | `runtime/spec.go` (`specVersion`), tests in `spec_test.go`, `materialize_test.go` |
| Operations | `operations/restic.go` (format v2, secrets.json, minicrond selection, stack tag, partial status, remove restore), new `operations/restic_clone.go` (inspect + clone), `operations/claims.go`, `operations/apps.go` (handler registration) |
| API | `api/restic.go`, `api/server.go`, `api/convert.go`, `api/dto/dto.go`, generated `apps/web/src/api/generated/types.ts` |
| CLI | `cmd/bento` client commands |
| Web | `features/applications/BackupPanel.tsx` (two cards, chooser, secrets switch, clone dialog), `features/applications/NewApp*` (from backup), `features/applications/ApplicationDetail.tsx` (home path row), `features/backups/BackupsPage.tsx` |
| Docs | `docs/.../guides/data/app-backups.md`, `backup-restore.md` (chooser), `reference/rest-api.md`, `reference/stack-layout.md`, `apps/backend/docs/architecture.md` (schema v3, clone) |

## 8. Phases

1. **Format v2:** minicrond main DB only, manifest v2, `includeSecrets` setting + `secrets.json`, stack tag, `partial`
   status. Remove in-place restic restore (API + operation + UI). Unit tests for excludes, manifest round trip, secrets
   on/off.
2. **Home path:** schema v3 migration (incl. import of a v2 `state.db`), `HomePath` through `ContainerHome()`,
   spec/materialize/passwd tests, `specVersion` bump.
3. **Clone on the same stack:** inspect + clone operations, DB name mapping, username rule, rollback on failure,
   result checklist, UI dialog. Integration test: back up `shop` (MySQL + SQLite + a minicrond job using
   `/home/shop/app`), clone to `shop-copy`, verify it's stopped, start it, check data, minicrond job present and
   runnable, home path `/home/shop`. Run once with secrets on and once off.
4. **Clone from another stack:** Apps → New → From app backup, pending key handling, cross-stack integration test with
   two disposable roots and one local rclone remote.
5. **Backup tab with two cards** and the chooser, docs.

Checks per phase: `make -C apps/backend ci`, `make -C apps/backend test-integration` (Docker), `bun run fmt:check &&
bun run lint && bun run check && bun run web:build`. Don't claim Docker, arm64, or real cloud-remote results unless they ran.
