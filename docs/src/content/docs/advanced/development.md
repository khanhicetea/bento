---
title: Development and release
description: Set up Bento for development, run its checks, and build matching Linux binaries.
---

# Development and release

Use Deno 2.9.3 and the committed lockfile when you develop Bento. On production hosts, use a compiled Linux release.

## Set up and verify

```sh
deno --version
deno task fmt
deno task lint
deno task check
deno task test
deno task test:integration
```

When Docker is unavailable, integration tests skip Docker-only checks. Read the test output so you do not mistake a skip for live proof.

Never run destructive tests against the checked-in `bento/` stack. Create a temporary stack root instead.

## Layers

- `src/commands/`: parse/present and coordinate use cases.
- `src/services/` and `src/domain/`: state transitions and operation plans.
- `src/schemas/`: runtime validation of untrusted input.
- `src/platform/`: filesystem, lock, process, clock, random, assets.
- `templates/`: immutable Compose, images, config, in-container helpers.
- `tests/`: unit, contract/parity, and Docker integration behavior.

Keep dependencies moving in one direction: command adapters → services/domain → narrow platform interfaces. Keep terminal formatting, input parsing, and unchecked external values out of domain code.

## Compile and parity

```sh
deno task compile
deno task test:parity
deno task compile:amd64
deno task compile:arm64
```

The compiled executable includes immutable templates and writes the required assets under the selected stack root. It must work from any current directory without Deno, Node.js, Python, `npm install`, or a source checkout.

Source mode and compiled mode must produce the same generated files, state changes, diagnostics, exit behavior, and safety checks for the same input.

## Permissions and dependencies

Tasks declare the exact read, write, environment, process, network, and system permissions they need. Do not use or document unrestricted `-A` as the normal path.

Keep imports in `deno.json` and keep dependency resolution locked. Every dependency must also work with `deno compile`.

## Release checks

The repository provides tasks for formatting, linting, type checking, locked dependency checks, tests, compile smoke tests, parity tests, and Linux builds for AMD64 and ARM64.

The current GitHub workflow runs for tags and releases and builds the two Linux binaries. It does not run every local quality gate. Docker tests for a specific CPU architecture still need a matching runner when emulation is unavailable.

## Contributor safety

Validate JSON, environment, CLI, and subprocess data at runtime. Preserve exact schema-version behavior. Keep secrets out of command arguments and output. Protect state with atomic writes and locks, and test rollback and destructive-operation guards.

## Next steps

- [Architecture](/advanced/architecture/)
- [Render/apply internals](/advanced/render-apply/)
- [Technical decisions](/advanced/technical-decisions/)
