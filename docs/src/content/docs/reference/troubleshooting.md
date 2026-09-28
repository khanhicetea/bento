---
title: Troubleshooting
description: Common failures and what to do.
---

- **`not-ready`** — the app did not pass readiness. Check `bento app logs`, the readiness path, and that the process
  listens on `0.0.0.0:$PORT`. The container keeps running; publication did not change.
- **`instance-exited`** — the operation includes the last log lines. Fix the app and start again.
- **`durable-state-missing`** / **`volume-missing`** — Bento refuses to recreate without data. Restore the home, SQLite
  directory, or volume, then run `bento app start`.
- **`blocked`** — five reconciliation attempts failed, or unknown/duplicate containers use Bento's names or labels.
  Inspect `bento op <id>`, resolve the cause, then start or restart explicitly to reset the budget. For the edge,
  tunnel, database browser, or a data service, re-apply its settings; any successful operation on the target clears
  the block. `GET /api/v1/reconcile` shows which targets are blocked.
- **`home-retained`** — a home from a removed app exists. Prune it (`bento retired prune`) or restore explicitly.
- **`edge-validation-failed`** — a route or custom drop-in is invalid; the live edge is unchanged.
- **Backend won't start** — another backend or offline command holds `locks/controller.lock`, or `bento.db` is from an
  unsupported version. Bento never rewrites or re-initializes unknown state.
