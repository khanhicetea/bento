# Bento product specifications

Status: current-product baseline  
Implementation snapshot: `bento 0.1.0`, state schema `4`, Deno target `2.9.3`  
Repository snapshot reviewed: `3b4345b`

These specifications describe the product that exists in this repository: its product model, operator promises, architecture, technology choices, safety boundaries, and acceptance contract. They replace the removed historical specifications and normalize the current implementation where older prose in `docs/` is stale.

## Specification set

1. [Product specification](01-product-spec.md) — users, value, core values, product areas, capabilities, journeys, and non-goals.
2. [System architecture](02-system-architecture.md) — control/data planes, app and repository structure, runtime topology, state/storage, security, and failure behavior.
3. [Technical decisions and reimplementation contract](03-reimplementation-contract.md) — decision record, invariants, verification requirements, and current conformance notes.

## Normative language

`MUST`, `MUST NOT`, `SHOULD`, and `MAY` are normative. Unqualified present-tense statements describe the reviewed implementation baseline.

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
