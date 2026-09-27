# Bento managed PHP runtime image. Built once per PHP version and shared by all
# PHP apps on that version; per-app configuration is mounted at /etc/bento.
# Built with the Docker Engine API (classic builder); no BuildKit features.
ARG PHP_BASE
ARG DEBIAN_BASE

FROM ${PHP_BASE} AS php-build
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    libcurl4-openssl-dev libfreetype6-dev libicu-dev libjpeg62-turbo-dev libonig-dev \
    libpng-dev libpq-dev libsqlite3-dev libssl-dev libxml2-dev libzip-dev; \
  docker-php-ext-configure gd --with-freetype --with-jpeg; \
  docker-php-ext-install -j"$(nproc)" bcmath exif gd intl pcntl pdo_mysql pdo_pgsql pdo_sqlite pgsql soap sockets zip mysqli; \
  (docker-php-ext-install -j"$(nproc)" opcache || docker-php-ext-enable opcache || true); \
  pecl install redis; \
  docker-php-ext-enable redis; \
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
COPY fetch-artifacts.sh /tmp/fetch-artifacts.sh
RUN sh /tmp/fetch-artifacts.sh

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
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    bash ca-certificates curl git gzip unzip zip zstd sqlite3 openssh-client nginx-light \
    libargon2-1 libcurl4 libffi8 libfreetype6 libgmp10 libicu72 libjpeg62-turbo libonig5 \
    libpng16-16 libpq5 libreadline8 libsodium23 libsqlite3-0 libssl3 libstdc++6 libxml2 libzip4; \
  rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled /etc/nginx/conf.d
COPY --from=php-build /usr/local/ /usr/local/
COPY --from=artifacts /rootfs/ /
COPY rootfs/ /
WORKDIR /home
ENTRYPOINT ["/usr/local/bin/bento-init"]
