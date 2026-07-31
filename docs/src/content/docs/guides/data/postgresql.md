---
title: Operate PostgreSQL
description: Add PostgreSQL majors, create isolated app databases, and administer them with protected credentials.
---

# Operate PostgreSQL

Use PostgreSQL as an explicit alternative to the default MySQL binding.

## Add a major and create an app

Only official major tags such as `17` are accepted:

```sh
bento postgres add 17
bento app create reports \
  --domain reports.example.com \
  --database-engine postgres --postgres 17 --db
```

This creates service `postgres17`, durable volume `postgres17-data`, and an unprivileged app role/database. PostgreSQL has no public port in the base topology.

## Add and inspect databases

```sh
bento postgres db reports reports_archive
bento postgres shell --app reports \
  --database reports
bento postgres size --app reports
bento postgres processlist --app reports
```

Root administration requires an explicit service:

```sh
bento postgres shell --root \
  --service postgres17
```

Credentials are staged in protected files and excluded from host argv. Activity output omits query text.

## Verify

```sh
bento app show reports
bento backup --app reports --gzip
```

## Troubleshooting

If provisioning fails, start PostgreSQL and confirm `POSTGRES_PASSWORD` matches the initialized volume; requested database failure leaves state unchanged. An app cannot move from MySQL to PostgreSQL through `app update`; perform and validate an external logical migration, then rebuild the app identity/state deliberately.

:::caution
Bento blocks PostgreSQL service/volume removal. Raw volume transfer requires a compatible major/image; use logical backup and restore for a major upgrade.
:::

## Advanced

App roles are `NOSUPERUSER`, `NOCREATEDB`, `NOCREATEROLE`, and `NOREPLICATION`. Bento creates databases, sets ownership, and revokes default `PUBLIC` database/schema access so apps cannot use one another's databases.

## Next steps

- [Back up and restore databases](/guides/data/backup-restore/)
- [Export a compatible stack](/guides/stacks/export-import/)
- [Isolation and security](/advanced/isolation-security/)
