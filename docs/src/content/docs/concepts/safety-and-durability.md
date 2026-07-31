---
title: Safety and durability
description: Identify what to protect, what Bento can rebuild, and which dangerous actions it blocks.
---

# Safety and durability

Bento separates your intent, generated output, custom files, and durable data. Learn which layer owns each path before you edit, delete, or back it up.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/data-ownership-layers.svg
Alt: Bento stack data grouped into desired state, generated, custom, durable, and temporary layers.
Show: Five horizontal layers with example paths. Add badges for sensitive, rebuildable, operator-owned, back up, and temporary. Draw Docker named volumes beside rather than inside the stack-root boundary.
-->

## Mental model

| Layer | Examples | Treatment |
| --- | --- | --- |
| Desired state | `.env`, `state.json` | Sensitive source of truth; change through the CLI |
| Generated | `generated/`, materialized `docker/` and `helpers/` | Rebuildable; never edit |
| Custom | `custom/`, `overlays/` | Operator-owned; review after upgrades |
| Durable | `homes/`, `sqlite/`, `certs/`, `backups/`, `logs/`, database and Redis volumes | Back up and protect |
| Ephemeral | `runtime/`, `locks/`, `.asset-cache/` | Recreated or recoverable |

The stack root contains SQLite files under `sqlite/`. MySQL, PostgreSQL, and Redis store their data in Compose named volumes outside the root.

For that reason, copying the stack root does not create a complete recovery copy. Copying a live SQLite file also does not guarantee a consistent backup.

## Safety controls

- `bento compose -- down -v` is refused because `-v` destroys durable volumes.
- Removing app desired state retains its home and databases. Permanent `app prune` is a separate interactive operation that lists retained parts and requires the literal `delete`.
- Managed MySQL/PostgreSQL service removal and automatic password rotation are unsupported.
- Logical restore is not object-level atomic and can leave a partial destination.
- MySQL/PostgreSQL dumps are created on-host. Scheduled runs may upload new artifacts through configured rclone, but you must verify remote retention and recovery separately.
- SQLite uses optional S3 continuous backup; its verification command restores a temporary copy but does not replace production data.

Treat `.env`, `state.json`, app credentials, deploy secrets, certificate private keys, and export archives as secrets. Bento redacts known credentials from support bundles, but you must still inspect every archive before sharing it.

## Trust boundary

Only Nginx is public in the base setup. App identities, FPM pools, file permissions, database grants, and optional Redis ACLs reduce accidental access between apps.

Apps on the same PHP version still share containers, private network access, and capacity. Bento is not a hostile multi-tenant sandbox. Use separate hosts or stronger isolation for mutually untrusted tenants.

## Recovery priorities

Keep encrypted off-host copies of desired state, homes, certificates, and logical database dumps. Configure and test [SQLite continuous backup](/guides/data/sqlite/) separately. A [stack export](/guides/stacks/export-import/) includes raw database volumes and mechanically archives the stack-root SQLite tree, but live SQLite consistency is not guaranteed; raw import also requires compatible architecture and database images.

## Next steps

- [Identify every stack path](/reference/stack-layout/)
- [Back up and restore databases](/guides/data/backup-restore/)
- [Understand isolation and security](/advanced/isolation-security/)
