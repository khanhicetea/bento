---
title: Architecture
description: Backend, containers, intent versus observation, and operations.
sidebar:
  order: 1
---

## Intent and observation

`bento.db` holds **intent**: apps, identities, Ingress hosts, bindings, desired runtime (`running`/`stopped`), ingress mode,
publication, settings, and the operation journal. Docker holds **observations**: whether a container exists, runs, and
is healthy. The UI shows both side by side; health never rewrites intent.

## Operations

Every change that touches Docker or files is a durable operation. The API validates your request, saves intent and the
operation in one transaction, and returns `202 Accepted` with the operation ID. A single executor runs operations in
order and records phases and events. If the backend stops mid-operation, the operation is marked `interrupted` on the
next start and is **never replayed automatically**; the reconciler converges safe state from intent instead.
Cancellation is honored only at phase boundaries.

## Reconciliation

The backend watches Docker events and re-inspects everything every minute. It:

- starts or recreates a desired-running app whose container is missing or has an outdated configuration,
- stops a desired-stopped app that someone started,
- recreates the edge, tunnel, and data services if they disappear,
- never recreates an app just because it is unhealthy,
- refuses to recreate anything whose durable data (home, SQLite directories, database volumes) is missing,
- gives up after five failed attempts per target (with backoff) and shows the target as **blocked** until you act.
  Any later successful operation on that target (for example saving the edge settings again, or starting the app)
  clears the block. `GET /api/v1/reconcile` lists every failing or blocked target, including the edge, tunnel, database
  browser, and data services.

## App containers

One persistent container per app, from a shared image per runtime version. s6 is PID 1 and supervises the runtime
(local Nginx + PHP-FPM, or your HTTP process) and minicrond. All of them run as the app's UID. A lock on the app home
prevents a second instance from starting, so schedules cannot run twice.

Configuration is generated into `apps/<appId>/config` and mounted read-only at `/etc/bento`. Changes that affect how
the container boots (image, command, resources, bindings, credentials) replace the container — the old one stops
before the new one starts. Nginx, FPM pool, and scheduler-task changes are validated and reloaded in place.
