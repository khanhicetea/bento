#!/bin/bash
# Smoke run on the stack that is already running (e.g. one made by scripts/bootstrap.sh), without resetting it:
# Let's Encrypt staging on the existing edge ports, the five e2e apps from Gitea (run gitea.sh first), certificates,
# HTTPS on IPv4/IPv6, and a Gitea push-to-deploy. Copy scripts/vps/*.sh and scripts/vps/bench/ to /root first.
set -uo pipefail
. /root/bench/env.sh
V6=$(ip -6 route get 2001:4860:4860::8888 2>/dev/null | grep -o 'src [^ ]*' | cut -d' ' -f2)
D=$IP4.nip.io
HTTPS_PORT=$(bento --json edge | jq -r .settings.httpsPort)
bento --json edge | jq '.settings | .acmeUrl = "https://acme-staging-v02.api.letsencrypt.org/directory"' >/root/edge.json
bento edge set --json /root/edge.json | tail -1
bento --json services | jq -e '.services[] | select(.name == "postgres18")' >/dev/null 2>&1 ||
  bento service add --engine postgres --version 18 | tail -1
bash /root/apps.sh 2>&1 | grep -E "deploy key added|^error"
bash /root/deploy.sh 2>&1 | tail -7
echo "== waiting for certificates"
for i in $(seq 1 60); do
  n=$(bento --json hosts | jq '[.hosts[] | select(.name | startswith("e2e-")) | select(.certificate.state == "issued")] | length')
  [ "$n" -ge 5 ] && break; sleep 5
done
echo "certs issued: $n of 5"
for a in e2e-node e2e-php e2e-legacy e2e-bun e2e-py; do
  v6=-
  [ -n "$V6" ] && v6=$(curl -sk -o /dev/null -w '%{http_code}' -g --resolve "$a.$D:$HTTPS_PORT:[$V6]" "https://$a.$D:$HTTPS_PORT/")
  echo "$a v4 $(curl -sk "https://$a.$D:$HTTPS_PORT/" | head -c 90) | v6 $v6"
done
echo "== webhook"
T=$(cat /root/.config/bento-test/gitea-token)
for h in $(curl -s -H "Authorization: token $T" http://127.0.0.1:3000/api/v1/repos/e2e/e2e-php/hooks | jq -r '.[].id'); do
  curl -s -o /dev/null -X DELETE -H "Authorization: token $T" "http://127.0.0.1:3000/api/v1/repos/e2e/e2e-php/hooks/$h"; done
W=$(bento --json app webhook e2e-php --enable); U=$(jq -r .url <<<"$W"); SEC=$(jq -r .secret <<<"$W")
curl -fsS -o /dev/null -H "Authorization: token $T" -H "Content-Type: application/json" -X POST http://127.0.0.1:3000/api/v1/repos/e2e/e2e-php/hooks \
  -d "$(jq -n --arg u "$U" --arg s "$SEC" '{type:"gitea",active:true,events:["push"],config:{url:$u,content_type:"json",secret:$s}}')"
M=smoke-$(date +%s)
/root/push.sh e2e-php main "s/const MARK = '[^']*'/const MARK = '$M'/" public/index.php >/dev/null
for i in $(seq 1 30); do curl -sk "https://e2e-php.$D:$HTTPS_PORT/" | grep -q "$M" && break; sleep 2; done
echo "webhook mark live: $(curl -sk "https://e2e-php.$D:$HTTPS_PORT/" | grep -c "$M")"
bento app webhook e2e-php 2>&1 | tail -1
