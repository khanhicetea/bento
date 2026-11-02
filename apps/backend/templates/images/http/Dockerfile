# Bento managed HTTP-process runtime image (Node.js, Bun, or Python toolchain).
# Built once per toolchain version and shared by all HTTP apps on it.
# Built with the Docker Engine API (classic builder); no BuildKit features.
ARG TOOLCHAIN_BASE
ARG DEBIAN_BASE
ARG VALKEY_BUILD_BASE

FROM ${DEBIAN_BASE} AS artifacts
ARG S6_OVERLAY_VERSION
ARG S6_OVERLAY_NOARCH_SHA256
ARG S6_OVERLAY_AMD64_SHA256
ARG S6_OVERLAY_ARM64_SHA256
ARG MINICROND_VERSION
ARG MINICROND_AMD64_SHA256
ARG MINICROND_ARM64_SHA256
COPY fetch-artifacts.sh /tmp/fetch-artifacts.sh
RUN sh /tmp/fetch-artifacts.sh

# Valkey for apps that turn it on, built once for every runtime image on the
# oldest Debian release among them from the checksum-verified source tarball.
FROM ${VALKEY_BUILD_BASE} AS valkey
ARG VALKEY_VERSION
ARG VALKEY_SHA256
COPY apt-sources.sh build-valkey.sh /tmp/
RUN sh /tmp/apt-sources.sh && sh /tmp/build-valkey.sh

FROM ${TOOLCHAIN_BASE}
ARG TOOLCHAIN
ENV S6_READ_ONLY_ROOT=1 \
    S6_KEEP_ENV=1 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_KILL_GRACETIME=3000 \
    S6_SERVICES_GRACETIME=8000 \
    BENTO_RUNTIME_KIND=http-process \
    BENTO_TOOLCHAIN=${TOOLCHAIN}
USER root
# Valkey is built on another release: it must run on this toolchain's libc.
COPY --from=valkey /rootfs/ /
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends bash ca-certificates curl git openssh-client sqlite3 xz-utils; \
  rm -rf /var/lib/apt/lists/*; \
  /usr/local/bin/valkey-server --version
COPY --from=artifacts /rootfs/ /
COPY rootfs/ /
WORKDIR /home
ENTRYPOINT ["/usr/local/bin/bento-init"]
