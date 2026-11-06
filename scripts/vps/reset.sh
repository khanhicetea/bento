#!/bin/bash
# Wipe the disposable /srv/bento/prod stack completely (containers, networks, volumes, root).
set -uo pipefail
SID=$(jq -r .stackId /srv/bento/prod/stack.json 2>/dev/null || true)
[ -z "$SID" ] && SID=$(BENTO_STACK_ROOT=/srv/bento/prod bento --json status 2>/dev/null | jq -r .stackId)
systemctl stop bento@prod 2>/dev/null
if [ -n "$SID" ] && [ "$SID" != null ]; then
  docker ps -aq --filter label=io.bento.stack-id=$SID | xargs -r docker rm -f >/dev/null
  docker network ls -q --filter label=io.bento.stack-id=$SID | xargs -r docker network rm >/dev/null
  docker volume ls -q --filter label=io.bento.stack-id=$SID | xargs -r docker volume rm >/dev/null
fi
rm -rf /srv/bento/prod
echo "reset $SID"
