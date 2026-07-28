---
title: Networking internals
description: Understand Bento network namespaces, FPM socket mappings, ingress trade-offs, HTTP/3, and multi-stack constraints.
---

# Networking internals

Address interpretation depends on Nginx's network namespace; PHP routing does not because it uses shared Unix sockets.

## Namespaces

| Address/name | Host-mode Nginx | Bridge-mode Nginx |
| --- | --- | --- |
| `127.0.0.1` | Host loopback | Nginx container |
| Stack Compose service name | Unavailable | Resolves on this stack's private network |
| `host.docker.internal` | Usually unnecessary | Host gateway via `extra_hosts` |

PHP FPM, runner, CLI, databases, and Redis join the stack-private network. MySQL/PostgreSQL/Redis publish no base host ports.

## Socket mapping

One app socket has namespace-specific paths:

```text
Host:    <stack>/runtime/php-fpm/<php-service>/<app>.sock
Nginx:   /run/php-fpm/<php-service>/<app>.sock
PHP-FPM: /run/php-fpm/<app>.sock
```

Mount and group alignment is invariant. Nginx can route PHP without joining backend networking in host mode.

## Host versus bridge

Host mode gives direct 80/443 binding and straightforward UDP/HTTP/3. Normally only one process/stack can own those ports. Bridge mode enables separate/internal stacks and same-stack service discovery; it optionally publishes selected HTTP/HTTPS host ports.

When `HTTP3=true`, bridge HTTPS publishes both TCP and matching UDP. Firewalls/NAT must forward both for QUIC. Clearing bridge publications keeps Nginx internal-only.

## Multi-stack boundaries

Every stack needs a unique `COMPOSE_PROJECT_NAME`, private network, and stack root. Service names resolve only within their network. A second stack should use bridge mode and distinct ports; it cannot safely share the primary stack's named volumes or identity. Docker address pools must have capacity for each private network.

## Operator consequences

- Diagnose `502` from Nginx's namespace, not the host shell alone.
- Do not publish database/cache ports to solve app connectivity.
- ACME still needs public DNS and reachable port 80 for the target stack.
- Reverse-proxy upstreams must be URLs valid from Nginx.

## Next steps

- [Networking concept](/concepts/networking/)
- [Run multiple stacks](/guides/stacks/multiple-stacks/)
- [Create a reverse proxy](/guides/apps/reverse-proxy/)
