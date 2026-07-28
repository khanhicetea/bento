---
title: Run multiple stacks
description: Add a distinctly named bridge-mode stack without conflicting with production ingress.
---

# Run multiple stacks

Run an additional Bento stack with its own stack root, Compose identity, private network, and non-conflicting ingress ports.

## Before you begin

- Choose a durable, empty root and unique stack name.
- Reserve distinct TCP ports for HTTP/HTTPS; reserve the same HTTPS UDP port if HTTP/3 is enabled.
- Keep the primary host-mode stack on ports 80/443.

## Create the second stack

```sh
sudo install -d -m 0700 /srv/bento/customer-b
bento --stack /srv/bento/customer-b init --name customer-b
bento --stack /srv/bento/customer-b stack ingress set bridge \
  --http-port 8080 --https-port 8443
bento --stack /srv/bento/customer-b render
bento --stack /srv/bento/customer-b compose -- up -d --build
```

Use `0` to clear a bridge publication and keep ingress internal-only:

```sh
bento --stack /srv/bento/customer-b stack ingress set bridge \
  --http-port 0 --https-port 0
```

## Verify

```sh
bento --stack /srv/bento/customer-b stack ingress show
bento --stack /srv/bento/customer-b status
bento --stack /srv/bento/customer-b compose -- ps
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

Bridge Nginx can resolve services on its own stack-private network. It cannot discover services in another stack by Compose name. Operator overlays may publish a specific address, but keep stack roots and names explicit in every scheduled or destructive command.

## Next steps

- [Understand networking](/concepts/networking/)
- [Create a reverse proxy](/guides/apps/reverse-proxy/)
- [Diagnose a stack](/guides/stacks/diagnostics/)
