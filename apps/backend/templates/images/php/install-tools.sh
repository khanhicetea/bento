#!/bin/sh
# Operator tooling and the app-local Nginx of the PHP runtime images. Run as the
# first final-stage step of both images/php and images/php-sury, before
# anything PHP-version-specific, so on one Debian release the classic builder
# reuses this layer, and the Node, artifact and Valkey layers stacked on it,
# for every PHP version.
set -eux
sh /tmp/apt-sources.sh
# EOL bases are the PHP image itself: drop its PHP so only the build stage's
# remains. Nothing matches on a plain Debian base.
rm -rf /usr/local/bin/* /usr/local/sbin/* /usr/local/lib/php /usr/local/include/php /usr/local/etc/*
. /etc/os-release
# Bullseye's nginx-light lacks the geo module the app config uses.
nginx_pkg=nginx-light
if [ "$VERSION_CODENAME" = bullseye ]; then nginx_pkg=nginx-core; fi
apt-get update
apt-get install -y --no-install-recommends \
  bash bash-completion ca-certificates curl git grep gzip jq less openssh-client \
  sqlite3 unzip vim-tiny zip zstd "$nginx_pkg"
# vim-tiny only registers vi; keep `vim` working in shells and deploy scripts.
update-alternatives --install /usr/bin/vim vim /usr/bin/vim.tiny 10
printf '%s\n' '[ -f /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion' \
  >> /etc/bash.bashrc
rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled /etc/nginx/conf.d /tmp/apt-sources.sh /tmp/install-tools.sh
