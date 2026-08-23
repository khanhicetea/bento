# LLM-agent support plan

Status: proposed roadmap (not an implemented product contract)  
Date: 2026-08-23

## Executive decision

**OpenAPI and MCP are adapters, not alternatives to the existing oRPC/domain API.** Keep oRPC and the Zod contracts as Bento's typed application boundary. Add a deliberately small, versioned **automation surface** and expose that same surface through:

1. **OpenAPI 3.1** for broad compatibility with agent frameworks, generated clients, CI, and ordinary HTTP integrations.
2. **MCP** for agent hosts that support tool discovery, structured tool results, resources, and local stdio execution.

Start with OpenAPI plus a local stdio MCP adapter. Do **not** expose the whole current web router to agents, and do not make the current web server a public management API. Remote agent access is a later product/security milestone requiring an explicit revision of `specs/01-product-spec.md`, `specs/02-system-architecture.md`, and `specs/03-reimplementation-contract.md`.

The recommended sequence is:

```text
Bento domain services
        ^
        |
versioned automation operations + policy/approval layer
        |
        +-- oRPC automation router -- OpenAPIHandler --> local/remote HTTP API
        |
        +-- typed in-process client/facade -- MCP tools --> stdio, later Streamable HTTP
```

OpenAPI is enough when the chosen agent runtime can import an OpenAPI document or define HTTP tools. MCP is worthwhile for portable discovery across agent hosts and for a good local integration, but MCP does not provide authorization, approvals, idempotency, or infrastructure safety by itself.

## Why the current API must not be exported unchanged

Bento already has useful foundations:

- `packages/shared/src/contract.ts` composes browser-safe oRPC contracts backed by Zod.
- `apps/cli/src/server/router.ts` composes independently implemented domain routers.
- `apps/cli/src/server/server.ts` serves `/rpc` and defaults to loopback through `bento serve`.
- Domain routers call the same platform abstractions, state store, locks, render/apply path, and redaction helpers as the CLI.

However, the current API is a **browser control-plane API**, not an agent capability contract:

- Contracts lack stable HTTP route metadata, operation descriptions, error contracts, risk classification, authorization scopes, and idempotency keys. oRPC's OpenAPI handler would otherwise default procedures to `POST` and derive paths from router keys.
- `applications.databaseCredentials` returns a database password and must never be an agent tool or OpenAPI operation.
- `operations.setupCloudflareTunnel` accepts a secret token. Secret enrollment needs a separate trusted user channel, not model-visible arguments or results.
- Existing literal confirmation fields prove only that a caller can repeat a resource name. An LLM can do that automatically, so they are not human approval.
- `operations.apply`, stack/service actions, restore, app/proxy removal, deploy draining, maintenance, backups, and job changes have materially different risk and interruption profiles.
- Several operations are synchronous even though Compose, backup, restore, render/apply, and deployment can be long-running or partially successful.
- The web listener has optional Basic authentication, but the product specs explicitly say that Bento has no authenticated public management API and remains loopback-safe by default.
- The current API can create app state and materialize an app home, but it does not define a safe general source/release deployment operation. Bento intentionally does not own a fixed Git strategy.

Therefore, use an explicit allowlist. Never generate agent tools by reflecting every procedure in `webContract`, and never auto-publish future web procedures.

## Product boundaries

Agent support must preserve these existing contracts:

- One operator-owned host and one selected stack per Bento process.
- No Kubernetes, multi-host scheduler, hostile tenancy claim, or arbitrary-language platform expansion.
- No raw Docker socket, raw Compose, shell, SQL, filesystem, or `bento exec` tool for the model.
- No `compose down -v`, implicit relational data deletion, automatic password rotation, or weakened destructive confirmations.
- No secrets in model context, tool schemas, tool results, logs, traces, URLs, command arguments, snapshots, or operation records.
- Generated stack output remains immutable; tools change desired source state or invoke the owning service.
- Source and compiled binaries must implement equivalent operations.
- The model may propose and orchestrate, but Bento remains the authority for validation, authorization, conflict checks, and safety policy.

“Manage infra” should mean **manage the Bento stack within these boundaries**, not administer the host arbitrarily.

## Target architecture

### 1. A versioned automation contract

Add a separate domain, for example:

```text
packages/shared/src/domains/automation/
  contract.ts
  schema.ts
  metadata.ts
apps/cli/src/server/domains/automation/router.ts
apps/cli/src/automation/
  operations.ts
  policy.ts
  approvals.ts
  records.ts
```

The contract should contain only supported agent operations. Each operation needs:

- stable `operationId` and versioned input/output schemas;
- concise, action-oriented descriptions and examples;
- risk level: `read`, `safe-write`, `disruptive`, `destructive`, or `secret`;
- required scope and whether interactive approval is mandatory;
- timeout and asynchronous-operation behavior;
- idempotency semantics;
- structured warnings, partial-result, error category, and recovery guidance;
- secret classification for every field;
- audit/event category.

Do not duplicate business logic in adapters. Initially extract router-owned orchestration into small operations where necessary, then have the browser oRPC router and automation router call the same operation. Keep validation at the shared Zod boundary and invariants in domain/services.

The automation contract should have an independent compatibility version from the desired-state schema and MCP protocol version.

### 2. Stable result and error envelopes

Implement the versioned machine contract proposed by F-06/T-07 before allowing writes. A typical result should include:

```json
{
  "schemaVersion": "1",
  "operationId": "op_...",
  "correlationId": "...",
  "status": "succeeded",
  "result": {},
  "warnings": [],
  "recovery": null,
  "startedAt": "...",
  "completedAt": "..."
}
```

Errors must distinguish validation, not-found, conflict/drift, authorization, approval-required, safety refusal, service failure, platform failure, timeout, cancellation, and partial result. Unknown errors must be generic and redacted. HTTP status, oRPC errors, and MCP tool execution errors should map from this one internal error model.

### 3. Plans and approvals are the write boundary

Implement the F-03 plan concept before broad mutation access:

1. An agent requests a plan, such as `plan_application_create`.
2. Bento returns a redacted immutable diff, durable side effects, validators, reload/recreate targets, risk, expected disruption, expiration, and digest.
3. The user approves the exact digest through a trusted UI/CLI channel outside model-controlled arguments.
4. The agent calls `execute_plan` with the opaque plan ID and an idempotency key.
5. Bento verifies actor, scopes, approval, expiry, one-time use, stack identity, and all drift-bound inputs before execution.

An approval is server-side state, not a caller-provided `confirmation: "app-name"`. The agent must not be able to mint or approve its own destructive plan. Safe writes may be pre-approved by an operator policy, but disruptive and destructive operations should remain explicit by default.

Plans must be opaque handles bound to the authenticated actor and stack. Possession of a plan ID must not grant authority. Use short expiry, unguessable IDs, one-time execution, and state/custom/assets digests to prevent TOCTOU mistakes.

### 4. Idempotency, conflicts, and long-running operations

Every mutating request needs a caller-generated idempotency key scoped to actor + stack + operation. Replays return the original operation record; the same key with different input is a conflict.

Long operations should return a durable, bounded operation ID rather than hold an HTTP/MCP request open indefinitely:

- `get_operation(operationId)` returns queued/running/succeeded/failed/cancelled and structured progress.
- `cancel_operation` is offered only where cancellation is actually safe.
- Records contain redacted summaries, not subprocess streams or credentials.
- Operations serialize through existing stack/data locks and expose conflicts rather than racing.

Use this common polling model first. MCP Tasks can be added later only after client support is sufficiently portable; it should map to the same internal operation record rather than create a second job system.

## Initial capability catalog

Names below are illustrative. Final names and schemas should be tested with target agents.

### Phase-A read tools

| Capability                              | Source                     | Notes                                                                 |
| --------------------------------------- | -------------------------- | --------------------------------------------------------------------- |
| `get_stack_overview`                    | operations overview/status | Bounded, redacted, no credentials                                     |
| `list_applications` / `get_application` | applications               | Separate compact detail output from form-option data                  |
| `get_routing_overview`                  | routing                    | Redact private path details where not needed                          |
| `get_jobs_overview`                     | jobs                       | No raw command expansion beyond bounded validated argv metadata       |
| `get_data_overview`                     | data                       | No passwords or admin connection material                             |
| `run_doctor`                            | operations                 | Return structured findings and recovery actions                       |
| `get_service_logs`                      | operations logs            | Bounded tail, strict service allowlist, redaction and output-size cap |
| `get_operation`                         | new operation records      | Actor/stack authorization on every read                               |

### Phase-B planned writes

| Capability                                             | Policy                                                                          |
| ------------------------------------------------------ | ------------------------------------------------------------------------------- |
| `plan_application_create` / `plan_application_update`  | Show identity, domain, runtime, DB side effects, generated changes, and reloads |
| `plan_application_enable` / `plan_application_disable` | Disruption classification required                                              |
| `plan_database_add`                                    | Never imply data migration; preserve existing bindings                          |
| `plan_proxy_save` / `plan_proxy_enable`                | Validate domain ownership and upstream boundary                                 |
| `plan_cron_save` / `plan_worker_save`                  | Preserve argv-vs-shell distinction; no arbitrary host command                   |
| `plan_stack_apply`                                     | Explain validators and every reload/recreate target                             |
| `create_backup`                                        | Write operation but not destructive; report artifact metadata only              |
| `execute_plan`                                         | Requires server-side policy/approval and idempotency                            |

### Phase-C deployment

“Auto deploy” needs a product decision and a dedicated abstraction. Do not expose shell/exec as a shortcut.

Recommended model:

- An app has an operator-configured, named **deployment profile** or existing app-owned deploy hook.
- Agent tools can inspect whether deployment is configured, submit a bounded deployment request, and inspect its status/log summary.
- Source credentials are enrolled through a non-model secret channel and referenced by opaque credential IDs.
- A request records source revision/artifact digest, app, plan digest, timeout, and idempotency key.
- Execution remains app-scoped, under the app UID/GID, using the existing queue/one-active-job behavior and bounded logs.
- The result reports the immutable deployed revision and OPcache-reset warning without revealing hook environment or payload secrets.

Before implementation, decide whether Bento supports only existing operator hooks, a constrained Git deployment profile, signed artifact upload, or more than one. Update product specs for the selected behavior. The safest first release is “invoke an already configured operator hook”; it retains Bento's current non-goal of owning a fixed Git strategy.

### Never expose as model-controlled tools

- database/admin/Redis passwords or `applications.databaseCredentials`;
- Cloudflare tunnel token enrollment or other secret values;
- interactive shell, arbitrary argv, shell strings, SQL, raw Compose, or Docker socket access;
- arbitrary filesystem read/write/upload paths;
- app/data prune, volume deletion, raw restore replacement, transfer import, or host-level permissions repair until dedicated plans and mandatory trusted approval exist;
- support bundles or unrestricted logs without structural redaction and bounds.

## OpenAPI adapter

### Implementation

- Add exact, compatible `@orpc/openapi` and Zod converter dependencies using Bun and commit `bun.lock`.
- Add explicit `.route(...)` metadata to the automation contract: HTTP method, versioned path, stable operation ID, summary, description, tags, success status, and documented errors.
- Generate OpenAPI 3.1 from the **automation contract/router**, not `webContract`.
- Add an `OpenAPIHandler` for execution under a versioned prefix such as `/api/automation/v1`.
- Publish the schema as both a deterministic build/test artifact and, when HTTP automation is enabled, `GET /api/automation/v1/openapi.json`.
- Put a conservative size limit on requests and responses. Disable browser CORS unless a specific trusted origin is configured.
- Filter/allowlist procedures in both generation and handling as defense in depth; fail tests if the two sets differ.

A CLI command such as `bento automation schema --format openapi` can emit the document without starting a resident server. This is useful for client generation and keeps schema discovery compatible with the current host-local architecture.

### Why OpenAPI first

- It reuses oRPC's supported handler/generator path and current Zod schemas.
- It is easy to contract-test and usable by non-LLM automation too.
- Most agent SDKs can wrap HTTP operations even when they do not support MCP.
- It forces stable operation descriptions, errors, and versioning before introducing a second protocol.

OpenAPI does not define model-specific approval behavior. The server-side plan/policy layer must enforce it regardless of what an agent UI claims to confirm.

## MCP adapter

### Local stdio first

Add a command such as:

```text
bento --stack /srv/bento mcp
```

The command should migrate/load the selected stack through the normal context, then connect an MCP server over stdio. It must import only the automation router/facade—not `apps/cli/src/server/server.ts`, whose top-level web asset imports would unnecessarily require a web build.

Requirements:

- stdout is exclusively MCP protocol traffic; diagnostics go to stderr and tool results;
- one explicit stack per process, inherited from `--stack`/`BENTO_STACK_ROOT`;
- register only the automation allowlist;
- expose read operations as tools initially; optional resources can later represent redacted stack/app/operation views;
- provide structured output conforming to the same result schemas;
- validate all inputs again at execution and map domain errors to actionable MCP tool errors;
- close cleanly on stdin EOF and signals;
- pin the then-current official TypeScript MCP server SDK exactly and verify Bun 1.4 plus compiled-binary compatibility in a spike before adopting it.

The official TypeScript SDK currently advertises Bun support. Its current stable line and package layout should be rechecked at implementation time because MCP and its SDK are evolving. Avoid a repo-wide schema-library migration merely for MCP; adapt the shared JSON Schemas or isolate the compatibility layer if required.

### Remote MCP later

Remote MCP uses Streamable HTTP and shares the same authorization/policy gateway as OpenAPI. Do not run a second authorization implementation. MCP scopes select tools/resources, but every call still needs server-side authorization and stack binding.

Do not dynamically translate every OpenAPI operation into an MCP tool. Explicit MCP registration permits safer names/descriptions, risk annotations, result presentation, and resources while preserving the same underlying operation IDs and schemas.

## Authentication and remote exposure

This is a separate milestone, not a flag on the current `bento serve` command.

### Local modes

- Keep web/OpenAPI HTTP loopback-only by default.
- Prefer stdio MCP for an agent running on the same trusted operator machine.
- Treat ability to launch `bento mcp` as equivalent to CLI access to that stack; use OS user/file permissions and, where practical, a restricted service account.
- Basic auth may remain a web convenience but is not the remote agent security design.

### Remote mode prerequisites

Before exposing either OpenAPI or MCP remotely:

- revise the product/architecture/security specs and threat model;
- terminate TLS and require OAuth 2.1/OIDC-style access tokens with audience validation, expiry, revocation, and narrowly defined scopes;
- bind every token, plan, operation, and idempotency record to an actor and allowed stack(s);
- separate scopes such as `stack:read`, `apps:plan`, `deploy:request`, `operations:execute`, and tightly gated destructive scopes;
- support step-up authorization/consent instead of issuing one permanent admin token;
- rate limit per actor/tool and cap concurrency, request bytes, result bytes, logs, and operation retention;
- write a private bounded audit/event record for discovery, plan, approval, execution, denial, and result;
- never put bearer tokens in query strings, logs, model messages, or downstream calls;
- reject token passthrough and validate token audience for the Bento resource;
- configure exact trusted origins where browser access is needed; add CSRF protections to browser-authenticated mutations;
- retain loopback as the default and refuse non-loopback startup without an explicit secure configuration.

Cloudflare Tunnel is transport, not authorization. Cloudflare Access can be one deployment option, but Bento must validate an application identity/token rather than trust that a request arrived through a tunnel. Direct listener exposure must remain independently safe.

## Delivery phases and exit gates

### Phase 0 — Approve product and threat model

Deliverables:

- Define supported agent journeys: inspect, create app, configure route/runtime/data, request deploy, diagnose, and recover.
- Decide deployment profile behavior and what “manage infra” excludes.
- Classify every candidate operation and field by risk/secrecy.
- Define local-only versus remote product scope and actor model.
- Approve spec changes before remote implementation.

Exit gate: no operation has an ambiguous owner, secret flow, approval rule, or recovery boundary.

### Phase 1 — Stable automation core and read-only OpenAPI

Deliverables:

- Versioned result/error envelope (F-06/T-07).
- Separate explicit automation contract and capability metadata.
- Refactor shared operation orchestration out of transport routers where needed.
- OpenAPI document generation and schema drift/golden tests.
- Read-only automation HTTP handler, loopback-only.

Exit gate: generated OpenAPI is deterministic; every exposed operation is allowlisted, bounded, redacted, and covered by contract tests; secret procedures are absent.

### Phase 2 — Local MCP read-only

Deliverables:

- `bento mcp` stdio command using the same automation operations.
- Read tools, structured results, protocol-clean stdout, clean lifecycle.
- MCP Inspector/smoke tests plus compiled-binary parity tests.

Exit gate: source and compiled modes list/call the same tools, malformed input cannot reach services, and no secret appears in fixtures or traces.

### Phase 3 — Plans, approvals, idempotency, and operation records

Deliverables:

- F-03 immutable plans and drift detection.
- Trusted approval channel in CLI/web UI.
- Idempotency store and bounded asynchronous operation records.
- Initial safe-write tools through both OpenAPI and MCP.
- Audit/event records with redaction and retention policy.

Exit gate: no agent mutation bypasses policy; replay and concurrent calls are deterministic; destructive plans cannot be self-approved by the agent.

### Phase 4 — Constrained application creation and deployment

Deliverables:

- Planned app-create/update flow.
- Operator-configured deployment profiles or existing-hook invocation.
- Opaque credential references and separate secret enrollment.
- Deployment request/status/log-summary tools.
- Failure and partial-result recovery tests.

Exit gate: a test agent can create and deploy a fixture app without shell/raw Compose access, credential disclosure, or weakening app identity and deploy queue invariants.

### Phase 5 — Authenticated remote access (optional)

Deliverables:

- Revised authoritative specs and threat model.
- Shared OAuth/resource-server gateway for OpenAPI and remote MCP.
- TLS, audience/scopes, step-up authorization, revocation, rate limits, audit, and incident guidance.
- Remote conformance and adversarial security testing.

Exit gate: non-loopback mode refuses insecure configuration; cross-user/stack access, replay, token passthrough, plan-handle theft, prompt-injected secret requests, and approval bypass are tested.

## Testing and release gates

Add tests near the existing boundaries:

- `packages/shared`: schema snapshots, operation metadata completeness, input/output examples, and secret-field denylist tests.
- `apps/cli/tests/unit/web_server_test.ts`: OpenAPI document route, handler matching, request limits, loopback/auth behavior, and security headers.
- New automation unit tests: policy matrix, error mapping, idempotency, plan drift/expiry/one-time use, operation ownership, redaction, and result bounds.
- New MCP tests: initialize/list/call, deterministic tool order, malformed arguments, structured errors/results, stdout purity, EOF/signal shutdown, and no secret tools.
- `apps/cli/tests/contract/parity_test.ts`: compiled OpenAPI schema equality and compiled stdio MCP handshake/tool smoke from outside the repository.
- Integration tests: concurrent plans/execution, lock conflicts, render/apply rollback, Compose/service failure, backup/deploy partial results, cancellation boundaries, and audit retention.
- Security tests: auth scope matrix, actor/stack isolation, replay, guessed plan/operation handles, prompt-injected arbitrary commands, output/log exfiltration, token redaction, and non-loopback refusal.

Use the repository gates and report unavailable integration checks honestly:

```bash
bun run fmt
bun run lint
bun run check
bun run test
bun run test:integration
bun run test:parity
```

Also test both supported compiled architectures before release and keep all new dependency versions exact with committed `bun.lock` changes.

## Measures of success

- An agent can discover supported operations without seeing secrets or unsupported admin capabilities.
- The same operation IDs, schemas, policy decisions, and results are observed through OpenAPI and MCP.
- Read-only use requires no resident/public control plane when stdio is selected.
- Mutations are idempotent, conflict-aware, auditable, and plan-bound.
- Disruptive/destructive actions cannot occur without the configured trusted approval path.
- App deployment uses a constrained Bento-owned operation, never arbitrary shell access.
- Source and compiled builds remain equivalent.
- Remote exposure, if shipped, is authenticated and least-privilege rather than merely tunnelled.

## Open decisions for the product owner

1. Is the first supported agent local to the Bento host/operator workstation, or must it be a hosted remote agent?
2. Should remote access be single-operator only, or are multiple actors/teams required?
3. Which mutations may an operator pre-approve, if any?
4. Is “deploy” limited to invoking an existing operator hook, or should Bento define constrained Git/artifact deployment profiles?
5. Should an agent ever receive application connection credentials? Recommended answer: **no**; use opaque secret references and perform dependent work inside trusted Bento/app execution paths.
6. What operation/audit retention is acceptable on the stack root?
7. Which agent hosts must be supported in the first release? Use them to validate OpenAPI import and MCP protocol/version compatibility before fixing the public contract.

## Primary references

- Bento baseline: [`specs/01-product-spec.md`](specs/01-product-spec.md), [`specs/02-system-architecture.md`](specs/02-system-architecture.md), and the F-03/F-06 proposals in [`specs/04-proposed-product-enhancements.md`](specs/04-proposed-product-enhancements.md).
- oRPC OpenAPI handler: https://orpc.dev/docs/openapi/openapi-handler
- oRPC OpenAPI generation and operation metadata: https://orpc.dev/docs/openapi/openapi-specification
- oRPC OpenAPI routing defaults: https://orpc.dev/docs/openapi/routing
- MCP architecture and transports: https://modelcontextprotocol.io/docs/2026-07-28/learn/architecture
- MCP server tools, structured outputs, human-in-the-loop and security guidance: https://modelcontextprotocol.io/specification/2026-07-28/server/tools
- MCP authorization: https://modelcontextprotocol.io/specification/latest/basic/authorization
- MCP security best practices: https://modelcontextprotocol.io/docs/2026-07-28/tutorials/security/security_best_practices
- Official TypeScript SDK (currently documents Bun support): https://github.com/modelcontextprotocol/typescript-sdk
