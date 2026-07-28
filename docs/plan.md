# Bento documentation plan

> Working document for documentation agents. Update this file after every writing pass. Delete it only when all completion criteria at the end are met. Keep `writing-guides.md` permanently.

## 1. Goal

Build documentation that helps a new operator install Bento, create a stack, launch an app, and perform routine operations without first learning the internals. Advanced readers must also be able to understand Bento's architecture, safety boundaries, and technical decisions.

The documentation should be:

- task-oriented and quick to scan;
- concise, with definitions near first use;
- explicit about prerequisites, verification, and destructive effects;
- illustrated with small, runnable examples where a choice or failure mode is not obvious;
- layered so basic procedures come before advanced details.

## 2. Progress ledger

Agents must add or update a row here before starting a page. Use `not started`, `in progress`, `blocked`, `review`, or `done`. A page is `done` only after it passes the acceptance checklist.

| Page | Status | Owner/session | Notes or blocker |
| --- | --- | --- | --- |
| `index.mdx` | done | 019fa6da | Replaced placeholder; added task entry points and audience paths. Build passes. |
| `start/overview.md` | done | 019fa6e7 | Added product-fit guidance, operating model, responsibilities, and verified non-goals. `phase_g_test.ts` and docs build pass. |
| `start/requirements.md` | done | 019fa6ea | Added Linux/architecture, Docker 20.10 and Compose 2.20 minimums, host utilities, storage/permissions, ports/DNS, and separate Deno 2.9.3 source requirements. Doctor-related tests and docs build pass. |
| `start/install.md` | done | 019fa6ec | Added architecture-specific release installation, version verification, durable stack-root setup, and source/build alternatives. Compiled/source parity tests and docs build pass. |
| `start/first-stack.md` | done | 019fa6ec | Added explicit init/render/config/start/apply/status/doctor flow, stable-name and host-ingress guidance, safety warnings, and recovery. Docs build and 7 ingress tests pass; disposable Docker startup reached image build but this host's exhausted Docker address pools blocked network creation, now covered in troubleshooting. |
| `start/first-app.md` | done | 019fa6ec | Added service readiness, explicit MySQL app/database creation, permission policy, deploy-key handling, local Host routing, code/credential locations, DNS/TLS next steps, and recovery. Disposable CLI flow, 24 app/MySQL/Redis tests, and docs build pass; live HTTP/MySQL smoke remained unavailable because this host cannot allocate another Docker network. |
| `concepts/stacks.md` | done | 019fa6ec | Added root/name mental model, path ownership classes, Compose identity and collision risks, explicit targeting checks, backup boundaries, and source/binary asset behavior. Disposable CLI checks, 7 stack/ingress tests, and docs build pass. |

## 3. Audiences and reading paths

### New operator

Needs a working stack and first application. Intended path:

1. Home
2. What is Bento?
3. Requirements and installation
4. Create your first stack
5. Add your first application
6. Day-to-day stack management

### Returning operator

Arrives from search and needs one operation. Guides must stand alone and link to only the required concepts or reference material.

### Advanced operator

Needs to run multiple stacks, customize generated services, plan recovery, or understand security and failure semantics. Intended path:

1. Core concepts
2. Relevant advanced guide
3. Architecture and technical decisions
4. Reference

### Contributor or reviewer

Needs the control-plane boundaries, repository layout, validation rules, tests, and source/binary parity requirements. This is a secondary audience; do not let contributor material interrupt operator workflows.

## 4. Information architecture

All published pages belong under `docs/src/content/docs/`. Use the following routes unless an existing route has already become public and changing it would break links.

### Home

| File | Purpose | Priority |
| --- | --- | --- |
| `index.mdx` | Explain Bento in one sentence; provide “Install Bento,” “Create a stack,” and “Understand the architecture” entry points; replace the current placeholder. | P0 |

### Start here

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `start/overview.md` | Decide whether Bento fits: single Linux host, PHP apps and reverse proxies, local CLI, major non-goals. | `README.md`; `specs/01-product-spec.md` §§1–5, 8 | P0 |
| `start/requirements.md` | Prepare Linux, Docker Engine, Compose v2, ports/DNS, and the supported Bento distribution. Clearly separate production binary requirements from source-development requirements. | `README.md` Requirements and Compile sections; `src/version.ts`; CI workflow | P0 |
| `start/install.md` | Install the compiled binary, verify `bento version`, choose a durable stack-root location, and know how source mode differs. Do not invent a release URL if one does not exist. | `README.md`; release/CI files; `deno.json` | P0 |
| `start/first-stack.md` | Initialize, render, start, inspect, and validate one stack. Explain `--stack`, stable stack name, and host ingress. | CLI `init`, `render`, `compose`, `apply`, `status`, `doctor` help; integration tests | P0 |
| `start/first-app.md` | Create a simple app, register its deploy key if needed, start services, verify routing, and identify next steps for code, DNS, database, and TLS. Include one MySQL path; link to PostgreSQL rather than mixing both paths. | CLI `app create/show`, `exec`, `tls` help; `README.md` Quick start; app tests | P0 |
| `start/daily-operations.md` | Give a short runbook for status, apply, logs, shell/exec, backups, updates, and diagnostics. | Relevant CLI help and service tests | P1 |

### Core concepts

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `concepts/stacks.md` | Understand stack root versus stable stack name, mutable versus generated data, and why commands should target a stack explicitly. | `README.md`; `src/platform/paths.ts`; `src/services/state_store.ts`; state tests | P0 |
| `concepts/desired-state.md` | Understand `state.json`, render versus apply, disposable generated files, and operator-owned customization. | `specs/02-system-architecture.md` §§5, 7.3; render implementation/tests | P0 |
| `concepts/app-model.md` | Understand app slug/identity, home, domains, PHP pool/socket, one database binding, Redis metadata, workers, and deploys. State that a slug is an identity, not a casual rename. | product spec §§1, 6.2; architecture §§3, 6 | P1 |
| `concepts/networking.md` | Understand public Nginx, private backend services, host versus bridge ingress, and how upstream addresses change meaning. | `README.md` Multiple stacks; architecture §4; multistack tests | P1 |
| `concepts/safety-and-durability.md` | Distinguish desired, generated, custom, and durable data; summarize guarded deletion, volume protection, secrets, backups, and trust boundaries. | architecture §§5, 8, 9; safety tests | P1 |

### Stack management

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `guides/stacks/manage.md` | Render/apply changes, start/stop services safely through the Compose wrapper, inspect files/status, and avoid `down -v`. | CLI help; `src/services/compose.ts`; tests | P0 |
| `guides/stacks/multiple-stacks.md` | Run an additional named stack in bridge mode with non-conflicting or internal-only ports; verify effective ingress. | `README.md`; ingress commands and multistack tests | P1 |
| `guides/stacks/export-import.md` | Export, transfer, and import a complete stack; explain downtime, archive sensitivity, identity/port overrides, architecture/version compatibility, and raw-volume limitations. | `README.md` Full stack export/import; `src/services/stack_transfer.ts`; tests | P1 |
| `guides/stacks/diagnostics.md` | Use `status`, `doctor`, JSON output, support bundles, and service logs; explain what can safely be shared. | CLI help; doctor/support-bundle implementations and tests | P1 |
| `guides/stacks/maintenance.md` | Run and schedule maintenance without implying that it creates an off-host backup. | CLI help; maintenance and schedule services/tests | P2 |

### Applications and traffic

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `guides/apps/manage.md` | List/show/update, enable/disable, use shell/exec, remove desired state, and permanently prune retained data with the correct warnings. | App CLI help; app/app-prune services and tests | P0 |
| `guides/apps/php-runtimes.md` | Add/list/remove PHP versions, select a version/profile, understand shared capacity, and plan a runtime change. | PHP CLI help; PHP service/tests; product spec §6.4 | P1 |
| `guides/apps/domains-tls.md` | Manage domains/aliases and choose shared, self-CA, ACME, or external TLS; include DNS/port checks and CA export. | `README.md` TLS modes; TLS CLI/service/tests | P0 |
| `guides/apps/reverse-proxy.md` | Create a proxy with one or more upstreams; choose a correct upstream for host/bridge mode; remove it safely. | Proxy CLI/help/tests; architecture §4 | P1 |
| `guides/apps/deploy.md` | Enable webhook deployment, configure the provider, replace the no-op hook, drain/inspect jobs, rotate the secret, and troubleshoot signatures/timeouts. | Deploy CLI/service/helpers/tests; product spec §6.6 | P1 |
| `guides/apps/schedules-workers.md` | Add and operate cron jobs and workers; explain identity, workdir, output, timeout, locks, and scoped controls. | Cron/worker CLI, services, tests; architecture §7.5 | P1 |
| `guides/apps/access-logs.md` | Enable/disable/rotate/report access logs and understand retention and sensitive data. | Access-log CLI/service/tests | P2 |
| `guides/apps/permissions.md` | Check, dry-run, shallow repair, and explicitly recursive repair; explain symlink behavior. | Permissions CLI/service/tests; product spec §6.9 | P1 |

### Data

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `guides/data/mysql.md` | Add/list MySQL, bind/create app databases, use shell/size/processlist, and understand add-only managed versions and password limitations. | MySQL CLI/service/tests; README command notes | P1 |
| `guides/data/postgresql.md` | Add/list PostgreSQL, create a PostgreSQL-backed app, administer it, and understand major-version and isolation rules. | `specs/pg-database.md`; PostgreSQL CLI/services/tests | P1 |
| `guides/data/redis.md` | Explain shared-prefix and ACL modes, private networking, and where applications obtain connection metadata. Only document CLI actions that actually exist. | State/render code; Redis service/tests; product spec §6.5 | P2 |
| `guides/data/backup-restore.md` | Back up one app/all databases, schedule on-host dumps, copy them off-host, restore to a verification database, and understand replacement confirmation/non-atomic restore. | `README.md` Logical backup; backup/restore CLI/services/tests | P0 |

### Customization

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `guides/customization/nginx.md` | Add safe Nginx drop-ins in the correct contexts, order them, apply/validate, and avoid editing generated output. | `README.md` Nginx customization; templates/render tests | P1 |
| `guides/customization/templates.md` | Select/inspect/reset app-owned vhost or pool templates and understand provenance/update warnings. Use the actual CLI terms. | Template CLI/customization service/tests | P2 |
| `guides/customization/compose-overlays.md` | Add ordered operator-owned Compose overlays without breaking required networks, mounts, sockets, identities, or durability. | Compose/customization services; architecture §§11–12 | P2 |

### Reference

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `reference/cli.md` | Find the command hierarchy, global flags, help conventions, JSON behavior, and links to task guides. Prefer generated or verified snippets over hand-copied exhaustive option tables. | `bento --help`; every subcommand `--help`; command router | P1 |
| `reference/configuration.md` | Find supported stack `.env` variables, defaults, allowed values, scope, and secret handling. Do not treat sample values as universal defaults. | validators, env services, templates, CLI help | P1 |
| `reference/stack-layout.md` | Identify which stack-root directories/files are source-of-truth, generated, custom, durable, sensitive, or ephemeral. | path and asset services; architecture §5 | P1 |
| `reference/state.md` | Explain schema ownership/versioning, migration, backup behavior, and “do not edit casually”; link to concepts rather than reproducing the whole schema unless generated from code. | `src/schemas/state.ts`; migrate service/tests | P2 |
| `reference/troubleshooting.md` | Symptom-first index linking to targeted checks and guides; cover port conflicts, Docker unavailable, failed validation, DNS/ACME, permissions, database health, runner jobs, and failed restore. | diagnostics, errors, tests, guide troubleshooting sections | P1 |
| `reference/limitations.md` | State current non-goals and unsupported/destructive workflows in one searchable page. | CLI behavior; README non-goals; product spec §8 | P1 |

### Advanced

| File | Reader outcome | Primary sources | Priority |
| --- | --- | --- | --- |
| `advanced/architecture.md` | See the complete control-plane/data-plane topology and component cardinality, with links to focused pages. | architecture §§1–2, 12; current Compose rendering | P1 |
| `advanced/render-apply.md` | Understand lock → recover → stage → promote → validate → targeted reload → finalize, rollback behavior, and why render does not signal services. | architecture §§7.3–7.4, 9; render/reload code and tests | P1 |
| `advanced/isolation-security.md` | Understand UID/GID, FPM socket, filesystem, database/Redis boundaries, secrets, public surface, and why this is not hostile multi-tenancy. | architecture §§3, 8; relevant templates/tests | P1 |
| `advanced/networking.md` | Understand namespaces, socket path mapping, host/bridge trade-offs, HTTP/3 UDP publication, and multi-stack constraints. | architecture §4; multistack render/tests | P2 |
| `advanced/runtime-supervision.md` | Understand per-PHP FPM/runner/CLI roles, s6 scan-tree reconciliation, Supercronic, singleton runners, and scoped signals. | README Runner supervision; architecture §§2.2, 7.5 | P2 |
| `advanced/storage-recovery.md` | Understand ownership layers, named volumes, logical versus raw backups, export/import consistency, and failure semantics. | architecture §§5, 7.7, 9; transfer/backup tests | P2 |
| `advanced/technical-decisions.md` | Explain major choices and trade-offs: single host/Compose, Deno+TypeScript, desired state without daemon, Nginx-only ingress, shared versioned PHP, Unix sockets, one DB binding, explicit add-only data services, staged apply, and binary/source parity. | specs; implementation and tests | P1 |
| `advanced/development.md` | Set up Deno 2.9.x, run quality tasks/tests, compile both architectures, understand layer boundaries, and preserve source/binary parity. | `deno.json`; README; CI; architecture §§2.3–2.4, 10 | P2 |

## 5. Navigation and cross-linking

When the first content batch lands:

1. Configure an explicit Starlight sidebar in `docs/astro.config.mjs` using the section order above. Do not expose `plan.md` or `writing-guides.md` in site navigation; they are outside the content collection already.
2. Keep “Start here” before concepts and reference.
3. Put Advanced near the end, not in the beginner path.
4. At the bottom of each guide, add two or three useful next steps rather than a large generic link list.
5. Link repeated explanations to one canonical page. In particular, do not repeat the full TLS mode table, stack ownership model, or render transaction on every guide.

## 6. Delivery phases

### Phase 0 — Foundation

- [x] Replace the placeholder home page.
- [x] Add the explicit sidebar.
- [x] Add any shared terminology links or reusable Starlight components only when at least two pages need them. (None are needed by more than one published page yet.)
- [x] Confirm `npm run build` passes in `docs/`.

### Phase 1 — First successful stack (P0)

- [ ] Write all P0 Start here pages.
- [ ] Write `concepts/stacks.md` and `concepts/desired-state.md`.
- [ ] Write stack management, app management, TLS, and backup/restore P0 guides.
- [ ] Test every happy-path command against current `--help`; run a disposable-stack smoke flow where practical.
- [ ] Have a fresh reader follow the sequence without consulting the root README.

### Phase 2 — Routine operations (P1)

- [ ] Complete all P1 concept, stack, app, data, customization, and reference pages.
- [ ] Add troubleshooting links to each operational guide.
- [ ] Confirm examples cover both MySQL and PostgreSQL without making the first-app path branch excessively.
- [ ] Confirm multi-stack examples always show explicit stack roots and distinct stack names.

### Phase 3 — Advanced understanding

- [ ] Complete all P1 Advanced pages, then P2 Advanced pages.
- [ ] Include diagrams only where they clarify a path or boundary.
- [ ] For each technical decision, state context, decision, benefits, trade-offs, and rejected scope—not merely the selected technology.
- [ ] Cross-check all architecture statements against current rendered topology and tests, not specs alone.

### Phase 4 — Completeness and polish

- [ ] Complete remaining P2 pages that represent shipped behavior.
- [ ] Run link, site-build, spelling/style, and command verification passes.
- [ ] Search published pages for placeholders, duplicated content, stale defaults, and undocumented warnings.
- [ ] Compare the final guide set with the top-level `bento --help` command list; every operator command must have a guide or reference destination.
- [ ] Remove outdated operator instructions from the root README or replace them with links only in a separate, explicitly approved cleanup change.

## 7. Agent writing loop

Each agent should complete one page, or a small inseparable page pair, per loop.

1. **Claim:** Add or update the page in the progress ledger before writing. Include an owner or session identifier when multiple agents are active.
2. **Inspect:** Read `writing-guides.md`, the listed primary sources, current command `--help`, and relevant tests. Do not rely on memory.
3. **Outline:** Write the reader outcome and shortest safe path first. Decide what belongs in “Advanced” or a linked page.
4. **Draft:** Follow the appropriate page template from `writing-guides.md`.
5. **Verify:** Check command syntax, defaults, file paths, side effects, expected output, and recovery advice. Run safe examples against a temporary stack where practical.
6. **Connect:** Add sidebar placement, canonical cross-links, and next steps.
7. **Validate:** From `docs/`, run `npm run build`. Also run any relevant Bento tests if the page depends on subtle behavior.
8. **Report:** Update this plan with completion status and any unresolved fact. Keep unresolved claims out of published docs.

If implementation, README, and specs disagree, follow the conflict process in `writing-guides.md`; never silently combine contradictory behavior.

## 8. Page acceptance checklist

A page is complete only when all applicable items pass:

- [ ] The title and first paragraph state a reader outcome.
- [ ] Prerequisites are explicit and minimal.
- [ ] The shortest safe procedure appears before background detail.
- [ ] Every command and option exists in current CLI help or implementation.
- [ ] Examples use consistent placeholders and do not expose real secrets.
- [ ] A verification step tells the reader how to know the operation worked.
- [ ] Destructive, downtime, public exposure, and on-host-only effects are called out before the relevant command.
- [ ] Troubleshooting covers likely failures or links to a canonical page.
- [ ] Advanced detail is under an `## Advanced` heading or linked Advanced page.
- [ ] Repeated concepts link to their canonical page instead of being copied.
- [ ] Frontmatter title/description are useful in navigation and search.
- [ ] Internal links resolve and `npm run build` passes.
- [ ] The page was checked against the truth-source hierarchy in `writing-guides.md`.

## 9. Completion criteria and removal of this file

Delete `docs/plan.md` only after:

1. all P0 and P1 pages are published and pass the page acceptance checklist;
2. every top-level CLI command is discoverable from the docs;
3. the architecture and technical-decisions pages are published;
4. a clean `npm ci && npm run build` succeeds in `docs/`;
5. all internal links have been checked;
6. a command/example audit has no unresolved failures;
7. open documentation gaps are moved to the normal project issue tracker; and
8. `docs/writing-guides.md` has been updated to reflect lessons from the completed documentation set.

Do not delete `docs/writing-guides.md`; it is the permanent maintenance standard.
