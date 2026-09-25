#!/usr/bin/env bash
set -euo pipefail

: "${BENTO_APP:?BENTO_APP is required}"
: "${BENTO_UID:?BENTO_UID is required}"
: "${BENTO_GID:?BENTO_GID is required}"
: "${BENTO_HTTP_PORT:?BENTO_HTTP_PORT is required}"

case "$BENTO_UID:$BENTO_GID:$BENTO_HTTP_PORT" in
  *[!0-9:]*|:*|*::*) echo "invalid numeric Bento process identity" >&2; exit 64 ;;
esac
if (( BENTO_HTTP_PORT < 1024 || BENTO_HTTP_PORT > 65535 )); then
  echo "invalid Bento process port" >&2
  exit 64
fi
if [[ $# -eq 0 ]]; then
  echo "process app command is empty" >&2
  exit 64
fi

export HOME="${HOME:-/home/$BENTO_APP}" USER="$BENTO_APP" LOGNAME="$BENTO_APP"

# Load generated connection metadata without evaluating it as shell code. Values
# are exported verbatim and never appear in Compose configuration or host argv.
if [[ -n "${BENTO_CREDENTIALS_FILE:-}" && -r "$BENTO_CREDENTIALS_FILE" ]]; then
  while IFS='=' read -r key value || [[ -n "$key$value" ]]; do
    [[ -z "$key" || "$key" == \#* ]] && continue
    if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      echo "invalid key in Bento credentials file" >&2
      exit 65
    fi
    value="${value%$'\r'}"
    export "$key=$value"
  done < "$BENTO_CREDENTIALS_FILE"
fi

if [[ "${BENTO_ROLE:-web}" == "cli" ]]; then
  exec setpriv --reuid="$BENTO_UID" --regid="$BENTO_GID" --clear-groups -- "$@"
fi

install -d -m 0750 -o 0 -g 5000 /run/bento-http
rm -f /run/bento-http/http.sock
socat \
  UNIX-LISTEN:/run/bento-http/http.sock,fork,unlink-early,mode=0660,uid=0,gid=5000 \
  TCP:127.0.0.1:"$BENTO_HTTP_PORT" &
socket_pid=$!
app_pid=""

forward_signal() {
  local signal="$1"
  if [[ -n "$app_pid" ]]; then kill -s "$signal" -- "-$app_pid" 2>/dev/null || true; fi
}
trap 'forward_signal TERM' TERM
trap 'forward_signal INT' INT
trap 'forward_signal HUP' HUP

setpriv --reuid="$BENTO_UID" --regid="$BENTO_GID" --clear-groups -- setsid -- "$@" &
app_pid=$!
set +e
wait "$app_pid"
status=$?
set -e
kill "$socket_pid" 2>/dev/null || true
wait "$socket_pid" 2>/dev/null || true
rm -f /run/bento-http/http.sock
exit "$status"
