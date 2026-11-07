#!/bin/bash
# Create the e2e fixture apps on the VPS stack (section 3 of e2e-live.md, with nip.io hosts and ACME).
set -uo pipefail
export BENTO_STACK_ROOT=/srv/bento/prod
# The host's public addresses (both test hosts have them on their interface); nip.io names resolve to IPv4.
IP4=$(ip -4 route get 1.1.1.1 | grep -o 'src [^ ]*' | cut -d' ' -f2)
V6=$(ip -6 route get 2001:4860:4860::8888 2>/dev/null | grep -o 'src [^ ]*' | cut -d' ' -f2)
D=$IP4.nip.io
T=$(cat /root/.config/bento-test/gitea-token)
R='{"tls":"acme","redirectHttps":true,"accessLog":true}'
mk() { # slug runtime-json bindings-json [extra]
  cat > /root/app-$1.json <<J
{"slug":"$1","runtime":$2,"ingress":"managed","hosts":[{"name":"$1.$D","route":$R}],"bindings":$3${4:-}}
J
  bento app create --json /root/app-$1.json | tail -1
  key=$(bento --json app git $1 --repo ssh://git@172.17.0.1:2222/e2e/$1.git --branch main | jq -r .publicKey)
  # Keys of earlier stacks are useless and Gitea refuses a second key with the same title.
  for k in $(curl -fsS -H "Authorization: token $T" http://127.0.0.1:3000/api/v1/repos/e2e/$1/keys | jq -r '.[].id'); do
    curl -fsS -o /dev/null -X DELETE -H "Authorization: token $T" http://127.0.0.1:3000/api/v1/repos/e2e/$1/keys/$k
  done
  curl -fsS -o /dev/null -H "Authorization: token $T" -H 'Content-Type: application/json' -X POST \
    http://127.0.0.1:3000/api/v1/repos/e2e/$1/keys -d "$(jq -n --arg k "$key" '{title:"bento",key:$k,read_only:true}')" && echo "$1 deploy key added"
}
mk e2e-node '{"kind":"http-process","http":{"toolchain":"node","version":"22","argv":["node","server.js"],"workdir":"","port":3000,"readyPath":"/health"}}' '[{"engine":"postgres","service":"postgres18"}]'
mk e2e-php '{"kind":"php-fpm","php":{"version":"8.4","documentRoot":"public","routing":"front-controller"}}' '[{"engine":"mysql","service":"mysql84"}]' ',"valkey":true'
mk e2e-legacy '{"kind":"php-fpm","php":{"version":"7.4","documentRoot":"","routing":"legacy"}}' '[{"engine":"mysql","service":"mysql84"}]'
mk e2e-bun '{"kind":"http-process","http":{"toolchain":"bun","version":"1.3","argv":["bun","server.ts"],"workdir":"","port":3000}}' '[{"engine":"sqlite"}]'
mk e2e-py '{"kind":"http-process","http":{"toolchain":"python","version":"3.13","argv":[".venv/bin/python","app.py"],"workdir":"","port":8000}}' '[{"engine":"postgres","service":"postgres18"}]'
bento apps
