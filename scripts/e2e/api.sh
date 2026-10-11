#!/bin/bash
# Call the live-e2e backend REST API over its root-only control socket (no session or CSRF needed).
# Accepted operations are polled until they finish; the response gains a "finalState" field.
#
#   api.sh METHOD PATH [JSON]        e.g. api.sh GET /api/v1/apps/<id>/restic
#   NO_WAIT=1 api.sh POST ...        return right after acceptance
set -euo pipefail
sock=${E2E_ROOT:-/srv/bento-e2e}/run/bento.sock
m=$1 p=$2 body=${3:-}
args=(-sS --unix-socket "$sock" -X "$m" -H 'Content-Type: application/json')
[ -n "$body" ] && args+=(-d "$body")
out=$(curl "${args[@]}" "http://bento$p")
op=$(jq -r '.operation.id // empty' <<<"$out" 2>/dev/null || true)
if [ -n "$op" ] && [ "${NO_WAIT:-}" != 1 ]; then
  st=queued
  for _ in $(seq 1 450); do
    st=$(curl -sS --unix-socket "$sock" "http://bento/api/v1/operations/$op" | jq -r '.operation.state // .state')
    case $st in succeeded | failed | canceled) break ;; esac
    sleep 2
  done
  jq -c --arg st "$st" '. + {finalState: $st}' <<<"$out"
else
  printf '%s\n' "$out"
fi
