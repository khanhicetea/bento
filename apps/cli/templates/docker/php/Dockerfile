# syntax=docker/dockerfile:1.7
# Bento PHP runtime: FPM + CLI + runner (s6-overlay) from one image.
ARG PHP_VERSION=8.4
ARG COMPOSER_VERSION=2
ARG NODE_VERSION=24

# Composer and Node come from versioned, digest-pinned official artifacts. Only
# their runtime files are copied into the final image.
FROM composer:${COMPOSER_VERSION} AS composer-artifact
FROM node:${NODE_VERSION}-bookworm-slim AS node-artifact

# Compile PHP extensions away from the runtime image. Development headers,
# compilers, PEAR sources, and apt metadata remain in this stage.
# Keep the PHP build stage on the same Debian release as the runtime stage.
# Unqualified PHP tags can advance to a newer Debian/glibc release independently,
# producing binaries that cannot start in the Bookworm runtime image.
FROM php:${PHP_VERSION}-fpm-bookworm AS php-extension-builder
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    libcurl4-openssl-dev \
    libfreetype6-dev \
    libicu-dev \
    libjpeg62-turbo-dev \
    libonig-dev \
    libpng-dev \
    libpq-dev \
    libsqlite3-dev \
    libssl-dev \
    libxml2-dev \
    libzip-dev; \
  docker-php-ext-configure gd --with-freetype --with-jpeg; \
  docker-php-ext-install -j"$(nproc)" bcmath exif gd intl pcntl pdo_mysql pdo_pgsql pdo_sqlite pgsql soap sockets zip mysqli; \
  (docker-php-ext-install -j"$(nproc)" opcache || docker-php-ext-enable opcache || true); \
  pecl install redis; \
  docker-php-ext-enable redis

# Export only the PHP runtime tree. The official PHP base intentionally ships
# PHPIZE_DEPS; copying from this cleaned stage avoids inheriting those layers.
FROM php-extension-builder AS php-runtime-artifact
RUN rm -rf \
    /usr/local/include/php \
    /usr/local/lib/php/build \
    /usr/local/bin/docker-php-ext-* \
    /usr/local/bin/peardev \
    /usr/local/bin/pecl \
    /usr/local/bin/php-config \
    /usr/local/bin/phpize \
    /usr/src/php* \
    /tmp/pear

# Download and unpack process-management assets in an intermediate rootfs so
# curl and xz used here do not add packages or archive layers to the runtime.
FROM debian:bookworm-slim AS process-manager-artifacts
ARG TARGETARCH
ARG S6_OVERLAY_VERSION=3.2.3.2
ARG S6_OVERLAY_NOARCH_SHA256=5379750ed30a84bbd2e2dd74847ba6b5bd29cd0b2e3ea2ec58049b57eb2eda12
ARG S6_OVERLAY_AMD64_SHA256=e6befcc96a437a3831386ecfc51808c5d3e939dc5fe3c02ae9284599e8aa2408
ARG S6_OVERLAY_ARM64_SHA256=b17f17a82e7a515c682a91edaf2ffdabb73f891981b6c1fd712115693a2f8b4c
ARG MINICROND_VERSION=0.2.2
ARG MINICROND_AMD64_SHA256=ff3e0011efabebd110a9e65277a13f595fe8216457c004b07e2958188c6a8652
ARG MINICROND_ARM64_SHA256=1e4e9ff7e4be7a319e378731df8963510400414be28b00ca7b6a554311edeb13
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends ca-certificates curl xz-utils; \
  case "${TARGETARCH:-$(dpkg --print-architecture)}" in \
    amd64|x86_64) \
      s6_arch=x86_64; \
      s6_sha="${S6_OVERLAY_AMD64_SHA256}"; \
      mc_arch=amd64; \
      mc_sha="${MINICROND_AMD64_SHA256}" ;; \
    arm64|aarch64) \
      s6_arch=aarch64; \
      s6_sha="${S6_OVERLAY_ARM64_SHA256}"; \
      mc_arch=arm64; \
      mc_sha="${MINICROND_ARM64_SHA256}" ;; \
    *) echo "unsupported architecture: ${TARGETARCH:-unknown}" >&2; exit 1 ;; \
  esac; \
  s6_base="https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}"; \
  curl -fsSLo /tmp/s6-noarch.tar.xz "${s6_base}/s6-overlay-noarch.tar.xz"; \
  curl -fsSLo /tmp/s6-arch.tar.xz "${s6_base}/s6-overlay-${s6_arch}.tar.xz"; \
  echo "${S6_OVERLAY_NOARCH_SHA256}  /tmp/s6-noarch.tar.xz" | sha256sum -c -; \
  echo "${s6_sha}  /tmp/s6-arch.tar.xz" | sha256sum -c -; \
  install -d /rootfs; \
  tar -C /rootfs -Jxpf /tmp/s6-noarch.tar.xz; \
  tar -C /rootfs -Jxpf /tmp/s6-arch.tar.xz; \
  install -d /rootfs/usr/local/bin; \
  curl -fsSLo /rootfs/usr/local/bin/minicrond \
    "https://github.com/khanhicetea/minicrond/releases/download/${MINICROND_VERSION}/minicrond-linux-${mc_arch}"; \
  echo "${mc_sha}  /rootfs/usr/local/bin/minicrond" | sha256sum -c -; \
  chmod 0755 /rootfs/usr/local/bin/minicrond

FROM debian:bookworm-slim
ARG PHP_VERSION
ENV BENTO_PHP_VERSION=${PHP_VERSION} \
    PHP_INI_DIR=/usr/local/etc/php

USER root

# Runtime libraries and operational tools only. Compilers, development
# headers, editors, pagers, and process-debugging packages are intentionally
# excluded from the production image.
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    gzip \
    libargon2-1 \
    libcurl4 \
    libffi8 \
    libfreetype6 \
    libgmp10 \
    libicu72 \
    libjpeg62-turbo \
    libldap-2.5-0 \
    libonig5 \
    libpng16-16 \
    libpq5 \
    libreadline8 \
    libsodium23 \
    libsqlite3-0 \
    sqlite3 \
    libssl3 \
    libstdc++6 \
    libxml2 \
    libzip4 \
    logrotate \
    openssh-client \
    unzip \
    vim \
    zip \
    zstd; \
  rm -rf /var/lib/apt/lists/*

COPY --from=php-runtime-artifact /usr/local/ /usr/local/
COPY --from=composer-artifact /usr/bin/composer /usr/local/bin/composer
COPY --from=node-artifact /usr/local/bin/node /usr/local/bin/node
COPY --from=node-artifact /usr/local/lib/node_modules/npm/ /usr/local/lib/node_modules/npm/
RUN ln -s node /usr/local/bin/nodejs \
  && ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
  && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
COPY --from=process-manager-artifacts /rootfs/ /

# The bind mount is available only during the build. Copying from it while
# creating the runtime user/group, directories, and permissions keeps all of
# this image initialization in one layer.
RUN --mount=type=bind,source=.,target=/tmp/bento-build,ro \
  set -eux; \
  getent group bento-web >/dev/null || groupadd -g 5000 bento-web; \
  install -d -m 0755 -o www-data -g bento-web /run/php-fpm; \
  install -d -m 0755 /etc/bento/minicrond /etc/bento/services /opt/bento/helpers /var/log/bento; \
  install -D -m 0644 /tmp/bento-build/conf/php-bento.ini /usr/local/etc/php/conf.d/zz-bento.ini; \
  install -D -m 0644 /tmp/bento-build/conf/opcache.ini /usr/local/etc/php/conf.d/zz-opcache.ini; \
  install -D -m 0644 /tmp/bento-build/conf/www-docker.conf /usr/local/etc/php-fpm.d/zz-docker.conf; \
  install -D -m 0755 /tmp/bento-build/entrypoint.sh /usr/local/bin/bento-php-entrypoint; \
  install -D -m 0755 /tmp/bento-build/runner-entrypoint.sh /usr/local/bin/bento-runner-entrypoint; \
  install -D -m 0755 /tmp/bento-build/s6-reconcile.sh /usr/local/bin/bento-s6-reconcile; \
  install -D -m 0755 /tmp/bento-build/minicrond-start.sh /usr/local/bin/bento-minicrond-start; \
  install -D -m 0644 /tmp/bento-build/minicrond-reconcile.php /usr/local/bin/bento-minicrond-reconcile; \
  cp -a /tmp/bento-build/helpers/. /opt/bento/helpers/; \
  find /opt/bento/helpers -type f -name '*.sh' -exec chmod 0755 {} +

WORKDIR /home
ENTRYPOINT ["bento-php-entrypoint"]
CMD ["php-fpm", "-F"]
