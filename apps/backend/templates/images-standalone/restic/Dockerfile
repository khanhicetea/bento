# App backup image: the pinned rclone image plus a checksum-verified restic.
ARG RCLONE_IMAGE
FROM ${RCLONE_IMAGE}
ARG RESTIC_VERSION
ARG RESTIC_AMD64_SHA256
ARG RESTIC_ARM64_SHA256
RUN set -eu; \
    case "$(uname -m)" in \
      x86_64) arch=amd64; sha="$RESTIC_AMD64_SHA256" ;; \
      aarch64) arch=arm64; sha="$RESTIC_ARM64_SHA256" ;; \
      *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
    esac; \
    wget -qO /tmp/restic.bz2 "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${arch}.bz2"; \
    echo "${sha}  /tmp/restic.bz2" | sha256sum -c -; \
    bunzip2 -c /tmp/restic.bz2 > /usr/local/bin/restic; \
    chmod 0755 /usr/local/bin/restic; \
    rm /tmp/restic.bz2; \
    restic version
ENTRYPOINT []
