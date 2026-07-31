---
title: Desired state and generated configuration
description: Understand what Bento treats as operator intent, what it regenerates, and how render and apply affect running services.
---

# Desired state and generated configuration

Bento stores your intended stack configuration as **desired state** and derives disposable service files from it. Understanding that direction prevents lost edits and helps you recover safely when validation or reload fails.

## Mental model

```text
Bento commands
     |
     v
state.json + stack .env       custom/ + overlays/
(operator intent and secrets) (operator-owned additions)
     |                              |
     +---------- render ------------+
                    |
                    v
            generated/ and materialized assets
                    |
              validate + reload
                    |
                    v
              running containers
```

Bento is an on-demand control plane, not a resident daemon. It reconciles the stack only when you run a command that applies a change or invoke `render` or `apply` yourself.

| Layer | Examples | Who changes it? | Can Bento recreate it? |
| --- | --- | --- | --- |
| Desired state | `state.json` | Normally Bento commands | No; protect and back it up |
| Stack environment | `.env` | Bento initialization and deliberate operator configuration | Secrets are not safely derivable from generated files |
| Customization | `custom/`, `overlays/` | Operator or supported customization commands | No; preserve it |
| Generated configuration | `generated/nginx/`, `generated/php/`, `generated/compose/`, generated client files | Bento | Yes, from current intent and assets |
| Durable runtime data | `homes/`, `sqlite/`, `litestream-meta/`, `certs/`, `backups/`, logs, Docker named volumes | Applications, services, and Bento operations | No; use an appropriate backup method |

`state.json` contains the versioned application model: apps, runtime assignments, domains, database bindings, the optional stack-wide SQLite backup policy, jobs, proxies, and related settings. It can also contain deployment secrets, so Bento writes it with restricted permissions. The stack `.env` contains Compose identity, topology settings, and administrator secrets. Treat both as sensitive source material.

## Change desired state through Bento

Use Bento commands rather than editing `state.json` directly. Commands validate identities, domains, runtime references, database relationships, and other cross-record rules before atomically saving a current-schema document.

For example, an app command changes the app model and normally applies the resulting configuration in the same operation:

```sh
bento app create demo --domain demo.example.com
```

Some mutation commands offer `--no-apply`. That option records intent without reconciling generated files or running services. It is useful for batching related changes, but it deliberately leaves the live generation behind the desired state until you run:

```sh
bento apply
```

Do not use `--no-apply` when you require the change to take effect immediately. Until a successful apply, status on disk, generated configuration, and runtime behavior may describe different points in the change.

:::caution
Do not casually hand-edit `state.json`. A syntactically valid edit can still violate stack-wide identities or relationships. Invalid JSON, an unsupported schema version, or invalid state prevents normal commands from loading the stack; Bento does not rewrite an invalid document during a routine read.
:::

## Choose render or apply

Both commands build a complete candidate from current desired state. Their operational effects differ:

| Command | Writes generated files | Runs service configuration validators | Signals running services |
| --- | ---: | ---: | ---: |
| `bento render` | Yes | No | No |
| `bento apply --preview` | No | No | No |
| `bento apply --render-only` | Yes | Yes | No |
| `bento apply` | Yes | Yes | Yes, for the planned targets |

Use `render` when you need generated Compose files before starting containers or want to materialize configuration without touching running services:

```sh
bento render
```

Use preview to inspect the candidate file list and pending reload targets without changing the live generation:

```sh
bento apply --preview
```

Use a normal apply to reconcile and activate current intent:

```sh
bento apply
```

A normal apply validates targeted services that are running, then reloads only the roles in its plan. Stopped services are not reloaded; they consume the generated configuration when they next start. `apply` does not start stopped containers—service lifecycle remains an explicit Compose operation.

:::caution
`--skip-validate` bypasses the check that protects running services from invalid generated configuration. Keep it out of normal operations. An apply can also cause a brief service reload; plan application-level verification after material runtime changes.
:::

## Generated files are outputs, not customization points

Every managed generated file carries a Bento marker. A complete render replaces current managed files and removes managed files that are no longer desired. Direct edits under `generated/` can therefore disappear on the next render or apply.

Put operator-owned changes in supported locations instead:

- additive Nginx configuration belongs under `custom/nginx/`;
- app vhost and pool replacements belong in custom sources selected through `bento template`;
- Compose additions belong in ordered `overlays/` files.

Bento creates required custom directories but preserves their contents. Custom input can still make validation fail, so apply and verify after changing it.

Generated output may contain sensitive database client material even though it is reconstructible. Do not publish, commit, or broadly share the stack root.

## How failure affects each layer

Render and apply serialize generation with a stack-local lock and promote files through a recoverable transaction.

- If candidate generation fails, the live generated files remain unchanged.
- If promotion or service validation fails, Bento restores the previous generated files and does not reload services.
- If a reload signal fails after validation, the valid new generated files remain live; fix the service or signal problem and retry `apply`.
- If the process stops during promotion, the next render or apply uses the transaction journal to recover the interrupted generation.

A failed apply does not generally undo the desired-state mutation that triggered it. This is intentional: desired state remains the requested outcome while the previous valid generation can continue serving. Correct the invalid intent or customization, then apply again. If you no longer want the change, reverse it with the relevant Bento command rather than reconstructing old generated files.

Verify the stack after reconciliation:

```sh
bento status
bento doctor
```

For a configuration error, read the validator diagnostic first. Check custom files and recent desired-state changes; do not patch the generated candidate to make one apply pass.

## Boundaries and limitations

- Desired state describes configuration intent; it is not a backup of app code, certificates, database contents, or Redis data.
- Render does not continuously converge runtime state. External container or file changes remain until an operator action detects or replaces them.
- Validation checks service configuration, not every application-level behavior. Verify HTTP, jobs, and data access after relevant changes.
- Operator-owned templates and overlays are trusted input. Bento preserves them but cannot guarantee that they remain compatible with every upgrade.
- State schema migration is an explicit, confirmed operation; routine reads do not silently migrate old state.

## Advanced

The apply transaction renders a complete candidate into same-filesystem staging, builds a deterministic managed-file manifest, journals existing files and modes, atomically promotes candidates, and removes stale managed files last. Validation happens after promotion because container validators consume the live mounted paths. A validation failure uses the journal to restore the prior generation before any reload.

Reload plans are scoped by the change path used by a command: web routing changes can target Nginx, pool changes can target PHP-FPM and Nginx, and scheduler or worker changes can target the matching PHP runner. A standalone full `apply` conservatively plans Nginx plus all relevant PHP-FPM and runner roles. Preview shows that full pending plan but does not stage, validate, or promote files.

Generation metadata in `generated/.generation.json` records the asset identity, render time, and managed manifest. It supports reconciliation; it does not replace `state.json` or serve as recovery data.

## Next steps

- [Create a stack and practice render, start, and apply](/start/first-stack/).
- [Understand how the stack root separates ownership classes](/concepts/stacks/).
- [Add an application through validated desired-state commands](/start/first-app/).
