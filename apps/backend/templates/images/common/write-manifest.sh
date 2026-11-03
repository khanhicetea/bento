#!/bin/sh
# Record what this image resolved from its floating inputs, one key=value per
# line, in /usr/local/share/bento-manifest. Bento reads it after a build: a
# rebuild whose manifest (plus the base image ids) matches the current build's
# changed nothing and is discarded. The last step of every runtime image.
set -eu
out=/usr/local/share/bento-manifest
has() { command -v "$1" >/dev/null 2>&1; }
put() { [ -n "$2" ] && printf '%s=%s\n' "$1" "$2"; return 0; }
{
  put debian "$(cat /etc/debian_version)"
  if has php; then
    put php "$(php -r 'echo PHP_VERSION;')"
    put phpredis "$(php -r 'echo phpversion("redis");')"
  fi
  if [ -x /usr/local/bin/composer ]; then
    put composer "$(COMPOSER_ALLOW_SUPERUSER=1 php /usr/local/bin/composer --version --no-ansi 2>/dev/null | awk '{ print $3 }')"
  fi
  has nginx && put nginx "$(nginx -v 2>&1 | sed 's|.*/||')"
  has node && put node "$(node --version | sed 's/^v//')"
  has npm && put npm "$(npm --version)"
  has pnpm && put pnpm "$(pnpm --version)"
  has bun && put bun "$(bun --version)"
  has python3 && put python "$(python3 -c 'import platform; print(platform.python_version())')"
  has mise && put mise "$(MISE_HIDE_UPDATE_WARNING=1 mise --version | awk '{ print $1 }')"
  has valkey-server && put valkey "$(valkey-server --version | sed 's/.* v=\([^ ]*\).*/\1/')"
  put debian-packages "$(dpkg-query -W -f '${Package}=${Version}\n' | sort | sha256sum | cut -c1-16)"
} > "$out"
cat "$out"
rm -f /tmp/write-manifest.sh
