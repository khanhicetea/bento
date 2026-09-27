---
title: Jobs and workers
description: The per-app minicrond scheduler.
sidebar:
  order: 3
---

Every app container runs [minicrond](https://github.com/khanhicetea/minicrond) as the app's UID. It owns the app's
scheduled jobs, long-running workers, run history, and logs under `~/.local/share/minicron`. Bento does not store your
jobs.

- **Web UI:** Applications → app → Scheduler. The view is proxied through Bento with your session; the browser never
  receives a scheduler token. Each app has its own relay process running as that app's UID, so one app's scheduler
  cannot reach another's.
- **CLI:** `bento app minicrond <slug> -- list`, `-- import /home/<slug>/jobs.toml`, `-- run <name>`, `-- logs <name>`.
  Starting a second daemon is refused.
- Jobs keep running while the app is unpublished. Stopping or restarting the app stops or restarts its jobs and
  workers too.
- Bento adds read-only internal tasks named `bento-internal-*` (weekly SQLite `VACUUM` for each SQLite binding).

The scheduler UI shares Bento's browser origin: only run scheduler content you trust.
