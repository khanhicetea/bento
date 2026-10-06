---
title: REST API
description: The /api/v1 contract used by the UI and CLI.
---

Types are defined in Go (`apps/backend/internal/api/dto`) and generated for TypeScript with tygo.

- JSON only; unknown fields, trailing data, wrong types, and bodies over 1 MiB are rejected. Errors are
  `{"error":{"code","message","fields"}}` with stable codes (`validation`, `not_found`, `conflict`,
  `precondition_failed`, `confirmation_required`, `unauthorized`, `forbidden`, …).
- Long-running mutations return **202** with `{operation, statusUrl}` and a `Location` header. Send an
  `Idempotency-Key` header to make retries safe.
- Browser writes need the session cookie, an exact allowed `Origin`, and `X-CSRF-Token`.
- Timestamps are RFC 3339 UTC strings with milliseconds.

| Area | Endpoints |
| --- | --- |
| Session | `GET/POST/DELETE /session`, `PUT /auth/password` |
| System | `GET /system`, `GET /catalog` |
| Apps | `GET/POST /apps`, `GET/PATCH/DELETE /apps/{id}`, `POST /apps/{id}/{start,stop,restart,publish,unpublish}`, `POST /apps/{id}/bindings`, `POST /apps/{id}/bindings/{bid}/databases`, `POST /apps/{id}/permissions`, `GET /apps/{id}/readiness`, `GET /apps/{id}/logs` (SSE), `POST /apps/{id}/exec`, `POST /apps/{id}/scheduler/command`, `GET /apps/{id}/terminal` (WebSocket) |
| Operations | `GET /operations`, `GET /operations/{id}`, `GET /operations/{id}/events` (SSE), `POST /operations/{id}/cancel` |
| Data | `GET/POST /services`, `GET /retired`, `POST /retired/{id}/prune` |
| Ingress | `GET/PUT /edge`, `GET /tunnel`, `PUT /tunnel/token`, `GET/POST /proxies`, `DELETE /proxies/{name}` |
| Backups | `GET /backups/artifacts`, `GET /backups/runs`, `POST /backups`, `POST /backups/restore`, `GET/POST /backups/schedules`, `GET/PUT/DELETE /backups/schedules/{id}`, `POST /backups/schedules/{id}/enabled`, `GET /backups/rclone`, `POST /backups/rclone/test`, `GET /backups/rclone/terminal` (WebSocket), `POST /stack/export` |
| App backups (restic) | `GET /backups/apps` (every app's summary and recent runs), `GET/PUT /apps/{id}/restic`, `POST /apps/{id}/restic/init` (returns the key once), `POST /apps/{id}/restic/connect`, `POST /apps/{id}/restic/backup`, `POST /apps/{id}/restic/refresh`, `POST /apps/{id}/restic/check`, `POST /apps/{id}/restic/unlock`, `POST /apps/{id}/restic/keys` (`export <slug>`; returns the key once), `DELETE /apps/{id}/restic/keys/{keyId}`, `POST /apps/{id}/restic/inspect`, `POST /apps/{id}/restic/clone` (`clone <new-slug>`), `POST /apps/restore-from-backup/inspect` and `POST /apps/restore-from-backup` (`clone <new-slug>`; another stack's repository: `repository` and `key` in the body, the key is held in a private file for the operation and deleted at its end) |

The scheduler UI is not part of the JSON API: it is proxied at `/apps/<slug>/scheduler/…` on the management listener and
needs a browser session. Writes need an exact allowed `Origin` (no CSRF header).
