---
title: Networking and ingress
description: Understand Bento's public ingress, private service network, host and bridge modes, and reverse-proxy addresses.
---

# Networking and ingress

Bento exposes web traffic through one Nginx service while keeping PHP, databases, Redis, and job runners on a stack-private network. The Nginx **ingress mode** determines how Nginx reaches the host and private services, so it also changes what an upstream address such as `127.0.0.1` means.

## Mental model

```text
                         host mode
Internet -> host :80/:443 -> Nginx
                              |
                              +-> shared PHP-FPM Unix sockets

                         bridge mode
Internet -> published host ports -> Nginx -> stack-private network
                                         |       +-> Redis/databases
                                         |       +-> overlay services
                                         +-> shared PHP-FPM Unix sockets
                                         +-> host.docker.internal -> host
```

In the base topology, Nginx is the only public service. PHP-FPM, PHP runners, ephemeral PHP CLI containers, MySQL, PostgreSQL, and Redis join a Compose network named from the [stable stack name](/concepts/stacks/), such as `production_private`. MySQL, PostgreSQL, and Redis do not publish host ports.

PHP web requests are a special case: Nginx reaches each app's PHP-FPM pool through a shared Unix socket mount. This path works in both ingress modes and does not require Nginx to resolve the PHP Compose service name.

## Host and bridge modes

| Behavior | Host mode | Bridge mode |
| --- | --- | --- |
| Nginx network | Host network | Stack-private Compose network |
| Default | Yes | No; opt in per stack |
| HTTP/HTTPS endpoint | Host ports `80` and `443` directly | Optional operator-selected host ports mapped to container ports `80` and `443` |
| `127.0.0.1` from Nginx | The host | The Nginx container |
| Compose service names from Nginx | Unavailable | Available within the same stack-private network |
| Host gateway name | Not needed | `host.docker.internal` |
| Typical use | Primary stack on a Linux host | Additional stack or internal-only ingress |

### Host mode

Host mode is Bento's default. Nginx shares the Linux host's network namespace and binds TCP ports `80` and `443` directly. If HTTP/3 is enabled, it also uses UDP `443`.

This gives Nginx direct access to host-loopback services: a reverse-proxy upstream such as `http://127.0.0.1:3000` refers to port `3000` on the host. Nginx does not join the stack-private Compose network in this mode, so a name such as `redis` or an overlay service name is not available to it through Compose DNS.

:::caution
Only one host-mode listener can own a given host port. Two default host-mode Bento stacks cannot both bind ports `80` and `443`; another web server can conflict for the same reason.
:::

### Bridge mode

Bridge mode joins Nginx to its stack-private network. You may publish distinct host ports—for example, host `18080` to container `80` and host `18443` to container `443`—or leave both unpublished for an internal-only or overlay-managed topology.

With `HTTP3=true`, a published HTTPS port maps both TCP and UDP to container port `443`. Without HTTP/3, only TCP is published. Bento uses the selected HTTPS host port in generated redirects, HTTP/3 advertisements, and deploy webhook instructions.

Bridge mode adds `host.docker.internal` as an explicit host-gateway name. A host upstream therefore uses an address such as `http://host.docker.internal:3000`, not `127.0.0.1`.

:::note
The host service must listen on an address reachable from Docker's host gateway, and the host firewall must allow the connection. A service bound only to host loopback may not accept bridge-originated traffic even when the port is correct.
:::

Blank HTTP and HTTPS publication settings mean Nginx has no directly published host endpoint. They do not configure an external load balancer or connect separate stacks automatically; those require an operator-owned Compose overlay or other network setup.

## Choose a reverse-proxy upstream

Choose the address from Nginx's network namespace, not from the browser's or operator shell's perspective:

| Upstream location | Host-mode URL | Bridge-mode URL |
| --- | --- | --- |
| Service on this host | `http://127.0.0.1:3000` | `http://host.docker.internal:3000` |
| Service added to this stack's private network | Not resolvable by Compose service name | `http://api:3000` |
| Nginx container itself | Not a distinct network namespace | `http://127.0.0.1:<port>` |
| Service in another stack's private network | Not directly available | Not directly available by default |

A service name such as `api` works only if that service actually joins the same stack-private network. Each stack receives a distinct network, so bridge mode does not make service discovery cross stack boundaries.

Use `http://` or `https://` explicitly in a Bento reverse-proxy upstream. Multiple upstreams must use the same protocol, path, and query. Do not put credentials in the URL; Bento rejects upstream URLs containing user information or fragments.

## How it affects operations

Inspect the effective stack name, mode, publications, and HTTP/3 UDP publication before diagnosing traffic:

```sh
bento stack ingress show
```

Also check the host listeners. A host-mode stack should own `80` and `443`; a bridge-mode stack should own only its selected published ports:

```sh
sudo ss -ltnp
sudo ss -lunp
```

When testing a domain before DNS is ready, connect to the effective host port while preserving the domain in the request. For a bridge-mode HTTP publication on `18080`:

```sh
curl --resolve demo.example.com:18080:127.0.0.1 \
  http://demo.example.com:18080/
```

Changing ingress mode rewrites the stack environment and generated Compose topology. If Nginx is running and Docker Compose is available, Bento validates the Compose configuration and force-recreates Nginx, which can briefly interrupt requests. Verify the endpoint again after any mode or publication change.

## Boundaries and limitations

- The default topology is designed for Linux; host networking does not provide the same behavior on every Docker platform.
- The private Compose network prevents base database and Redis ports from being published. It is not encryption and is not a security boundary for mutually hostile apps sharing the stack.
- Bridge publications select host ports, not bind addresses. Binding a specific host address requires an operator-owned Compose overlay.
- A distinct stack name separates Compose networks and resources but does not reserve host ports or configure DNS.
- Public TLS still depends on the selected ingress endpoint, correct DNS, and reachable ports. ACME issuance requires public port `80` to reach the relevant Nginx service.

## Advanced

The Nginx container sees PHP sockets at `/run/php-fpm/<php-service>/<app>.sock`, while each PHP-FPM service sees its app sockets under its own `/run/php-fpm/`. Bind mounts align those namespace-specific paths. This socket design keeps ordinary PHP request routing independent of whether Nginx uses the host or private bridge network.

Backend containers use Compose service names for private traffic: apps connect to their selected MySQL or PostgreSQL service and to Redis rather than to `localhost`. Ephemeral app commands launched by Bento join the same private network, so they use the same connection metadata as the running app.

Host mode favors direct standard ports, host-loopback proxy targets, and straightforward UDP handling. Bridge mode adds port flexibility and same-stack service discovery, at the cost of another network namespace and different host-upstream addressing. Neither mode turns Bento into a cross-host network or an automatic multi-stack router.

## Next steps

- [Review stack roots, names, and Compose resource identity](/concepts/stacks/).
- [Configure application domains and TLS](/guides/apps/domains-tls/).
- [Diagnose and operate a running stack](/start/daily-operations/).
