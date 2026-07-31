---
title: Operate MySQL
description: Add a private MySQL service, create app databases, and inspect them safely.
---

# Operate MySQL

Add private, versioned MySQL services and create databases that belong to Bento apps.

## Add and list services

```sh
bento mysql list
bento mysql add 8.0
```

MySQL 8.4 is the new-stack default. Added services are private and have durable named volumes; Bento intentionally does not offer managed service removal.

## Create an app database

The app must have a MySQL binding, and names must remain in its namespace:

```sh
bento mysql db demo demo_archive
bento app show demo
```

The service must be reachable. Failure occurs before Bento records a requested database.

## Open a shell and inspect

```sh
bento mysql shell --app demo --database demo
bento mysql size --app demo
bento mysql processlist --app demo
```

For administration, select root and a service explicitly:

```sh
bento mysql shell --root --service mysql84
```

Bento uses protected option files so passwords do not appear in host process arguments. `--print` shows a redacted shell plan.

## Verify

Run an application-level connection check through its identity, then create a logical dump:

```sh
bento backup --app demo --database demo --gzip
```

## Troubleshooting

If the service is unavailable, check `compose -- ps`, MySQL logs, and that `MYSQL_ROOT_PASSWORD` still matches the initialized volume. Changing `.env` does not reset an existing server password. Automatic app/root password rotation is unsupported; coordinate manual changes across MySQL, Bento state/credentials, and the application.

## Advanced

Each app may link multiple database kinds. Its MySQL binding has a same-name user limited to recorded namespaced databases and coexists with any PostgreSQL, SQLite, or Litestream bindings. Never use `compose down -v`.

## Next steps

- [Back up and restore databases](/guides/data/backup-restore/)
- [Use PostgreSQL instead](/guides/data/postgresql/)
- [Database limitations](/reference/limitations/)
