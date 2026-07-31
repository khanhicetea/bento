---
title: Requirements and host preparation
description: Check that your Linux host, Docker, ports, DNS, and storage are ready for Bento.
---

# Requirements and host preparation

Prepare one Linux host that can run Docker Compose and store durable stack data. Production hosts need only the compiled Bento release and its host dependencies. Contributors also need the source toolchain described later on this page.

## Production host requirements

### Operating system and architecture

Use a Linux host with one of the architectures for which Bento builds release binaries:

- x86-64 (`amd64`); or
- 64-bit Arm (`arm64`/`aarch64`).

Bento does not currently publish macOS or Windows binaries. It operates a Linux data plane and relies on Linux filesystem modes and host utilities; Docker Desktop is not a supported production-host substitute.

Choose the compiled binary that matches the host:

```sh
uname -s
uname -m
```

Expect `Linux`, followed by `x86_64` or `aarch64`. The compiled binary includes the Bento control plane and immutable templates. It does not require Deno, Node.js, Python, `npm install`, or a source checkout on the production host.

### Docker Engine and Compose

Install and start:

- Docker Engine 20.10 or newer, running rootful without user-namespace remapping when SQLite continuous backup is used; and
- the Docker Compose v2 plugin, version 2.20 or newer.

The account that runs `bento` must reach the Docker daemon. Docker access effectively grants control of the host. Follow Docker's security guidance when you choose between `sudo` and Docker-group membership.

Verify both the client and daemon, not only the presence of the `docker` command:

```sh
docker version --format '{{.Server.Version}}'
docker compose version
docker info >/dev/null
```

All three commands must succeed. If they fail with a permission error, fix Docker access for the operator account. If they report that the daemon is unavailable, start Docker before continuing.

### Host utilities

Install these standard host tools before the first stack:

- OpenSSL, used to create and inspect stack-managed certificates;
- OpenSSH `ssh-keygen`, used to create each app's deploy key.

Check that both are available:

```sh
openssl version
command -v ssh-keygen
```

Later optional operations also use `tar` for stack transfer and support bundles and `crontab` for scheduled backups. SQLite continuous backup does not require `setfacl`; it uses a narrowly mounted, capability-limited root Litestream container.

### Storage and permissions

Choose a local, durable filesystem for the **stack root**. The examples use `/var/lib/bento`.

The root will contain desired state, secrets, app homes, SQLite databases, certificates, logs, and on-host backups. Docker stores MySQL, PostgreSQL, and Redis data in separate named volumes.

Ensure that:

- the operator account can create and modify the stack root;
- the filesystem supports normal Linux ownership and permission bits;
- the host has enough free space and inodes for application code, database volumes, images, logs, and backups;
- the stack root and Docker data directory are not on ephemeral storage.

Bento has no single CPU, memory, or disk minimum. Size the host for your PHP workers, databases, traffic, and backup retention. Leave free disk for live data and temporary backup or upgrade work.

Some permission repairs need elevated host privileges. Otherwise, `bento` does not need to run as root when the operator can access Docker and the stack root.

## Prepare ports, firewall, and DNS

The first-stack path uses **host mode**, in which Nginx binds directly to host TCP ports 80 and 443. Those ports must not already belong to another web server or stack.

Check for listeners before startup:

```sh
sudo ss -ltnp | grep -E ':(80|443)\b' || true
```

No output means no TCP listener was found on those ports. If another process owns either port, stop or reconfigure it before using the default path. An additional Bento stack must use bridge mode and distinct published ports; do not try to make two host-mode stacks share ports 80 and 443.

For a publicly reachable site:

- allow inbound TCP 80 and 443 through the host firewall and any provider firewall;
- point each site's DNS A and/or AAAA record at this host;
- if you later enable HTTP/3, also allow UDP 443;
- keep the host clock synchronized so certificate and diagnostic checks are reliable.

ACME certificate issuance has a stricter requirement: every requested domain must resolve to this host, and public TCP port 80 must reach this stack's Nginx. Prepare those records before selecting ACME TLS.

You can check whether a configured name resolves from the host:

```sh
getent ahosts demo.example.com
```

Confirm that the returned address is the intended server address. DNS changes may take time to propagate.

## Source-development requirements

You need the source toolchain only when running or modifying Bento from a repository checkout. Install Deno 2.9.3, the version pinned by the implementation and CI:

```sh
deno --version
```

The first line must report `deno 2.9.3`. Source tasks use the permissions declared in `deno.json`; do not replace them with unrestricted `-A` in the supported workflow. Dependencies resolve through Deno and the lockfile, so source mode still does not require Node.js, Python, or `npm install` for the Bento control plane.

Before changing Bento, verify the checkout:

```sh
deno task fmt:check
deno task lint
deno task check
deno task test
```

Docker is also required for integration tests and the real stack harness. Those development checks are not production installation steps.

## Verify readiness

Before installing Bento, confirm this checklist:

- `uname` reports Linux on `x86_64` or `aarch64`;
- the Docker daemon is running and meets the minimum version;
- `docker compose` is v2.20 or newer;
- the operator can use Docker and write the planned stack root;
- ports 80 and 443 are available for the first host-mode stack;
- firewall and DNS changes are possible for each public domain;
- OpenSSL and `ssh-keygen` are installed;
- Deno 2.9.3 is installed only if you intend to use source mode.

## Troubleshooting

**`docker version` shows client information but no server version:** the daemon is stopped, unreachable, or denied to your account. Fix that before initializing a production stack.

**`docker compose` is unknown:** install the Compose v2 plugin. The legacy standalone `docker-compose` command is not the interface Bento invokes.

**Port 80 or 443 is already occupied:** identify the listener with `ss`. Stop or relocate it, or plan a bridge-mode stack on different ports. The beginner host-mode path cannot coexist with another listener on those ports.

**A domain resolves to the wrong address:** correct its A/AAAA records and wait for DNS propagation before attempting ACME issuance.

## Next steps

- [Review what Bento manages and its operating boundaries](/start/overview/)
- [Install and verify the compiled Bento command](/start/install/).
