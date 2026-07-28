---
title: Runtime supervision
description: Understand PHP FPM, runner, CLI roles and scoped s6 reconciliation for schedules, deploys, and workers.
---

# Runtime supervision

Each managed PHP version uses one image for three roles so web, background, and operator commands share the same runtime/toolchain.

| Role | Lifetime | Responsibility |
| --- | --- | --- |
| `<php-service>` | Persistent | FPM with one app pool/socket per assigned app |
| `<php-service>-runner` | Persistent singleton | s6 PID 1 supervising schedulers/workers |
| `<php-service>-cli` | Ephemeral Compose profile | App shell/argv under app UID/GID |

## Runner model

s6-overlay owns a dynamic scan tree. Bento generates one Supercronic service per app that has schedules or deploy draining, plus one flat service per enabled worker. Reconciliation adds/removes only affected service directories; sibling services and the container need not restart.

Crontab-only changes validate then signal the matching `scheduler-<app>` with USR2. Worker `start|stop|restart|signal|inspect` addresses one `worker-<app>-<name>` service. Runtime locks are volatile and app-scoped.

The runner must stay at one replica. Scaling it duplicates schedulers, drains, and workers and violates Bento's singleton assumption.

## Deploy supervision

A per-app minute schedule drains at most one queued job. The hook runs as app identity with a bounded timeout/workdir and writes app logs. After a terminal attempt, the runner asks the app's FPM socket to reset OPcache. The HTTP endpoint only authenticates/enqueues.

## Log supervision

Docker uses the `local` driver with 10 MiB/three-file bounds. A root maintenance scheduler in each runner invokes logrotate hourly for app/worker/FPM files, using `copytruncate` and retaining two rotations. This is distinct from host `maintenance run`.

## Migration/recovery

A change to runner image/entrypoint assets can require a planned image rebuild and runner recreation:

```sh
bento --stack /var/lib/bento render
bento --stack /var/lib/bento compose -- build php85
bento --stack /var/lib/bento compose -- up -d --force-recreate php85-runner
bento --stack /var/lib/bento apply
```

Inspect failures with `worker inspect`, runner logs, and scoped s6 status. Recreate only when generated config/image changes require it; ordinary cron/worker mutations reconcile live.

## Next steps

- [Schedules and workers](/guides/apps/schedules-workers/)
- [Webhook deploys](/guides/apps/deploy/)
- [PHP runtimes](/guides/apps/php-runtimes/)
