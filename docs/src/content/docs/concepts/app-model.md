---
title: Application identity and resources
description: Understand how one stable app slug connects runtime, traffic, data, jobs, and durable files in Bento.
---

# Application identity and resources

A Bento app is a stable logical identity, not only a website record. Its app slug connects the same codebase and operating identity to web requests, PHP commands, data access, background work, and deployment.

## Mental model

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

Each app selects one managed PHP version and one named FPM capacity profile. Bento renders a dedicated pool and Unix socket for the app, but apps on the same PHP version share the PHP image, FPM service, global process cap, and runner container. The profile controls this app's pool capacity; it does not reserve a separate container or fixed host resources.

Nginx resolves the app's primary and additional domain links to its document root and FPM socket. Domain links are stack-wide unique, including links used by reverse proxies. An app also selects:

- a document root relative to its code directory, commonly `public`;
- front-controller or legacy PHP routing;
- a TLS mode;
- whether to write a per-app access log.

Disabling an app removes its generated vhost, pool, schedules, and workers after apply. It retains the app model, domain claims, home, credentials, and database records. See [Manage applications](/guides/apps/manage/) for the enable, disable, remove, and prune lifecycle.

## Files and credentials

Bento creates the durable host directory `<stack-root>/homes/<app>/`. PHP roles see the same home at `/home/<app>`. Important locations include:

| Container path | Purpose |
| --- | --- |
| `/home/<app>/code/` | Application code and selected document root |
| `/home/<app>/logs/` | App schedule, worker, deploy, and PHP logs |
| `/home/<app>/credentials/app.env` | Mode-restricted database and Redis connection metadata |
| `/home/<app>/.ssh/` | Stable app deploy key and SSH state |
| `/home/<app>/.bento/` | Deploy hook, queue, and app runtime metadata outside the public document root |

The credential file supplies connection values; Bento does not automatically load it into a framework's configuration. Do not commit it or print its secrets. The authoritative `state.json` also contains app database passwords and may contain a deploy HMAC secret, so protect the [stack's desired state](/concepts/desired-state/).

## Data binding

An app owns a collection of database bindings and may mix engines. Each MySQL or PostgreSQL binding selects one managed service, receives a same-name user or role, and may own multiple recorded databases in its namespace: either `<app>` or `<app>_*`. Each `sqlite` binding receives a private local file under the stack-root `sqlite/` directory, weekly Supercronic `VACUUM`, and `.backup` logical backups. A `litestream` binding is also SQLite but explicitly opts into continuous S3 replication. Adding a binding preserves every existing binding.

Adding a different engine or service creates another binding; it does not move or convert an existing database. Bento does not move data between bindings.

Redis is shared stack infrastructure. In shared mode, every app must use its recorded key prefix. In ACL mode, Bento additionally gives the app a Redis username and credentials restricted to its namespace. Redis metadata does not create a separate Redis instance per app.

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
bento --stack /var/lib/bento app show demo
```

When operating the app, use its slug rather than manually selecting a container or UID. For example, this runs with the app's recorded PHP runtime, identity, home, and private network access:

```sh
bento --stack /var/lib/bento exec demo -- php -v
```

Re-running `app create` or using `app update` is the supported way to change an app's linked domains, document root, routing mode, PHP version, or FPM profile. Omitted PHP, profile, and database choices preserve the existing recorded selections; inspect the result after every update.

## Boundaries and limitations

The app model protects normal ownership boundaries and reduces accidental cross-app access, but it is not a hostile multi-tenant sandbox. Apps sharing a PHP version also share a container namespace, runtime image, network access, and capacity envelope. Use a separate host or stronger isolation system for mutually hostile tenants.

Bento also does not:

- rename app identities;
- reserve dedicated CPU or memory per app;
- automatically configure a framework from `credentials/app.env`;
- migrate an app between relational engines or services;
- replicate code, relational database data, or credentials off-host; optional [SQLite continuous backup](/guides/data/sqlite/) is the exception.

## Advanced

The app's stable UID/GID is used consistently by its PHP-FPM pool, ephemeral CLI role, scheduler, workers, and deploy hook. Nginx reads the public tree and reaches PHP through the app's Unix socket without receiving general write access to the private home.

Versioned PHP services have different cardinalities: one persistent FPM service and one singleton runner exist per managed PHP version, while CLI containers are ephemeral. The app-specific pool, socket, scheduler, and worker definitions live inside those shared roles. This gives Bento app-level operational identity without the resource cost or isolation guarantees of one complete container stack per app.

## Next steps

- [Create and verify your first app](/start/first-app/).
- [Manage an app's lifecycle and commands](/guides/apps/manage/).
- [Understand desired state and generated configuration](/concepts/desired-state/).
