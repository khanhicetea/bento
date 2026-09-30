---
title: Deploy script recipes
description: Example ~/deploy.sh scripts for PHP apps, in place or with releases.
sidebar:
  order: 7
---

Every deploy (manual or [auto](/guides/apps/auto-deploy/)) fetches the branch into `~/app` and then runs
`~/deploy.sh` as the app user, from `~/app`, with the app's environment (database credentials included). If the script
fails, the app is not reloaded. The full contract and variables are in [Git deploy](/guides/apps/deploy/).

Install a script from the app shell:

```bash
bento app shell shop
vim ~/deploy.sh && chmod 755 ~/deploy.sh
```

Pick one of two layouts:

| | Classic (one directory) | Releases (Deployer style) |
| --- | --- | --- |
| Live code | `~/app` (the checkout itself) | `~/app/current` → `~/releases/<release>` |
| During a deploy | files change in place before `composer install` finishes | live code is untouched until the symlink switch |
| Failed script | new code is on disk, old code may be partly replaced | previous release keeps serving |
| Rollback | redeploy an older commit | switch the symlink back |
| Disk | one copy | one copy per kept release |

Both examples are for Laravel; adapt the artisan lines for other frameworks.

## Classic: one directory

Runtime (unchanged from a normal app):

```json
{ "kind": "php-fpm", "php": { "version": "8.4", "documentRoot": "public", "routing": "front-controller", "mode": "standard" } }
```

Untracked files in `~/app` survive every deploy (`.env`, `vendor/`, `node_modules/`, `storage/*` content), so they
live right there.

```bash
#!/bin/bash
# ~/deploy.sh — classic layout: build in ~/app, Bento reloads PHP-FPM afterwards.
set -euo pipefail
cd "$HOME/app"

# changed PATH... → true if PATH changed since the last successful deploy (or on the first one)
changed() {
  [ -z "${BENTO_PREVIOUS_COMMIT:-}" ] || ! git diff --quiet "$BENTO_PREVIOUS_COMMIT" "$BENTO_COMMIT" -- "$@"
}

echo "deploying ${BENTO_COMMIT:0:7} (previous: ${BENTO_PREVIOUS_COMMIT:0:7}, via ${BENTO_DEPLOY_TRIGGER})"

[ -f .env ] || { echo "~/app/.env is missing" >&2; exit 1; }

if changed composer.json composer.lock || [ ! -d vendor ]; then
  composer install --no-dev --no-interaction --prefer-dist --optimize-autoloader
fi

if [ -f package-lock.json ] && { changed package.json package-lock.json resources vite.config.js || [ ! -d public/build ]; }; then
  npm ci --no-audit --no-fund
  npm run build
fi

php artisan migrate --force
php artisan optimize        # config, route, event, and view caches
php artisan queue:restart   # workers pick up the new code after their current job
```

Because the checkout changes before the script runs, requests during the build can hit new PHP files with old
`vendor/`. For busy apps, wrap the build in `php artisan down --retry=15` / `php artisan up`, or use releases.

## Releases: Deployer style

```text
/home/shop/
├── app/                 Bento's git checkout (the "repository cache"; never served directly)
│   └── current ──►      ../releases/20260928T091500-1a2b3c4   (untracked symlink)
├── releases/
│   ├── 20260928T091500-1a2b3c4/
│   └── 20260927T180210-9f8e7d6/
├── shared/
│   ├── .env
│   └── storage/
└── deploy.sh
```

Runtime: serve through the `current` symlink. `releaseSymlink` lets local Nginx traverse exactly that symlink.

```json
{ "kind": "php-fpm", "php": { "version": "8.4", "documentRoot": "current/public", "releaseSymlink": "current",
  "routing": "front-controller", "mode": "standard" } }
```

One-time setup (in `bento app shell shop`): put the production `.env` at `~/shared/.env`. Run the first deploy before
starting the app, since `current` does not exist until then.

```bash
#!/bin/bash
# ~/deploy.sh — release layout: build a new release, switch ~/app/current atomically.
set -euo pipefail

KEEP=5
APP="$HOME/app"
RELEASES="$HOME/releases"
SHARED="$HOME/shared"
NAME="$(date -u +%Y%m%dT%H%M%S)-${BENTO_COMMIT:0:7}"
RELEASE="$RELEASES/$NAME"

echo "building release $NAME from $BENTO_BRANCH (${BENTO_DEPLOY_TRIGGER})"

# Shared state, created once.
mkdir -p "$RELEASES" \
  "$SHARED/storage/app/public" "$SHARED/storage/logs" \
  "$SHARED/storage/framework/cache/data" "$SHARED/storage/framework/sessions" "$SHARED/storage/framework/views"
[ -f "$SHARED/.env" ] || { echo "$SHARED/.env is missing" >&2; exit 1; }

# Keep the live symlink out of the checkout's git status.
grep -qxF /current "$APP/.git/info/exclude" 2>/dev/null || echo /current >>"$APP/.git/info/exclude"

# Remove a half-built release if anything below fails; the live release is untouched.
switched=0
cleanup() { [ "$switched" = 1 ] || rm -rf "$RELEASE"; }
trap cleanup EXIT

# 1. Export the fetched commit (tracked files only; use `git submodule foreach` too if you rely on submodules).
mkdir -p "$RELEASE"
git -C "$APP" archive "$BENTO_COMMIT" | tar -x -C "$RELEASE"

# 2. Link shared files.
ln -s "$SHARED/.env" "$RELEASE/.env"
rm -rf "$RELEASE/storage"
ln -s "$SHARED/storage" "$RELEASE/storage"

# 3. Build. Seed vendor/ from the live release so composer only applies the diff.
cd "$RELEASE"
if [ -d "$APP/current/vendor" ]; then cp -a "$APP/current/vendor" .; fi
composer install --no-dev --no-interaction --prefer-dist --optimize-autoloader
if [ -f package-lock.json ]; then
  npm ci --no-audit --no-fund
  npm run build
  rm -rf node_modules
fi
php artisan migrate --force
php artisan optimize

# 4. Switch atomically: rename a new symlink over the old one.
ln -sfn "$RELEASE" "$APP/current.next"
mv -T "$APP/current.next" "$APP/current"
switched=1
echo "current -> $NAME"

php artisan queue:restart

# 5. Keep the newest $KEEP releases.
ls -1dt "$RELEASES"/*/ | tail -n +$((KEEP + 1)) | xargs -r rm -rf
```

After the script succeeds, Bento reloads PHP-FPM gracefully, which also clears opcache and the realpath cache so the
new release is served immediately.

Notes for the release layout:

- **Migrations run before the switch**, while the old release still serves. Keep them backward compatible
  (add columns first, drop them in a later deploy).
- **Uploads**: local Nginx refuses symlinks below the document root, so a `public/storage` symlink
  (`php artisan storage:link`) is not served. Serve uploads through a PHP route or object storage.
- **Scheduler and workers** should use `~/app/current` as their working directory, not `~/app`.
- **Disk**: each release has its own `vendor/`; lower `KEEP` on small disks.

### Roll back

Point `current` at the previous release and restart the app:

```bash
bento app exec shop -- bash -c '
  prev=$(ls -1dt ~/releases/*/ | sed -n 2p)
  ln -sfn "${prev%/}" ~/app/current.next && mv -T ~/app/current.next ~/app/current
  echo "current -> $(readlink ~/app/current)"'
bento app restart shop
```

The next deploy builds a new release from the branch head again. Migrations are not rolled back; run
`php artisan migrate:rollback` in the old release first if you need to.
