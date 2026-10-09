---
title: App lifecycle
description: Create, start, publish, update, restart, stop, and remove.
sidebar:
  order: 1
---

| Command | What happens |
| --- | --- |
| `bento app create --json app.json` | Allocates identity, provisions home, bindings, and config. Stays stopped and unpublished. |
| `bento app start <slug>` | Builds or reuses the runtime image, starts the container, waits for readiness. Never publishes. |
| `bento app publish <slug>` | Managed ingress only. Requires a running, ready app on the current configuration; activates the edge route last. |
| `bento app unpublish <slug>` | Removes only the edge route. The app, its jobs, and workers keep running. |
| `bento app update <slug> --json patch.json` | Applies config. Boot changes replace a running container; Nginx/FPM/scheduler changes reload in place after validation. Stopped apps stay stopped. |
| `bento app restart <slug>` | Restarts the container. This also restarts the app's scheduler and workers. |
| `bento app stop <slug>` | Records stopped + unpublished, removes the edge route first, then stops everything in the container. Stays stopped across backend and host restarts. |
| `bento app remove <slug>` | Asks for `delete <slug>`. Deletes containers and generated config, retires the UID, keeps data. |

Add `--no-wait` to return right after acceptance, and `--json` for machine output. `bento ops` and `bento op <id>`
show progress, errors, and guidance.

## Readiness

An app is ready when its scheduler answers on its private socket, PHP-FPM answers its ping (PHP apps), and an HTTP
request to the app's readiness path (default `/`) returns a status below 500 both inside the container and over the
app network. A readiness failure fails the operation with a log excerpt; it does not loop or recreate.

## Update payload

`app update` accepts any subset of `runtime`, `resources`, `ingress`, `accessLog`, and an optional
`expectedGeneration` to refuse a stale edit. The runtime kind cannot change; create a new app instead.
