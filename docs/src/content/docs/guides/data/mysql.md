---
title: Operate MySQL
description: Add MySQL services, create app databases, open protected shells, and inspect usage safely.
---

# Operate MySQL

Manage private versioned MySQL services and databases assigned to Bento apps.

## Add and list services

```sh
bento --stack /var/lib/bento mysql list
bento --stack /var/lib/bento mysql add 8.0
```

MySQL 8.4 is the new-stack default. Added services are private and have durable named volumes; Bento intentionally does not offer managed service removal.

## Create an app database

The app must already be MySQL-backed, and names must remain in its namespace:

```sh
bento --stack /var/lib/bento mysql db demo demo_archive
bento --stack /var/lib/bento app show demo
```

The service must be reachable. Failure occurs before Bento records a requested database.

## Open a shell and inspect

```sh
bento --stack /var/lib/bento mysql shell --app demo --database demo
bento --stack /var/lib/bento mysql size --app demo
bento --stack /var/lib/bento mysql processlist --app demo
```

For administration, select root and a service explicitly:

```sh
bento --stack /var/lib/bento mysql shell --root --service mysql84
```

Bento uses protected option files so passwords do not appear in host process arguments. `--print` shows a redacted shell plan.

## Verify

Run an application-level connection check through its identity, then create a logical dump:

```sh
bento --stack /var/lib/bento backup --app demo --database demo --gzip
```

## Troubleshooting

If the service is unavailable, check `compose -- ps`, MySQL logs, and that `MYSQL_ROOT_PASSWORD` still matches the initialized volume. Changing `.env` does not reset an existing server password. Automatic app/root password rotation is unsupported; coordinate manual changes across MySQL, Bento state/credentials, and the application.

## Advanced

Each app has one engine/service binding and a same-name user limited to recorded namespaced databases. Moving engines/services requires an external migration. Never use `compose down -v`.

## Next steps

- [Back up and restore databases](/guides/data/backup-restore/)
- [Use PostgreSQL instead](/guides/data/postgresql/)
- [Database limitations](/reference/limitations/)
