---
title: Limitations
description: Find Bento non-goals and unsupported or deliberately guarded workflows.
---

# Limitations

Bento targets several PHP applications on one operator-owned Linux host. The following boundaries are deliberate.

## Platform and availability

- No multi-host scheduling, clustering, high availability, autoscaling, or Kubernetes.
- No browser administration UI, public management API, remote control plane, or resident Bento daemon.
- Nginx is the only supported public service; arbitrary languages are reverse-proxy targets, not managed app runtimes.
- Bento does not promise zero-downtime application deploys, database transfer, restore, or stack export.

## Isolation and capacity

- Apps share containers by PHP version; this is not hostile multi-tenant isolation.
- No one-container-per-app model or per-app CPU/memory quotas inside shared PHP roles.
- Runner replicas must remain one per PHP version to avoid duplicate jobs.

## Data and deletion

- `compose down -v`, managed MySQL/PostgreSQL service removal, and automatic volume deletion are blocked.
- App desired-state removal retains data; permanent prune is interactive, lists known parts, and requires literal `delete` with no bypass.
- No automatic migration between database engines/services or automatic database password rotation.
- Restore is not object-level atomic and can leave a partial destination.
- Scheduled MySQL/PostgreSQL logical backups are on-host only; no automatic upload or off-host replication.
- SQLite continuous backup supports S3-compatible replication, temporary verification, and export to a separate database file, but no public production replacement-restore command.
- Stack export/import currently excludes the stack-root `sqlite/` directory.
- Raw PostgreSQL transfer requires compatible major/image versions; major upgrades use logical dump/restore.

## Deployment and customization

- Bento provides signed queue orchestration and an operator-owned hook, not a fixed Git checkout, release-directory, rollback, or migration strategy.
- App template and Compose overlay input is trusted and can violate Bento invariants.
- Access-log analytics is ad hoc, not a hosted real-time analytics service.

## Networking and TLS

- Normally one host-mode stack owns ports 80/443. Additional stacks require bridge mode, distinct publications, or internal-only ingress.
- ACME depends on public DNS and reachable port 80; Bento cannot fix upstream firewall/NAT/DNS errors.
- External certificate renewal and trust-store distribution remain operator responsibilities.

Choose separate hosts or a stronger orchestration/isolation platform when these boundaries do not fit. See [What is Bento?](/start/overview/) and [technical decisions](/advanced/technical-decisions/).
