---
title: Networking internals
description: Learn how host and bridge networking change addresses, ports, and service discovery.
---

# Networking internals

An address means different things in host and bridge mode. PHP routing stays the same because Nginx uses shared Unix sockets for PHP requests.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/network-namespaces.svg
Alt: Host-mode and bridge-mode Nginx showing what localhost, host.docker.internal, and Compose service names point to.
Show: Two side-by-side panels. In host mode, connect Nginx directly to host ports and host localhost, but not Compose DNS. In bridge mode, place Nginx inside the private network, connect Compose service names there, and connect host.docker.internal back to the host. Show PHP sockets as a separate path that works in both modes.
-->

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

Host mode binds Nginx directly to host ports 80 and 443. It also gives HTTP/3 a direct UDP path. Usually, only one process or stack can own those ports.

Bridge mode supports additional or internal stacks and lets Nginx resolve services on the same private network. You choose whether to publish HTTP and HTTPS ports on the host.

When `HTTP3=true`, bridge HTTPS publishes both TCP and matching UDP. Firewalls/NAT must forward both for QUIC. Clearing bridge publications keeps Nginx internal-only.

## Multi-stack boundaries

Give every stack its own `COMPOSE_PROJECT_NAME`, private network, and stack root. Service names resolve only inside their network.

Run a second stack in bridge mode on different host ports. Never share the first stack's name or named volumes. Docker must also have enough address space to create a private network for each stack.

## Operator consequences

- Diagnose `502` from Nginx's namespace, not the host shell alone.
- Do not publish database/cache ports to solve app connectivity.
- ACME still needs public DNS and reachable port 80 for the target stack.
- Reverse-proxy upstreams must be URLs valid from Nginx.

## Next steps

- [Networking concept](/concepts/networking/)
- [Run multiple stacks](/guides/stacks/multiple-stacks/)
- [Create a reverse proxy](/guides/apps/reverse-proxy/)
