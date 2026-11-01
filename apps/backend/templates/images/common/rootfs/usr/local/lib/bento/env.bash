# shellcheck shell=bash
# Load Bento-generated runtime metadata and protected credentials into the
# environment without evaluating either file as shell code. Keys are validated;
# values are exported verbatim. Missing files are tolerated so ephemeral tools
# that were deliberately given no credentials still start.
bento_load_env_file() {
  local file="$1" line key value
  [[ -r "$file" ]] || return 0
  # Split each line at its first '=' with parameter expansion. `IFS='=' read
  # key value` would drop one trailing '=' from the value (base64 padding).
  while IFS= read -r line || [[ -n "$line" ]]; do
    key="${line%%=*}"
    value=""
    [[ "$line" == *=* ]] && value="${line#*=}"
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
export VALKEY_DATA="${HOME}/.local/share/valkey"
# Lets the Bento UI embed the scheduler page in a same-origin iframe.
export MINICRON_ALLOW_IFRAME=1
export BASE_PATH="${BENTO_SCHEDULER_BASE_PATH:-}"
