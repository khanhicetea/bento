---
title: Safety model
description: Confirmations, secrets, and what Bento refuses to do.
sidebar:
  order: 4
---

- **Exact confirmations:** `delete <slug>` (remove app), `delete` (prune retained data), `replace <database>`
  (restore), `export` (stack export), `delete <host>` (remove an Ingress host).
- **No destructive shortcuts:** Bento never removes data volumes, never replaces a missing volume with an empty one,
  never drops a database outside prune, and never rotates database passwords.
- **Secrets** live in private files (`0400`/`0440`). They are passed over exec stdin or files — never in command
  arguments, container labels, environment shown by `docker inspect`, logs, or API responses. Redis ACLs store only
  password hashes.
- **Containers** run as the app UID, read-only, with all capabilities dropped and `no-new-privileges`.
- **Management** is loopback-only with a password session, exact-origin checks, and CSRF tokens. Loopback is not
  treated as authentication.

Bento is not a hostile-tenant sandbox: trust the code you run.
