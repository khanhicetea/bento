---
title: Create your first stack
description: Initialize, render, start, inspect, and validate one Bento stack on a Linux host.
---

# Create your first stack

Create and start an empty Bento stack named `production` under `/var/lib/bento`. This path uses host-mode Nginx on ports 80 and 443; you will add an application in the next step.

## Before you begin

- [Install and verify `bento`](/start/install/).
- Confirm that the Docker daemon is running and the operator can use it.
- Confirm that `/var/lib/bento` is writable and ports 80 and 443 are free.
- Use a different stack root and stack name if this host already has a Bento stack.

Every command below includes `--stack /var/lib/bento`. Keep that option consistent: running a command against a different path targets a different stack.

## Initialize the stack

Choose the stack name before initialization. The name becomes the stable Docker Compose identity that prefixes containers, networks, and named volumes; it is not inferred from `/var/lib/bento`.

```sh
bento --stack /var/lib/bento init --name production
```

Bento creates private `state.json` and `.env` files along with the initial stack directories. The `.env` file includes generated database and Redis administrator secrets.

:::caution
Treat the stack name as permanent. Changing `COMPOSE_PROJECT_NAME` later would point Compose at differently named resources, including durable volumes. Do not use `init --force` as a retry command: it intentionally overwrites existing desired state.
:::

Verify the selected identity and ingress mode:

```sh
bento --stack /var/lib/bento stack ingress show
```

The result should show the name `production` and mode `host`. In host mode, Nginx uses the host network and binds directly to ports 80 and 443; the “host port” publication fields are therefore not used.

## Render and inspect the configuration

Render the desired state into generated configuration without starting or signaling services:

```sh
bento --stack /var/lib/bento render
```

Render should report that it wrote files with no service signals. Inspect the deterministic Compose file chain:

```sh
bento --stack /var/lib/bento compose files
```

Validate the merged Compose model before startup:

```sh
bento --stack /var/lib/bento compose -- config --quiet
```

No output and a zero exit status mean Docker Compose accepted the generated model. Generated files under `/var/lib/bento/generated/` are disposable output; do not edit them.

## Start the stack

:::caution
The next command builds or downloads container images, creates durable Docker volumes, and starts Nginx on host ports 80 and 443. If the host firewall permits inbound traffic, Nginx becomes publicly reachable. Confirm that those ports belong to this stack before continuing.
:::

Start all services in the background:

```sh
bento --stack /var/lib/bento compose -- up -d --build
```

The first build can take several minutes. The stack initially includes Nginx, Redis, the default PHP FPM and runner roles, and MySQL 8.4.

After the containers start, run a validated apply. This regenerates the candidate configuration, validates the merged Compose model and available service configurations, and reloads only affected running services:

```sh
bento --stack /var/lib/bento apply
```

Do not add `--skip-validate` to the normal startup path.

## Verify the stack

Inspect Bento's view of the stack:

```sh
bento --stack /var/lib/bento status
```

Confirm that it reports:

- stack name `production` and root `/var/lib/bento`;
- host ingress on ports 80 and 443;
- the expected Nginx, Redis, PHP, runner, and MySQL roles;
- no applications or domains yet.

Check the underlying containers when a role is still starting:

```sh
bento --stack /var/lib/bento compose -- ps
```

Finally, run the broader host and stack diagnostics:

```sh
bento --stack /var/lib/bento doctor
```

A healthy first stack has no failed checks. Warnings can still describe optional or transitional conditions; read each one before proceeding. `doctor` exits nonzero when any check fails.

## Troubleshooting

**Initialization says state already exists:** verify that `--stack` points to the intended directory. Continue with the existing stack instead of using `--force`, unless you deliberately intend to replace its desired state.

**Compose reports that port 80 or 443 is already allocated:** stop or reconfigure the existing listener. One host-mode stack owns those ports. An additional stack needs a distinct name, bridge mode, and non-conflicting publications.

**A build or image pull fails:** confirm outbound network access and available disk space, then rerun the same `compose -- up -d --build` command. Inspect Docker's error before changing generated files.

**Docker says its address pools are fully subnetted:** Docker cannot allocate the stack-private backend network. Inspect `docker network ls` and remove only networks you have confirmed are unused, or configure suitable Docker daemon address pools. Do not delete an active stack's network.

**A container is restarting or unhealthy:** inspect its recent logs:

```sh
bento --stack /var/lib/bento compose -- logs --tail 100 <service>
```

Replace `<service>` with the role shown by `status` or `compose -- ps`, such as `nginx`, `php85`, `mysql84`, or `redis`.

**`apply` rejects the candidate:** keep the reported error. Bento does not finalize an invalid candidate. Check service logs and run `doctor`; do not bypass validation merely to make the command succeed.

## Advanced

`render` and `apply` serve different purposes. `render` writes generated files but never signals services. `apply` performs a staged transaction and, when relevant services are running, validates and reloads only the affected roles. Starting containers remains an explicit Compose operation through Bento's wrapper.

The wrapper assembles every managed Compose fragment and operator overlay in deterministic order. Use it instead of manually selecting files. It also blocks `docker compose down -v`, `--volumes`, and destructive `--rmi` forms to protect durable database and Redis resources.

The examples use explicit `--stack` even though Bento accepts `BENTO_STACK_ROOT` and defaults to `./bento`. Explicit targeting prevents the current directory from silently selecting another production stack.

## Next steps

- [Add and verify your first application](/start/first-app/).
- [Understand stack roots, names, and resource identity](/concepts/stacks/).
- [Review host networking, ports, and DNS requirements](/start/requirements/).
