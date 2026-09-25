#!/bin/sh
# Launch an isolated socket-only daemon. Config-owned tasks are synced before
# minicrond starts scheduling or accepts requests; s6 supervises its lifetime.
set -eu

uid=$1 gid=$2 home=$3 name=$4 data=$5 config=$6
export HOME="$home" USER="$name" MINICRON_DATA="$data" MINICRON_CONFIG="$config"
export PATH=/usr/local/bin:/usr/bin:/bin
: "${TZ:=UTC}"
export TZ
umask 077
if [ "$uid" = 0 ]; then
  mkdir -p "$data"
  chmod 700 "$data"
  exec /usr/local/bin/minicrond daemon
fi
exec /command/s6-applyuidgid -u "$uid" -g "$gid" -G '' /usr/local/bin/minicrond daemon
