#!/bin/sh
# Install a runtime image's toolchain with mise, in a stage of its own.
#
# MISE_TOOLS lists mise tool requests ("node@24 pnpm@latest", "python@3.13",
# "bun@1.3"). Each resolves to the newest matching release when the image is
# built, so rebuilding the image picks up patch releases without a Bento
# release. The mise binary itself is pinned by version and checksum.
#
# Results, copied into the runtime stage:
#   /opt/mise                             the installed tools (read-only)
#   /rootfs/usr/local/bin/mise            mise, for operator-installed tools
#   /rootfs/usr/local/bin/<tool>          links to every installed executable
#   /rootfs/usr/local/share/bento-toolchain-deps
#                                         Debian packages the tools link against
#
# Python is compiled from source (python-build), so it links against this
# Debian release's libraries; node, pnpm and bun are release binaries that
# mise verifies against their published checksums.
set -eux
sh /tmp/apt-sources.sh
apt-get update
build_deps=""
case " ${MISE_TOOLS} " in
  *" python@"*)
    build_deps="build-essential libbz2-dev libffi-dev libgdbm-dev liblzma-dev libncurses-dev \
      libreadline-dev libsqlite3-dev libssl-dev libzstd-dev tk-dev uuid-dev zlib1g-dev"
    ;;
esac
# shellcheck disable=SC2086
apt-get install -y --no-install-recommends ca-certificates curl git unzip xz-utils $build_deps
arch="$(dpkg --print-architecture)"
case "$arch" in
  amd64) mise_arch=x64; mise_sha="$MISE_AMD64_SHA256" ;;
  arm64) mise_arch=arm64; mise_sha="$MISE_ARM64_SHA256" ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
install -d /rootfs/usr/local/bin /rootfs/usr/local/share
curl -fsSLo /rootfs/usr/local/bin/mise \
  "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-${mise_arch}"
echo "${mise_sha}  /rootfs/usr/local/bin/mise" | sha256sum -c -
chmod 0755 /rootfs/usr/local/bin/mise
# The tools live in /opt/mise at their final path: Python's prefix and pip's
# shebangs are absolute. Config, cache and state stay in this stage.
export MISE_DATA_DIR=/opt/mise MISE_CACHE_DIR=/tmp/mise/cache MISE_CONFIG_DIR=/tmp/mise/config \
  MISE_STATE_DIR=/tmp/mise/state MISE_YES=1 MISE_PYTHON_COMPILE=1 MISE_NODE_NPM_SHIM=0 \
  MISE_HIDE_UPDATE_WARNING=1 MISE_NODE_COREPACK=0
mise() { /rootfs/usr/local/bin/mise "$@"; }
# shellcheck disable=SC2086
mise use --global $MISE_TOOLS
# One stable directory on the default PATH, independent of the resolved
# versions: link every installed executable into /usr/local/bin.
for dir in $(mise bin-paths); do
  for f in "$dir"/*; do
    if [ ! -f "$f" ] || [ ! -x "$f" ]; then
      continue
    fi
    name="${f##*/}"
    [ "$name" = mise ] && continue
    ln -sfn "$(readlink -f "$f")" "/rootfs/usr/local/bin/$name"
  done
done
# The Debian packages providing the shared libraries the tools link against,
# so the runtime stage installs exactly those.
find /opt/mise/installs -type f \( -perm -u+x -o -name '*.so' -o -name '*.so.*' \) -exec ldd {} + 2>/dev/null \
  | awk '/=> \// { print $3 }' | sort -u \
  | while read -r lib; do echo "$lib"; realpath "$lib"; done \
  | xargs -r dpkg -S 2>/dev/null | grep -v '^diversion by' | cut -d: -f1 | tr ',' '\n' \
  | sed 's/^ *//' | sort -u > /rootfs/usr/local/share/bento-toolchain-deps
cat /rootfs/usr/local/share/bento-toolchain-deps
rm -rf /opt/mise/downloads /opt/mise/shims /opt/mise/cache /tmp/mise
