#!/bin/bash
# all.sh : the full benchmark round on this host. Builds the WordPress fixture if missing, then runs the visitor
# mix at 1/16/64 connections and the e2e Node, PHP and Bun apps (smoke.sh or final.sh first) at 200 connections
# over HTTPS. Results go to /root/bench/results.tsv.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
[ -s "$BENCH_DIR/wpbench-paths.txt" ] || bash "$here/wp-up.sh"
sleep 20
bash "$here/run.sh" wp-mix wpbench wordpress.lua 1 16 64
export SCHEME=https DUR=20s
DB_CONTAINER=$PREFIX-postgres18 bash "$here/run.sh" plain e2e-node plain.lua 200
bash "$here/run.sh" plain e2e-php plain.lua 200
DB_CONTAINER=none bash "$here/run.sh" plain e2e-bun plain.lua 200
