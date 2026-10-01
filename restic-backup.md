# App-scoped restic backups: plan

Status: phases 1–4 implemented 2026-10-01 (backend, API, UI, docs). Decisions taken: showing an added key once is
accepted; env vars go in `app.json` with values of keys containing `_KEY`/`KEY`/`SECRET`/`PASSWORD`/`TOKEN`
redacted; backups run while the app runs (no stop mode); relational restores require the exact service version
(MySQL 8.4, PostgreSQL 18); restic 0.19.1 supports negated excludes, and Bento re-owns restored files itself.
Not built yet: the "new app from backup" wizard (create the app, then connect + restore instead), pre-backup hook,
single-file download, CLI commands.

## Goal

One app, one encrypted, deduplicated restic repository on any rclone remote. Each snapshot can recreate the app
(its Bento settings, databases, home including files like `storage/`, and minicrond jobs and workers) on the same
Bento or on a new app in another stack.

Non-goals: replacing the existing per-database backups (they stay as the lightweight option), replacing stack
transfer, backing up Redis (it's treated as a cache).

## Decisions at a glance

| Question | Decision |
| --- | --- |
| Where does restic run? | A dedicated **backup job container** (pinned `bento-backup` image = rclone + restic). Never the app container or the app's tool container. |
| How are rclone remotes reused? | `rclone:` backend of restic, `rclone.program` = the rclone binary in the same image, same `rclone/` dir and `rclone.conf` as today. |
| Per-app settings? | Yes: one restic setting per app (repo, key, paths, excludes, mode, retention). Stack-level defaults prefill it. |
| Repository layout | One repository per app (isolation, portability, per-app prune, deletion without touching other apps). |
| Keys | Bento generates the key at init and shows it once. Afterwards: add or remove extra restic keys (shown once). "Connect" takes a key. The stored key is never revealed. |
| DB preparation | Dump into a private staging dir **before** the snapshot: MySQL/Postgres as **plain uncompressed SQL**, SQLite as a `.backup` binary copy. Everything goes into **one snapshot** together with the home. |
| Order | Databases first, then files: files then contain everything the DB references. |
| Include/exclude | Paths relative to the home + exclude globs + defaults. Dry-run preview before saving. |

## 1. Per-app restic settings

Stored under the `settings` key `restic:<appId>` (same precedent as `webhook:<appId>`; no schema bump).

```jsonc
{
  "repository": "b2:bento/apps/shop-7f3a",   // rclone "remote:path"; restic sees rclone:<this>
  "paths": ["."],                             // relative to the home; "." = whole home
  "excludes": ["app/node_modules", "app/storage/framework/cache/**"],
  "defaultExcludes": true,                    // see §5
  "sqlitePaths": ["app/database/app.sqlite"], // extra SQLite files in the home to snapshot safely
  "mode": "online",                           // online | stop
  "retention": { "hourly": 24, "daily": 7, "weekly": 4, "monthly": 6 },
  "initializedAt": "…",
  "repositoryId": "…"                         // restic repo id, detects a swapped repo
}
```

- Stack-level defaults (under the `backup-default` schedule spec or a `restic-default` settings key) supply the
  remote, path prefix, and retention. "Enable backups" on a new app then takes one click.
- Validate `repository` with the existing `backup.ValidateRemote` + `checkRemote` (must exist in `rclone.conf`, which
  must not be encrypted).
- Scheduling: new schedule kind `app-backup` in `scheduleKinds`, spec `{ "appId": "…" }`. Several schedules per app
  are allowed (`RunSchedules` already skips a slot while the previous op is running). Retention lives in the
  setting, not the schedule, because it applies to the whole repository.

## 2. Image and container

### Image: `bento-backup`

Built from a template in `templates/images/backup/`, in the same style as the runtime images:

- `FROM` the pinned `domain.RcloneImage` (alpine, already has rclone).
- Add the static restic binary, pinned by version + SHA-256 per arch in `domain.BuildArgs` (same
  `fetch-artifacts.sh` pattern as minicrond), plus `sqlite3`: not strictly needed, because SQLite snapshots use
  the app image (§4).
- Tag = hash of the Dockerfile + build args, built by `image.prepare` like the runtime images.

Rejected: mounting a restic binary into the stock rclone image (unpinned layout, awkward on arm64), or two containers
(`rclone serve restic` + restic over REST: extra network plumbing for no gain).

### Why not the app container or the app's tool container

- Both run app-controlled code as the app UID. restic needs `rclone.conf` (credentials for **every** remote) and the
  repository key. Mounting those there hands them to the app. The architecture's trust boundary ("the app owns its
  minicrond") would be broken.
- The app container has a read-only root, limited tmpfs, and resource limits sized for the app, not for chunking
  GBs.

### Job container spec (extends `backup.rcloneSpec`)

| | backup | restore |
| --- | --- | --- |
| Image | `bento-backup` | same |
| User | root, `CapDrop: ALL`, `CapAdd: DAC_READ_SEARCH` (read any file, write nothing) | root, `CapAdd: CHOWN, FOWNER, DAC_OVERRIDE` |
| Mounts | `rclone/` → `/config/rclone` (rw, OAuth refresh), `homes/<slug>` → `/backup/home` **ro**, staging → `/backup/bento` **ro**, `cache/restic/<appId>` → `/cache` rw, key file → `/run/bento/restic.key` ro | same, but target is an empty staging dir mounted rw at `/restore`; home **not** mounted |
| Network | default bridge (outbound only), never the apps/data networks | same |
| Env | `RESTIC_REPOSITORY=rclone:<remote:path>`, `RESTIC_PASSWORD_FILE=/run/bento/restic.key`, `RESTIC_CACHE_DIR=/cache`, `RCLONE_CONFIG=…`, `-o rclone.program=/usr/local/bin/rclone` | same |
| Other | read-only root, tmpfs `/tmp`, `no-new-privileges`, `Init`, restart `no`, labels `role=backup`, `operation-id` | same |

The key never reaches argv, labels, logs, or container env. Only its **file path** does. `cache/` is already excluded
from stack transfer, so the restic cache doesn't travel.

Fixed in-container paths (`/backup/home`, `/backup/bento`) make snapshots look the same for every app and stack. Run
with `--host bento` and tags `app=<appId>`, `slug=<slug>`, `stack=<stackId>`, `bento-app-format=1`, and
`schedule=<id>` or `manual`.

## 3. Key management

restic encrypts the repo's master key with one or more **key slots**, each with its own password. This allows "get
a key" without ever revealing the key Bento stores.

| Action | Behaviour |
| --- | --- |
| **Init** (new repo) | Bento generates 256-bit random → `secrets/restic/<appId>.key` (`0400`). `restic init --repository-version 2`. Response shows the key **once** and the UI makes the operator copy or download it before continuing. |
| **Add access key** | `restic key add --new-password-file` (key written via exec stdin into the container's tmpfs). Returned **once**, with a label ("handover to staging"). |
| **List / remove keys** | `restic key list --json` / `restic key remove <id>`. The key Bento uses cannot be removed. |
| **Connect** (existing repo) | Operator provides `repository` + a key (request body or CLI `--key-file`, never argv). Bento runs `restic cat config` to verify it, stores the key, and records `repositoryId`. |
| **Rotate** | Add a new key slot → switch the stored file → remove the old slot, all in one operation. |

Rules:
- No "reveal stored key" endpoint. This keeps the "secrets never in API responses" rule, with the same once-only
  exception the webhook secret has.
- The request needs an exact confirmation for adding a key (`export <slug>`), and it's recorded as an operation
  event (label only, never the key).
- Warn clearly: if every key slot is lost, the backups can't be recovered.

## 4. Capture pipeline (what happens before `restic backup`)

Operation `app.backup` (claims below). Staging dir `staging/restic-<opId>/`, `0700 root`, always removed at the end
(`staging/` is already excluded from stack transfer).

```
1. preflight   settings valid, remote in rclone.conf, repo reachable (restic cat config),
               repositoryId matches, free disk ≥ estimated dump size + 10 %
2. quiesce     mode=stop: stop app (same helper as quiesceForExport), remember to restart
3. bento/      app.json (§6) + manifest.json (engine versions, arch, runtime image key, mode, app format)
4. db/         per relational binding database:
                 mysql-<service>-<db>.sql   mysqldump --single-transaction (existing dumpRelational,
                 postgres-<service>-<db>.sql  incl. DEFINER stripping) streamed to file UNCOMPRESSED
5. sqlite/     per SQLite binding: <fileId>.db via existing dumpSQLite (.backup in a network-less
               job container of the app image, app UID), then `PRAGMA quick_check` on the copy
6. home-sqlite/  minicrond DB (.local/share/minicron/*.db) + each settings.sqlitePaths entry:
               same .backup method, mounted home read-only, output keeps relative path
7. snapshot    restic backup /backup/home /backup/bento --host bento --tag … --exclude-file …
               --exclude-caches --json   (parse summary: snapshot id, files new/changed, bytes added)
8. resume      mode=stop: restart exactly what was stopped (also on failure)
9. forget      restic forget --tag app=<appId> --keep-hourly … (no --prune; see §8)
10. record     backup_runs row: kind=restic, snapshot id, bytes added, duration, result
```

Why this shape:

- **Plain SQL, not zstd.** A compressed stream changes almost completely when one row changes, so it defeats
  deduplication. restic's repository v2 compresses chunks itself. Plain SQL is also portable across engine versions.
- **SQLite as a `.backup` copy, not `.dump`.** It's exact, fast, and restores through the existing `RestoreSQLite`
  path, and restic's content-defined chunks still deduplicate unchanged pages well. `.dump` could be added later as
  a portability option.
- **Live SQLite files in the home are excluded** from the raw home snapshot (`*.db-wal`, `*.db-shm`, and the listed
  files themselves). They're replaced by the consistent `.backup` copies under `home-sqlite/`. Copying a live WAL
  database gives a torn copy.
- **Everything in one snapshot**, so one snapshot id = one restore point. `--stdin-from-command` would avoid staging
  space but yields one snapshot per stream. Worth revisiting only if staging space becomes a real problem.
- **DB before files** in online mode: a file uploaded after the dump is an orphan (harmless). With the reverse order
  a DB row could point at a file that isn't in the backup.
- `mode=stop` makes the home, minicrond, and SQLite all consistent at once. The downtime is the duration of steps
  4–7, which is usually short for incremental runs but includes a full scan of the home.

Later option: an app hook `.bento/pre-backup` run in the tool container (like the deploy script check) to flush
queues or put the app in maintenance mode.

## 5. Choosing what gets backed up

- `paths`: relative to the home, validated (no absolute paths, no `..`, must exist). Default `["."]`.
- `excludes`: restic glob patterns, relative, rewritten to `/backup/home/...` into a generated `--exclude-file`.
- `defaultExcludes: true` adds: `.cache/`, `.npm/`, `.composer/cache/`, `.local/share/minicron/*.db*` (replaced by
  `home-sqlite/`), the `sqlitePaths` files and their `-wal`/`-shm`, plus `--exclude-caches` (honours `CACHEDIR.TAG`).
- `.nobackup` marker: `--exclude-if-present .nobackup` lets the app owner exclude a directory from inside the code.
- Presets in the UI:
  - **Everything** (`.`): the default, and right for old sites.
  - **Data only** for git-deployed apps: `app/storage`, `app/public/uploads`, `.local/share/minicron`, … Code comes
    back through a git deploy of `DeployedCommit` at restore time.
- **Dry run**: `restic backup --dry-run --json` returns file count and size, and the top 20 largest directories. The
  UI shows this before saving the settings, so the operator sees what will be included.

Verify at implementation: negated excludes (`!pattern`) and `--exclude-larger-than` in the pinned restic version.
Only expose them if supported.

## 6. `bento/app.json` (portable app spec)

Its own `formatVersion`, independent of the state DB `schemaVersion` (unlike stack transfer, which requires them to
match exactly).

Included: slug, runtime (kind, PHP/HTTP settings, **env vars**: the repo is encrypted and apps often can't run
without them), resources, desired runtime, ingress, route, domains, bindings as intent (engine, service name +
version, database names, SQLite file ids), git source (URL, branch, `DeployedCommit`), app minicrond config-owned
tasks, the restic setting itself (without the key).

Excluded: app/binding/domain IDs, UID/GID, generations, `Provisioned`, all passwords (DB, Redis), deploy private
key, webhook secret, publication state, observed state.

## 7. Restore flows

All restores unpack into `staging/restore-<opId>/` first and validate before touching the app.

### 7a. New app from a restic backup (same or different stack)

Wizard *Apps → New → From restic backup*, or CLI
`bento app restore-new --repo b2:bento/apps/shop-7f3a --key-file ./shop.key [--snapshot latest] [--slug shop]`.

```
1. connect      verify key (restic cat config); list snapshots (restic snapshots --json --tag app=…)
2. inspect      restic dump <snap> /backup/bento/app.json + manifest.json → preview:
                runtime, bindings, domains, sizes, source Bento/arch; refuse unknown formatVersion
3. map          choose slug (default: original), target service per binding (same engine,
                version ≥ source), domains to attach (conflicts must be resolved or dropped),
                code source: snapshot (default) | git deploy at DeployedCommit
4. create       app with new ID + new UID (non-reuse preserved), stopped, unpublished;
                provision bindings → new credentials; refuse existing databases
5. restore      restic restore <snap> --target /restore  (restore container)
6. remap        host side: walk restored home with lchown/no-follow, every entry → new UID:GID;
                reuse transfer.ExtractRoot's rules (contained relative symlinks, no devices);
                move into homes/<slug> (must not exist; refuse otherwise)
7. data         RestoreRelational per db/*.sql, RestoreSQLite per sqlite/*.db,
                copy home-sqlite/* back to their relative paths (app UID, 0600)
8. finish       image.prepare for the runtime; new deploy key generated (show public key if git);
                optionally attach the restic setting to the new app:
                  "continue in this repo" (default off) | "new repo" | "none"
                app stays stopped until the operator starts it
```

On failure, remove only what this operation created (same rule as stack import).

If the old app keeps backing up to the same repo, both apps would write to it. restic allows concurrent backups,
but two different retention policies would prune each other's snapshots, since `forget` is scoped only by tag. The
default is therefore not to continue in the same repo, and the UI warns when it's selected.

### 7b. In-place restore (rollback the same app)

Confirmation `restore <slug>`. Steps: stop the app → **take a pre-restore snapshot** (tag `pre-restore`) → restore
to staging → rename `homes/<slug>` to `homes/.<slug>.pre-restore-<ts>` (retained, never deleted automatically) →
move the restored home in → `RestoreRelational`/`RestoreSQLite` (existing `replace <db>` semantics) → start if it
was running.

### 7c. Partial restores

- Files: `restic restore --include /backup/home/app/storage/…` into staging, then the operator picks a merge or
  replace for that subtree.
- One database only: `restic dump <snap> /backup/bento/db/<file>` → existing restore path.
- Download one file: `restic dump`, streamed with the same caps as other downloads (later phase).

## 8. Repository maintenance

- `forget` after every backup (cheap, metadata only).
- `prune` at most weekly per repo (`--max-unused 5%`), as its own operation, because it takes an **exclusive**
  repository lock.
- `check --read-data-subset 5%` weekly, and on demand from the UI. The result is shown on the app's backup card.
- Stale lock (crashed run): the UI offers `restic unlock` only when the lock is older than the longest possible
  run, never automatically during another operation.

## 9. Operations, claims, concurrency

| Operation | Claims |
| --- | --- |
| `app.backup` (online) | shared `app:<id>`, shared `service:<name>` per bound service, exclusive `restic:<appId>`, pool `backup-restic` (max 2) |
| `app.backup` (stop) | exclusive `app:<id>` + the rest as above |
| `app.restore` (in place) | exclusive `app:<id>`, shared services, exclusive `restic:<appId>` |
| `app.restore-new` | global (creates an app and provisions grants, like an unprovisioned `app.start`) |
| `restic.prune`, `restic.check`, `restic.key-*` | exclusive `restic:<appId>` |

Don't reuse `locks/backup.lock`: one large home would block every other app's database backups. Cancelling an
operation kills the job container. restic leaves a lock behind, and the next run warns and can unlock it as above.

## 10. Interaction with existing features

- **App removal**: never touches the remote repo. The key file moves to `secrets/restic/retained/<appId>.key` and is
  listed as retained data. The operator deletes it explicitly.
- **Stack export/import**: `secrets/restic/*` travels with the root archive. Import already disables all
  schedules, so a transferred stack won't start writing to the same repos until someone re-enables them.
- **Existing DB backups**: unchanged. The UI makes the difference clear: "Database dumps" (per DB, local + rclone
  copy) vs "App backup (restic)".
- **Rclone shell**: unchanged, and it remains the way to add remotes.

## 11. Surfaces

- **API** (`dto` + `make generate-types`): `GET/PUT /apps/{id}/restic`, `POST …/restic/init`, `POST …/restic/connect`,
  `POST …/restic/dry-run`, `GET …/restic/snapshots`, `POST …/restic/backup`, `POST …/restic/restore`,
  `GET/POST/DELETE …/restic/keys`, `POST …/restic/check|prune|unlock`, `POST /apps/restore-from-restic` (inspect +
  create). All mutations return `202` + operation.
- **CLI**: `bento app backup <slug>`, `bento app snapshots <slug>`, `bento app restore <slug> --snapshot`,
  `bento app restore-new --repo … --key-file …`, `bento app restic-key add|list|remove <slug>`.
- **UI**: App → Backups tab with a restic card (repo status, last run, bytes added, next schedule, check result),
  path/exclude editor with dry-run preview, snapshot list, key management, restore dialogs.

## 12. Security checklist

- The key only exists as a `0400` file on the host and a read-only mount in the job container. It's never in argv,
  env values, labels, logs, events, or responses (except the once-only returns above).
- `rclone.conf` is only ever mounted into the `bento-backup` job container, never into app or tool containers.
- The backup container can read the home but never write it. The restore container never mounts the live home.
- Restored trees are untrusted: containment and symlink rules from `transfer.ExtractRoot`, no device nodes,
  ownership forced to the target UID, and a size/inode cap checked against free disk before moving.
- `app.json` from a snapshot is untrusted input: strict decoding, `DisallowUnknownFields`, full domain validation
  as if an operator had submitted it.
- Output of restic and rclone in operation events goes through the app's redactor, and the key value is added to it.

## 13. Phases

1. **Image + settings + manual backup**: `bento-backup` image, key init, settings, `app.backup` (online), snapshot
   list, CLI. Integration test with an rclone `type = local` remote in a disposable root.
2. **Restore new app**: inspect, map, UID remap, data restore; cross-stack test (two disposable roots, same local
   remote).
3. **Schedules + retention + maintenance**: `app-backup` kind, forget/prune/check, stop mode.
4. **In-place and partial restore**, key add/remove/rotate UI, dry-run preview.
5. Later: pre-backup hook, single-file download, `.dump` SQLite option, optional Redis key export.

Tests: unit tests (settings and pattern validation, exclude-file generation, `app.json` round trip + rejection of
unknown fields, UID-remap walker against symlink/device/`..` fixtures, claims), operation tests with `docker.Fake`,
and `make test-integration` for real backup → restore-new → app serves the same data. Don't claim arm64 or real
cloud-remote results unless they actually ran.

## 14. Open questions

1. Is showing an added key once acceptable under the "secrets never reach API responses" rule? (Proposed: yes, same
   exception as the webhook secret.)
2. Should env var values go in `app.json` (proposed: yes, the repo is encrypted), or be opt-in?
3. Default mode for scheduled runs: `online` (proposed) or `stop`?
4. Should the minimum target engine version be enforced as ≥ source, or only warned about?
5. Pinned restic version, and whether it supports negated excludes and restore of ownership when running as root
   with only `CHOWN`/`FOWNER`/`DAC_OVERRIDE`. Verify before phase 1.
