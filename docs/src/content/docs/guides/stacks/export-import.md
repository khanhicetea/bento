---
title: Export, import, and clones
description: Move or clone a whole stack.
sidebar:
  order: 1
---

```bash
bento export --to /backup/prod-2026-01-01      # asks for "export"
bento --stack /srv/bento/copy import --from /backup/prod-2026-01-01 --name copy --uid-first 20000 --uid-last 29999
```

Export briefly **stops running apps and data services** so SQLite files (including scheduler databases) and database
volumes are consistent, snapshots the state database with `VACUUM INTO`, archives the stack root and each volume, then
restarts exactly what was running. The export directory contains secrets and data: protect it.

Import requires an empty root and a compatible architecture. The imported stack gets a new stack ID and new networks.
It starts with **all apps stopped and unpublished**, the edge, tunnel, and backup schedules disabled, and no pending
operations. Use `--name` to clone on the same host, and `--uid-first/--uid-last` to move future UID allocations to a
range that does not overlap the source stack. If import fails, it removes only the root contents and volumes it
created.
