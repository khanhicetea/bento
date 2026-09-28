---
title: Tunnel and external proxies
description: Cloudflare Tunnel and operator-owned proxies.
sidebar:
  order: 2
---

## Cloudflare Tunnel

```bash
bento tunnel set-token      # paste the token; it is stored in a private file and never shown again
bento tunnel disable
```

The `cloudflared` container joins the apps network. In Cloudflare, point public hostnames either at the edge
(`http://bento-<stack>-edge:80`) or directly at an app (`http://app-<appId>:<port>` — shown as "Internal URL" in the
app detail). For deploy webhooks on a hostname routed directly to an app, add a path rule for `^/_webhook/` to
Bento's public listener (see [Auto deploy](/guides/apps/auto-deploy/)). Replacing the token recreates only the
tunnel container. Cloudflare hostname rules are yours; Bento does not manage them.

## External mode

Set an app's `ingress` to `external` when a tunnel or your own proxy routes to it directly. Bento then:

- does not offer publish/unpublish for it,
- cannot remove that route: **stopping the app makes it unavailable, but the route stays** and returns errors,
- cannot apply edge features (redirects, TLS policy, limits, access logs) to that traffic.

To attach your own proxy (Traefik, Caddy, …), connect its container to `bento-<stack>-apps` and route to
`app-<appId>:<port>`. Anything on that network can reach every app listener. Bento remains the only owner of app
containers; do not let another platform manage them.
