---
title: Customize Nginx with drop-ins
description: Add Nginx directives in supported custom directories and validate them safely.
---

# Customize Nginx with drop-ins

Put your Nginx additions under `custom/nginx/`. Bento preserves these files across renders and mounts them read-only. Never edit generated Nginx files.

## Choose the correct context

| Path | Context |
| --- | --- |
| `main.d/*.conf` | main |
| `events.d/*.conf` | end of `events` |
| `http.d/*.conf` | `http`, before generated sites |
| `sites.d/*.conf` | `http`, after generated sites |
| `apps/<slug>/server.d/*.conf` | app HTTP and HTTPS server blocks |
| `apps/<slug>/http.d/*.conf` / `https.d/*.conf` | one protocol server block |
| `proxies/<name>/upstream.d/*.conf` | proxy upstream block |
| `proxies/<name>/server.d/*.conf` | proxy HTTP and HTTPS blocks |

Files load lexically; use `10-`, `20-`, and similar prefixes when order matters.

## Add and validate a health endpoint

Create `/var/lib/bento/custom/nginx/apps/demo/server.d/20-health.conf`:

```nginx
location = /healthz {
  access_log off;
  return 204;
}
```

```sh
bento apply
curl -H 'Host: demo.example.com' http://127.0.0.1/healthz
```

:::caution
A directive valid in one Nginx context can be invalid in another. Redefining a generated singleton directive can make validation fail. The prior generation remains available and no reload occurs on validation failure.
:::

## Troubleshooting

Run `doctor`, inspect the Nginx validation message, and move or correct the drop-in. Never patch `generated/nginx/`; render replaces it. If additive includes cannot express the change, select an app-owned complete vhost template.

## Advanced

Drop-ins are trusted operator input. They can weaken security, expose files, or disrupt routing. Review custom files after upgrades and use `compose -- config` to inspect merged mounts.

## Next steps

- [Customize app templates](/guides/customization/templates/)
- [Use Compose overlays](/guides/customization/compose-overlays/)
- [Render and apply internals](/advanced/render-apply/)
