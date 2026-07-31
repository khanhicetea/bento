---
title: Stacks, roots, and names
description: Learn how a stack root selects host files while a stack name selects Docker resources.
---

# Stacks, roots, and names

A Bento **stack** includes one installation's state, generated configuration, app homes, certificates, backups, and Docker Compose resources.

Two independent identifiers locate it. The **stack root** selects files on the host. The **stack name** selects Compose resources.

## Mental model

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/stack-root-vs-name.svg
Alt: A stack root pointing to host files and a separate stack name pointing to Docker networks, containers, and volumes.
Show: Two input boxes, /var/lib/bento and production. Route the first to filesystem paths and the second to production_private, containers, and named volumes. Add a warning showing that copying the root keeps the same Docker identity unless the import workflow changes it.
-->

| Identifier | Purpose | Example |
| --- | --- | --- |
| **Stack root** | Filesystem path from `BENTO_STACK_ROOT`, or the `./bento` default | `/var/lib/bento` |
| **Stack name** | Stable Compose project identity stored as `COMPOSE_PROJECT_NAME` | `production` |

They are deliberately independent:

```text
/var/lib/bento  --root path----> host files
production      --name-------> production_private, production_mysql84-data, …
```

The directory basename does not become the stack name. This command creates a root at `/var/lib/bento` with the name `production`:

```sh
bento init --name production
```

If `--name` is omitted, the name defaults to `bento`; it is still not inferred from the directory. Stack names use lowercase letters, digits, hyphens, and underscores and must start with a letter or digit.

## The stack root selects host data

Bento resolves the selected stack root to an absolute path and reads and writes all stack-local data beneath it. Set the production root once in the operator environment:

```sh
export BENTO_STACK_ROOT=/var/lib/bento
bento status
```

Without `BENTO_STACK_ROOT`, Bento uses `./bento`. This relative path changes with your current directory, so do not rely on it for production.

Use `--stack PATH` when you need to target another root for one command.

A stack root contains several ownership classes:

| Class | Examples | Treatment |
| --- | --- | --- |
| Operator intent and secrets | `state.json`, `.env` | Sensitive source of truth; normally change through Bento commands |
| Operator customization | `custom/`, `overlays/` | Preserve and review across upgrades |
| Durable runtime data | `homes/`, `sqlite/`, `certs/`, `backups/`, `logs/` | Back up according to recovery requirements |
| Generated output | `generated/`, `docker/`, `helpers/`, `.asset-cache/` | Reconstructible; do not customize generated copies |
| Ephemeral coordination | `runtime/`, `locks/` | Recreated as needed; not recovery data |

Docker stores durable MySQL, PostgreSQL, and Redis data in named volumes outside the stack root. SQLite databases live under the root's `sqlite/` directory.

The stack name links generated Compose configuration to named volumes and the remote SQLite replica prefix.

:::caution
A filesystem backup of the stack root alone is not a complete database backup. It omits Docker named-volume contents, and copying a live SQLite file does not guarantee consistency. Use Bento's relational logical backup or stack export workflows, configure SQLite continuous backup where needed, and keep verified off-host copies.
:::

## The stack name selects Compose resources

The stack name is written to the private stack `.env` as `COMPOSE_PROJECT_NAME`. Bento uses it for the Compose project, private network, containers, and named volumes.

Treat the stack name as permanent after initialization. Bento refuses to initialize an existing stack with another name because the new name would point to differently prefixed Docker resources.

The original volumes may still exist, but the renamed project would not use them automatically.

Two stack roots on the same Docker host must not share a stack name. If both are named `production`, their host-side locks and desired state are separate while their Compose resource identity collides.

:::caution
Copying a stack directory does not create an independent clone. The copy retains its stack name and secrets and can target the source stack's Docker resources. Use the guarded stack export/import workflow with a new name and non-conflicting ingress instead.
:::

## How this affects operations

### Target every important command

Use the same selected root for status checks, changes, scheduled tasks, backups, and destructive operations:

```sh
bento app show demo
bento backup --app demo
```

For scripts, set `BENTO_STACK_ROOT` once. Do not depend on a script's working directory.

### Verify both identifiers before a change

`status` reports the resolved root and stack name:

```sh
bento status
```

The ingress view also reports the name and networking mode:

```sh
bento stack ingress show
```

Check these values before an import, restore, app deletion, or multi-stack operation. A valid command aimed at the wrong root can still change the wrong stack.

### Keep the root durable and stable

Do not put a production root under `/tmp`, a release checkout, or another directory replaced during deployment. The executable can move or be upgraded independently; mutable stack data remains under the selected root.

Plan before you move or restore a stack root. Host schedules may contain the old absolute path, running containers may mount it, and a copied root does not contain a consistent copy of live database volumes.

## Boundaries and limitations

- Deleting a stack root does not necessarily delete its Docker named volumes, but it can remove the state and secrets needed to identify and operate them safely.
- `bento compose -- down` stops and removes containers and networks while preserving volumes. Bento blocks `down -v`, `--volumes`, and destructive `--rmi` forms.
- Generated files are not an alternative source of truth. Bento replaces them during render or apply.
- On-host files and volumes remain a single-host failure domain until you copy suitable backups off the machine.
- The stack name separates Compose resources; it is not a security boundary between mutually hostile operators.

## Advanced

Bento has no background daemon that remembers a current stack. Every command selects a root from `BENTO_STACK_ROOT`, the `./bento` default, or `--stack`. It then loads that root's state and environment. Locks also live under the root.

This design makes stack targeting explicit and portable. It cannot stop two different roots with the same stack name from competing for the same Docker resources.

Compiled and source-mode Bento use the same external-root model. Immutable templates come from the binary or checkout, enter a digest-addressed `.asset-cache/`, and are published to stable `docker/` and `helpers/` paths for Compose. Replacing the executable does not relocate operator state.

The explicit Compose name also keeps resource identity stable when the root's directory basename differs across hosts. That stability is useful during a planned restore, but it is why casual copies and renames are unsafe.

## Next steps

- [Create and inspect a stack with explicit identifiers](/start/first-stack/).
- [Review installation and durable stack-root selection](/start/install/).
- [Review what Bento manages and what remains your responsibility](/start/overview/).
