#!/bin/sh
# Download checksum-verified supervision, scheduler and Composer artifacts into
# /rootfs. s6-overlay and minicrond are pinned by version and checksum.
set -eux
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl xz-utils
arch="$(dpkg --print-architecture)"
case "$arch" in
  amd64) s6_arch=x86_64; s6_sha="$S6_OVERLAY_AMD64_SHA256"; mc_arch=amd64; mc_sha="$MINICROND_AMD64_SHA256" ;;
  arm64) s6_arch=aarch64; s6_sha="$S6_OVERLAY_ARM64_SHA256"; mc_arch=arm64; mc_sha="$MINICROND_ARM64_SHA256" ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
base="https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}"
curl -fsSLo /tmp/s6-noarch.tar.xz "$base/s6-overlay-noarch.tar.xz"
curl -fsSLo /tmp/s6-arch.tar.xz "$base/s6-overlay-${s6_arch}.tar.xz"
echo "${S6_OVERLAY_NOARCH_SHA256}  /tmp/s6-noarch.tar.xz" | sha256sum -c -
echo "${s6_sha}  /tmp/s6-arch.tar.xz" | sha256sum -c -
install -d /rootfs/usr/local/bin
tar -C /rootfs -Jxpf /tmp/s6-noarch.tar.xz
tar -C /rootfs -Jxpf /tmp/s6-arch.tar.xz
curl -fsSLo /rootfs/usr/local/bin/minicrond \
  "https://github.com/khanhicetea/minicrond/releases/download/${MINICROND_VERSION}/minicrond-linux-${mc_arch}"
echo "${mc_sha}  /rootfs/usr/local/bin/minicrond" | sha256sum -c -
chmod 0755 /rootfs/usr/local/bin/minicrond
# Composer follows its release channel (latest-2.x): rebuilding the image picks
# up new releases. The phar is checked against the channel's published SHA-256.
if [ -n "${COMPOSER_CHANNEL:-}" ]; then
  composer_url="https://getcomposer.org/download/${COMPOSER_CHANNEL}/composer.phar"
  curl -fsSLo /rootfs/usr/local/bin/composer "$composer_url"
  composer_sha="$(curl -fsSL "${composer_url}.sha256" | awk '{ print $1 }')"
  echo "${composer_sha}  /rootfs/usr/local/bin/composer" | sha256sum -c -
  chmod 0755 /rootfs/usr/local/bin/composer
fi
