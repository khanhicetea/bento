# Bento managed PHP runtime image for versions upstream no longer releases
# (7.4, 8.0, 8.1). PHP comes from the deb.sury.org packages on Debian, which
# carry security backports under the old upstream version. Overlaid on
# images/php, so the s6 services, nginx and ini files are shared with it.
# Built with the Docker Engine API (classic builder); no BuildKit features.
ARG DEBIAN_BASE
ARG VALKEY_BUILD_BASE

# Node.js LTS with npm and pnpm, installed by mise, so apps can install
# node_modules and build front-end assets with the tooling exec.
FROM ${DEBIAN_BASE} AS toolchain
ARG MISE_VERSION
ARG MISE_AMD64_SHA256
ARG MISE_ARM64_SHA256
ARG MISE_TOOLS
COPY apt-sources.sh build-toolchain.sh /tmp/
RUN sh /tmp/build-toolchain.sh

FROM ${DEBIAN_BASE} AS artifacts
ARG S6_OVERLAY_VERSION
ARG S6_OVERLAY_NOARCH_SHA256
ARG S6_OVERLAY_AMD64_SHA256
ARG S6_OVERLAY_ARM64_SHA256
ARG MINICROND_VERSION
ARG MINICROND_AMD64_SHA256
ARG MINICROND_ARM64_SHA256
ARG COMPOSER_CHANNEL
COPY apt-sources.sh fetch-artifacts.sh /tmp/
RUN sh /tmp/apt-sources.sh && sh /tmp/fetch-artifacts.sh

# Valkey for apps that turn it on, built once for every runtime image on the
# oldest Debian release among them from the checksum-verified source tarball.
FROM ${VALKEY_BUILD_BASE} AS valkey
ARG VALKEY_VERSION
ARG VALKEY_SHA256
COPY apt-sources.sh build-valkey.sh /tmp/
RUN sh /tmp/apt-sources.sh && sh /tmp/build-valkey.sh

# Up to the PHP-specific ARGs, this stage is identical in images/php: on one
# Debian release every PHP version shares these layers.
FROM ${DEBIAN_BASE}
ENV S6_READ_ONLY_ROOT=1 \
    S6_KEEP_ENV=1 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_KILL_GRACETIME=3000 \
    S6_SERVICES_GRACETIME=8000 \
    BENTO_RUNTIME_KIND=php-fpm \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    PNPM_CONFIG_UPDATE_NOTIFIER=false \
    MISE_HIDE_UPDATE_WARNING=1
COPY apt-sources.sh install-tools.sh /tmp/
RUN sh /tmp/install-tools.sh
COPY --from=toolchain /opt/mise /opt/mise
COPY --from=toolchain /rootfs/ /
COPY --from=artifacts /rootfs/ /
COPY --from=valkey /rootfs/ /
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends $(cat /usr/local/share/bento-toolchain-deps); \
  rm -rf /var/lib/apt/lists/*; \
  node --version; npm --version; pnpm --version; mise --version; valkey-server --version
ARG PHP_VERSION
ARG PHP_MIN_PKG_VERSION
ARG SURY_KEY_SHA256
# Debian's own php.ini files are dropped below, so the scan dirs are the only
# configuration: Debian's module ini files, then Bento's, then the app's.
ENV PHP_INI_SCAN_DIR=:/usr/local/etc/php/conf.d:/etc/bento/php.d
# The signing key is pinned by checksum. The installed packages must be at least
# the release that carries the PHP2Shell fix (CVE-2026-109618, CVE-2026-109619);
# php -v does not change with a backport, so the package version is the proof.
# Debian's php.ini (production values) is removed so PHP runs on the same
# compiled defaults as the official images: it also disables session garbage
# collection in favour of a cron job this container does not have.
RUN set -eux; \
  curl -fsSLo /usr/share/keyrings/deb.sury.org-php.gpg https://packages.sury.org/php/apt.gpg; \
  echo "${SURY_KEY_SHA256}  /usr/share/keyrings/deb.sury.org-php.gpg" | sha256sum -c -; \
  . /etc/os-release; \
  echo "deb [signed-by=/usr/share/keyrings/deb.sury.org-php.gpg] https://packages.sury.org/php/ ${VERSION_CODENAME} main" \
    > /etc/apt/sources.list.d/php-sury.list; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    "php${PHP_VERSION}-cli" "php${PHP_VERSION}-fpm" "php${PHP_VERSION}-bcmath" "php${PHP_VERSION}-curl" \
    "php${PHP_VERSION}-gd" "php${PHP_VERSION}-intl" "php${PHP_VERSION}-mbstring" "php${PHP_VERSION}-mysql" \
    "php${PHP_VERSION}-opcache" "php${PHP_VERSION}-pgsql" "php${PHP_VERSION}-readline" "php${PHP_VERSION}-redis" \
    "php${PHP_VERSION}-soap" "php${PHP_VERSION}-sqlite3" "php${PHP_VERSION}-xml" "php${PHP_VERSION}-zip"; \
  for pkg in cli fpm common; do \
    have="$(dpkg-query -W -f='${Version}' "php${PHP_VERSION}-${pkg}")"; \
    dpkg --compare-versions "$have" ge "${PHP_MIN_PKG_VERSION}" \
      || { echo "php${PHP_VERSION}-${pkg} ${have} is older than ${PHP_MIN_PKG_VERSION}" >&2; exit 1; }; \
  done; \
  rm -f /etc/php/*/cli/php.ini /etc/php/*/fpm/php.ini; \
  install -d /usr/local/bin /usr/local/sbin; \
  ln -s "/usr/bin/php${PHP_VERSION}" /usr/local/bin/php; \
  ln -s "/usr/sbin/php-fpm${PHP_VERSION}" /usr/local/sbin/php-fpm; \
  php -v; php-fpm -v; \
  rm -rf /var/lib/apt/lists/* /etc/apt/sources.list.d/php-sury.list
COPY rootfs/ /
COPY write-manifest.sh /tmp/
RUN sh /tmp/write-manifest.sh
WORKDIR /home
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/bento-init"]
CMD []
