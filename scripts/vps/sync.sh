#!/bin/bash
# Sync the repo source (with .git, without node_modules/dist) to the VPS at /root/src.
# BENTO_VPS names the ssh host (default bento-test; bento-arm for the arm64 host).
set -e
cd "$(dirname "$0")/../.."
rsync -az --delete --exclude node_modules --exclude /dist --exclude '.cache' ./ "${BENTO_VPS:-bento-test}":/root/src/
