#!/bin/bash
# Final smoke on a fresh stack: install, public edge + ACME staging, five apps from Gitea, HTTPS/IPv6, webhook.
set -uo pipefail
export BENTO_STACK_ROOT=/srv/bento/prod
D=104.64.207.10.nip.io
V6=2600:3c15::2000:86ff:fe39:93f1
bash /root/reset.sh
bash /root/install.sh 2>&1 | tail -3
cat > /root/edge.json <<'J'
{"enabled":true,"bind":"0.0.0.0","httpPort":80,"httpsPort":443,"http3":true,"acmeEmail":"",
 "acmeUrl":"https://acme-staging-v02.api.letsencrypt.org/directory",
 "frontProxy":{"enabled":false,"bind":"0.0.0.0","httpPort":8080,"httpsPort":8443,"trustedProxies":[]}}
J
bento edge set --json /root/edge.json | tail -1
bash /root/apps.sh 2>&1 | grep -E "deploy key added|^error" 
bash /root/deploy.sh 2>&1 | tail -7
echo "== waiting for certificates"
for i in $(seq 1 60); do n=$(ls /srv/bento/prod/edge/acme/bento_acme/*.crt 2>/dev/null | wc -l); [ "$n" -ge 5 ] && break; sleep 5; done
echo "certs: $n"
for a in e2e-node e2e-php e2e-legacy e2e-bun e2e-py; do
  echo "$a v4 $(curl -sk https://$a.$D/ | head -c 90) | v6 $(curl -sk -o /dev/null -w '%{http_code}' -g --resolve "$a.$D:443:[$V6]" https://$a.$D/)"
done
bento --json hosts | jq -r '.hosts[] | "\(.name) \(.certificate.state)"'
echo "== webhook"
T=$(cat /root/.config/bento-test/gitea-token)
for h in $(curl -s -H "Authorization: token $T" http://127.0.0.1:3000/api/v1/repos/e2e/e2e-php/hooks | jq -r '.[].id'); do
  curl -s -o /dev/null -X DELETE -H "Authorization: token $T" http://127.0.0.1:3000/api/v1/repos/e2e/e2e-php/hooks/$h; done
W=$(bento --json app webhook e2e-php --enable); U=$(jq -r .url <<<"$W"); SEC=$(jq -r .secret <<<"$W")
curl -fsS -o /dev/null -H "Authorization: token $T" -H "Content-Type: application/json" -X POST http://127.0.0.1:3000/api/v1/repos/e2e/e2e-php/hooks \
  -d "$(jq -n --arg u "$U" --arg s "$SEC" '{type:"gitea",active:true,events:["push"],config:{url:$u,content_type:"json",secret:$s}}')"
M=final-$(date +%s)
/root/push.sh e2e-php main "s/const MARK = '[^']*'/const MARK = '$M'/" public/index.php >/dev/null
for i in $(seq 1 30); do curl -sk https://e2e-php.$D/ | grep -q "$M" && break; sleep 2; done
echo "webhook mark live: $(curl -sk https://e2e-php.$D/ | grep -c "$M")"
bento app webhook e2e-php 2>&1 | tail -1
