# shellcheck shell=bash
# Load Bento-generated runtime metadata and protected credentials into the
# environment without evaluating either file as shell code. Keys are validated;
# values are exported verbatim. Missing files are tolerated so ephemeral tools
# that were deliberately given no credentials still start.
bento_load_env_file() {
  local file="$1" key value
  [[ -r "$file" ]] || return 0
  while IFS='=' read -r key value || [[ -n "$key$value" ]]; do
    [[ -z "$key" || "$key" == \#* ]] && continue
    if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      echo "bento: invalid key in $file" >&2
      return 65
    fi
    value="${value%$'\r'}"
    export "$key=$value"
  done < "$file"
}

bento_load_env_file /etc/bento/app.env || exit $?
bento_load_env_file /etc/bento/runtime.env || exit $?
bento_load_env_file /etc/bento/credentials.env || exit $?
export USER="${BENTO_APP_SLUG:-app}" LOGNAME="${BENTO_APP_SLUG:-app}"
export MINICRON_DATA="${HOME}/.local/share/minicron"
export MINICRON_CONFIG=/etc/bento/minicrond.toml
export BASE_PATH="${BENTO_SCHEDULER_BASE_PATH:-}"
