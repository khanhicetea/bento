# Bento reimplementation and acceptance contract

Status: current implementation contract with PostgreSQL extension locked for phased delivery

This contract distinguishes behavior that implementations must preserve from replaceable internal organization. The product requirements in [`01-product-spec.md`](01-product-spec.md), architecture invariants in [`02-system-architecture.md`](02-system-architecture.md), and acceptance evidence tracked in [`todo.md`](todo.md) remain authoritative. Existing `F-*` and `R-*` test annotations continue to identify the shipped MySQL-era acceptance suite.

## 1. Fixed compatibility contract

- Source and compiled execution use the same domain and command implementation and must remain behavior-equivalent.
- MySQL 8.4 remains the default for existing and newly initialized stacks unless the operator explicitly changes the database default.
- Existing MySQL commands and safety behavior remain compatible.
- Desired state is runtime-validated before becoming a domain value; unsupported schema versions are rejected without writes.
- Secrets are absent from host argv and normal human/JSON diagnostics and are mode-restricted on disk.
- Render/apply remains serialized, staged, validated, recoverable, and reload-scoped.
- Durable app homes, database volumes, backups, credentials, and certificates survive routine reconciliation.
- Automated database service/version/volume removal remains unsupported.

Internal file layout, helper names, and adapter boundaries may change when the observable contract and strict boundary validation remain intact.

## 2. PostgreSQL extension contract

PostgreSQL is a first-class relational backend, not an additional loose set of MySQL fields. Managed MySQL and PostgreSQL versions may coexist, and each app owns a collection of discriminated database bindings that may mix those engines with SQLite and Litestream. Adding a binding never converts or removes another binding.

Managed PostgreSQL versions use official major-only image tags such as `17`. Derived names are deterministic: version `17` maps to service `postgres17` and volume `postgres17-data`. Raw PostgreSQL volume transfer requires a compatible PostgreSQL major/image; logical backup and restore is the supported major-upgrade path.

Current-schema Compose/runtime support, provisioning, administration, backup/restore, diagnostics, pruning, transfer, wizard, and release proof are delivered only through implemented behavior. This fresh project has no legacy state migration command. A documentation phase does not make an unimplemented CLI command available.

## 3. PostgreSQL acceptance IDs

The normative PostgreSQL gates are `PG-01` through `PG-18` in [`pg-database.md` §10](pg-database.md#10-final-acceptance-gates). Every ID has a required automated or live evidence class and a delivery phase in that matrix. An ID may be checked only when its stated evidence exists and passes.

Phase 0 locks this contract only. Production behavior must remain unchanged until its corresponding implementation phase is completed.
