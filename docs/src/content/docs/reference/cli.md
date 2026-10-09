---
title: CLI
description: Command reference.
---

```text
bento [--stack ROOT] [--json] [--no-wait] <command>

serve [--listen 127.0.0.1:7780] [--origin URL]... [--utils-listen IP:PORT|apps:PORT|off]... [--op-concurrency 4]
init --name NAME [--mysql V] [--postgres V] [--uid-first N --uid-last N] [--password-stdin]
import --from DIR [--name NAME] [--uid-first N --uid-last N]
version | status | auth set-password
apps | app show|create|update|start|stop|restart|publish|unpublish|remove|bind|add-db|logs|exec|shell|minicrond|permissions
app git|deploy|webhook SLUG
ops [--target ID] | op ID | op cancel ID
services | service add --engine E --version V
edge | edge set --json FILE | tunnel | tunnel set-token | tunnel disable
hosts | host add --json FILE | host set --json FILE | host remove NAME
retired | retired prune APP_ID
backup run|list|runs|restore|schedule
export --to DIR
```

`serve --op-concurrency N` (1–16, default 4) bounds how many operations run at once. Only operations on different
apps or data services overlap (start, stop, restart, update, deploy, reconcile, service repair); everything else runs
alone. `1` runs every operation strictly one after another. A queued operation that waits behind another shows
`waiting on <id>` in `bento ops`.

`serve`, `init`, and `import` operate on the stack root directly and take the stack lock. All other commands talk to
the running backend through `run/bento.sock` (root only). Exit codes: 0 success, 2 usage, 3 rejected request,
1 other failures. Mutations use an idempotency key, so a retried request cannot run twice.
