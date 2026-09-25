#!/bin/sh
# Launch an isolated socket-only daemon, reconcile Bento-internal definitions
# with its ordinary CLI, and keep s6 supervising the daemon's lifetime.
set -eu

uid=$1 gid=$2 home=$3 name=$4 seed=$5 data=$6 config=$7 expected=$8
export HOME="$home" USER="$name" MINICRON_DATA="$data" MINICRON_CONFIG="$config"
export PATH=/usr/local/bin:/usr/bin:/bin
: "${TZ:=UTC}"
export TZ
umask 077
if [ "$uid" = 0 ]; then
  mkdir -p "$data"
  chmod 700 "$data"
  /usr/local/bin/minicrond daemon &
else
  /command/s6-applyuidgid -u "$uid" -g "$gid" -G '' /usr/local/bin/minicrond daemon &
fi
pid=$!
trap 'kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true' EXIT

attempt=0
until [ "$attempt" -ge 100 ]; do
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "minicrond daemon exited before reconciliation" >&2
    exit 1
  fi
  if [ "$uid" = 0 ]; then
    /usr/local/bin/minicrond status >/dev/null 2>&1 && break
  else
    /command/s6-applyuidgid -u "$uid" -g "$gid" -G '' /usr/local/bin/minicrond status >/dev/null 2>&1 && break
  fi
  attempt=$((attempt + 1))
  sleep .1
done
if [ "$attempt" -ge 100 ]; then
  echo "minicrond socket did not become ready" >&2
  exit 1
fi
if [ -s "$seed" ]; then
  if [ "$uid" = 0 ]; then
    /usr/local/bin/minicrond import "$seed" >/dev/null
  else
    /command/s6-applyuidgid -u "$uid" -g "$gid" -G '' /usr/local/bin/minicrond import "$seed" >/dev/null
  fi
fi
if [ "$uid" = 0 ]; then
  php /usr/local/bin/bento-minicrond-reconcile "$expected"
else
  /command/s6-applyuidgid -u "$uid" -g "$gid" -G '' php /usr/local/bin/bento-minicrond-reconcile "$expected"
fi
# Import/delete errors fail the service; s6 retries. User definitions remain untouched.
wait "$pid"
