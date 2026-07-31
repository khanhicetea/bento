---
title: Manage applications
description: Inspect and update an app, run commands, pause traffic, or remove data safely.
---

# Manage applications

Inspect and update an app, or run commands as its user. This guide also separates reversible changes—such as disabling an app—from permanent data deletion.

## Before you begin

- Use an initialized stack and confirm its root. The examples target `/var/lib/bento` and app `demo`.
- Complete [Add your first application](/start/first-app/) if the stack has no app yet.
- Ensure Docker is available for apply, shell, exec, and database cleanup operations.
- Back up app code and databases before removing or pruning an app.

## Inspect applications

List the apps in desired state:

```sh
bento app list
```

The table shows each app's enabled state, numeric identity, primary domain, PHP version, FPM profile, TLS mode, and database binding.

Show one app's complete model with database, Redis, and deploy secrets redacted:

```sh
bento app show demo
```

Use the stack-wide view to correlate the app with domains, service health, capacity warnings, and generated roles:

```sh
bento status
```

## Update an app

Inspect the current values before updating. `app update` requires the primary domain and treats the alias list as the complete replacement list.

:::caution
Updating runtime, document-root, routing, domain, or FPM settings can briefly interrupt requests while affected services reload. Plan to verify the app after the command. Bento does not automatically move an existing app between database engines or managed database services.
:::

For example, replace `demo`'s aliases while retaining its primary domain:

```sh
bento app update demo \
  --domain demo.example.com \
  --alias www.demo.example.com,admin.demo.example.com
```

Bento preserves the app slug, UID/GID, home, credentials, database binding, deploy settings, and template selections. It renders and applies the change unless you add `--no-apply`. A selected PHP version must already be managed by the stack.

Important update behavior:

- repeat every alias you want to keep; omitting `--alias` clears the alias list;
- repeat `--access-log` if per-app access logging is currently enabled and should remain enabled;
- use `--front` or `--legacy` to change routing mode; omitting both preserves the current mode;
- use `--db --database <name>` only when intentionally creating another database in the app's namespace;
- omit database selection options to preserve the existing database binding.

Verify the recorded result and HTTP route:

```sh
bento app show demo
curl -I -H 'Host: demo.example.com' http://127.0.0.1/
```

If you batch changes with `--no-apply`, activate them afterward:

```sh
bento apply
```

Until apply succeeds, desired state and the running configuration can differ.

## Disable and enable an app

Disable an app when you want Bento to stop serving and supervising it without deleting its model or durable data:

:::caution
Disabling removes the app's generated vhost, PHP pool, scheduler, and worker configuration. Requests stop working and background jobs stop after apply, while the app remains in desired state and retains its home, credentials, database records, and domain ownership.
:::

```sh
bento app disable demo
```

Confirm that `app list` marks it disabled and that its vhost and runtime entries are no longer active:

```sh
bento app list
bento status
```

Enable and reapply its runtime configuration later:

```sh
bento app enable demo
```

Verify the app route after enabling it. Both commands accept `--no-apply`; if you use that option, run `bento apply` before expecting runtime behavior to change.

## Open a shell or run a command

Open an ephemeral Bash shell using the app's configured PHP version and numeric identity:

```sh
bento app shell demo
```

The shell container is removed when you exit. Its default working directory is `/home/demo`; app files remain durable because the stack home is mounted into the container.

Run one noninteractive command with arguments after `--`:

```sh
bento exec demo -- php -v
```

:::caution
Application commands and migrations can change durable files or database contents even though the CLI container itself is ephemeral. Review the command and take an appropriate backup first.
:::

Run a framework command from the code directory:

```sh
bento exec demo \
  --workdir /home/demo/code -- php artisan migrate --force
```

Use `--php <version>` only for a deliberate one-off run under another PHP version already managed by the stack. It does not update the app's configured runtime. `--workdir` must stay inside the app home.

To inspect the Compose invocation without running it:

```sh
bento exec demo --print -- php -v
```

## Remove desired state but retain data

Removing an app is different from pruning it. Removal deletes the app, its domain claims, cron jobs, and workers from desired state and removes its generated runtime configuration. It intentionally retains the app home, databases, and database account for review or manual recovery.

:::caution
The next command takes the app out of service and releases its domains for reuse. Confirm backups and inspect `app show demo` before proceeding. The exact confirmation is `delete demo`; this step does not delete the retained home or database data.
:::

```sh
bento app remove demo --confirm 'delete demo'
```

`app delete` is the same operation. Do not add `--no-apply` unless you intentionally want stale generated runtime configuration to remain until a later apply.

Verify that the app is absent from desired state while its home remains on disk:

```sh
bento app list
sudo test -d /var/lib/bento/homes/demo && echo 'retained home exists'
```

The retained home contains `.bento/prune.json`, a restricted cleanup manifest that records the database service, account or role, and app databases known at removal time. Do not edit this file.

## Permanently prune retained data

Prune only after the app has been removed from desired state and its retained data is no longer needed.

:::danger
`app prune` permanently drops every recorded app database, drops the app's MySQL account or PostgreSQL role, and recursively deletes the retained app home. This cannot be undone by Bento. Verify off-host code and database backups before continuing.
:::

Ensure the recorded database service is running, then invoke the interactive command from a terminal:

```sh
bento app prune demo
```

Bento prints every known database, the database account or role, and the app-home path before changing data. Review the list. To proceed, type exactly:

```text
delete
```

There is no confirmation-bypass flag. Bento rechecks the plan under an exclusive lock after you respond and refuses cleanup if the retained-data plan changed.

A successful prune reports each cleaned database, account or role, and home. Verify that no retained home remains:

```sh
sudo test ! -e /var/lib/bento/homes/demo && echo 'retained home removed'
```

If cleanup metadata is missing, Bento warns that it cannot identify database data and offers to delete only the home. Investigate databases manually before accepting; do not assume missing metadata means no database exists.

## Troubleshooting

**An update reports a domain conflict:** inspect `status` to find the current owner. Every primary domain and alias must be unique across apps and reverse proxies.

**A PHP update says the version is not managed:** add the required runtime first or keep the current `--php` value. Do not hand-edit the app's service identity.

**Shell or exec cannot create the CLI container:** inspect the matching PHP image and Compose output. Use `--print` to verify the selected profile, service, workdir, and arguments, then check Docker with:

```sh
bento compose -- ps
```

**A disabled app still responds:** confirm that the disable command applied successfully. Run `bento apply`, inspect the Nginx configuration error if it fails, and retry the request with the exact host name.

**Removal rejects the confirmation:** pass the exact case-sensitive text `--confirm 'delete demo'`. Removal has no generic yes flag.

**Prune refuses an active app:** remove it from desired state first. Review the retained-data list in the separate prune command rather than combining both operations.

**Prune cannot reach MySQL or PostgreSQL:** start the recorded database service and retry. Bento does not remove the app home when database cleanup reports failure. A failed multi-database cleanup may still have completed earlier database statements, so inspect the listed databases before retrying.

## Advanced

An enabled app contributes an Nginx vhost, a pool in its shared versioned PHP-FPM service, and any scheduler or worker definitions in that PHP version's singleton runner. Disable and enable therefore target Nginx, the app's PHP-FPM role, and its runner without deleting the durable ownership layer.

`app shell` and `bento exec` use the profile-gated `<php-service>-cli` Compose role. The container starts with enough privilege to install the app's passwd/group identity, then drops to the app UID/GID before running Bash or the requested argv. The app shares its home and private stack network with this ephemeral role; this is convenient operational identity, not a hostile multi-tenant sandbox.

Removal writes only non-secret cleanup metadata to the retained home before deleting desired state. Prune validates that the manifest belongs to the requested slug, references a currently managed database service, and contains only databases in the app namespace. For PostgreSQL, it terminates sessions to each recorded database before dropping it and then drops the role. These checks narrow accidental scope but do not replace backups.

## Next steps

- [Manage stack status, logs, and service lifecycle](/guides/stacks/manage/).
- [Understand desired state and generated configuration](/concepts/desired-state/).
- [Review the app's initial code, database, DNS, and TLS path](/start/first-app/).
