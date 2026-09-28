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

- Routes exist only for apps with `ingress: managed` that are **published**, and for reverse proxies.
- TLS per app: `none`, `self-signed` (shared boot certificate), `acme` (Nginx ACME module, needs public DNS and port 80),
  `external` (files at `edge/certs/external/<name>/fullchain.pem` and `privkey.pem`).
- The edge resolves `app-<appId>` through Docker DNS every 10 seconds, so replaced containers are picked up without a
  reload.
- Every change is rendered into a candidate directory, checked with `nginx -t`, swapped in atomically, then reloaded.
  A rejected candidate leaves the live configuration untouched.
- The edge never mounts app homes, sockets, credentials, or backups.

## Custom Nginx drop-ins

Operator-owned files under `edge/custom/` are included as trusted input and validated with every change:
`main.d/*.conf`, `http.d/*.conf`, `sites.d/*.conf`, and `routes/<route-name>/*.conf` (inside a route's server block,
where the route name is `app-<slug>` or `proxy-<name>`).

## Reverse proxies

`bento proxy set --json proxy.json` with `{"name":"grafana","upstreams":["http://10.0.0.5:3000"],"domains":["grafana.example.com"],"route":{"tls":"acme","redirectHttps":true,"accessLog":false,"staticCache":false},"enabled":true}`.

Each proxy gets its own `upstream` block with pooled keepalive connections. `"staticCache": true` caches public
static files (css, js, images, fonts) in the edge's in-memory cache. Unlike app routes, a proxy's cache honors the
upstream's own headers: responses marked `private`, `no-store`, or `no-cache`, and responses that set cookies, are
never stored, and upstream freshness (`max-age`) wins over the 10-minute default.
