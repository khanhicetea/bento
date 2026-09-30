#!/usr/bin/env bash
# Seed demo apps into a running dev stack: a PHP app with MySQL and a Node app
# with PostgreSQL. Usage: dev-seed.sh BIN ROOT (called by dev.sh --seed).
set -euo pipefail

BIN=$1 ROOT=$2
bento() { "$BIN" --stack "$ROOT" "$@"; }
log() { printf '\033[1;35m[seed]\033[0m %s\n' "$*"; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# create SLUG JSON: create the app unless it exists.
create() {
  if bento app show "$1" >/dev/null 2>&1; then log "$1 exists"; return; fi
  log "creating $1"
  printf '%s' "$2" >"$tmp/$1.json"
  bento app create --json "$tmp/$1.json" >/dev/null
}

# code SLUG RELPATH: write stdin into the app's code dir as the app UID.
code() {
  local uid dir
  uid=$(bento --json app show "$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["uid"])')
  dir=$ROOT/homes/$1/app/$(dirname "$2")
  mkdir -p "$dir" && chown "$uid:$uid" "$dir"
  cat >"$ROOT/homes/$1/app/$2"
  chown "$uid:$uid" "$ROOT/homes/$1/app/$2"
}

# Managed edge on loopback high ports (host 80/443 are often taken in dev).
if [[ $(bento --json edge | python3 -c 'import json,sys; print(json.load(sys.stdin)["settings"]["enabled"])') != True ]]; then
  log "enabling edge on 127.0.0.1:${EDGE_HTTP:-8880}/${EDGE_HTTPS:-8843}"
  printf '{"enabled":true,"bind":"127.0.0.1","httpPort":%s,"httpsPort":%s,"http3":false}' \
    "${EDGE_HTTP:-8880}" "${EDGE_HTTPS:-8843}" >"$tmp/edge.json"
  bento edge set --json "$tmp/edge.json" >/dev/null
fi

create demo-php '{
  "slug": "demo-php",
  "runtime": {"kind": "php-fpm", "php": {"version": "8.4", "documentRoot": "public", "routing": "front-controller", "mode": "standard"}},
  "domains": ["demo-php.localhost"],
  "bindings": [{"engine": "mysql", "service": "mysql84"}, {"engine": "sqlite"}]
}'
code demo-php public/index.php <<'PHP'
<?php
header('Content-Type: text/plain');
echo "Hello from demo-php (PHP " . PHP_VERSION . ")\n\n";
foreach ($_ENV + getenv() as $k => $v) {
    if (str_starts_with($k, 'DB_') || str_starts_with($k, 'MYSQL_') || str_starts_with($k, 'REDIS_')) {
        echo str_contains($k, 'PASS') ? "$k=***\n" : "$k=$v\n";
    }
}
PHP

create demo-node '{
  "slug": "demo-node",
  "runtime": {"kind": "http-process", "http": {"toolchain": "node", "version": "24", "argv": ["node", "server.js"], "port": 3000}},
  "domains": ["demo-node.localhost"],
  "bindings": [{"engine": "postgres", "service": "postgres17"}]
}'
code demo-node server.js <<'JS'
const http = require('http');
const port = Number(process.env.PORT || 3000);
http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ app: 'demo-node', node: process.version, path: req.url }));
}).listen(port, '0.0.0.0', () => console.log(`demo-node on :${port}`));
JS

for s in demo-php demo-node; do
  bento app start "$s" >/dev/null && bento app publish "$s" >/dev/null || log "start/publish $s failed"
done
bento apps
log "try: curl -H 'Host: demo-php.localhost' http://127.0.0.1:${EDGE_HTTP:-8880}/"
