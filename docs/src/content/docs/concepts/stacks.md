---
title: Stacks, roots, and names
description: Understand how a stack root and stable stack name identify one Bento installation and its resources.
---

# Stacks, roots, and names

A Bento **stack** is one installation's desired state, generated configuration, app homes, certificates, backups, and Docker Compose resources. Two independent identifiers locate it: the stack root selects files on the host, while the stack name selects Compose resources.

## Mental model

| Identifier | Purpose | Example |
| --- | --- | --- |
| **Stack root** | Filesystem path targeted by `--stack` or `BENTO_STACK_ROOT` | `/var/lib/bento` |
| **Stack name** | Stable Compose project identity stored as `COMPOSE_PROJECT_NAME` | `production` |

They are deliberately independent:

```text
/var/lib/bento  --stack path--> host files
production      --name-------> production_private, production_mysql84-data, …
```

The directory basename does not become the stack name. This command creates a root at `/var/lib/bento` with the name `production`:

```sh
bento --stack /var/lib/bento init --name production
```

If `--name` is omitted, the name defaults to `bento`; it is still not inferred from the directory. Stack names use lowercase letters, digits, hyphens, and underscores and must start with a letter or digit.

## The stack root selects host data

Bento resolves the selected stack root to an absolute path and reads and writes all stack-local data beneath it. The production guides show `--stack` explicitly:

```sh
bento --stack /var/lib/bento status
```

Without `--stack` or `BENTO_STACK_ROOT`, Bento defaults to `./bento`. That relative default changes meaning with the current working directory, so it is unsafe as an implicit production target.

A stack root contains several ownership classes:

| Class | Examples | Treatment |
| --- | --- | --- |
| Operator intent and secrets | `state.json`, `.env` | Sensitive source of truth; normally change through Bento commands |
| Operator customization | `custom/`, `overlays/` | Preserve and review across upgrades |
| Durable runtime data | `homes/`, `certs/`, `backups/`, `logs/` | Back up according to recovery requirements |
| Generated output | `generated/`, `docker/`, `helpers/`, `.asset-cache/` | Reconstructible; do not customize generated copies |
| Ephemeral coordination | `runtime/`, `locks/` | Recreated as needed; not recovery data |

Docker named volumes for MySQL, PostgreSQL, and Redis are durable stack resources but do **not** live under the stack root. The stack name connects generated Compose configuration to those volumes.

:::caution
A filesystem backup of the stack root alone is not a complete database backup. It omits Docker named-volume contents. Use Bento's logical backup or stack export workflows and keep verified off-host copies.
:::

## The stack name selects Compose resources

The stack name is written to the private stack `.env` as `COMPOSE_PROJECT_NAME`. Bento uses it for the Compose project, private network, containers, and named volumes.

Treat the name as immutable after initialization. Bento refuses an initialization request that tries to replace an existing name because a different name would address differently prefixed resources. The original data volumes may still exist, but the renamed project would not automatically use them.

Two stack roots on the same Docker host must not share a stack name. If both are named `production`, their host-side locks and desired state are separate while their Compose resource identity collides.

:::caution
Copying a stack directory does not create an independent clone. The copy retains its stack name and secrets and can target the source stack's Docker resources. Use the guarded stack export/import workflow with a new name and non-conflicting ingress instead.
:::

## How this affects operations

### Target every important command

Use the same explicit root for status checks, changes, scheduled tasks, backups, and destructive operations:

```sh
bento --stack /var/lib/bento app show demo
bento --stack /var/lib/bento backup --app demo
```

For scripts, set `BENTO_STACK_ROOT` once or retain `--stack` in every invocation. Do not depend on a script's working directory.

### Verify both identifiers before a change

`status` reports the resolved root and stack name:

```sh
bento --stack /var/lib/bento status
```

The ingress view also reports the name and networking mode:

```sh
bento --stack /var/lib/bento stack ingress show
```

Check these values before an import, restore, app deletion, or multi-stack operation. A valid command aimed at the wrong root can still change the wrong stack.

### Keep the root durable and stable

Do not put a production root under `/tmp`, a release checkout, or another directory replaced during deployment. The executable can move or be upgraded independently; mutable stack data remains under the selected root.

Moving or restoring a root requires planning even though its name is independent. Host schedules can contain the old absolute path, running containers can have mounts from it, and a copied root is not a consistent copy of live database volumes.

## Boundaries and limitations

- Deleting a stack root does not necessarily delete its Docker named volumes, but it can remove the state and secrets needed to identify and operate them safely.
- `bento compose -- down` stops and removes containers and networks while preserving volumes. Bento blocks `down -v`, `--volumes`, and destructive `--rmi` forms.
- Generated files are not an alternative source of truth. Bento replaces them during render or apply.
- On-host files and volumes remain a single-host failure domain until you copy suitable backups off the machine.
- The stack name separates Compose resources; it is not a security boundary between mutually hostile operators.

## Advanced

Bento has no resident daemon that remembers a current stack. Each CLI invocation resolves its target from `--stack`, then loads the stack-local state and environment. Locks also live under that root. This makes stacks portable and explicit, but it cannot prevent two different roots with the same name from contending for the same Docker resources.

Compiled and source-mode Bento use the same external-root model. Immutable templates come from the binary or checkout, enter a digest-addressed `.asset-cache/`, and are published to stable `docker/` and `helpers/` paths for Compose. Replacing the executable does not relocate operator state.

The explicit Compose name also keeps resource identity stable when the root's directory basename differs across hosts. That stability is useful during a planned restore, but it is why casual copies and renames are unsafe.

## Next steps

- [Create and inspect a stack with explicit identifiers](/start/first-stack/).
- [Review installation and durable stack-root selection](/start/install/).
- [Review what Bento manages and what remains your responsibility](/start/overview/).
