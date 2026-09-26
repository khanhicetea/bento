# syntax=docker/dockerfile:1.7

ARG BUN_IMAGE=oven/bun:1.4.0-debian@sha256:5bb0f9be3a1a36a03e27c9a9dd894a3b1ad26657155c7df4dda771e17bf872ef
ARG DOCKER_CLI_IMAGE=docker:27.5.1-cli@sha256:851f91d241214e7c6db86513b270d58776379aacc5eb9c4a87e5b47115e3065c
ARG RUNTIME_IMAGE=debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241

FROM ${BUN_IMAGE} AS build
WORKDIR /src

COPY package.json bun.lock ./
COPY apps/cli/package.json apps/cli/package.json
COPY apps/web/package.json apps/web/package.json
COPY packages/shared/package.json packages/shared/package.json
COPY docs/package.json docs/package.json
RUN bun install --frozen-lockfile

COPY . .
RUN bun run compile

FROM ${DOCKER_CLI_IMAGE} AS docker-cli

FROM ${RUNTIME_IMAGE} AS runtime

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
      ca-certificates \
      openssh-client \
      openssl \
      tar \
    && rm -rf /var/lib/apt/lists/*

COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker-cli /usr/local/libexec/docker/cli-plugins/docker-buildx /usr/local/libexec/docker/cli-plugins/docker-buildx
COPY --from=docker-cli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
COPY --from=build /src/dist/bento /usr/local/bin/bento

# A published or platform-routed control plane has host-level Docker access.
# Keep the container web server closed until its operator supplies credentials.
ENV BENTO_STACK_ROOT=/var/lib/bento \
    BENTO_REQUIRE_WEB_AUTH=1
WORKDIR /var/lib/bento
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/bento"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8080"]
