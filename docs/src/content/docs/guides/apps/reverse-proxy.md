---
title: Create a reverse proxy
description: Route a domain through Bento Nginx to an HTTP service that already exists.
---

# Create a reverse proxy

Publish an existing HTTP service through Bento's Nginx, domain, and TLS settings. Choose the upstream address from Nginx's point of view.

## Choose the upstream address

- Host mode: `127.0.0.1` means the host; Compose service names are unavailable to Nginx.
- Bridge mode: Compose names in the same stack resolve; `127.0.0.1` means Nginx itself. Use `host.docker.internal` for a host service.

## Create and verify

For host-mode Nginx:

```sh
bento proxy create api \
  --domain api.example.com \
  --upstream http://127.0.0.1:9000
bento proxy list
curl -H 'Host: api.example.com' http://127.0.0.1/
```

Repeat `--upstream` for multiple servers:

```sh
bento proxy create api \
  --domain api.example.com \
  --upstream http://127.0.0.1:9000 \
  --upstream http://127.0.0.1:9001
```

Domains and aliases must be globally unique across apps and proxies. Select production TLS only after the HTTP route works; see [domains and TLS](/guides/apps/domains-tls/).

## Remove desired state

:::caution
Removal stops Bento from serving the proxy domain but does not stop or delete the upstream service.
:::

```sh
bento proxy remove api --confirm 'delete api'
```

## Troubleshooting

A `502` usually means the upstream address is wrong in Nginx's network namespace or the service is not listening. Check `stack ingress show`, test the upstream from the appropriate namespace, and inspect Nginx logs. A validation failure leaves the prior generated configuration available; fix the upstream/customization and retry `apply`.

## Advanced

Proxy sites use the same shared, self-CA, ACME, or external certificate choices as apps. Additive upstream/server directives belong under `custom/nginx/proxies/<name>/`; do not edit generated vhosts.

## Next steps

- [Understand networking](/concepts/networking/)
- [Configure TLS](/guides/apps/domains-tls/)
- [Customize Nginx safely](/guides/customization/nginx/)
