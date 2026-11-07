#!/bin/bash
# Database version matrix on a VPS stack: for each ENGINE:VERSION, add the service (if missing), deploy a fixture app
# bound to it from the local Gitea, check that it reads and writes, add a second database, back up to the e2es3 remote,
# write more rows, restore the database from the backup (swap), restart the service, remove the app and prune it
# (which drops its databases and user).
#
#   dbmatrix.sh mysql:8.0 postgres:14 ...        default: every supported version
#   APP=legacy|py dbmatrix.sh mysql:9.7          another fixture client (PHP 7.4, Python psycopg)
#
# Also opens each binding in the database browser (dbadmin.py next to this script; DBADMIN=0 skips it).
# Needs gitea.sh and s3.sh. Prints one PASS/FAIL line per version; details in /root/dbmatrix/<slug>.log.
set -uo pipefail
export BENTO_STACK_ROOT=/srv/bento/prod E2E_ROOT=/srv/bento/prod
API=/root/src/scripts/e2e/api.sh
IP4=$(ip -4 route get 1.1.1.1 | grep -o 'src [^ ]*' | cut -d' ' -f2)
D=$IP4.nip.io
T=$(cat /root/.config/bento-test/gitea-token)
GITEA=http://127.0.0.1:3000/api/v1/repos/e2e
mkdir -p /root/dbmatrix
RUN=$(date +%H%M%S)  # slugs are never reused, even after a prune
if [ "${DBADMIN:-1}" = 1 ]; then
  bash $API PUT /api/v1/dbadmin '{"enabled":true}' >/dev/null
  python3 -I /root/dbadmin.py login >/dev/null || { echo "FAIL database browser login"; exit 1; }
fi
[ $# -gt 0 ] || set -- mysql:8.0 mysql:8.4 mysql:9.7 postgres:14 postgres:15 postgres:16 postgres:17 postgres:18

hits() { curl -s -m 10 "http://$1.$D/" | grep -o '"hits": *[0-9]*' | grep -o '[0-9]*$'; }

one() {
  local engine=${1%%:*} version=${1#*:}
  local svc=$engine${version//./}
  local app=${APP:-}
  if [ -z "$app" ]; then [ "$engine" = mysql ] && app=php || app=node; fi
  local slug=$svc-$app-$RUN repo runtime deploy
  case $app in
    php) repo=e2e-php runtime='{"kind":"php-fpm","php":{"version":"8.4","documentRoot":"public","routing":"front-controller"}}' deploy="'date -u +%FT%TZ > deployed-at.txt'" ;;
    legacy) repo=e2e-legacy runtime='{"kind":"php-fpm","php":{"version":"7.4","documentRoot":"","routing":"legacy"}}' deploy="" ;;
    node) repo=e2e-node runtime='{"kind":"http-process","http":{"toolchain":"node","version":"22","argv":["node","server.js"],"workdir":"","port":3000,"readyPath":"/health"}}' deploy="'npm install --omit=dev'" ;;
    py) repo=e2e-py runtime='{"kind":"http-process","http":{"toolchain":"python","version":"3.13","argv":[".venv/bin/python","app.py"],"workdir":"","port":8000}}' deploy="'[ -x .venv/bin/python ] || python3 -m venv .venv' '.venv/bin/pip install -q -r requirements.txt'" ;;
  esac
  local log=/root/dbmatrix/$slug.log
  exec 3>"$log"
  fail() { echo "FAIL $1 $slug: $2 (see $log)"; echo "FAIL: $2" >&3; return 1; }
  # 1. Service
  if ! bento --json services | jq -e --arg n "$svc" '.services[] | select(.name == $n)' >/dev/null; then
    bento service add --engine "$engine" --version "$version" >&3 2>&1 || { fail "$1" "service add"; return; }
  fi
  local st=""
  for _ in $(seq 1 90); do
    st=$(bento --json services | jq -r --arg n "$svc" '.services[] | select(.name == $n) | .state')
    [ "$st" = healthy ] && break; sleep 2
  done
  [ "$st" = healthy ] || { fail "$1" "service $svc is $st"; return; }
  # 2. App bound to it
  echo "{\"slug\":\"$slug\",\"runtime\":$runtime,\"ingress\":\"managed\",\"hosts\":[{\"name\":\"$slug.$D\",\"route\":{\"tls\":\"none\",\"redirectHttps\":false,\"accessLog\":false}}],\"bindings\":[{\"engine\":\"$engine\",\"service\":\"$svc\"}],\"valkey\":$([ "$app" = php ] && echo true || echo false)}" > /tmp/$slug.json
  bento app create --json /tmp/$slug.json >&3 2>&1 || { fail "$1" "app create"; return; }
  local key kid
  key=$(bento --json app git "$slug" --repo "ssh://git@172.17.0.1:2222/e2e/$repo.git" --branch main | jq -r .publicKey)
  for kid in $(curl -fsS -H "Authorization: token $T" "$GITEA/$repo/keys" | jq -r --arg t "$slug" '.[] | select(.title == $t) | .id'); do
    curl -fsS -o /dev/null -X DELETE -H "Authorization: token $T" "$GITEA/$repo/keys/$kid"
  done
  curl -fsS -o /dev/null -H "Authorization: token $T" -H 'Content-Type: application/json' -X POST "$GITEA/$repo/keys" \
    -d "$(jq -n --arg k "$key" --arg t "$slug" '{title:$t,key:$k,read_only:true}')" || { fail "$1" "deploy key"; return; }
  [ -n "$deploy" ] && bento app exec "$slug" -- bash -c "printf '%s\n' '#!/bin/bash' 'set -e' $deploy > ~/deploy.sh && chmod 755 ~/deploy.sh" >&3 2>&1
  bento app deploy "$slug" >&3 2>&1 || { fail "$1" "deploy"; return; }
  bento app start "$slug" >&3 2>&1 || { fail "$1" "start"; return; }
  bento app publish "$slug" >&3 2>&1 || { fail "$1" "publish"; return; }
  local body h1 h2
  body=$(curl -s -m 10 "http://$slug.$D/"); echo "first response: $body" >&3
  h1=$(hits "$slug"); h2=$(hits "$slug")
  [ -n "$h1" ] && [ -n "$h2" ] && [ "$h2" -gt "$h1" ] || { fail "$1" "reads/writes: hits $h1 -> $h2"; return; }
  local reported
  reported=$(grep -o -E '"(mysql|db|psycopg|php|python)": *"[^"]*"' <<<"$body" | tr '\n' ' ')
  # 3. A second database on the binding
  local id bid db
  id=$(bento --json app show "$slug" | jq -r .id)
  bid=$(bento --json app show "$slug" | jq -r '.bindings[0].id')
  db=$(bento --json app show "$slug" | jq -r '.bindings[0].databases[0]')
  bento app add-db "$slug" --binding "$bid" --name extra >&3 2>&1 || { fail "$1" "add-db"; return; }
  local ndb
  ndb=$(bento --json app show "$slug" | jq -r '.bindings[0].databases | length')
  [ "$ndb" = 2 ] || { fail "$1" "add-db left $ndb databases"; return; }
  # 4. Backup, more writes, restore the database from the backup
  local set
  set=$(bash $API GET "/api/v1/apps/$id/restic" | jq --arg r "e2es3:bento/dbmatrix/$(hostname)/$slug-$(date +%s)" \
    '(.settings // .) | .repository=$r | .paths=["."] | .retention={"daily":7}')
  bash $API PUT "/api/v1/apps/$id/restic" "$set" >&3 2>&1
  [ "$(bash $API POST "/api/v1/apps/$id/restic/init" "{}" | jq -r .finalState)" = succeeded ] || { fail "$1" "restic init"; return; }
  local snap
  snap=$(hits "$slug")
  [ "$(bash $API POST "/api/v1/apps/$id/restic/backup" "{}" | jq -r .finalState)" = succeeded ] || { fail "$1" "backup"; return; }
  for _ in $(seq 1 9); do hits "$slug" >/dev/null; done
  local before
  before=$(hits "$slug")
  echo "replace $db" | bento app restore-db "$slug" --database "$db" --snapshot latest >&3 2>&1 || { fail "$1" "restore-db"; return; }
  # A PostgreSQL swap ends the app's connections; the Node fixture's pool has no error handler, so it exits and is
  # restarted. Wait for it, as a visitor would.
  local after=""
  for _ in $(seq 1 15); do after=$(hits "$slug"); [ -n "$after" ] && break; sleep 1; done
  # The container's health check also requests / (one more row each time), so allow a little slack.
  [ -n "$after" ] && [ "$after" -gt "$snap" ] && [ "$after" -le $((snap + 4)) ] ||
    { fail "$1" "restore: hits $snap at backup, $before before restore, $after after"; return; }
  # 5. Database browser: Adminer must reach this server version and list the app's table
  local browse=""
  if [ "${DBADMIN:-1}" = 1 ]; then
    browse=$(python3 -I /root/dbadmin.py open "$id" "$bid" hits 2>&1); echo "browser: $browse" >&3
    jq -e '.table and .server != null and .error == null' <<<"$browse" >/dev/null 2>&1 || { fail "$1" "database browser: $browse"; return; }
    browse="browser $(jq -r .server <<<"$browse"), "
  fi
  # 6. Service restart
  bento service restart "$svc" >&3 2>&1 || { fail "$1" "service restart"; return; }
  local h3=""
  for _ in $(seq 1 30); do h3=$(hits "$slug"); [ -n "$h3" ] && break; sleep 2; done
  [ -n "$h3" ] && [ "$h3" -gt "$after" ] || { fail "$1" "after service restart: hits $h3"; return; }
  # 7. Remove the app, then prune what it retained (drops its databases and user on this server version)
  echo "delete $slug" | bento app remove "$slug" >&3 2>&1 || { fail "$1" "app remove"; return; }
  echo delete | bento retired prune "$id" >&3 2>&1 || { fail "$1" "retired prune"; return; }
  echo "PASS $1 $slug: $reported| hits $h1->$h2, backup at $snap, restored from $before to $after, ${browse}add-db, restart, prune ok"
}

for v in "$@"; do one "$v"; done
