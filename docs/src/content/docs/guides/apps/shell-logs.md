---
title: Shell, exec, logs, permissions
description: Run commands as the app, read logs, and repair ownership.
sidebar:
  order: 4
---

- `bento app shell <slug>` — interactive shell in a **tooling container**: same image, UID, home, bindings, and
  credentials, but no daemons and no instance lock. Use it for deploys. Add `--running` to exec into the live
  container instead.
- `bento app exec <slug> -- php artisan migrate` — bounded, non-interactive command (tooling container by default,
  `--running` for the live one, `--workdir` relative to the home).
- `bento app logs <slug> [--tail 200] [--follow]` — container output: runtime, Nginx, FPM, scheduler. Known app
  secrets are redacted. Docker keeps at most 30 MB per app.
- `bento app permissions <slug> --mode check|dry-run|shallow|recursive` — never follows symbolic links.

The web UI offers the same shell and logs in the app's detail view.
