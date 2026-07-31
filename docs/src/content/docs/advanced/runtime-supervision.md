---
title: Runtime supervision
description: See how Bento runs PHP requests, commands, schedules, deploys, and workers.
---

# Runtime supervision

Each PHP version uses one image for web requests, background work, and operator commands. This keeps the runtime and tools consistent across all three roles.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/php-runtime-roles.svg
Alt: One PHP image split into persistent FPM, singleton runner, and temporary CLI roles for several apps.
Show: Put one PHP image at the top and branch to FPM, runner, and CLI. Inside FPM show per-app pools and sockets. Inside runner show per-app schedulers, deploy queues, and workers. Mark CLI containers as temporary and the runner as exactly one replica.
-->

| Role | Lifetime | Responsibility |
| --- | --- | --- |
| `<php-service>` | Persistent | FPM with one app pool/socket per assigned app |
| `<php-service>-runner` | Persistent singleton | s6 PID 1 supervising schedulers/workers |
| `<php-service>-cli` | Ephemeral Compose profile | App shell/argv under app UID/GID |

## Runner model

The runner uses s6-overlay to watch a dynamic service tree. Bento adds one Supercronic service for each app that uses schedules or deploy draining. It also adds one service for each enabled worker.

When the configuration changes, Bento updates only the affected service directories. Other services and the runner container can keep running.

Crontab-only changes validate then signal the matching `scheduler-<app>` with USR2. Worker `start|stop|restart|signal|inspect` addresses one `worker-<app>-<name>` service. Runtime locks are volatile and app-scoped.

The runner must stay at one replica. Scaling it duplicates schedulers, drains, and workers and violates Bento's singleton assumption.

## Deploy supervision

Each app checks its deploy queue once per minute and runs at most one queued job. The hook runs as the app user, inside a fixed working directory and timeout, and writes to app logs.

After the job finishes or fails, the runner asks the app's FPM socket to reset OPcache. The HTTP endpoint only authenticates and queues the job; it never performs the deployment itself.

## Log supervision

Docker uses the `local` log driver and keeps three files of up to 10 MiB each. Each runner also invokes `logrotate` every hour for app, worker, and FPM logs. It uses `copytruncate` and keeps two rotations.

This runner task is separate from the host-level `bento maintenance run` command.

## Migration/recovery

A change to runner image/entrypoint assets can require a planned image rebuild and runner recreation:

```sh
bento render
bento compose -- build php85
bento compose -- up -d --force-recreate php85-runner
bento apply
```

Inspect failures with `worker inspect`, runner logs, and scoped s6 status. Recreate only when generated config/image changes require it; ordinary cron/worker mutations reconcile live.

## Next steps

- [Schedules and workers](/guides/apps/schedules-workers/)
- [Webhook deploys](/guides/apps/deploy/)
- [PHP runtimes](/guides/apps/php-runtimes/)
