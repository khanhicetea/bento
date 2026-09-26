---
title: Application identity and resources
description: See how one app slug connects files, traffic, runtime, data, jobs, and deployment.
---

# Application identity and resources

A Bento app is more than a website record. Its stable slug connects one codebase and operating identity to web requests, PHP or process commands, data, and supported background work.

## Mental model

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/app-resource-map.svg
Alt: The demo app slug connected to its Linux identity, home, domains, PHP pool, databases, Redis namespace, jobs, and deploy queue.
Show: Put the app slug in the center. Group connected resources into five labeled areas: identity, traffic, runtime, data, and background work. Mark shared services, such as a PHP version and Redis, with a different color from app-owned resources.
-->

```text
app slug
  ├─ identity: UID/GID and private home
  ├─ traffic: domains, Nginx vhost and app Unix socket
  ├─ runtime: PHP pool or dedicated Node.js/Bun/Python process
  ├─ data: one or more database bindings and Redis metadata
  └─ work: runtime-specific schedules, workers and deploy support
```

For an app named `demo`, Bento uses `demo` across several resources:

| Part             | App-owned value or resource                                                                                           |
| ---------------- | --------------------------------------------------------------------------------------------------------------------- |
| Filesystem       | `/home/demo` inside its runtime and `<stack-root>/homes/demo` on the host                                             |
| Process identity | One stable private UID/GID for web and CLI commands, plus supported jobs/deploys                                      |
| Web requests     | A private PHP-FPM socket or process-app Unix socket owned by the app route                                            |
| Domains          | Independent link records: one primary plus any number of additional links, all unique across apps and reverse proxies |
| Database         | One managed MySQL/PostgreSQL service binding or one private SQLite file                                               |
| Redis            | An app-specific key prefix, plus a per-app identity when the stack uses ACL mode                                      |
| Background work  | PHP cron/worker/deploy jobs scoped to `demo`; process jobs currently fail closed                                      |

The slug must be 2–32 characters, start with a lowercase letter, and contain only lowercase letters, digits, and hyphens.

:::caution
Treat the app slug as permanent. Changing `demo` would require coordinated migration of its Linux identity, home, PHP pool and socket, database identity, Redis namespace, jobs, and deployment state. Bento does not provide an app rename command.
:::

Changing a primary domain is different: it updates traffic ownership while preserving the app slug, UID/GID, home, and credentials.

## Runtime and traffic

A PHP app selects one managed PHP version and FPM capacity profile. Bento gives it a pool and Unix socket inside the shared version service. Apps on the same PHP version share the image, FPM service, process limit, and runner.

A process app selects Node.js, Bun, or Python, an exact version, explicit argv/workdir/private port, and optional health path. It gets one dedicated private service plus a same-image ephemeral CLI role. Nginx reaches it through an app-specific Unix socket; the service publishes no host port.

Nginx resolves every app's primary and additional domain links to the appropriate runtime socket. Domain links are stack-wide unique, including links used by reverse proxies. An app also selects:

- a document root relative to its code directory, commonly `public`;
- front-controller or legacy PHP routing;
- a TLS mode;
- whether to write a per-app access log.

After you disable and apply an app, Bento removes its generated virtual host and runtime exposure. PHP pools/jobs are reconciled; a process container is stopped. Bento keeps the app record, domain claims, home, credentials, and database records.

See [Manage applications](/guides/apps/manage/) for the full enable, disable, remove, and prune lifecycle.

## Files and credentials

Bento creates the durable host directory `<stack-root>/homes/<app>/`. The selected PHP roles or dedicated process service see it at `/home/<app>`. Important locations include:

| Container path                    | Purpose                                                                       |
| --------------------------------- | ----------------------------------------------------------------------------- |
| `/home/<app>/code/`               | Application code and selected document root                                   |
| `/home/<app>/logs/`               | App schedule, worker, deploy, and PHP logs                                    |
| `/home/<app>/credentials/app.env` | Mode-restricted database and Redis connection metadata                        |
| `/home/<app>/.ssh/`               | Stable app deploy key and SSH state                                           |
| `/home/<app>/.bento/`             | Deploy hook, queue, and app runtime metadata outside the public document root |

The credential file provides connection values. PHP frameworks remain operator-configured. A process entrypoint exports the generated values to its app command without putting secret values in Compose or host argv. Never commit the file or print its secrets.

`state.db` also contains app database passwords and may contain a deploy secret. Protect the [stack's desired state](/concepts/desired-state/).

## Data binding

An app can own several database bindings and mix engines:

- A MySQL or PostgreSQL binding selects one managed service. Bento creates a matching user or role. The app can own databases named `<app>` or `<app>_*`.
- A `sqlite` binding creates a private file under the stack root's `sqlite/` directory. Bento schedules a weekly `VACUUM` and uses SQLite's `.backup` command for logical backups.

Adding a binding never removes an existing one.

Adding a different engine or service creates another binding; it does not move or convert an existing database. Bento does not move data between bindings.

All apps use the stack's shared Redis service. In shared mode, each app must use its recorded key prefix. In ACL mode, Bento also gives the app a Redis username and credentials limited to that namespace.

An app does not receive its own Redis instance.

## Schedules, workers, and deploys

For PHP apps, schedules and workers live in the app-owned minicrond registry under the app UID/GID in the selected PHP runner. Manage them with `bento app minicrond <slug> -- <args>` or the protected scheduler UI. Bento does not persist a second copy of these definitions. **Webhook deploy** is optional and adds an authenticated app-specific queue whose trusted hook runs as the app user.

The generated deploy hook deliberately skips work until you replace it. Enabling deployment does not invent a Git or framework workflow. Process-app schedules, workers, and signed webhook deploy are currently unsupported and are rejected rather than routed through PHP.

Removing an app from Bento desired state retains its app home, including the minicrond registry, and database data for separate review. Permanent prune is a distinct destructive operation.

## How it affects operations

Inspect the app model without exposing its database, Redis, or deploy secrets:

```sh
bento app show demo
```

When operating the app, use its slug rather than manually selecting a container or UID. For example, this runs with the app's recorded PHP or process runtime, identity, home, and private network access:

```sh
bento exec demo -- php -v
```

Use `app update` to change domains and runtime-specific fields. Process updates repeat `--runtime node|bun|python`; runtime kind cannot change in place. Omitted database options keep recorded bindings.

Inspect the app after every update.

## Boundaries and limitations

The app model reduces accidental access between apps. It is not a hostile multi-tenant sandbox.

PHP apps on the same version share a container namespace, image, network access, and capacity. Process apps mount only their own homes but still share the host kernel and private backend network. Put mutually hostile tenants on separate hosts or use stronger isolation.

Bento also does not:

- rename app identities;
- reserve dedicated CPU or memory per app;
- automatically configure a framework from `credentials/app.env`;
- migrate an app between relational engines or services;
- manage source-code replication, remote retention, or provider durability; scheduled logical backups can upload through an operator-configured rclone sidecar, and [SQLite continuous backup](/guides/data/sqlite/) uses separate S3-compatible storage.

## Advanced

Bento uses the app's stable UID/GID for its FPM pool, temporary CLI containers, scheduler, workers, and deploy hook. Nginx can read the public tree and use the app's Unix socket, but it cannot write freely to the private home.

Each managed PHP version has one persistent FPM service and singleton runner; app pools, sockets, and per-app minicrond daemons live in those shared roles. Each process app instead has one dedicated service and profile-gated CLI role with app-only mounts. Neither design provides VM-grade isolation.

## Next steps

- [Create and verify your first app](/start/first-app/).
- [Manage an app's lifecycle and commands](/guides/apps/manage/).
- [Run Node.js, Bun, and Python projects](/guides/apps/process-runtimes/).
- [Understand desired state and generated configuration](/concepts/desired-state/).
