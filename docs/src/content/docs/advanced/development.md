---
title: Development and release
description: Set up Bento for development, run its checks, and build matching Linux binaries.
---

# Development and release

Use Bun 1.4.0 and the committed lockfile when you develop Bento. On production hosts, use a compiled Linux release.

## Set up and verify

```sh
bun --version
bun run fmt
bun run lint
bun run check
bun run test
bun run test:integration
```

When Docker is unavailable, integration tests skip Docker-only checks. Read the test output so you do not mistake a skip for live proof.

Never run destructive tests against the checked-in `bento/` stack. Create a temporary stack root instead.

## Layers

- `apps/cli/src/commands/` and `apps/cli/src/server/domains/`: transport adapters that parse/present and delegate.
- `apps/cli/src/use_cases/`: shared CLI/web workflows and persistence/apply ordering.
- `apps/cli/src/services/` and `apps/cli/src/domain/`: focused capabilities, state transitions, and operation plans.
- `apps/cli/src/schemas/`: runtime validation of untrusted input.
- `apps/cli/src/platform/`: filesystem, lock, process, clock, random, assets.
- `apps/cli/templates/`: immutable Compose, images, config, in-container helpers.
- `apps/cli/tests/`: unit, contract/parity, and Docker integration behavior.

Keep dependencies moving in one direction: command/web adapters → use cases → services/domain → narrow platform interfaces. Keep terminal formatting, input parsing, and unchecked external values out of domain code. Prefer pure functions for state and planning; use classes only where a resource or transaction has a cohesive lifecycle.

## Compile and parity

```sh
bun run compile
bun run test:parity
bun run compile:amd64
bun run compile:arm64
```

The compiled executable includes immutable templates and writes the required assets under the selected stack root. It must work from any current directory without Bun, Node.js, Python, `npm install`, or a source checkout.

Source mode and compiled mode must produce the same generated files, state changes, diagnostics, exit behavior, and safety checks for the same input.

## Permissions and dependencies

Install with `bun install --frozen-lockfile`; source commands use normal host filesystem and process access.

Keep imports in `package.json` and keep dependency resolution locked. Every dependency must also work with `bun build --compile`.

## Release checks

The repository provides tasks for formatting, linting, type checking, locked dependency checks, tests, compile smoke tests, parity tests, and Linux builds for AMD64 and ARM64.

The GitHub workflow runs frozen install, formatting, linting, type checking, unit/contract tests, integration tests, and both Linux builds for tags and releases. Parity and smoke remain additional local gates. Docker tests for a specific CPU architecture still need a matching runner when emulation is unavailable.

## Contributor safety

Validate JSON, environment, CLI, and subprocess data at runtime. Preserve exact schema-version behavior. Keep secrets out of command arguments and output. Protect state with atomic writes and locks, and test rollback and destructive-operation guards.

## Next steps

- [Architecture](/advanced/architecture/)
- [Render/apply internals](/advanced/render-apply/)
- [Technical decisions](/advanced/technical-decisions/)
