---
title: Overview
description: What Bento is, who it is for, and what it deliberately does not do.
sidebar:
  order: 1
---

Bento runs several applications on **one Linux server** you own. You describe each app — its runtime, domains, and
databases — and Bento creates and supervises one Docker container for it.

- **Backend:** `bento serve` is a small resident Go process. It stores intent in `bento.db` (SQLite), talks to the
  Docker Engine API, serves a REST API and the web UI on loopback, and reconciles drift.
- **CLI:** `bento` commands talk to the running backend over a root-only Unix socket.
- **Data plane:** app containers, the optional edge proxy, the optional tunnel, and shared MySQL/PostgreSQL/Redis.
  They keep running when the backend is stopped.

## Good fit

- A developer or small team with one VPS or dedicated server.
- Laravel, Symfony, WordPress, or other PHP apps, plus trusted Node.js/Bun/Python HTTP services.
- You want real isolation between apps' files and credentials, and safe defaults around data.

## Not a fit

Multi-host clusters, autoscaling, zero-downtime rollouts, hostile tenants, Git-push deployment pipelines, or an
internet-facing management API. Bento does not build your code: you deploy code into `/home/<slug>/app` yourself (for
example with `git` in `bento app shell`).
