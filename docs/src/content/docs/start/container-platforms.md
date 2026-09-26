---
title: Run Bento alongside a container platform
description: Deploy the Bento control plane next to Dokploy, Coolify, or another Docker-based platform without sharing ownership of its stack.
---

# Run Bento alongside a container platform

Bento can run as a **control-plane container** on the same Linux Docker host as Dokploy or Coolify. It is not a plugin or a managed sidecar of that platform: Bento talks to the host Docker Engine and creates its **own Compose project**, network, Nginx, runtimes, and data volumes. Neither platform automatically discovers or manages Bento's application containers. Use a dedicated host if you cannot grant Bento host-level Docker access or safely separate ingress.

## Before deploying

- Use a supported rootful Linux Docker Engine and a pinned Bento release image (`ghcr.io/khanhicetea/bento:<release-tag>`). The image includes Docker CLI, Compose v2, and Buildx; do **not** run Docker-in-Docker.
- Reserve a persistent host directory, such as `/var/lib/bento`, outside the platform's ephemeral deployment directories. Bind it **to that exact absolute path** in the Bento container. The host Docker daemon resolves generated sibling-container mounts by host path. Do not substitute a named volume or mount `/var/lib/bento` at a different path.
- Mount `/var/run/docker.sock:/var/run/docker.sock` (write access). Bento runs as root in the control-plane image. Whoever can access its CLI or UI can effectively administer the Docker host. Do not share it with untrusted tenants.
- Set `BENTO_STACK_ROOT=/var/lib/bento` and a strong `WEB_BASIC_AUTH` value in `user:password` form. The image refuses to start `serve` without it. Give the secret through the platform's protected environment settings rather than a command line, but remember Docker administrators can inspect container environment values. Basic auth is not TLS, rate limiting, or a substitute for a private management network.
- Leave the image's entrypoint and command unchanged (`bento serve --host 0.0.0.0 --port 8080`). Do not enable automatic stack cleanup or volume removal for Bento's Compose project.

The checked-in [Compose example](https://github.com/khanhicetea/bento/blob/main/deploy/container/compose.yml) shows the socket, same-path bind, and loopback-only UI port publication. If using a platform's container/Compose editor instead, reproduce those settings. Avoid deploying a second Bento instance against the same stack root and Compose project: concurrent controllers can race on service lifecycle even though Bento locks state changes.

## Initialize before starting Bento's services

1. Create the durable host directory (`sudo install -d -m 0750 /var/lib/bento`). Create the platform deployment with the mounts and environment above. Keep the UI reachable only via host loopback or a trusted private tunnel. Do not route the management UI through the platform's public proxy by default.
2. Run **once**, using the platform's console or a temporary container with the same mounts and environment: `bento init --name <unique-stack-name>`. Choose a Compose project name that does not collide with the platform or any other Bento stack. Initialization does not start managed services. If the container is already running, use its console or `docker exec <bento-container> bento init --name <unique-stack-name>`.
3. On the host, review `/var/lib/bento/.env` before running `bootstrap`. If the platform already owns host ports 80/443, change the generated values to, for example:

   ```dotenv
   NGINX_HOST_NETWORK=0
   NGINX_HTTP_PORT=18080
   NGINX_HTTPS_PORT=
   HTTP3=false
   ```

   This puts **Bento's managed Nginx** in bridge mode and publishes only HTTP on a distinct host port. The control-plane UI port `8080` is independent of this ingress setting. Select a free port and restrict its exposure using your host firewall; bridge publications are not loopback-bound by default. Do not edit generated Compose output.
4. Run `bento bootstrap` in the Bento container (for example, `docker exec <bento-container> bento bootstrap`). This builds and starts Bento's separate services; it does **not** make them part of the platform deployment. Check `bento stack ingress show`, `bento compose -- ps`, `bento status`, and `bento doctor` from the same container.

If you prefer the checked-in Compose example, follow [the container installation steps](/start/install/) and change the stack `.env` **before** its `bootstrap` step. Do not delete `/var/lib/bento` or the Bento project's Docker volumes when redeploying or upgrading the control-plane container.

## Ingress and limitations

Your platform's proxy may forward a site's HTTP traffic to the selected **Bento Nginx** host port, provided it preserves the expected `Host` header and can actually reach the host port from its own container/network. `127.0.0.1` inside a proxy container is **not** the Docker host. Use a host-gateway address reachable from that proxy, or a deliberately configured shared network/Compose overlay; the correct address and proxy configuration depend on your platform. Do not assume the two Compose projects share service DNS or networks. Validate routing with a real site before relying on it.

For this topology, normally terminate public TLS and manage public certificates at the **existing platform proxy**. Bento's own ACME HTTP-01 and direct HTTP/3 require their traffic to reach Bento Nginx on the appropriate ports; they will not work automatically behind another proxy. If you need Bento-managed ACME or HTTPS, plan and test that routing explicitly. Do not let Bento default to host-mode 80/443 while the other proxy owns them.

Keep the Bento management UI on host loopback (for example, `127.0.0.1:8080:8080`) and use SSH forwarding to administer it. Publishing `8080` through a public platform domain exposes a host-privileged management plane; only do so behind independently enforced TLS, strong access policy, and trusted operators. Browser scheduler links are designed for a fixed local Bento origin and may not work through a non-local platform proxy.

## Operational ownership

- Control-plane redeploys must reuse the **same stack root path** and Compose project identity; upgrades may migrate `state.db`. Back up the stack root and Bento-owned named volumes separately. Never use `docker compose down -v` on Bento's managed project.
- The container cannot register the host crontab. Configure a host-owned scheduler to invoke `docker exec <bento-container> bento backup schedule run`; monitor failures and verify off-host backups/restores. Transfer directories outside the stack root also need identical host/container bind paths.
- Bento's status and lifecycle commands operate **its own stack only**. Removing the container in Dokploy or Coolify does not intentionally remove Bento's managed containers or their volumes. Inspect Bento's project separately when troubleshooting or retiring the installation.
