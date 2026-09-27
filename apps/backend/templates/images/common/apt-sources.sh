#!/bin/sh
# Point end-of-life Debian releases at archive.debian.org. The security suite
# is dropped: its pool is withdrawn before the archive carries it, and the
# base images' installed versions resolve against main + updates.
set -eu
. /etc/os-release
case "${VERSION_CODENAME:-}" in
  bullseye)
    rm -f /etc/apt/sources.list.d/*
    printf '%s\n' \
      "deb http://archive.debian.org/debian ${VERSION_CODENAME} main" \
      "deb http://archive.debian.org/debian ${VERSION_CODENAME}-updates main" \
      > /etc/apt/sources.list
    ;;
esac
