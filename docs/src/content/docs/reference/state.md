---
title: Desired-state schema
description: Understand state ownership, schema migration, backup behavior, and why direct edits are risky.
---

# Desired-state schema

`state.json` is Bento's sensitive, versioned record of operator intent. Use CLI commands to mutate it; do not treat it as a hand-edited configuration file.

## What it records

The current schema records stack defaults, managed PHP and database services, apps, proxies, global domain ownership, cron jobs, workers, deploy settings, TLS choices, Redis identities, and template provenance. App database bindings are discriminated by `mysql` or `postgres`, so one app cannot validly mix engines/services.

Bento parses JSON as untrusted input, requires the exact current schema version, validates cross-record references and domain ownership, then writes validated state atomically. Unknown or malformed fields are rejected rather than silently ignored.

:::caution
`state.json` contains app database passwords and deploy HMAC secrets. Keep mode `0600`, never commit it, and redact it before sharing.
:::

## Schema migration

Routine reads never rewrite an older schema. A schema-v1 stack must be migrated deliberately:

```sh
bento --stack /var/lib/bento state migrate \
  --confirm migrate-v1-to-v2
```

Bento validates v1, builds and validates v2, writes a private backup beside the state file, and atomically replaces the source. Existing MySQL identifiers and secrets are preserved. Keep the backup until the migrated stack passes `render`, `doctor`, and application checks.

An old binary rejects newer state instead of modifying it. Upgrade the binary; do not downgrade schema fields manually.

## Recovery

Before a risky operation, copy the private state and `.env` through a secure channel. If state is corrupt, preserve the failing bytes for private analysis, restore a known-good state backup, render, and verify durable resources before applying. Restoring state alone does not restore homes or databases.

## Related pages

- [Desired state and generated configuration](/concepts/desired-state/)
- [Stack layout](/reference/stack-layout/)
- [Render and apply internals](/advanced/render-apply/)
