# Bento product specifications

Status: current-product baseline  
Implementation snapshot: `bento 0.1.0`, state schema `4`, Deno target `2.9.3`  
Repository snapshot reviewed: `3b4345b`

These specifications describe the product that exists in this repository: its product model, operator promises, architecture, technology choices, safety boundaries, and acceptance contract. They replace the removed historical specifications and normalize the current implementation where older prose in `docs/` is stale.

## Specification set

### Implemented baseline

1. [Product specification](01-product-spec.md) — users, value, core values, product areas, capabilities, journeys, and non-goals.
2. [System architecture](02-system-architecture.md) — control/data planes, app and repository structure, runtime topology, state/storage, security, and failure behavior.
3. [Technical decisions and reimplementation contract](03-reimplementation-contract.md) — decision record, invariants, verification requirements, and current conformance notes.

### Proposed roadmap

4. [Proposed product enhancements](04-proposed-product-enhancements.md) — prioritized recovery, planning, migration, data lifecycle, automation, capacity, deploy, TLS, and maintenance features.
5. [Proposed technical improvements](05-proposed-technical-improvements.md) — prioritized CI, documentation, backup, transfer, locking, reload, supply-chain, process, secret, journal, and testing fixes.

Proposal documents describe candidate work only. They do not alter the implemented baseline until the product owner approves them and corresponding code, tests, baseline specs, and operator documentation ship together.

## Normative language

In baseline documents, `MUST`, `MUST NOT`, `SHOULD`, and `MAY` are normative. In proposal documents, those terms define acceptance requirements only if the proposal is approved. Unqualified present-tense statements in baseline documents describe the reviewed implementation.

When sources conflict, use this precedence for product behavior:

1. validated behavior in `src/` and safety/contract tests;
2. these specifications;
3. operator documentation in `docs/` and `README.md`;
4. comments and historical Git content.

A specification change does not by itself change runtime behavior. Product changes require code, tests, and operator documentation to move together.

## Evidence reviewed

The baseline was derived from:

- `README.md`, `deno.json`, `deno.lock`, and `.github/workflows/ci.yml`;
- `src/domain/`, `src/schemas/`, `src/platform/`, `src/services/`, `src/commands/`, and `src/ui/`;
- immutable assets in `templates/`;
- unit, contract, parity, and integration tests in `tests/`;
- architecture, concepts, guides, and reference material in `docs/src/content/docs/`;
- the current CLI help surface.

## Current baseline corrections

These specs intentionally follow current code over older documentation:

- An app persists `databases[]` and can hold multiple add-only MySQL, PostgreSQL, plain SQLite, and Litestream bindings. The first binding is the compatibility/default connection, not the only possible binding.
- Scheduled logical backups can upload newly created artifacts through the isolated rclone sidecar. This supersedes older statements that all off-host replication is manual.
- `stack.tar.gz` mechanically includes the stack-root `sqlite/` tree because it archives the root; it is not a consistency-guaranteed live SQLite backup. Use SQLite `.backup` or Litestream for recovery assurance.
- The checked-in GitHub workflow currently builds release binaries on tags/releases only. The broader formatting, linting, typecheck, test, smoke, and parity gates exist as Deno tasks but are not all enforced by that workflow.
