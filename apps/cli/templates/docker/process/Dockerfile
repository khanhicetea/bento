# syntax=docker/dockerfile:1.7
ARG BASE_IMAGE=node:24-bookworm-slim
FROM ${BASE_IMAGE}

USER root
RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    openssh-client \
    socat \
    sqlite3 \
    util-linux; \
  rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /usr/local/bin/bento-process-entrypoint
RUN chmod 0755 /usr/local/bin/bento-process-entrypoint \
  && getent group bento-web >/dev/null || groupadd -g 5000 bento-web

WORKDIR /home
ENTRYPOINT ["/usr/local/bin/bento-process-entrypoint"]
