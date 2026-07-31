---
title: Technical decisions
description: Understand why Bento uses its current architecture and what each choice costs.
---

# Technical decisions

Bento aims to stay understandable on one operator-owned Linux host. It does not try to become a general orchestration system.

Each section explains the problem, Bento's choice, its benefit, and its cost.

## Single host and Compose

- **Problem:** Small deployments need repeatable services without cluster operations.
- **Choice:** Run Docker Compose on one Linux host.
- **Benefit:** Operators get familiar packaging, durable named volumes, and a small control plane.
- **Cost:** A host failure affects the whole stack. Bento leaves scaling and high availability to other systems.
- **Boundary:** Bento does not include Kubernetes or a multi-host scheduler.

## Deno, strict TypeScript, one entrypoint

- **Problem:** Bento must validate JSON, environment, and process data at runtime, while production releases should not require a language runtime.
- **Choice:** Use Deno 2.9.x, strict TypeScript, runtime validation, and compiled releases.
- **Benefit:** Source and compiled builds share one toolchain and parity tests.
- **Cost:** The project pins the runtime and dependencies and must package its assets into each binary.
- **Boundary:** Bento does not include a Python compatibility layer or use unrestricted `-A` by default.

## Desired state without a daemon

- **Problem:** Hand-edited files drift, but a small single-host stack does not need continuous reconciliation.
- **Choice:** Store versioned local desired state and reconcile it with explicit `render` and `apply` commands.
- **Benefit:** Operators can inspect intent, and Bento needs no resident service.
- **Cost:** Operators must run reconciliation. Bento does not continuously repair external changes.
- **Boundary:** Generated output is disposable.

## Nginx-only ingress and Unix sockets

- **Problem:** The public surface and routing path should stay narrow.
- **Choice:** Expose one Nginx service and route PHP through per-app FPM sockets.
- **Benefit:** FPM, database, and cache ports stay private. Routing follows app identity, and host mode supports direct HTTP/3.
- **Cost:** Host and bridge modes interpret addresses differently, and normally only one stack can use host mode.
- **Boundary:** Bento only reverse-proxies non-PHP applications.

## Shared versioned PHP roles

- **Problem:** A full container set for every app duplicates tools and images.
- **Choice:** Run one FPM service and one runner per PHP version, then create a temporary CLI container for each command.
- **Benefit:** Apps can use different PHP versions while sharing builds and consistent identities.
- **Cost:** Apps share capacity and a container namespace. Bento does not provide hostile isolation or app quotas.
- **Rejected scope:** One container per app.

## One database binding, add-only services

- **Problem:** Moving between database engines or deleting volumes carries high risk.
- **Choice:** Apps keep add-only bindings to managed MySQL, PostgreSQL, or SQLite services. Bento adds versions but does not remove them automatically. The first binding remains the default for compatibility.
- **Benefit:** Grants, tools, and volume ownership stay explicit, and Bento never performs a destructive rebind.
- **Cost:** Operators coordinate data migration and password rotation outside Bento.
- **Boundary:** Bento does not convert data between engines automatically.

## Staged and scoped apply

- **Problem:** Partial configuration and broad restarts can cause avoidable outages.
- **Choice:** Lock the stack, recover old work, stage a complete candidate, publish it, validate it, and reload only affected services.
- **Benefit:** Bento can recover files and keep the reload scope small.
- **Cost:** The transaction logic is more complex. It is not a distributed transaction and does not promise zero downtime.

## Operator-owned escape hatches

- **Problem:** Real hosts need supported customization points.
- **Choice:** Accept drop-ins, complete app templates, and ordered Compose overlays.
- **Benefit:** Operators can customize a stack without forking generated output.
- **Cost:** Trusted custom input can break Bento's assumptions and needs review after upgrades.

## Next steps

- [Architecture](/advanced/architecture/)
- [Limitations](/reference/limitations/)
- [Development](/advanced/development/)
