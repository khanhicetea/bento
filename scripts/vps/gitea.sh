#!/bin/bash
# Private Gitea for e2e fixtures on the test VPS. Reachable only on loopback and the docker0
# bridge address (172.17.0.1), never on the public interface. SSH for clones, HTTP API for setup.
set -euo pipefail
B=172.17.0.1
docker volume create e2e-gitea >/dev/null
if ! docker ps -q --filter name=^e2e-gitea$ | grep -q .; then
  docker rm -f e2e-gitea >/dev/null 2>&1 || true
  docker run -d --name e2e-gitea --restart unless-stopped -v e2e-gitea:/data \
    -p 127.0.0.1:3000:3000 -p $B:3000:3000 -p $B:2222:22 \
    -e GITEA__security__INSTALL_LOCK=true -e GITEA__database__DB_TYPE=sqlite3 \
    -e GITEA__server__ROOT_URL=http://$B:3000/ -e GITEA__server__SSH_DOMAIN=$B -e GITEA__server__SSH_PORT=2222 \
    -e GITEA__webhook__ALLOWED_HOST_LIST='*' -e GITEA__webhook__SKIP_TLS_VERIFY=true \
    -e GITEA__service__DISABLE_REGISTRATION=true gitea/gitea:1 >/dev/null
fi
for i in $(seq 1 60); do curl -fsS http://127.0.0.1:3000/api/healthz >/dev/null 2>&1 && break; sleep 2; done
install -d -m 700 /root/.config/bento-test
if [ ! -s /root/.config/bento-test/gitea-token ]; then
  PW=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
  docker exec -u git e2e-gitea gitea admin user create --admin --username e2e --password "$PW" --email e2e@bento.invalid --must-change-password=false >/dev/null
  (umask 077; docker exec -u git e2e-gitea gitea admin user generate-access-token --username e2e --token-name e2e --scopes all --raw > /root/.config/bento-test/gitea-token)
fi
T=$(cat /root/.config/bento-test/gitea-token)
for r in e2e-node e2e-php e2e-legacy e2e-bun e2e-py; do
  curl -fsS -o /dev/null -H "Authorization: token $T" -H 'Content-Type: application/json' \
    -X POST http://127.0.0.1:3000/api/v1/user/repos -d "{\"name\":\"$r\",\"private\":true}" || true
  d=$(mktemp -d); cp -r /root/src/scripts/e2e/apps/$r/. $d/
  (cd $d && git init -q -b main && git add -A && git -c user.name=e2e -c user.email=e2e@bento.invalid commit -qm fixture &&
   git push -q -f "http://e2e:$T@127.0.0.1:3000/e2e/$r.git" main) && echo "$r pushed"
  rm -rf $d
done
