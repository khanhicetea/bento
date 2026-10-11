#!/bin/bash
# Manage one live-e2e tunnel hostname: NAME.$E2E_DOMAIN on the e2e Cloudflare Tunnel plus its proxied CNAME.
#
#   cf-host.sh add NAME [SERVICE]   route NAME to SERVICE (default: the e2e stack edge)
#   cf-host.sh rm NAME              drop the route and its DNS record
#
# Reads ~/.config/bento-e2e/env (CF_API_TOKEN, CF_ACCOUNT_ID, CF_ZONE_ID, E2E_DOMAIN) and
# ~/.config/bento-e2e/tunnel-id. Only touches the named hostname.
set -euo pipefail
set -a
# shellcheck source=/dev/null
. "$HOME/.config/bento-e2e/env"
set +a
op=$1 name=$2 svc=${3:-http://bento-${E2E_STACK:-e2e}-edge:80}
fqdn="$name.$E2E_DOMAIN"
tid=$(cat "$HOME/.config/bento-e2e/tunnel-id")
H="Authorization: Bearer $CF_API_TOKEN"

api=https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/cfd_tunnel/$tid/configurations
cur=$(curl -fsS -H "$H" "$api" | jq '.result.config.ingress // []')
rules=$(jq --arg h "$fqdn" '[.[] | select(.hostname != $h and .hostname != null)]' <<<"$cur")
if [ "$op" = add ]; then
  rules=$(jq --arg h "$fqdn" --arg s "$svc" '[{hostname: $h, service: $s}] + .' <<<"$rules")
fi
jq -n --argjson r "$rules" '{config: {ingress: ($r + [{service: "http_status:404"}])}}' |
  curl -fsS -X PUT -H "$H" -H 'Content-Type: application/json' "$api" -d @- | jq -c '{tunnel: .success}'

zone=https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID/dns_records
ids=$(curl -fsS -H "$H" "$zone?type=CNAME&name=$fqdn" | jq -r '.result[].id')
if [ "$op" = add ] && [ -z "$ids" ]; then
  jq -n --arg n "$fqdn" --arg c "$tid.cfargotunnel.com" \
    '{type: "CNAME", name: $n, content: $c, proxied: true, comment: "bento-e2e"}' |
    curl -fsS -X POST -H "$H" -H 'Content-Type: application/json' "$zone" -d @- | jq -c '{dns: .success}'
elif [ "$op" = rm ]; then
  for id in $ids; do
    curl -fsS -X DELETE -H "$H" "$zone/$id" | jq -c '{dnsDeleted: .success}'
  done
fi
