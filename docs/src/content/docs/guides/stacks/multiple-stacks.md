---
title: Run multiple stacks
description: Add a second bridge-mode stack with its own name, root, network, and ports.
---

# Run multiple stacks

Run another Bento stack without colliding with the first one. Give it a separate root, Compose identity, private network, and host ports.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/two-stacks-one-host.svg
Alt: One host-mode production stack on ports 80 and 443 beside a bridge-mode staging stack on different published ports.
Show: One Linux host containing two stack boundaries. Give each its own root, stack name, private network, and named volumes. Connect public 80/443 only to production and 18080/18443 to staging. Show that service-name DNS and volumes do not cross the boundary.
-->

## Before you begin

- Choose a durable, empty root and unique stack name.
- Reserve distinct TCP ports for HTTP/HTTPS; reserve the same HTTPS UDP port if HTTP/3 is enabled.
- Keep the primary host-mode stack on ports 80/443.

## Create the second stack

```sh
sudo install -d -m 0700 /srv/bento/customer-b
export BENTO_STACK_ROOT=/srv/bento/customer-b
bento init --name customer-b
bento stack ingress set bridge \
  --http-port 8080 --https-port 8443
bento render
bento compose -- up -d --build
```

Use `0` to clear a bridge publication and keep ingress internal-only:

```sh
bento stack ingress set bridge \
  --http-port 0 --https-port 0
```

## Verify

```sh
bento stack ingress show
bento status
bento compose -- ps
```

Check routing while DNS is pending:

```sh
curl -H 'Host: demo.example.com' http://127.0.0.1:8080/
```

:::caution
Never reuse `COMPOSE_PROJECT_NAME` across stacks. Compose resources and named volumes use this stable identity; a collision can target another stack's data.
:::

## Troubleshooting

If a port is occupied, choose another publication and apply again. In bridge mode, `127.0.0.1` is the Nginx container; use `host.docker.internal` for a service on the host. Docker address-pool exhaustion can also prevent private-network creation; remove genuinely unused networks or expand Docker's configured address pools—never delete an active stack network blindly.

## Advanced

Bridge Nginx can resolve services on its own stack-private network. It cannot discover services in another stack by Compose name. Operator overlays may publish a specific address, but verify `BENTO_STACK_ROOT` and the stack name before every scheduled or destructive operation.

## Next steps

- [Understand networking](/concepts/networking/)
- [Create a reverse proxy](/guides/apps/reverse-proxy/)
- [Diagnose a stack](/guides/stacks/diagnostics/)
