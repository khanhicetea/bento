---
title: What is Bento?
description: Decide whether Bento fits your PHP applications, Linux host, and operating model.
---

# What is Bento?

Bento is a host-local command-line control plane for running multiple PHP applications and reverse-proxied HTTP services on one Linux server. Use this page to decide whether its single-host, Docker Compose operating model fits your workload.

## When Bento fits

Bento is designed for a technically capable developer or small team that:

- owns and administers one Linux VPS or dedicated server;
- runs several PHP applications, such as Laravel, Symfony, WordPress, or legacy PHP;
- wants Nginx, PHP, relational databases, Redis, jobs, workers, TLS, and backups managed as one stack;
- accepts a command-line workflow rather than a browser control panel;
- wants reproducible configuration without adopting Kubernetes or a cloud platform.

Bento can also place Nginx and TLS in front of non-PHP HTTP services that are reachable from the host. It does not run arbitrary application runtimes for those services; it acts as their reverse proxy.

## What Bento manages

An **app** is Bento's primary unit of ownership. Each app has a stable identity that connects its home directory, Linux user and group, PHP version and pool, domains, database binding, Redis metadata, scheduled jobs, workers, and deploy settings.

At the stack level, Bento manages:

- one Nginx ingress, which is the only public service in the base topology;
- shared, versioned PHP services with a separate pool and Unix socket for each app;
- private MySQL or PostgreSQL services and Redis;
- generated Docker Compose and service configuration;
- logical database backups, diagnostics, and guarded operational commands.

You express the intended configuration as desired state. The `bento` command renders that state into generated files and operates the resulting Docker Compose services. There is no resident Bento daemon.

```text
Operator -> bento CLI -> desired state -> generated configuration -> Docker Compose
Internet -> Nginx -> app PHP-FPM socket or reverse-proxy upstream
                       -> private MySQL, PostgreSQL, and Redis services
```

## Operating boundaries

Bento deliberately optimizes for a comprehensible single-server platform. It does **not** provide:

- multi-host scheduling, clustering, high availability, or horizontal autoscaling;
- Kubernetes integration, a remote control plane, a browser administration UI, or a public management API;
- one container per app or hard isolation between mutually untrusted tenants;
- general-purpose hosting for non-PHP runtimes beyond reverse proxying to an existing service;
- an application-specific Git checkout, release, or rollback strategy;
- managed off-host storage, retention, or recovery guarantees (scheduled uploads require an operator-configured rclone remote);
- per-app CPU or memory quotas inside shared PHP containers.

The app boundaries—Linux identities, filesystem permissions, PHP pools, database grants, and optional Redis ACLs—reduce accidental cross-app access. They are not a hostile multi-tenant sandbox. Use separate hosts or stronger isolation when app operators or code do not trust one another.

## What you remain responsible for

You own the host, stack state, application files, database volumes, certificates, and backups. You are also responsible for:

- securing and updating the Linux host and Docker Engine;
- configuring DNS and any upstream firewall or network rules;
- supplying and operating application code;
- copying backups off the host and testing recovery;
- monitoring capacity and planning downtime or migration beyond one server.

## Next steps

- [Return to the documentation home](/)
- [Prepare a supported Linux host](/start/requirements/)
- [Install the compiled command or use source mode](/start/install/)
