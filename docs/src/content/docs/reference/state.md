---
title: Desired-state schema
description: Learn what state.json stores, how Bento validates it, and how to recover it.
---

# Desired-state schema

`state.json` is Bento's sensitive, versioned record of your intent. Change it through Bento commands. Do not use it as a hand-edited configuration file.

## What it records

The schema records stack defaults, managed PHP and database services, apps, proxies, domains, cron jobs, workers, deploy settings, TLS choices, Redis identities, and template history.

Each app has a `databases[]` list whose bindings can use MySQL, PostgreSQL, SQLite, or Litestream. Domain records point to an app or proxy instead of being copied into those records. Cron jobs and workers also point back to their app.

Bento treats JSON as untrusted input. It requires the exact current schema version, validates links and domain ownership, and then writes the state atomically. It rejects unknown or malformed fields instead of ignoring them.

:::caution
`state.json` contains app database passwords and deploy HMAC secrets. Keep mode `0600`, never commit it, and redact it before sharing.
:::

## Schema versions

This project starts directly at the current schema. Unsupported older or newer versions are rejected without modifying the file, and there is no schema migration command. Reinitialize a development stack or restore state produced by the matching binary rather than copying fields between schema versions.

## Recovery

Before a risky operation, copy the private state and `.env` through a secure channel. If state is corrupt, preserve the failing bytes for private analysis, restore a known-good state backup, render, and verify durable resources before applying. Restoring state alone does not restore homes or databases.

## Related pages

- [Desired state and generated configuration](/concepts/desired-state/)
- [Stack layout](/reference/stack-layout/)
- [Render and apply internals](/advanced/render-apply/)
