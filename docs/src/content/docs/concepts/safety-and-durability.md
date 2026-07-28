---
title: Safety and durability
description: Distinguish Bento state, generated files, customization, durable data, and security boundaries.
---

# Safety and durability

Bento separates operator intent from disposable output and durable data. Knowing which layer owns a path prevents accidental data loss and secret exposure.

## Mental model

| Layer | Examples | Treatment |
| --- | --- | --- |
| Desired state | `.env`, `state.json` | Sensitive source of truth; change through the CLI |
| Generated | `generated/`, materialized `docker/` and `helpers/` | Rebuildable; never edit |
| Custom | `custom/`, `overlays/` | Operator-owned; review after upgrades |
| Durable | `homes/`, `certs/`, `backups/`, `logs/`, database and Redis volumes | Back up and protect |
| Ephemeral | `runtime/`, `locks/`, `.asset-cache/` | Recreated or recoverable |

The stack root contains files, but database and Redis contents live in Compose named volumes. Copying the stack root alone is therefore not a complete recovery copy.

## Safety controls

- `bento compose -- down -v` is refused because `-v` destroys durable volumes.
- Removing app desired state retains its home and databases. Permanent `app prune` is a separate interactive operation that lists retained parts and requires the literal `delete`.
- Managed MySQL/PostgreSQL service removal and automatic password rotation are unsupported.
- Logical restore is not object-level atomic and can leave a partial destination.
- Scheduled dumps are on-host only until you replicate and verify them elsewhere.

Treat `.env`, `state.json`, app credential files, deploy secrets, certificate private keys, and export archives as secrets. Support bundles redact known credentials, but inspect any archive before sharing it.

## Trust boundary

Only Nginx is public in the base topology. App identity, FPM pools, filesystem modes, database grants, and optional Redis ACLs reduce accidental cross-app access. Apps sharing a PHP version still share containers, network reachability, and capacity. Bento is not a hostile multi-tenant sandbox; use separate hosts or stronger isolation for mutually untrusted tenants.

## Recovery priorities

Keep encrypted off-host copies of desired state, homes, certificates, and logical database dumps. Test restoration to a separate database or host. A [full stack export](/guides/stacks/export-import/) includes raw volumes but requires compatible architecture and database images.

## Next steps

- [Identify every stack path](/reference/stack-layout/)
- [Back up and restore databases](/guides/data/backup-restore/)
- [Understand isolation and security](/advanced/isolation-security/)
