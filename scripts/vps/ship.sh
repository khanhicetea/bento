#!/bin/bash
# Build the release binary (with embedded UI) here and install it on the VPS.
# BENTO_VPS names the ssh host (default bento-test); BENTO_ARCH the architecture (default amd64).
set -e
cd "$(dirname "$0")/../../apps/backend"
if [ "${1:-}" = web ]; then (cd ../web && bun run build >/dev/null); find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete; cp -R ../web/dist/. internal/webui/dist/; fi
V=$(git describe --always --dirty)
CGO_ENABLED=0 GOOS=linux GOARCH=${BENTO_ARCH:-amd64} go build -trimpath -ldflags "-s -w -X main.version=0.2.0-dev+$V" -o ../../dist/bento-linux-${BENTO_ARCH:-amd64} ./cmd/bento
rsync -z ../../dist/bento-linux-${BENTO_ARCH:-amd64} "${BENTO_VPS:-bento-test}":/usr/local/bin/bento.new
ssh "${BENTO_VPS:-bento-test}" 'mv /usr/local/bin/bento.new /usr/local/bin/bento && bento version'
