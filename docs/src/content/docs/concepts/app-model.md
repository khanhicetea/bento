---
title: Application identity and resources
description: See how one app slug connects files, traffic, PHP, data, jobs, and deployment.
---

# Application identity and resources

A Bento app is more than a website record. Its stable slug connects one codebase and operating identity to web requests, PHP commands, data, background jobs, and deployments.

## Mental model

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/app-resource-map.svg
Alt: The demo app slug connected to its Linux identity, home, domains, PHP pool, databases, Redis namespace, jobs, and deploy queue.
Show: Put the app slug in the center. Group connected resources into five labeled areas: identity, traffic, runtime, data, and background work. Mark shared services, such as a PHP version and Redis, with a different color from app-owned resources.
-->

```text
app slug
  ├─ identity: UID/GID and private home
  ├─ traffic: domains, Nginx vhost, PHP pool and socket
  ├─ runtime: one PHP version and FPM capacity profile
  ├─ data: one database-engine binding and Redis metadata
  └─ work: schedules, workers and optional deploy queue
```

For an app named `demo`, Bento uses `demo` across several resources:

| Part | App-owned value or resource |
| --- | --- |
| Filesystem | `/home/demo` inside PHP containers and `<stack-root>/homes/demo` on the host |
| Process identity | One stable private UID/GID for FPM, CLI, schedules, workers, and deploy commands |
| PHP requests | A `demo` FPM pool and `demo.sock` under the app's selected PHP service |
| Domains | Independent link records: one primary plus any number of additional links, all unique across apps and reverse proxies |
| Database | One managed MySQL/PostgreSQL service binding or one private SQLite file |
| Redis | An app-specific key prefix, plus a per-app identity when the stack uses ACL mode |
| Background work | Cron jobs, workers, and deploy jobs scoped to `demo` |

The slug must be 2–32 characters, start with a lowercase letter, and contain only lowercase letters, digits, and hyphens.

:::caution
Treat the app slug as permanent. Changing `demo` would require coordinated migration of its Linux identity, home, PHP pool and socket, database identity, Redis namespace, jobs, and deployment state. Bento does not provide an app rename command.
:::

Changing a primary domain is different: it updates traffic ownership while preserving the app slug, UID/GID, home, and credentials.

## Runtime and traffic

Each app selects one managed PHP version and one FPM capacity profile. Bento gives the app its own pool and Unix socket.

Apps on the same PHP version still share the PHP image, FPM service, global process limit, and runner. The profile limits the app's pool; it does not reserve a container, CPU, or memory for that app.

Nginx resolves the app's primary and additional domain links to its document root and FPM socket. Domain links are stack-wide unique, including links used by reverse proxies. An app also selects:

- a document root relative to its code directory, commonly `public`;
- front-controller or legacy PHP routing;
- a TLS mode;
- whether to write a per-app access log.

After you disable and apply an app, Bento removes its generated virtual host, pool, schedules, and workers. Bento keeps the app record, domain claims, home, credentials, and database records.

See [Manage applications](/guides/apps/manage/) for the full enable, disable, remove, and prune lifecycle.

## Files and credentials

Bento creates the durable host directory `<stack-root>/homes/<app>/`. PHP roles see the same home at `/home/<app>`. Important locations include:

| Container path | Purpose |
| --- | --- |
| `/home/<app>/code/` | Application code and selected document root |
| `/home/<app>/logs/` | App schedule, worker, deploy, and PHP logs |
| `/home/<app>/credentials/app.env` | Mode-restricted database and Redis connection metadata |
| `/home/<app>/.ssh/` | Stable app deploy key and SSH state |
| `/home/<app>/.bento/` | Deploy hook, queue, and app runtime metadata outside the public document root |

The credential file provides connection values, but Bento does not load it into your framework automatically. Never commit the file or print its secrets.

`state.json` also contains app database passwords and may contain a deploy secret. Protect the [stack's desired state](/concepts/desired-state/).

## Data binding

An app can own several database bindings and mix engines:

- A MySQL or PostgreSQL binding selects one managed service. Bento creates a matching user or role. The app can own databases named `<app>` or `<app>_*`.
- A `sqlite` binding creates a private file under the stack root's `sqlite/` directory. Bento schedules a weekly `VACUUM` and uses SQLite's `.backup` command for logical backups.
- A `litestream` binding also uses SQLite and adds continuous replication to S3-compatible storage.

Adding a binding never removes an existing one.

Adding a different engine or service creates another binding; it does not move or convert an existing database. Bento does not move data between bindings.

All apps use the stack's shared Redis service. In shared mode, each app must use its recorded key prefix. In ACL mode, Bento also gives the app a Redis username and credentials limited to that namespace.

An app does not receive its own Redis instance.

## Schedules, workers, and deploys

Cron jobs and workers are separate desired-state records that refer back to one app. Bento runs them under that app's UID/GID, within its home, using the runner for its selected PHP version:

- a **scheduled job** is a timed command with its own schedule, workdir, output behavior, timeout, and optional lock;
- a **worker** is a named long-running command with scoped start, stop, restart, signal, and inspection controls;
- **webhook deploy** is optional and adds an authenticated app-specific queue whose trusted hook runs as the app user.

The generated deploy hook deliberately skips work until you replace it. Enabling deployment does not invent a Git or framework deployment workflow.

Removing an app from desired state also removes its cron and worker records, but retains durable app and database data for separate review. Permanent prune is a distinct destructive operation.

## How it affects operations

Inspect the app model without exposing its database, Redis, or deploy secrets:

```sh
bento app show demo
```

When operating the app, use its slug rather than manually selecting a container or UID. For example, this runs with the app's recorded PHP runtime, identity, home, and private network access:

```sh
bento exec demo -- php -v
```

Use `app create` again or run `app update` to change domains, the document root, routing, PHP, or the FPM profile. If you omit PHP, profile, or database options, Bento keeps the recorded choices.

Inspect the app after every update.

## Boundaries and limitations

The app model reduces accidental access between apps. It is not a hostile multi-tenant sandbox.

Apps on the same PHP version share a container namespace, runtime image, network access, and capacity. Put mutually hostile tenants on separate hosts or use a stronger isolation system.

Bento also does not:

- rename app identities;
- reserve dedicated CPU or memory per app;
- automatically configure a framework from `credentials/app.env`;
- migrate an app between relational engines or services;
- manage source-code replication, remote retention, or provider durability; scheduled logical backups can upload through an operator-configured rclone sidecar, and [SQLite continuous backup](/guides/data/sqlite/) uses separate S3-compatible storage.

## Advanced

Bento uses the app's stable UID/GID for its FPM pool, temporary CLI containers, scheduler, workers, and deploy hook. Nginx can read the public tree and use the app's Unix socket, but it cannot write freely to the private home.

Each managed PHP version has one persistent FPM service and one singleton runner. Bento creates CLI containers only when a command needs one.

App-specific pools, sockets, schedulers, and workers live inside those shared roles. This saves resources, but it does not provide the isolation of a complete container stack for every app.

## Next steps

- [Create and verify your first app](/start/first-app/).
- [Manage an app's lifecycle and commands](/guides/apps/manage/).
- [Understand desired state and generated configuration](/concepts/desired-state/).
