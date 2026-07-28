---
title: Manage a stack
description: Render and apply changes, inspect status and logs, and start or stop Bento services safely.
---

# Manage a stack

Use Bento's status, apply, and Compose commands to operate an existing stack without bypassing its generated file chain or volume safeguards.

## Before you begin

- Use an initialized stack that has already been rendered. See [Create your first stack](/start/first-stack/) for initial setup.
- Confirm the stack root before every operation. The examples target `/var/lib/bento`.
- Ensure the Docker daemon is available for service status, lifecycle, validation, and logs.

## Check the stack

Start each maintenance session with Bento's combined desired-state and runtime view:

```sh
bento --stack /var/lib/bento status
```

Check the reported stack root and stack name before changing anything. Review expected roles, database health, apps, ingress, generation time, warnings, and notes. A role marked `config-ready` has generated configuration but is not running; `apply` does not start it.

For automation, request the secret-redacted JSON report:

```sh
bento --stack /var/lib/bento --json status
```

Inspect the underlying Compose processes when you need container-level detail:

```sh
bento --stack /var/lib/bento compose -- ps
```

## Reconcile a change

Most Bento commands that change desired state apply automatically. If you used a command's `--no-apply` option, changed supported custom files, or need to reconcile the complete stack, preview the planned targets:

```sh
bento --stack /var/lib/bento apply --preview
```

Preview creates a candidate in memory and lists its files and reload plan. It does not write generated files, validate service configuration, or signal containers.

:::caution
An apply can briefly affect traffic or application processes while it reloads targeted roles. Plan to verify the affected application after runtime, routing, TLS, or custom configuration changes. Do not use `--skip-validate` to force a failed candidate into service during normal operations.
:::

Apply the current desired state:

```sh
bento --stack /var/lib/bento apply
```

Bento renders a complete generation, validates the relevant service configurations, and reloads targeted running services. Stopped roles read the generated configuration when you start them later.

Verify control-plane and container status again:

```sh
bento --stack /var/lib/bento status
bento --stack /var/lib/bento compose -- ps
```

For a web change, also make an HTTP request with the application's expected host name. For example, from the server:

```sh
curl -I -H 'Host: demo.example.com' http://127.0.0.1/
```

## Start, stop, and restart services

Use Bento's Compose wrapper rather than invoking `docker compose` with a hand-built file list. The wrapper renders the current desired state and includes generated fragments plus operator overlays in deterministic order.

:::caution
In host ingress mode, starting Nginx claims host ports 80 and 443 and can expose the configured sites publicly. Confirm that those ports belong to this stack. `up -d --build` can also recreate containers; verify service readiness after it completes.
:::

Start all existing stack containers:

```sh
bento --stack /var/lib/bento compose -- start
```

If containers have not been created, or an image or Compose definition changed, reconcile them in the background instead:

```sh
bento --stack /var/lib/bento compose -- up -d --build
```

Stop all stack services while preserving their containers and durable volumes:

```sh
bento --stack /var/lib/bento compose -- stop
```

Start or stop one role by its Compose service name when you need a narrower outage:

```sh
bento --stack /var/lib/bento compose -- stop php85-runner
bento --stack /var/lib/bento compose -- start php85-runner
```

Use the service names reported by `status`, `compose -- ps`, or the merged Compose configuration. A broad restart interrupts every selected role and is normally unnecessary after `apply`; if a process must be recreated, target it explicitly:

```sh
bento --stack /var/lib/bento compose -- restart nginx
```

:::danger
Never bypass Bento to run `docker compose down -v`. Removing volumes destroys durable MySQL, PostgreSQL, and Redis data. Bento blocks `down` combined with `-v`, `--volumes`, or `--rmi`.
:::

Plain `down` is allowed, but it stops the whole stack and removes its containers and network. Named data volumes remain. Prefer `stop` for a temporary shutdown; use `down` only when you intend to recreate the Compose resources.

## Inspect configuration and logs

List the exact Compose files and overlay order selected by Bento:

```sh
bento --stack /var/lib/bento compose files
```

Ask Compose to validate and print the merged model:

```sh
bento --stack /var/lib/bento compose -- config
```

Files below `/var/lib/bento/generated/` are disposable outputs. Do not edit them; change desired state through Bento or use a [supported operator-owned input](/concepts/desired-state/#generated-files-are-outputs-not-customization-points).

Follow logs for the complete stack:

```sh
bento --stack /var/lib/bento compose -- logs --tail 100 --follow
```

Or limit output to one service:

```sh
bento --stack /var/lib/bento compose -- logs --tail 100 nginx
```

Press `Ctrl+C` to stop following logs; this does not stop the service. Docker's service logs are separate from application files under each app's `logs/` directory and Nginx files under the stack's `logs/nginx/` directory.

## Troubleshooting

**`status` reports `unknown`:** Docker process observation was unavailable. Confirm the daemon is running and that your user can run Docker, then retry `compose -- ps` and `status`.

**A role is `config-ready`:** its configuration exists, but its container is not running. Inspect `compose -- ps -a` and recent service logs, then use `compose -- start <service>` or `compose -- up -d <service>` as appropriate.

**`apply` fails validation:** read the named validator error and inspect recent desired-state or custom-file changes. Bento restores the previous generated configuration after a validation failure. Correct the input and retry; do not edit `generated/` or add `--skip-validate` as a workaround.

**`apply` says the reload signal failed:** the validated new generation remains live. Check the affected service with `compose -- ps` and `compose -- logs`, restore service health, and retry `apply`.

**A service repeatedly restarts:** inspect its first error with `compose -- logs --tail 100 <service>`. Also run the broader checks:

```sh
bento --stack /var/lib/bento doctor
```

`doctor` exits nonzero when a check fails. Resolve host ports, storage, permissions, configuration, or service health findings before repeating a broad restart.

## Advanced

The Compose wrapper does more than prepend `docker compose`: before each invocation it materializes bundled Docker assets, performs a render-only generation, and constructs absolute `-f` arguments for every managed fragment and lexically ordered overlay. `bento compose files` shows that order without running Compose; `bento compose --print -- <arguments>` prints the full command instead of executing it.

Render-only generation does not validate or signal running services. Use `apply` to activate a configuration change safely; use the wrapper for explicit container lifecycle and inspection operations. The distinction and transaction recovery behavior are described in [Desired state and generated configuration](/concepts/desired-state/).

Bento's wrapper protects its supported path, not arbitrary direct Docker commands. Operators with Docker access remain able to remove containers or volumes outside Bento, so Docker access is part of the stack's trust boundary.

## Next steps

- [Understand desired state, generated files, and apply recovery](/concepts/desired-state/).
- [Understand stack identity and durable resource boundaries](/concepts/stacks/).
- [Manage an application's code and data path](/start/first-app/).
