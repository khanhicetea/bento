---
title: Stack layout
description: Files and directories in a stack root.
---

| Path | Contents |
| --- | --- |
| `bento.db` | Intent, identities, UID ledger, operations, sessions (`0600`). |
| `homes/<slug>/` | App homes (owned by the app UID). Durable. Source code lives in `homes/<slug>/app/`. |
| `sqlite/<id>/` | SQLite binding directories. Durable. |
| `apps/<appId>/config/` | Generated per-app config and credentials, mounted read-only at `/etc/bento`. |
| `apps/<appId>/identity/` | Generated `passwd`/`group`. |
| `services/<name>/` | Data-service secrets (`0400`) and Redis configuration. |
| `edge/conf/{live,previous}` | Edge Nginx generations. `edge/custom/` holds your drop-ins; `edge/certs/` certificates; `edge/acme/` ACME state. |
| `cloudflared/token` | Tunnel token (`0440`). |
| `backups/<slug>/` | Backup artifacts. |
| `rclone/rclone.conf` | rclone remote configuration (yours; editable in the UI's rclone shell). Must not be encrypted. |
| `run/`, `locks/`, `cache/`, `staging/` | Runtime coordination; not durable. |

Database and Redis data live in Docker volumes named `bento-<stack>-<service>-data`.
