---
title: Desired-state schema
description: Understand state ownership, linked resources, version checks, and why direct edits are risky.
---

# Desired-state schema

`state.json` is Bento's sensitive, versioned record of operator intent. Use CLI commands to mutate it; do not treat it as a hand-edited configuration file.

## What it records

The current schema records stack defaults, managed PHP and database services, apps, proxies, linked domains, cron jobs, workers, deploy settings, TLS choices, Redis identities, and template provenance. Each app has a `databases[]` collection whose independent bindings may use MySQL, PostgreSQL, SQLite, or Litestream. Domains are authoritative link records pointing to an app or proxy; they are not duplicated inside app records. Cron jobs and workers likewise link back to their app.

Bento parses JSON as untrusted input, requires the exact current schema version, validates cross-record references and domain ownership, then writes validated state atomically. Unknown or malformed fields are rejected rather than silently ignored.

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
