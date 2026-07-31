---
title: Technical decisions
description: Review Bento's major architecture choices, benefits, trade-offs, and deliberately rejected scope.
---

# Technical decisions

Bento optimizes for a comprehensible operator-owned PHP platform on one Linux host, not a general orchestration system.

## Single host and Compose

**Context:** Small deployments need repeatable services without cluster operations. **Decision:** Docker Compose on one Linux host. **Benefits:** familiar packaging, named-volume durability, low control-plane overhead. **Trade-offs:** host failure is stack failure; scaling/HA is external. **Boundary:** no Kubernetes or multi-host scheduler.

## Deno, strict TypeScript, one entrypoint

**Context:** External JSON/env/process data needs runtime validation while releases should avoid a language runtime. **Decision:** Deno 2.9.x, strict TypeScript, validated boundaries, source and compiled delivery. **Benefits:** one toolchain and parity-tested binaries. **Trade-offs:** pinned runtime/dependency policy and compiled asset materialization. **Boundary:** no Python compatibility layer or unrestricted `-A` default.

## Desired state without a daemon

**Context:** Hand-edited configs drift, but continuous reconciliation is unnecessary. **Decision:** versioned local JSON plus explicit render/apply. **Benefits:** inspectable intent and no resident control service. **Trade-offs:** operators invoke reconciliation; external drift is not continuously healed. **Boundary:** generated output is disposable.

## Nginx-only ingress and Unix sockets

**Context:** Public surface and routing should remain narrow. **Decision:** one Nginx; app FPM via per-app sockets. **Benefits:** no public FPM/database/cache ports, identity-aligned routing, direct host HTTP/3. **Trade-offs:** host/bridge namespace complexity and normally one host-mode stack. **Boundary:** non-PHP apps are proxy upstreams.

## Shared versioned PHP roles

**Context:** Per-app containers duplicate toolchains. **Decision:** FPM and singleton runner per version, ephemeral CLI per command. **Benefits:** concurrent runtimes with shared builds and consistent identities. **Trade-offs:** shared capacity/namespace; no hostile isolation or app quotas. **Rejected scope:** one container per app.

## One database binding, add-only services

**Context:** Engine moves and volume deletion are high-risk. **Decision:** apps hold add-only database bindings across managed MySQL/PostgreSQL services and SQLite kinds; versions are added but not automatically removed. The first binding remains the compatibility/default connection. **Benefits:** explicit grants, tools, and volume ownership without destructive rebinding. **Trade-offs:** migrations and password rotation are coordinated externally. **Boundary:** no automatic cross-engine conversion.

## Staged and scoped apply

**Context:** Partial generation and broad restarts create outages. **Decision:** lock, recover, stage, promote, validate, targeted reload, finalize. **Benefits:** recoverable files and narrower blast radius. **Trade-offs:** more transaction logic; not a distributed transaction or zero-downtime promise.

## Operator-owned escape hatches

**Context:** Real hosts need customization. **Decision:** drop-ins, complete app templates, ordered overlays. **Benefits:** upgrades need not fork core output. **Trade-offs:** trusted input can violate invariants and needs upgrade review.

## Next steps

- [Architecture](/advanced/architecture/)
- [Limitations](/reference/limitations/)
- [Development](/advanced/development/)
