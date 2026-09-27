# Bento managed PHP runtime image. Built once per PHP version and shared by all
# PHP apps on that version; per-app configuration is mounted at /etc/bento.
# Built with the Docker Engine API (classic builder); no BuildKit features.
ARG PHP_BASE
ARG DEBIAN_BASE

FROM ${PHP_BASE} AS php-build
# Also records the Debian packages providing the shared libraries PHP links
# against, so the runtime stage installs exactly those on any base release.
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
  pecl install redis; \
  docker-php-ext-enable redis; \
  find /usr/local/bin /usr/local/sbin /usr/local/lib/php/extensions -type f \
    \( -perm -u+x -o -name '*.so' \) -exec ldd {} + 2>/dev/null \
    | awk '/=> \// { print $3 }' | sort -u \
    | while read -r lib; do echo "$lib"; realpath "$lib"; done \
    | xargs -r dpkg -S 2>/dev/null | cut -d: -f1 | sort -u \
    > /usr/local/share/bento-php-runtime-deps; \
  cat /usr/local/share/bento-php-runtime-deps; \
  rm -rf /usr/local/include/php /usr/local/lib/php/build /usr/local/bin/docker-php-ext-* \
    /usr/local/bin/peardev /usr/local/bin/pecl /usr/local/bin/php-config /usr/local/bin/phpize \
    /usr/src/php* /tmp/pear /usr/local/etc/php-fpm.d/*

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

FROM ${DEBIAN_BASE}
ENV PHP_INI_DIR=/usr/local/etc/php \
    PHP_INI_SCAN_DIR=:/etc/bento/php.d \
    S6_READ_ONLY_ROOT=1 \
    S6_KEEP_ENV=1 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_KILL_GRACETIME=3000 \
    S6_SERVICES_GRACETIME=8000 \
    BENTO_RUNTIME_KIND=php-fpm
COPY --from=php-build /usr/local/share/bento-php-runtime-deps /tmp/bento-php-runtime-deps
COPY apt-sources.sh /tmp/apt-sources.sh
# EOL bases are the PHP image itself: drop its PHP so only the build stage's
# remains, and reset the stop signal and command it would otherwise pass on.
# Bullseye's nginx-light lacks the geo module the app config uses.
RUN set -eux; \
  sh /tmp/apt-sources.sh; \
  rm /tmp/apt-sources.sh; \
  rm -rf /usr/local/bin/* /usr/local/sbin/* /usr/local/lib/php /usr/local/include/php /usr/local/etc/*; \
  . /etc/os-release; \
  nginx_pkg=nginx-light; \
  if [ "$VERSION_CODENAME" = bullseye ]; then nginx_pkg=nginx-core; fi; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    bash bash-completion ca-certificates curl git grep gzip jq less openssh-client \
    sqlite3 unzip vim zip zstd "$nginx_pkg" \
    $(cat /tmp/bento-php-runtime-deps); \
  printf '%s\n' '[ -f /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion' \
    >> /etc/bash.bashrc; \
  rm -rf /var/lib/apt/lists/* /tmp/bento-php-runtime-deps /etc/nginx/sites-enabled /etc/nginx/conf.d
COPY --from=php-build /usr/local/ /usr/local/
COPY --from=artifacts /rootfs/ /
COPY rootfs/ /
WORKDIR /home
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/bento-init"]
CMD []
