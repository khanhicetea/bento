---
title: Identity and durable data
description: App IDs, UIDs, homes, and what survives removal.
sidebar:
  order: 2
---

- **Slug:** your name for the app, permanent; also names `/home/<slug>`.
- **App ID:** random, immutable, one per incarnation. Removing and recreating a slug creates a new incarnation.
- **UID/GID:** allocated from the stack's range, recorded in a ledger, and **never reused** within the stack, even
  after removal or prune.

The home carries a small `.bento-identity.json` (stack, app ID, UID). Bento refuses to use a home that belongs to a
different incarnation and never re-owns such a directory automatically.

## Removal and prune

`bento app remove shop` asks you to type `delete shop`. It removes the route and containers and retires the UID. The
home, SQLite files, and relational databases stay. `bento retired` lists what was kept; `bento retired prune <appId>`
shows the exact list and asks you to type `delete` before deleting it. A new app with the same slug cannot be created
while the old home is retained.
