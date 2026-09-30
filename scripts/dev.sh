#!/usr/bin/env bash
# Local dev loop: build bento, (re)create a disposable dev stack, run the
# backend in the foreground, and seed demo apps on a fresh stack.
#
#   sudo scripts/dev.sh              build + serve (init the stack if missing)
#   sudo scripts/dev.sh --fresh      wipe the dev stack (db, root, its containers,
#                                    volumes, networks), init, serve, seed apps
#   sudo scripts/dev.sh --seed       seed demo apps into the running/started stack
#   sudo scripts/dev.sh --web        also rebuild and embed the React UI
#   sudo scripts/dev.sh --no-build   reuse dist/bento
#
# Env: BENTO_DEV_ROOT (/tmp/dev-stack), BENTO_DEV_NAME (dev),
#      BENTO_DEV_PASSWORD (bento-dev-pass), BENTO_DEV_LISTEN (127.0.0.1:7780).
# Only for disposable dev stacks: --fresh deletes that stack's data volumes.
set -euo pipefail

ROOT=${BENTO_DEV_ROOT:-/tmp/dev-stack}
NAME=${BENTO_DEV_NAME:-dev}
PASSWORD=${BENTO_DEV_PASSWORD:-bento-dev-pass}
LISTEN=${BENTO_DEV_LISTEN:-127.0.0.1:7780}
REPO=$(cd "$(dirname "$0")/.." && pwd)
BIN=$REPO/dist/bento

fresh=0 seed=0 web=0 build=1
for a in "$@"; do
  case $a in
    --fresh) fresh=1 seed=1 ;;
    --seed) seed=1 ;;
    --web) web=1 ;;
    --no-build) build=0 ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "unknown flag: $a" >&2; exit 2 ;;
  esac
done

[[ $(id -u) == 0 ]] || { echo "run as root (bento manages app UIDs and Docker)" >&2; exit 1; }
case $ROOT in /tmp/*|*/dev-*|*-dev) ;; *) echo "refusing non-dev root: $ROOT" >&2; exit 1 ;; esac

log() { printf '\033[1;36m[dev]\033[0m %s\n' "$*"; }
bento() { "$BIN" --stack "$ROOT" "$@"; }

if ((web)); then log "building web UI"; make -C "$REPO/apps/backend" web; fi
if ((build)); then log "building dist/bento"; make -C "$REPO/apps/backend" build; fi

stop_server() {
  local pids
  pids=$(pgrep -f -- "--stack $ROOT serve" || true)
  [[ -z $pids ]] && return
  log "stopping running backend ($pids)"
  kill $pids
  for _ in $(seq 50); do pgrep -f -- "--stack $ROOT serve" >/dev/null || return 0; sleep 0.2; done
  kill -9 $pids 2>/dev/null || true
}

wipe_stack() {
  # The stack id is the label every resource of this stack carries; find it
  # from any container named for this stack.
  local id
  id=$(docker ps -a --filter "name=^bento-$NAME-" --format '{{.Label "io.bento.stack-id"}}' | grep -m1 . || true)
  if [[ -n $id ]]; then
    log "removing docker resources of stack $id"
    docker ps -aq --filter "label=io.bento.stack-id=$id" | xargs -r docker rm -f >/dev/null
    docker volume ls -q --filter "label=io.bento.stack-id=$id" | xargs -r docker volume rm >/dev/null
    docker network ls -q --filter "label=io.bento.stack-id=$id" | xargs -r docker network rm >/dev/null
  fi
  log "removing $ROOT"
  rm -rf -- "$ROOT"
}

stop_server
if ((fresh)); then wipe_stack; fi

if [[ ! -e $ROOT/bento.db ]]; then
  log "init stack '$NAME' at $ROOT (password: $PASSWORD)"
  mkdir -p "$ROOT"
  printf '%s\n' "$PASSWORD" | bento init --name "$NAME" --mysql 8.4 --postgres 17 --password-stdin
fi

log "serving on http://$LISTEN"
bento serve --listen "$LISTEN" &
SERVER=$!
trap 'kill $SERVER 2>/dev/null; wait $SERVER 2>/dev/null' INT TERM EXIT

if ((seed)); then
  for _ in $(seq 100); do bento status >/dev/null 2>&1 && break; sleep 0.3; done
  "$REPO/scripts/dev-seed.sh" "$BIN" "$ROOT" || log "seeding failed (backend keeps running)"
fi

wait $SERVER
