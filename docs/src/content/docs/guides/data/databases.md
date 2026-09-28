---
title: Databases and Redis
description: Data services, bindings, and credentials.
sidebar:
  order: 1
---

## Services

`bento services` lists MySQL, PostgreSQL, and Redis. Add a managed version with
`bento service add --engine postgres --version 17` (MySQL 8.0/8.4/9.4, PostgreSQL 14–18; 17 and 18 use the [pglayers](https://github.com/pglayers/pglayers) full profile with pgvector, PostGIS, pg_cron, timescaledb and more preinstalled). Each version has one container
and one named volume on the private data network. Services are never removed by Bento, and an established service with
a missing volume is **blocked** instead of started empty — restore the volume from an export or backup.

## Bindings

- `bento app bind <slug> --engine mysql --service mysql84` — creates a user `u<appId>` and a database `<slug>_main`.
  Existing bindings are untouched (bindings are add-only).
- `bento app add-db <slug> --binding <id> --name reports` — adds `<slug>_reports`.

Every database is named `<slug>_<name>`, with hyphens in the slug written as `_`. Slugs and `<name>` never contain `_`
(`<name>` is 1-30 lowercase letters or digits, and `main` is reserved), so two apps can never produce the same name.
- `bento app bind <slug> --engine sqlite` — a private directory mounted at `/var/lib/bento/sqlite/<id>/<slug>.db`,
  with a weekly randomized `VACUUM` task in the app's scheduler.

Bento refuses to grant access to a database that already exists without this binding's ownership (for example one
kept from a removed app). PostgreSQL databases are owned by the app role with public access revoked.

A running app is recreated after a binding change so its environment includes the new credentials.

To inspect or edit a binding's data from the UI, use the [database browser](/guides/data/database-browser/).

## Redis

Every app has its own ACL user restricted to keys and channels under `<slug>:` and without admin or dangerous
commands. Use `REDIS_HOST`, `REDIS_PORT`, `REDIS_USERNAME`, `REDIS_PASSWORD`, and `REDIS_PREFIX`.

`INFO` is allowed. `FLUSHDB`, `FLUSHALL`, `KEYS`, `SORT`, and the rest of `@dangerous` are refused with `NOPERM`,
because they ignore key prefixes and would touch other apps' data. Clear a cache by prefix or tag instead (for Laravel,
avoid `cache:clear` against Redis; use tagged caches or delete by prefix with `SCAN`).

Redis is shared by all apps and capped at a quarter of host memory (`maxmemory`) with `noeviction`: when it is full,
writes fail with an OOM error instead of evicting another app's keys.
