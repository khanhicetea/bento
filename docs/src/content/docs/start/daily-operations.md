---
title: Daily operations
description: Check health, inspect logs, run app commands, back up data, apply changes, and collect diagnostics.
---

# Daily operations

Use this runbook to check an existing Bento stack, investigate routine problems, operate an app, and make recoverable changes. The examples assume `BENTO_STACK_ROOT=/var/lib/bento` and use the app `demo`.

## Before you begin

- Complete [Create your first stack](/start/first-stack/) and [Add your first application](/start/first-app/).
- Confirm that the Docker daemon is available and your user can access it.
- Keep `BENTO_STACK_ROOT=/var/lib/bento` throughout the maintenance session.
- Know the app slug and domain you intend to operate.

## Start with status

Check Bento's desired-state and runtime summary before changing anything:

```sh
bento status
```

Confirm the displayed stack root and stack name. Review service health, apps, domains, ingress, warnings, and notes. A role reported as `config-ready` has generated configuration but is not running.

Inspect the underlying containers when a service is unhealthy or still starting:

```sh
bento compose -- ps
```

For monitoring scripts, `--json` provides a secret-redacted status document:

```sh
bento --json status
```

## Inspect logs

Follow recent logs for all Compose services:

```sh
bento compose -- logs --tail 100 --follow
```

Press `Ctrl+C` to stop following; this does not stop any service. Limit the output when one role is implicated:

```sh
bento compose -- logs --tail 100 nginx
```

Use the service name shown by `status` or `compose -- ps`, such as `nginx`, `php85`, `php85-runner`, `mysql84`, or `redis`. Application files also have logs under `/var/lib/bento/homes/<app>/logs/`; Nginx files are under `/var/lib/bento/logs/nginx/`.

## Run an application command

:::caution
The CLI container is ephemeral, but the command can change durable app files or database data. Back up the affected data before migrations, imports, or cleanup commands.
:::

Open an ephemeral shell under the app's configured PHP version and UID/GID:

```sh
bento app shell demo
```

Run a noninteractive command from the app's code directory with arguments after `--`:

```sh
bento exec demo \
  --workdir /home/demo/code -- php artisan queue:restart
```

Use `app show demo` to confirm the selected runtime and data binding. For shell options, one-off PHP overrides, and command inspection, see [Manage applications](/guides/apps/manage/#open-a-shell-or-run-a-command).

## Back up application databases

:::caution
Bento writes dumps only to this host under `/var/lib/bento/backups/`. An on-host dump does not protect against loss of the server or its storage, and it does not include app homes, Redis data, certificates, desired state, or stack secrets.
:::

Create a logical dump of every database recorded for `demo`:

```sh
bento backup --app demo
```

Bento reports each completed path and byte size. Copy dumps to an encrypted off-host destination and monitor that copy separately.

Follow [Back up and restore databases](/guides/data/backup-restore/) to schedule dumps, verify a restore under a separate database name, or replace a database safely.

## Apply routine changes

Inspect an app before updating its desired state:

```sh
bento app show demo
```

Most Bento commands that change desired state apply automatically. If you used `--no-apply`, changed a supported custom file, or need to reconcile the stack, preview the pending reload targets:

```sh
bento apply --preview
```

Preview does not write generated files, validate service configuration, or signal containers. Activate the current desired state with validation:

:::caution
Apply can briefly affect traffic or application processes while targeted services reload. Do not use `--skip-validate` to force an invalid candidate into service during routine operations.
:::

```sh
bento apply
```

Apply does not start stopped services. If a role remains `config-ready`, inspect it and start its existing container explicitly:

```sh
bento compose -- start <service>
```

Do not edit files under `generated/`; Bento replaces them. See [Desired state and generated configuration](/concepts/desired-state/) and [Manage a stack](/guides/stacks/manage/) before changing container lifecycle or custom configuration.

## Verify after a change

Repeat the status checks:

```sh
bento status
bento compose -- ps
```

Verify the app route from the host with its expected host name:

```sh
curl -I -H 'Host: demo.example.com' http://127.0.0.1/
```

Then check the affected service logs and an application-specific function, such as a read-only page or health endpoint. A zero exit status from `apply` proves validation and reload handling completed; it does not prove every application workflow is healthy.

## Diagnose a problem

Run Bento's broader checks:

```sh
bento doctor
```

`doctor` checks host tools, versions, network and ports, storage, TLS, services, permissions, volumes, overlays, and secret modes. It exits nonzero when any check fails. Resolve failed checks first; review warnings for conditions that may be intentional or transitional.

Create a redacted diagnostic archive when you need to preserve or share a snapshot:

```sh
bento support-bundle \
  /var/lib/bento/support/incident.tar.gz
```

:::caution
Bento redacts known credential fields and writes the archive with private permissions, but the bundle still contains stack topology, domains, host details, desired-state metadata, and service output. Review its contents and use a protected transfer channel before sharing it.
:::

## Troubleshooting

**`status` reports a role as `unknown`:** Docker observation was unavailable. Confirm the daemon is running and that your user can run Docker, then retry `compose -- ps` and `status`.

**A container repeatedly restarts:** inspect its earliest recent error with `compose -- logs --tail 100 <service>`. Correct the reported configuration, permission, port, storage, or dependency failure before restarting it.

**`apply` fails validation:** correct the desired-state or supported custom input named by the error and retry. Bento restores the previous generated configuration after validation failure; do not edit `generated/` or bypass validation.

**The HTTP check reaches the wrong site or cannot connect:** confirm the exact `Host` value, app domain, app enabled state, Nginx status, and effective ingress ports. Host and bridge ingress use different host endpoints.

**A backup fails:** confirm the app's recorded database service is healthy, inspect free disk space and database logs, and retry. Bento does not publish an empty final dump.

## Advanced

For unattended checks, use `bento --json status` and `--json doctor`; both redact known secrets. Alert on command failure and on unhealthy service fields rather than matching the formatted human output.

The Compose wrapper materializes Bento's bundled assets, renders current configuration, and selects every managed Compose fragment and operator overlay before invoking Docker Compose. Use it instead of assembling `docker compose -f` arguments manually. Bento blocks `down` with volume or image-removal flags on this path because those options can destroy durable data; Docker access itself remains a trusted operator capability.

`support-bundle` includes redacted doctor and status reports, redacted desired state and environment data, Docker information, Compose process status, and host system output. It does not replace a database or stack backup.

## Next steps

- [Manage stack lifecycle, configuration, and logs](/guides/stacks/manage/).
- [Manage app updates, availability, shell access, and removal](/guides/apps/manage/).
- [Back up and verify database restores](/guides/data/backup-restore/).
