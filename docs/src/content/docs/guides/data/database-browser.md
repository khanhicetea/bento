---
title: Database browser
description: Open an app's MySQL or PostgreSQL databases in Adminer from the UI.
sidebar:
  order: 2
---

Every MySQL or PostgreSQL binding in **Apps → _app_ → Data** has a **Browse** button. It opens the binding's
databases in [Adminer](https://www.adminer.org/) in a new tab, signed in as the app's own database user.

```text
Bento UI ──ticket──► new tab: <utils>/_dbadmin/t/<ticket> ──► grant cookie ──► /_dbadmin/b/<binding>/
                                                             Bento injects the binding's credentials
                                                             ──► shared Adminer container (data network)
```

## Enable it

**Ingress → Utils → Database browser → Enable** starts one shared container, `bento-<stack>-dbadmin`, for all apps.
It joins only the private data network, runs as a non-root user with a read-only root filesystem, and stores no
credentials. Disabling it removes the container.

## Where the tab opens

The browser runs on the **utils listener** (the same port as [deploy webhooks](/guides/apps/auto-deploy/)), not on
the management UI's origin:

- With **Ingress → Utils → Base URL** set, links open there (for example `https://utils.example.com/_dbadmin/…`).
  Route `/_dbadmin/*` on that host to the utils listener, as you did for `/_webhook/*`:

  ```nginx
  location /_dbadmin/ {
    proxy_pass http://127.0.0.1:7781;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 80m;
    proxy_read_timeout 15m;
  }
  ```

- Without a base URL, links open on the host you use for the UI at the utils listener's loopback port
  (`http://127.0.0.1:7781/…`). If you reach the UI through an SSH tunnel, forward that port too:
  `ssh -L 7780:127.0.0.1:7780 -L 7781:127.0.0.1:7781 host`.

The managed edge does **not** forward `/_dbadmin/*` on app domains.

## Access model

- **Browse** asks the management API (session + CSRF) for a single-use ticket that expires after one minute.
- Opening the ticket sets an HttpOnly grant cookie scoped to `/_dbadmin/b/<binding>/`. A grant opens only that
  binding. It expires after 30 idle minutes, and immediately when you sign out, change the operator password, or
  the backend restarts; click **Browse** again.
- On every request Bento re-checks the grant, your session, and the binding, then forwards the binding's host,
  user, password, and database list to Adminer in request headers with a gateway token. The Adminer container
  refuses requests without the token and ignores any server, driver, user, or database in the URL.
- Adminer connects as the app user (`u<appId>`), so it sees exactly what the app can see. Write access is the
  app user's full access: queries and imports change live data.
- Cross-site form posts are refused (`Sec-Fetch-Site` / `Origin` must match the utils host).

The utils listener can be exposed to the internet, and a valid ticket is the only way in. Still, expose it over
HTTPS only: the grant cookie is a bearer credential for the binding.

## Limits

- SQLite and Redis bindings are not supported.
- Requests may upload up to 80 MB and run for 15 minutes.
