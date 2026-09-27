---
title: Databases and Redis
description: Data services, bindings, and credentials.
sidebar:
  order: 1
---

## Services

`bento services` lists MySQL, PostgreSQL, and Redis. Add a managed version with
`bento service add --engine postgres --version 17` (MySQL 8.0/8.4, PostgreSQL 16/17). Each version has one container
and one named volume on the private data network. Services are never removed by Bento, and an established service with
a missing volume is **blocked** instead of started empty — restore the volume from an export or backup.

## Bindings

- `bento app bind <slug> --engine mysql --service mysql84` — creates a user `u<appId>` and a database named after the
  slug. Existing bindings are untouched (bindings are add-only).
- `bento app add-db <slug> --binding <id> --name reports` — adds `<slug>_reports`.
- `bento app bind <slug> --engine sqlite` — a private directory mounted at `/var/lib/bento/sqlite/<id>/<slug>.db`,
  with a weekly randomized `VACUUM` task in the app's scheduler.

Bento refuses to grant access to a database that already exists without this binding's ownership (for example one
kept from a removed app). PostgreSQL databases are owned by the app role with public access revoked.

A running app is recreated after a binding change so its environment includes the new credentials.

## Redis

Every app has its own ACL user restricted to keys and channels under `<slug>:` and without admin or dangerous
commands. Use `REDIS_HOST`, `REDIS_PORT`, `REDIS_USERNAME`, `REDIS_PASSWORD`, and `REDIS_PREFIX`.
