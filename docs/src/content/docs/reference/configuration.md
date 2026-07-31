---
title: Stack configuration
description: Look up supported stack environment variables, defaults, networking effects, and secret handling.
---

# Stack configuration

Bento initializes the stack-root `.env` with mode `0600`. Edit only confirmed variables, then run `apply` or the relevant `stack ingress set` command.

| Variable | Initial/default behavior | Scope |
| --- | --- | --- |
| `COMPOSE_PROJECT_NAME` | `bento` if `init --name` is omitted | Stable stack/Compose identity |
| `MYSQL_ROOT_PASSWORD` | Random on initialization | MySQL administrator secret |
| `POSTGRES_PASSWORD` | Random on initialization | PostgreSQL administrator secret |
| `REDIS_PASSWORD` | Random on initialization | Shared Redis secret |
| `ACME_EMAIL` | Empty | Shared ACME contact; required for ACME operation |
| `ACME_URL` | Let's Encrypt production directory | ACME endpoint; HTTP(S) URL |
| `HTTP3` | `false` | Enable generated HTTP/3 listeners/headers |
| `NGINX_HOST_NETWORK` | `1` | `1` host mode; `0` bridge mode |
| `NGINX_HTTP_PORT` | Empty | Optional bridge host publication; ignored in host mode |
| `NGINX_HTTPS_PORT` | Empty | Optional bridge HTTPS TCP and, with HTTP/3, UDP publication |
| `BENTO_LITESTREAM_ENABLED` | `false` | Permit the optional shared Litestream service |
| `S3_BUCKET_NAME` | Empty | Required bucket for SQLite continuous backup |
| `S3_REGION` | Empty | Required S3 region for SQLite continuous backup |
| `S3_ENDPOINT` | Empty | Optional custom S3-compatible endpoint |
| `S3_ACCESS_KEY_ID` | Empty | S3 credential; sensitive |
| `S3_SECRET_ACCESS_KEY` | Empty | S3 credential; sensitive |

Use the CLI for ingress values:

```sh
bento stack ingress set bridge \
  --http-port 8080 --https-port 8443
bento stack ingress show
```

Port `0` clears a publication. HTTP and HTTPS bridge ports must differ. Boolean parsing accepts the values validated by the current implementation; prefer generated `1`/`0` for ingress and `true`/`false` for HTTP/3.

:::danger
Do not rotate database passwords by editing `.env`. Existing volumes keep their initialized administrator credentials, and Bento does not automatically coordinate app password rotation. A mismatch can make administration unavailable.
:::

Treat `.env` as secret. Do not commit, paste, or attach it. `COMPOSE_PROJECT_NAME` must remain stable and unique; changing it retargets Compose resources and can make existing named volumes appear missing.

Stack defaults for PHP, database service, FPM profile, and Redis mode live in validated `state.json`, not `.env`, and should normally change through supported CLI operations. Per-app credentials are generated into protected app-owned files.

## Verify changes

```sh
bento doctor
bento compose -- config
```

See [networking](/concepts/networking/) and [TLS modes](/guides/apps/domains-tls/) before changing public ingress. See [SQLite continuous backup](/guides/data/sqlite/) before configuring the Litestream and S3 values.
