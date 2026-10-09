---
title: Managed edge
description: Bento's shared Nginx for domains, TLS, and routing.
sidebar:
  order: 1
---

The edge is optional and disabled by default.

```bash
echo '{"enabled":true,"bind":"0.0.0.0","httpPort":80,"httpsPort":443,"http3":false,
       "acmeEmail":"you@example.com","acmeUrl":""}' | bento edge set --json -
```

- The edge serves **Ingress hosts** (see below). A host that targets an app is served only while the app has
  `ingress: managed` and is **published**.
- TLS per host: `none`, `self-signed` (shared boot certificate), `acme` (Nginx ACME module, needs public DNS and port
  80), `external` (files at `edge/certs/external/<name>/fullchain.pem` and `privkey.pem`).
- The edge resolves `app-<appId>` through Docker DNS every 10 seconds, so replaced containers are picked up without a
  reload.
- Every change is rendered into a candidate directory, checked with `nginx -t`, swapped in atomically, then reloaded.
  A rejected candidate leaves the live configuration untouched.
- The edge never mounts app homes, sockets, credentials, or backups.

## Custom Nginx drop-ins

Operator-owned files under `edge/custom/` are included as trusted input and validated with every change:
`main.d/*.conf`, `http.d/*.conf`, `sites.d/*.conf`, and `routes/<route-name>/*.conf` inside a host's server blocks.
Every host includes `routes/host-<host name>/`; a host that targets an app also includes `routes/app-<slug>/`, so one
drop-in can cover all of an app's hosts.

## Ingress hosts

Ingress owns domains. Each host name is unique and points at exactly one target, with its own TLS and edge settings:

| Target     | Sends requests to                                                                |
| ---------- | -------------------------------------------------------------------------------- |
| `app`      | A Bento app (by slug). The app must use managed ingress.                         |
| `upstream` | One or more `http(s)://` URLs outside Bento, load-balanced with pooled keepalive |
| `redirect` | Another host name, as a permanent (301) redirect that keeps the path and query   |

```bash
bento host add --json www.json   # {"name":"www.shop.example.com","target":{"kind":"redirect","redirectTo":"shop.example.com"},"route":{"tls":"acme"},"enabled":true}
bento host add --json grafana.json   # {"name":"grafana.example.com","target":{"kind":"upstream","upstreams":["http://10.0.0.5:3000"]},"route":{"tls":"acme","redirectHttps":true},"enabled":true}
bento host set --json grafana.json   # replace an existing host's target and settings
bento host remove grafana.example.com   # prompts for "delete grafana.example.com"
bento hosts
```

`add` refuses a name that already exists; `set` re-points an existing host, for example from one app to another.
Removing an app removes the hosts that target it. A redirect to a host the edge serves uses that host's scheme and
port; a redirect to any other name keeps the request's scheme.

An app's first enabled host is its display address and, when no utils base URL is set, the origin of its webhook URL.
Publishing an app needs at least one enabled host that targets it.

`"staticCache": true` caches public static files (css, js, images, fonts) in the edge's in-memory cache. App hosts
cache for 10 minutes regardless of the app's headers. Upstream hosts honor the upstream's own headers: responses
marked `private`, `no-store`, or `no-cache`, and responses that set cookies, are never stored, and upstream freshness
(`max-age`) wins over the 10-minute default.

### Upgrading from app domains and reverse proxies

Opening a stack from an earlier Bento migrates it once: every app domain becomes an `app` host and every reverse
proxy domain an `upstream` host, each with its former owner's TLS settings; an app's former primary domain stays its
display host. Drop-ins under `routes/app-<slug>/` keep applying. Drop-ins under `routes/proxy-<name>/` no longer do:
move them to `routes/host-<host name>/`.
