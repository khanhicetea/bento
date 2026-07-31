# Bento documentation writing guide

This is the permanent standard for writing and maintaining Bento documentation. It applies to human and agent authors. The temporary delivery backlog lives in `plan.md`.

## 1. Documentation promise

Bento documentation helps an operator complete a task safely before explaining all implementation details. It uses progressive disclosure:

1. what the task accomplishes;
2. what must be true before starting;
3. the shortest safe procedure;
4. how to verify and recover;
5. advanced behavior and design context.

Optimize for correctness and clarity, not word count. Concise does not mean omitting a warning, prerequisite, or verification step.

## 2. Sources of truth

Check facts in this order:

1. **Current executable and implementation:** command `--help`, validators, domain/services code, templates, and rendered output define what ships.
2. **Current tests:** contract and integration tests define important edge cases, safety behavior, and expected state transitions.
3. **Root `README.md`:** useful current operator summary, but verify copied commands and defaults.
4. **`specs/`:** authoritative product intent, architecture rationale, invariants, and non-goals; it may describe a baseline that has since gained features.
5. **Existing published docs:** useful for continuity, never sufficient proof by themselves.

Do not use these as behavioral sources:

- `docs/dist/` or `web/dist/` generated output;
- `docs/node_modules/`;
- the checked-in `bento/` sample/runtime stack as proof of universal defaults;
- old Git history without confirming current behavior.

### Resolve conflicts explicitly

When sources disagree:

1. reproduce the behavior with current CLI help or a disposable stack;
2. inspect the responsible implementation and tests;
3. document only confirmed current behavior;
4. record a code/spec mismatch in the project issue tracker or `plan.md` while it exists;
5. do not blend old and new behavior into a third, unsupported workflow.

For architecture claims, distinguish **implemented behavior** from **intended invariant**. If a claim cannot be confirmed, omit it from published docs and report the gap.

## 3. Audience and voice

Write primarily for a technically capable developer or small-team operator managing a Linux host.

- Address the reader as **you**.
- Use active voice: “Bento writes the candidate configuration,” not “The candidate is written.”
- Use present tense for current behavior.
- Use direct verbs: “Run,” “Check,” “Copy,” “Choose.”
- Prefer familiar words. Define Bento-specific terms at first use.
- Do not call a task “easy,” “simple,” “obvious,” or “just.” These words do not help a blocked reader.
- Do not use marketing superlatives or promise zero downtime, complete isolation, or guaranteed recovery unless the implementation proves it.
- Use **Bento** for the product and `bento` for the command.

Keep paragraphs short. Use lists for choices or checks, not for every sentence. A short example is better than a long abstract explanation when a difficult choice has a realistic case.

## 4. Canonical terminology

Use these terms consistently:

| Term | Meaning |
| --- | --- |
| **stack** | One Bento installation and its desired state, generated files, app homes, certificates, backups, and Compose resources on a host. |
| **stack root** | The mutable, operator-owned filesystem path selected by `BENTO_STACK_ROOT`, the `./bento` default, or a one-command `--stack` override. |
| **stack name** | Stable `COMPOSE_PROJECT_NAME` identity that prefixes Compose resources. It is not inferred from the stack-root directory. |
| **app** | Bento's logical application identity: slug, UID/GID, home, PHP pool/socket, domains, data binding, jobs, and deploy settings. |
| **app slug** | Stable stack-wide identity reused across system resources. Do not describe changing it as a rename. |
| **desired state** | Operator intent stored in `state.json`. It is authoritative but sensitive and should normally be changed through the CLI. |
| **generated configuration** | Disposable files derived from desired state and templates. Never instruct readers to edit these files. |
| **render** | Generate configuration without signaling services. |
| **apply** | Render, validate, and reload the affected running services. |
| **control plane** | The host-local `bento` CLI, state transitions, render/apply logic, and platform adapters. There is no resident Bento daemon. |
| **data plane** | Nginx, PHP roles, databases, Redis, and supervised jobs running in containers. |
| **host mode** | Nginx uses the host network; normally one stack owns host ports 80/443. |
| **bridge mode** | Nginx joins the stack-private Compose network and may publish explicitly selected host ports. |
| **database service** | One managed MySQL version or PostgreSQL major and its durable volume. An app can hold multiple add-only relational and SQLite bindings; the first is its compatibility/default connection. |
| **runner** | Singleton service per PHP version that supervises app schedulers, deploy drains, and workers. |
| **overlay** | Operator-owned Compose customization loaded in deterministic order. |
| **drop-in** | Additive operator-owned configuration included from a supported `custom/` location. |

Use **MySQL 8.4** with a space for the product version and `mysql84` only when referring to a service identifier. Use **PostgreSQL 17** and `postgres17` in the same way.

## 5. Progressive disclosure rules

Every beginner or routine-operation page should expose one safe default path first.

- Do not branch the first-stack tutorial into every ingress, database, or TLS choice.
- Use MySQL for the shortest first-app path because it is the current default; link to the PostgreSQL guide for that alternative.
- Put optional flags, internals, uncommon topology, performance notes, and trade-offs under `## Advanced` or on an Advanced page.
- Bring a detail forward when omitting it could cause data loss, exposure, downtime, or a failed procedure.
- Link to one canonical concept instead of repeating a full explanation.

An `## Advanced` section is not a dumping ground. It should explain one or more of:

- how the operation maps to Bento's architecture;
- why a safety rule exists;
- behavior in multi-stack or customized deployments;
- validation, reload, rollback, or failure semantics;
- relevant technical trade-offs and limits.

## 6. Page types and templates

All published pages use Markdown or MDX under `docs/src/content/docs/` with Starlight-compatible frontmatter.

### Task guide

Use for installation and operational procedures.

```md
---
title: Back up and restore databases
description: Create verified logical dumps and restore them safely with Bento.
---

# Back up and restore databases

One or two sentences describing the outcome and important scope.

## Before you begin

- Required state, access, services, DNS, or backups.

## Do the task

1. Explain intent.

   ```sh
   bento ...
   ```

2. Continue with the shortest safe path.

## Verify

Show a command or observable result.

## Troubleshooting

Cover likely symptoms or link to the canonical troubleshooting page.

## Advanced

Explain internals, alternatives, and trade-offs only after the task works.

## Next steps

- [One closely related task](...)
```

Use a specific task title: “Run multiple stacks” is better than “Stack guide.”

### Concept page

Use when the reader needs a mental model rather than a procedure.

```md
---
title: Desired state and generated configuration
description: Understand what Bento owns and what you can safely customize.
---

# Desired state and generated configuration

State the concept and why an operator should care.

## Mental model

Use a short diagram, table, or example.

## How it affects operations

Give concrete consequences.

## Boundaries and limitations

State what the model does not guarantee.

## Advanced

Explain implementation or design rationale and link to focused architecture pages.
```

### Reference page

Use for exact lookup information.

- Begin with scope, not a tutorial.
- Prefer tables for flags, values, modes, paths, and ownership.
- Keep entries factual and parallel in grammar.
- Link each command group to its task guide.
- Generate exhaustive data from code where practical; otherwise write down the command and date/commit used to verify it in the authoring change, not in the published prose.
- Do not copy full `--help` output if it will immediately drift.

### Advanced architecture or decision page

Start with the operator consequence, then explain internals.

For a technical decision, use:

1. **Context** — the problem and constraints.
2. **Decision** — what Bento does.
3. **Benefits** — why it fits the constraints.
4. **Trade-offs** — costs and failure modes.
5. **Boundaries** — what Bento deliberately does not solve.

Do not rewrite source files module by module. Explain stable responsibilities and observable behavior.

## 7. Command examples

### Use copyable commands

- Use `sh` fences for shell commands and `text`, `json`, `yaml`, or `nginx` for output/configuration.
- Prefer the compiled command form in operator docs:

  ```sh
  bento status
  ```

- Use `deno task run ...` only in contributor/source-mode documentation.
- Establish `BENTO_STACK_ROOT` once, then omit repeated `--stack` options from examples. Document the `./bento` default and reserve `--stack PATH` for a deliberate one-command override.
- Put placeholders in clearly recognizable forms such as `app.example.com`, `<app>`, and `/path/to/export`. Use RFC-reserved example domains (`example.com`, `example.test`) instead of real domains.
- Use one sample stack consistently where practical: stack root `/var/lib/bento`, stack name `production`, app `demo`, domain `demo.example.com`.
- Use long option names in docs unless the short option is the normal interface.
- Put `sudo` only on commands that require host privilege. Do not imply the Bento CLI must always run as root unless verified.
- Never expose a real password, HMAC secret, private key, token, host address, or user path from a development machine.

### Verify every command

At minimum:

```sh
bento <command> --help
bento <command> <subcommand> --help
```

For state-changing examples, use a disposable stack when practical:

```sh
stack_root="$(mktemp -d)"
export BENTO_STACK_ROOT="$stack_root"
bento init --name docs-check
# Run only safe, relevant checks.
rm -rf "$stack_root"
```

Do not run destructive examples against the repository's checked-in `bento/` stack. Docker-dependent behavior should be verified only where a test host is available; otherwise inspect the implementation and relevant integration tests and state that limitation in the authoring report.

Do not fabricate output. If exact output is not important, describe the result instead of including a brittle transcript. If output matters, use an excerpt and mark omitted lines with `…`.

## 8. Examples and real cases

Include an example when it resolves a difficult choice, namespace difference, confirmation rule, or recovery path. Good cases include:

- choosing `host.docker.internal` instead of `127.0.0.1` for a host service from bridge-mode Nginx;
- restoring a dump to `demo_verify` before replacing `demo`;
- giving a second stack a distinct name and published ports;
- showing a safe Nginx health-check drop-in rather than editing `generated/nginx/`;
- explaining that dumps are created on-host and scheduled rclone uploads still require independent remote verification.

Keep examples focused. Do not build a fictional company or application across many pages. A page should usually have one primary example and, only when needed, one contrasting advanced case.

## 9. Safety writing

Warnings must appear before the action they qualify. Use Starlight asides where they improve scanning:

```md
:::caution
This restore replaces an existing database and is not object-level atomic. Verify a restore under a new database name first.
:::
```

Use severity consistently:

- `:::note` for useful context;
- `:::tip` for optional efficiency;
- `:::caution` for possible outage, irreversible change, public exposure, or partial recovery;
- `:::danger` only for likely destructive data loss or secret compromise.

Always call out these product boundaries when relevant:

- `compose down -v` is blocked because it destroys durable volumes.
- App desired-state removal and permanent prune are different operations; document exact confirmations from current help.
- Managed MySQL/PostgreSQL version removal and automatic DB password rotation are unsupported.
- Logical dumps are created on-host; optional scheduled rclone uploads do not provide remote retention or recovery guarantees.
- Restore is not object-level atomic and may leave a partial destination.
- Raw PostgreSQL transfer requires a compatible major/image; use logical backup/restore for a major upgrade.
- Export archives contain secrets and private keys.
- ACME requires correct public DNS and reachable public port 80 before issuance.
- The shared-container app model is not a hostile multi-tenant sandbox.
- Generated files are disposable; edits belong in supported custom locations.

Do not weaken an exact typed-confirmation requirement into “confirm when prompted.” Show what the current CLI requires.

## 10. Architecture writing

Architecture documentation must connect internals to operator consequences.

Prefer a compact text or Mermaid diagram for paths such as:

```text
Operator -> bento CLI -> desired state -> candidate render -> validate -> targeted reload
Internet -> Nginx -> app PHP-FPM socket -> private database/Redis
PHP runner -> per-app scheduler and workers
```

A useful architecture section answers:

- Who owns this component or file?
- Is it public, private, generated, custom, durable, or ephemeral?
- What is its cardinality: per host, stack, version, or app?
- What validates it?
- What reloads or restarts when it changes?
- What remains after failure or service recreation?
- Which security boundary does it provide, and which does it not provide?

Keep implementation names only when operators or contributors encounter them. Avoid making private function/class names part of the documentation contract.

## 11. Formatting and structure

- Use sentence case for headings.
- Use one H1 matching the page title. Starlight may render the frontmatter title; follow the established site convention consistently once pages are added.
- Keep heading levels sequential; do not jump from H2 to H4.
- Put one command per code block when the intervening explanation matters. Group a short linear sequence when it is meant to be copied together.
- Wrap filenames, commands, flags, environment variables, service names, and literal values in backticks.
- Use bold only for emphasis or UI labels, not for every term.
- Tables are good for comparable modes and lookup data; do not put multi-step procedures in tables.
- Add alt text that explains the information in an image. Do not repeat a decorative image's appearance at length.
- Use relative links for repository docs and root-relative or Starlight-compatible links for site pages, following existing site conventions.
- Link descriptive text, not “click here.”
- Avoid raw HTML unless Markdown/MDX cannot express the content accessibly.

### Frontmatter

Every published page needs at least:

```yaml
---
title: Create your first stack
description: Initialize, start, and verify a Bento stack on one Linux host.
---
```

Descriptions should state the reader outcome in roughly one sentence and remain useful in search results. Do not prefix every title with “Bento.”

## 12. Avoid duplication and drift

Assign one canonical location to volatile facts:

| Fact | Canonical page |
| --- | --- |
| Requirements and supported distribution | `start/requirements.md` |
| Stack root/name model | `concepts/stacks.md` |
| Ownership and generated files | `concepts/desired-state.md` and `reference/stack-layout.md` |
| Host versus bridge behavior | `concepts/networking.md` |
| TLS modes | `guides/apps/domains-tls.md` |
| Backup/restore caveats | `guides/data/backup-restore.md` |
| Unsupported workflows | `reference/limitations.md` |
| Full render transaction | `advanced/render-apply.md` |
| Isolation limits | `advanced/isolation-security.md` |

Other pages should summarize only the one fact necessary to continue and link to the canonical page.

Do not hard-code a version unless it changes the procedure or is a current compatibility requirement. Before changing a default/version, search all docs:

```sh
rg '8\.5|8\.4|2\.9|PostgreSQL 17|postgres17|mysql84' docs/src/content/docs README.md
```

## 13. Author workflow

For each page:

1. Read this guide and the page entry in `plan.md` if it still exists.
2. State the intended reader outcome in one sentence.
3. Locate the owning command/service and relevant tests with `rg`.
4. Run current top-level and nested `--help` commands.
5. Read only the relevant spec sections for rationale and boundaries.
6. Draft the shortest safe path before adding Advanced content.
7. Verify commands, files, defaults, side effects, and likely failures.
8. Add canonical links and two or three next steps.
9. Build the site.
10. Update `plan.md` and report facts that remain unresolved.

Useful repository checks:

```sh
# Documentation site
cd docs
npm run build

# Find accidental placeholders or drafting notes
rg -n 'TODO|TBD|FIXME|lorem|Documentation is being prepared' src/content/docs

# Review links and command mentions manually when no link checker is configured
rg -n '\]\(' src/content/docs
rg -n '```(sh|bash|console)' src/content/docs
```

Use `npm ci` in a clean environment. Do not edit `docs/dist/` manually; it is generated by the documentation build.

## 14. Review checklist

### Accuracy

- [ ] Current CLI help and implementation confirm commands, options, paths, and defaults.
- [ ] Relevant tests confirm safety/failure claims.
- [ ] The page does not present a planned spec feature as shipped.
- [ ] Version-sensitive statements are necessary and current.

### Usability

- [ ] The reader outcome is clear in the opening.
- [ ] Prerequisites appear before commands.
- [ ] The basic path is linear and copyable.
- [ ] Verification and likely recovery steps are present.
- [ ] Advanced details do not interrupt the first successful path.

### Safety

- [ ] Warnings precede destructive, exposed, downtime-causing, or non-atomic actions.
- [ ] Examples use an explicit stack root where ambiguity is risky.
- [ ] No secrets, private keys, or machine-specific values are included.
- [ ] Backup instructions distinguish on-host dumps from off-host recovery.

### Quality

- [ ] Terms match the canonical terminology table.
- [ ] The page links instead of duplicating canonical explanations.
- [ ] Headings, code fences, asides, tables, and frontmatter are valid.
- [ ] Links resolve and `npm run build` passes.
- [ ] The change does not modify generated `docs/dist/` unless release policy explicitly requires committing it.

## 15. Maintaining this guide

Keep this file after the initial documentation project is complete. Update it when:

- a repeated review issue reveals a missing rule;
- a command-verification or link-checking tool is added;
- terminology or site conventions change;
- the source-of-truth hierarchy changes;
- a new major operator safety boundary is introduced.

Prefer small, reviewed changes. Do not turn this guide into a second product manual; operator facts belong in published pages, while this file defines how those pages are written and verified.

### Lessons from the initial complete documentation set

- Maintain one command-group map in `reference/cli.md`, then link each group to a task guide. Compare it with current top-level `bento --help` whenever commands change.
- Audit root-relative links against source routes, not generated HTML. A clean Starlight build proves page parsing but does not by itself prove every authored link target.
- Never use a secret-printing command as a connectivity example. Name the protected credential path and verify through an application-specific health operation instead.
- Distinguish a configuration reload from container recreation. Compose changes to images, mounts, environment, networks, or publications generally need `compose -- up -d`; render/apply alone cannot alter an existing container definition.
- Record Docker soft-skips explicitly. A passing integration task can still lack live data-plane proof when Docker networking, images, or architecture are unavailable.
- For a completed batch, run a clean-room reading-path review from Requirements through Daily operations without relying on the root README, in addition to testing individual pages.

A lightweight source-route link audit can derive routes from every `.md`/`.mdx` file under `src/content/docs/`, extract Markdown destinations, and fail when a root-relative route has no source page. Keep the script local to review until the repository adopts a maintained link-checking task.
