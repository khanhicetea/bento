---
title: Install and initialize
description: Install the binary, create a stack root, and run the backend under systemd.
sidebar:
  order: 2
---

## Requirements

- Linux amd64 or arm64 (arm64 binaries are built but have not yet been exercised end to end).
- Docker Engine with API version 1.44 or newer. Nothing else is required on the host; the Docker CLI is optional.
- Root. The backend owns app home directories and starts per-app scheduler relays under each app's UID.

## Install

```bash
install -m 0755 bento-linux-amd64 /usr/local/bin/bento
bento version
```

## Initialize a stack

A stack is one independent installation with its own root directory. Pick a root outside any source checkout:

```bash
bento --stack /srv/bento/prod init --name prod --mysql 8.4
```

`init` refuses a non-empty directory without changing it. It creates the
private layout, the state database, and queues creation of Redis (always) and the data services you asked for. The
services are created when the backend first starts.

Options: `--postgres 17`, `--uid-first/--uid-last` (default 10000–19999; use non-overlapping ranges per stack),
`--password-stdin` to set the web password immediately.

## Run the backend

```bash
install -m 0644 deploy/systemd/bento@.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now bento@prod      # serves /srv/bento/prod on 127.0.0.1:7780
export BENTO_STACK_ROOT=/srv/bento/prod
bento auth set-password
bento status
```

The listener is loopback-only; non-loopback addresses are refused. Reach the UI with an SSH tunnel:
`ssh -L 7780:127.0.0.1:7780 your-host`, then open `http://127.0.0.1:7780`. If you browse through a different local
port, add it with `--origin http://127.0.0.1:<port>`.
