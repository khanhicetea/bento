---
title: Desired-state database
description: Learn what state.db stores, how Bento validates and migrates it, and how to recover it.
---

# Desired-state database

`state.db` is Bento's sensitive, versioned record of your intent. It is a private Bun SQLite database, not a hand-edited configuration file. Change it through Bento commands.

## What it records

Normalized relational tables record stack defaults, managed PHP and database services, apps, app database bindings, logical databases, proxies, authoritative domains, cron jobs, workers, deploy settings, TLS choices, Redis identities, template history, and ordered command arguments.

Each app can have MySQL, PostgreSQL, SQLite, or Litestream bindings. Domain rows point to an app or proxy. Cron jobs and workers point back to their app. Checks, unique indexes, and foreign keys enforce local relationships.

Bento still treats database contents as untrusted. On every load it reconstructs the complete desired-state model and applies the strict domain validator. This catches cross-record rules such as managed-service compatibility, exactly one primary domain per owner, unique binding identities, and valid app links. A save validates first and replaces desired state in one SQLite transaction.

:::caution
`state.db` contains app database passwords and deploy HMAC secrets. Keep it mode `0600`, never commit it, and never share a raw copy without protecting it as a secret.
:::

## Schema versions and migrations

Bento tracks two separate versions:

- the **database schema version** is recorded in `schema_migrations` and controls tables, indexes, and constraints;
- the **domain schema version** is stored with desired state and controls the validated in-memory model.

Apply pending database migrations after installing a new Bento binary:

```sh
bento migrate
```

Migrations are numbered and transactional. Their DDL and applied marker commit together. Bento refuses unknown future migration versions. `bento init` establishes the current schema before writing initial state, while `bento serve` and `bento tui` automatically run the migration gate before entering the server or wizard.

Routine state reads do not migrate the database. Bento does not import or fall back to `state.json`.

## Recovery

Protect `state.db` and `.env` together. Prefer a Bento stack export or another quiesced, SQLite-consistent backup rather than copying the database while a mutation may be active. If the database is corrupt, preserve the failing bytes for private analysis, restore a known-good `state.db`, run `bento migrate`, render, and verify durable resources before applying.

Restoring desired state alone does not restore homes, SQLite application files, MySQL/PostgreSQL data, Redis data, certificates, or other durable assets.

## Related pages

- [Desired state and generated configuration](/concepts/desired-state/)
- [Stack layout](/reference/stack-layout/)
- [Render and apply internals](/advanced/render-apply/)
