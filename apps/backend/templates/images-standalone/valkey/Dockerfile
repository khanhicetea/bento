# Valkey server and CLI for every runtime image, built once per inputs and
# copied into each runtime image (FROM ${VALKEY_IMAGE} AS valkey, then
# COPY --from=valkey). Rebuilding a runtime image, even without the layer
# cache, reuses this image instead of compiling Valkey again.
# Built on the oldest Debian release among the runtime bases: the binaries link
# only libc and libm, and glibc runs programs built against an older release.
# Built with the Docker Engine API (classic builder); no BuildKit features.
ARG VALKEY_BUILD_BASE
FROM ${VALKEY_BUILD_BASE} AS build
ARG VALKEY_VERSION
ARG VALKEY_SHA256
COPY build-valkey.sh /tmp/
RUN sh /tmp/build-valkey.sh

FROM scratch
COPY --from=build /rootfs/ /rootfs/
