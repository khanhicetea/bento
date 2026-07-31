---
title: What is Bento?
description: Decide whether Bento's single-host model fits your apps and operating needs.
---

# What is Bento?

Bento runs several PHP apps and reverse-proxied HTTP services on one Linux server. You manage it with a local command-line tool, and it operates the services through Docker Compose.

Use this page to decide whether that model fits your workload.

## When Bento fits

Bento is designed for a technically capable developer or small team that:

- owns and administers one Linux VPS or dedicated server;
- runs several PHP applications, such as Laravel, Symfony, WordPress, or legacy PHP;
- wants Nginx, PHP, relational databases, Redis, jobs, workers, TLS, and backups managed as one stack;
- accepts a command-line workflow rather than a browser control panel;
- wants reproducible configuration without adopting Kubernetes or a cloud platform.

Bento can also place Nginx and TLS in front of non-PHP HTTP services that are reachable from the host. It does not run arbitrary application runtimes for those services; it acts as their reverse proxy.

## What Bento manages

An **app** is Bento's main unit of ownership. Each app has a stable identity that links its home, Linux user and group, PHP runtime, domains, data access, background jobs, and deploy settings.

At the stack level, Bento manages:

- one Nginx ingress, which is the only public service in the base topology;
- shared, versioned PHP services with a separate pool and Unix socket for each app;
- private MySQL or PostgreSQL services and Redis;
- generated Docker Compose and service configuration;
- logical database backups, diagnostics, and guarded operational commands.

You record the configuration you want as desired state. The `bento` command turns that state into generated files and operates the Docker Compose services. Bento does not run a background daemon.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/what-bento-manages.svg
Alt: Responsibilities split between Bento and the operator on one Linux host.
Show: Two columns. Under Bento, include generated configuration, Nginx, PHP roles, managed data services, jobs, and guarded operations. Under Operator, include host security, DNS, app code, off-host backups, monitoring, and capacity. Put shared responsibility for recovery testing between the columns.
-->

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

Linux identities, file permissions, PHP pools, database grants, and optional Redis ACLs reduce accidental access between apps. They do not create a hostile multi-tenant sandbox.

Use separate hosts or stronger isolation when app operators or code do not trust one another.

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
