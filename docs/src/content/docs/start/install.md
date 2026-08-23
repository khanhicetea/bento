---
title: Install Bento
description: Install the Bento command and choose a safe location for stack data.
---

# Install Bento

Install the compiled `bento` command on a supported Linux host. Then choose a durable location for stack data.

The compiled command includes its templates. A production host does not need Bun, Node.js, Python, or a source checkout.

## Before you begin

- [Prepare a supported Linux host](/start/requirements/) with Docker Engine and Docker Compose v2.
- Confirm that `uname -m` reports `x86_64` or `aarch64`.
- Choose an account that can use Docker and write the stack root.

## Install the compiled command

1. Open the project's [GitHub Releases page](https://github.com/khanhicetea/bento/releases) and download the asset from the release you intend to run:

   | `uname -m` | Release asset |
   | --- | --- |
   | `x86_64` | `bento-linux-amd64` |
   | `aarch64` | `bento-linux-arm64` |

   Bento does not publish a stable “latest” download URL or checksum file. Choose a specific trusted release instead of building a download URL yourself. Record the release version for future recovery and audits.

2. Install the downloaded asset on the command path. For example, on an x86-64 host:

   ```sh
   sudo install -m 0755 ./bento-linux-amd64 /usr/local/bin/bento
   ```

   On a 64-bit Arm host, replace the source filename with `./bento-linux-arm64`. Installing to `/usr/local/bin` also gives scheduled commands a stable absolute path.

3. Verify the installed command:

   ```sh
   command -v bento
   bento version
   ```

   `command -v` should report `/usr/local/bin/bento`. The version output identifies both the Bento version and the Bun target embedded in that build. A production host does not need a separate Bun installation.

## Choose a stack root

The **stack root** stores desired state, secrets, generated configuration, app homes, certificates, logs, and on-host backups. It does not need to sit beside `/usr/local/bin/bento`. Put it on durable local storage.

The documentation uses `/var/lib/bento`. Create it for the account that will operate Bento:

```sh
sudo install -d -m 0750 -o "$USER" -g "$(id -gn)" /var/lib/bento
```

Verify access without creating a stack yet:

```sh
test -w /var/lib/bento && echo "stack root is writable"
```

:::caution
Do not place the stack root in `/tmp`, an ephemeral deployment directory, or the source checkout. Losing this directory can lose desired state, credentials, app files, SQLite databases, certificates, and on-host backups. Docker MySQL, PostgreSQL, and Redis data also require separate protection because they live in named volumes.
:::

Bento reads the stack root from `BENTO_STACK_ROOT`. If the variable is unset, it uses `./bento`.

Set the durable production root in your operator environment so commands do not depend on the current directory:

```sh
export BENTO_STACK_ROOT=/var/lib/bento
bento version
```

Add the export to the operator's shell profile or service environment if it should persist. The rest of these guides assume the variable is set.

Use `--stack PATH` when you deliberately need to target another stack for one command.

## Run the control plane as a container

Tagged releases also publish an optional `ghcr.io/khanhicetea/bento:<release-tag>` image containing the compiled Bento command, embedded web UI, Docker CLI, and Compose v2 plugin. This uses the host Docker daemon rather than Docker-in-Docker. Select a specific release tag rather than an unpinned moving tag.

```sh
sudo install -d -m 0750 /var/lib/bento
export BENTO_STACK_ROOT=/var/lib/bento
export BENTO_IMAGE=ghcr.io/khanhicetea/bento:<release-tag>
docker compose -f deploy/container/compose.yml pull
docker compose -f deploy/container/compose.yml run --rm bento init --name bento
docker compose -f deploy/container/compose.yml up -d
```

For a reviewed source checkout, build `bento:local` with `bun run container:build` and leave `BENTO_IMAGE` unset.

Open `http://127.0.0.1:8080`. The example deliberately publishes only on loopback because the management UI has no authentication.

:::danger
Mounting `/var/run/docker.sock` grants effective root control of the host. Anyone who can operate this Bento instance or access its management UI must be trusted as a host administrator.
:::

Use an absolute stack-root path and mount it at exactly the same path inside the control-plane container. For example, host `/var/lib/bento` must remain container `/var/lib/bento`. Compose resolves bind sources in the client container, while Docker Engine applies them on the host; changing the path can make sibling stack containers mount the wrong host directories.

The container does not manage the host crontab. Do not use `backup schedule register` inside it; configure a host scheduler to invoke a one-off Bento container or `docker compose exec` instead. Export/import destinations and other paths outside the stack root also need same-path bind mounts when those operations are used.

## Run from source instead

Source mode is for development or for a reviewed checkout when no compiled release is suitable. It requires Bun 1.4.0 and must be run from the repository checkout:

```sh
bun --version
bun run apps/cli/src/main.ts version
```

Use `bun install --frozen-lockfile` so dependency resolution matches the committed lockfile. Source mode and the compiled binary are tested to produce equivalent state transitions and generated files. Mutable data still belongs in the external stack root, not beside the source or binary.

To build a binary from a reviewed checkout, select the task for the target host:

```sh
bun run compile:amd64
bun run compile:arm64
```

These tasks write `dist/bento-linux-amd64` and `dist/bento-linux-arm64`. Install only the matching artifact with the same `install` command shown above.

## Troubleshooting

**`bento: command not found`:** confirm that `/usr/local/bin` is in the operator's `PATH`, or invoke `/usr/local/bin/bento version` directly.

**`Exec format error`:** the installed asset does not match the host architecture. Compare `uname -m` with the asset table and reinstall the correct file.

**The stack root is not writable:** correct its owner and mode for the Bento operator. Do not work around the problem by moving production state into a temporary directory.

**Source mode cannot find `package.json` or a task:** change to the repository root before running `bun run apps/cli/src/main.ts`, and confirm that the checkout contains `package.json` and `apps/cli/templates/`.

## Next steps

- [Create and validate your first stack](/start/first-stack/).
- [Review Bento's operating model and boundaries](/start/overview/).
- [Recheck host, Docker, port, and storage requirements](/start/requirements/).
