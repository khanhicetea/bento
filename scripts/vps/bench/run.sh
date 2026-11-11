#!/bin/bash
# run.sh LABEL SLUG LUA [CONNS...] : wrk through the edge (same host) against an app's first host.
# SCHEME=https goes to https://<host>:<edge https port>/ (for hosts that redirect HTTP); default http via 127.0.0.1.
# LUA is wordpress.lua (visitor mix over $BENCH_DIR/<slug>-paths.txt) or plain.lua (GET /).
# For each connection count: req/s, p50/p99, errors, and CPU per request of the app container and of DB_CONTAINER
# (default <prefix>-mysql84; "none" to skip) from their cgroup cpu.stat. DUR (default 30s) and WARM (10s) apply.
# Results are appended to $BENCH_DIR/results.tsv.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
LABEL=$1 SLUG=$2 LUA=$3
shift 3
if [ $# -gt 0 ]; then CONNS=("$@"); else CONNS=(1 16); fi
DUR=${DUR:-30s}
WARM=${WARM:-10s}
DB=${DB_CONTAINER:-$PREFIX-mysql84}
HOST=$(bento --json hosts | jq -r --arg s "$SLUG" '[.hosts[] | select(.target.app == $s)][0].name // empty')
[ -n "$HOST" ] || { echo "$SLUG has no host" >&2; exit 1; }
APPC=$(app_container "$SLUG")
if [ "${SCHEME:-http}" = https ]; then
  URL="https://$HOST:$(bento --json edge | jq -r .settings.httpsPort)"
else
  URL="http://127.0.0.1:$EDGE_HTTP"
fi
export BENCH_PATHS="$BENCH_DIR/$SLUG-paths.txt"
cd "$here"

wrk -t2 -c16 -d"$WARM" -H "Host: $HOST" -s "$LUA" "$URL" >/dev/null 2>&1 || true
[ -s "$BENCH_DIR/results.tsv" ] ||
  printf 'when\thost\tlabel\tslug\tconns\treq\trps\tp50_ms\tp99_ms\terrors\tnon2xx\tapp_cpu_ms\tdb_cpu_ms\n' >"$BENCH_DIR/results.tsv"
for c in "${CONNS[@]}"; do
  t=2; [ "$c" -lt 2 ] && t=1
  a0=$(cpu_usec "$APPC")
  d0=0; [ "$DB" != none ] && d0=$(cpu_usec "$DB")
  out=$(wrk -t"$t" -c"$c" -d"$DUR" --timeout 10s -H "Host: $HOST" -s "$LUA" "$URL")
  a1=$(cpu_usec "$APPC")
  d1=0; [ "$DB" != none ] && d1=$(cpu_usec "$DB")
  line=$(grep '^RESULT' <<<"$out")
  req=$(sed -n 's/.* req=\([0-9]*\).*/\1/p' <<<"$line")
  app_ms=$(awk -v a="$((a1 - a0))" -v r="$req" 'BEGIN {printf "%.1f", r ? a / r / 1000 : 0}')
  db_ms=$(awk -v a="$((d1 - d0))" -v r="$req" 'BEGIN {printf "%.1f", r ? a / r / 1000 : 0}')
  rps=$(sed -n 's/.* rps=\([0-9.]*\).*/\1/p' <<<"$line")
  p50=$(sed -n 's/.* p50=\([0-9.]*\).*/\1/p' <<<"$line")
  p99=$(sed -n 's/.* p99=\([0-9.]*\).*/\1/p' <<<"$line")
  errs=$(sed -n 's/.* errors=\([0-9]*\).*/\1/p' <<<"$line")
  non2xx=$(sed -n 's/.* non2xx=\([0-9]*\).*/\1/p' <<<"$line")
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$(date -u +%FT%TZ)" "$(hostname)" "$LABEL" "$SLUG" \
    "$c" "$req" "$rps" "$p50" "$p99" "$errs" "$non2xx" "$app_ms" "$db_ms" | tee -a "$BENCH_DIR/results.tsv"
done
