# shellcheck shell=bash
# Sourced by the bench scripts: find the running stack, its name, and the edge HTTP port.
# BENTO_STACK_ROOT may be set; otherwise it is read from the first running bento@ service.
if [ -z "${BENTO_STACK_ROOT:-}" ]; then
  unit=$(systemctl list-units --type=service --state=running --plain --no-legend 'bento@*' | awk 'NR == 1 {print $1}')
  [ -n "$unit" ] || { echo "no running bento@ service" >&2; exit 1; }
  pid=$(systemctl show "$unit" -p MainPID --value)
  BENTO_STACK_ROOT=$(tr '\0' '\n' <"/proc/$pid/environ" | sed -n 's/^BENTO_STACK_ROOT=//p')
fi
export BENTO_STACK_ROOT
STACK_NAME=$(bento --json status | jq -r .stackName)
PREFIX=bento-$STACK_NAME
EDGE_HTTP=$(bento --json edge | jq -r .settings.httpPort)
IP4=$(ip -4 route get 1.1.1.1 | grep -o 'src [^ ]*' | cut -d' ' -f2)
BENCH_DIR=/root/bench
mkdir -p "$BENCH_DIR"

# cpu_usec CONTAINER: total CPU time of a container's cgroup, in microseconds.
cpu_usec() {
  local id
  id=$(docker inspect -f '{{.Id}}' "$1")
  awk '/^usage_usec/ {print $2}' "/sys/fs/cgroup/system.slice/docker-$id.scope/cpu.stat"
}

app_container() { echo "$PREFIX-app-$(bento --json app show "$1" | jq -r .id)"; }
