#!/bin/bash
# Sync the repo source (with .git, without node_modules/dist) to the VPS at /root/src.
set -e
cd "$(dirname "$0")/../.."
rsync -az --delete --exclude node_modules --exclude /dist --exclude '.cache' ./ bento-test:/root/src/
