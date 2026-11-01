#!/bin/sh
# Build the pinned, checksum-verified Valkey server and CLI into /rootfs. Runs
# on the runtime image's own base, so the binaries link against its libc.
# TLS and systemd support are left out: the server is loopback-only.
set -eux
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl gcc libc6-dev make
curl -fsSLo /tmp/valkey.tar.gz "https://github.com/valkey-io/valkey/archive/refs/tags/${VALKEY_VERSION}.tar.gz"
echo "${VALKEY_SHA256}  /tmp/valkey.tar.gz" | sha256sum -c -
mkdir /tmp/valkey
tar -C /tmp/valkey --strip-components=1 -xzf /tmp/valkey.tar.gz
make -C /tmp/valkey -j"$(nproc)" BUILD_TLS=no USE_SYSTEMD=no
install -d /rootfs/usr/local/bin
install -m 0755 -s /tmp/valkey/src/valkey-server /tmp/valkey/src/valkey-cli /rootfs/usr/local/bin/
# Familiar alias, as Valkey's own `make install` provides.
ln -s valkey-cli /rootfs/usr/local/bin/redis-cli
/rootfs/usr/local/bin/valkey-server --version
/rootfs/usr/local/bin/redis-cli --version
