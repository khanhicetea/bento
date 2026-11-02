# Bento managed PHP runtime image. Built once per PHP version and shared by all
# PHP apps on that version; per-app configuration is mounted at /etc/bento.
# Built with the Docker Engine API (classic builder); no BuildKit features.
ARG PHP_BASE
ARG DEBIAN_BASE
ARG NODE_BASE
ARG VALKEY_BUILD_BASE

FROM ${PHP_BASE} AS php-build
ARG PHPREDIS_VERSION
ARG PHPREDIS_SHA256
# Also records the Debian packages providing the shared libraries PHP links
# against, so the runtime stage installs exactly those on any base release,
# and the loaded modules, which the runtime stage must reproduce.
COPY apt-sources.sh /tmp/apt-sources.sh
RUN set -eux; \
  sh /tmp/apt-sources.sh; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    libcurl4-openssl-dev libfreetype6-dev libicu-dev libjpeg62-turbo-dev libonig-dev \
    libpng-dev libpq-dev libsqlite3-dev libssl-dev libxml2-dev libzip-dev; \
  docker-php-ext-configure gd --with-freetype --with-jpeg; \
  docker-php-ext-install -j"$(nproc)" bcmath exif gd intl pcntl pdo_mysql pdo_pgsql pdo_sqlite pgsql soap sockets zip mysqli; \
  (docker-php-ext-install -j"$(nproc)" opcache || docker-php-ext-enable opcache || true); \
  curl -fsSLo /tmp/redis.tgz "https://pecl.php.net/get/redis-${PHPREDIS_VERSION}.tgz"; \
  echo "${PHPREDIS_SHA256}  /tmp/redis.tgz" | sha256sum -c -; \
  pecl install /tmp/redis.tgz; \
  docker-php-ext-enable redis; \
  php -m | grep -qx 'Zend OPcache'; \
  php -m | grep -qx redis; \
  php -m > /usr/local/share/bento-php-modules; \
  find /usr/local/bin /usr/local/sbin /usr/local/lib/php/extensions -type f \
    \( -perm -u+x -o -name '*.so' \) -exec ldd {} + 2>/dev/null \
    | awk '/=> \// { print $3 }' | sort -u \
    | while read -r lib; do echo "$lib"; realpath "$lib"; done \
    | xargs -r dpkg -S 2>/dev/null | grep -v '^diversion by' | cut -d: -f1 | sort -u \
    > /usr/local/share/bento-php-runtime-deps; \
  cat /usr/local/share/bento-php-runtime-deps; \
  rm -rf /usr/local/include/php /usr/local/lib/php/build /usr/local/bin/docker-php-ext-* \
    /usr/local/bin/peardev /usr/local/bin/pecl /usr/local/bin/php-config /usr/local/bin/phpize \
    /usr/src/php* /tmp/pear /tmp/redis.tgz /usr/local/etc/php-fpm.d/*

# Node.js LTS with npm and pnpm, so apps can install node_modules and build
# front-end assets with the tooling exec. pnpm's tarball is checksum-verified;
# its install script swaps in the native binary for this architecture.
FROM ${NODE_BASE} AS node
ARG PNPM_VERSION
ARG PNPM_SHA256
RUN set -eux; \
  cd /tmp; \
  npm pack --silent "pnpm@${PNPM_VERSION}"; \
  echo "${PNPM_SHA256}  pnpm-${PNPM_VERSION}.tgz" | sha256sum -c -; \
  npm install -g --no-audit --no-fund --allow-scripts=pnpm "./pnpm-${PNPM_VERSION}.tgz"; \
  pnpm --version; \
  rm -rf /tmp/* /root/.npm

FROM ${DEBIAN_BASE} AS artifacts
ARG S6_OVERLAY_VERSION
ARG S6_OVERLAY_NOARCH_SHA256
ARG S6_OVERLAY_AMD64_SHA256
ARG S6_OVERLAY_ARM64_SHA256
ARG MINICROND_VERSION
ARG MINICROND_AMD64_SHA256
ARG MINICROND_ARM64_SHA256
ARG COMPOSER_VERSION
ARG COMPOSER_SHA256
COPY apt-sources.sh fetch-artifacts.sh /tmp/
RUN sh /tmp/apt-sources.sh && sh /tmp/fetch-artifacts.sh

# Valkey for apps that turn it on, built once for every runtime image on the
# oldest Debian release among them from the checksum-verified source tarball.
FROM ${VALKEY_BUILD_BASE} AS valkey
ARG VALKEY_VERSION
ARG VALKEY_SHA256
COPY apt-sources.sh build-valkey.sh /tmp/
RUN sh /tmp/apt-sources.sh && sh /tmp/build-valkey.sh

# Up to the PHP-specific ENV, this stage is identical in images/php-sury: on
# one Debian release every PHP version shares these layers.
FROM ${DEBIAN_BASE}
ENV S6_READ_ONLY_ROOT=1 \
    S6_KEEP_ENV=1 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_KILL_GRACETIME=3000 \
    S6_SERVICES_GRACETIME=8000 \
    BENTO_RUNTIME_KIND=php-fpm \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    PNPM_CONFIG_UPDATE_NOTIFIER=false
COPY apt-sources.sh install-tools.sh /tmp/
RUN sh /tmp/install-tools.sh
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
COPY --from=node /usr/local/lib/node_modules/pnpm /usr/local/lib/node_modules/pnpm
COPY --from=artifacts /rootfs/ /
COPY --from=valkey /rootfs/ /
RUN set -eux; \
  cd /usr/local/bin; \
  ln -s ../lib/node_modules/npm/bin/npm-cli.js npm; \
  ln -s ../lib/node_modules/npm/bin/npx-cli.js npx; \
  for b in pnpm pnpx pn pnx; do ln -s "../lib/node_modules/pnpm/$b" "$b"; done; \
  node --version; npm --version; pnpm --version; valkey-server --version
# PHP itself and the libraries it links against. A module the build stage
# loaded but this image cannot (a missing library) fails the build here.
ENV PHP_INI_DIR=/usr/local/etc/php \
    PHP_INI_SCAN_DIR=:/etc/bento/php.d
COPY --from=php-build /usr/local/ /usr/local/
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends $(cat /usr/local/share/bento-php-runtime-deps); \
  rm -rf /var/lib/apt/lists/*; \
  php -m > /tmp/php-modules; \
  diff /usr/local/share/bento-php-modules /tmp/php-modules; \
  rm /tmp/php-modules; \
  php-fpm -v
COPY rootfs/ /
WORKDIR /home
# EOL bases are the PHP image itself: reset the stop signal and command it
# would otherwise pass on.
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/bento-init"]
CMD []
