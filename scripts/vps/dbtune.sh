#!/bin/bash
# Service tuning on every MySQL/PostgreSQL service of the VPS stack: apply non-default values through the API (which
# restarts the service), check it comes back healthy and that the server reports them, then reset to the defaults.
set -uo pipefail
export BENTO_STACK_ROOT=/srv/bento/prod E2E_ROOT=/srv/bento/prod
API=/root/src/scripts/e2e/api.sh

mysql_q() { docker exec "bento-prod-$1" sh -c 'MYSQL_PWD=$(cat "$MYSQL_ROOT_PASSWORD_FILE") mysql -uroot -N -B -e "$0"' "$2"; }
pg_q() { docker exec "bento-prod-$1" psql -U postgres -tA -c "$2"; }

healthy() {
  local st=""
  for _ in $(seq 1 90); do
    st=$(bento --json services | jq -r --arg n "$1" '.services[] | select(.name == $n) | "\(.state) \(.settingsApplied)"')
    [ "$st" = "healthy true" ] && return 0; sleep 2
  done
  echo "$st"; return 1
}

for row in $(bento --json services | jq -r '.services[] | "\(.name):\(.engine)"'); do
  svc=${row%%:*} engine=${row#*:}
  if [ "$engine" = mysql ]; then
    set='{"innodb_buffer_pool_size":"192M","innodb_redo_log_capacity":"256M","max_connections":"120","max_allowed_packet":"32M","tmp_table_size":"24M"}'
    want="201326592 268435456 120 33554432 25165824"
    q() { mysql_q "$svc" "SELECT CONCAT_WS(' ', @@innodb_buffer_pool_size, @@innodb_redo_log_capacity, @@max_connections, @@max_allowed_packet, @@tmp_table_size)"; }
  else
    set='{"shared_buffers":"160M","effective_cache_size":"512M","work_mem":"8M","maintenance_work_mem":"96M","max_connections":"120"}'
    want="160MB 512MB 8MB 96MB 120"
    q() { echo $(for p in shared_buffers effective_cache_size work_mem maintenance_work_mem max_connections; do pg_q "$svc" "SHOW $p"; done); }
  fi
  r=$(bash $API PUT "/api/v1/services/$svc/settings" "{\"settings\":$set}" | jq -r .finalState)
  st=$(healthy "$svc") || { echo "FAIL $svc: after tuning ($r): $st"; continue; }
  got=$(q 2>&1)
  [ "$got" = "$want" ] || { echo "FAIL $svc: reports '$got', want '$want'"; continue; }
  reset=$(jq -c 'with_entries(.value = "")' <<<"$set")
  bash $API PUT "/api/v1/services/$svc/settings" "{\"settings\":$reset}" >/dev/null
  healthy "$svc" >/dev/null || { echo "FAIL $svc: not healthy after reset"; continue; }
  echo "PASS $svc: tuned ($got), restarted healthy, reset to defaults ($(q 2>&1))"
done
