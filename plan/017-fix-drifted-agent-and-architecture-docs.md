# Plan 017: The agent rules, architecture invariants and backlog say what the code actually does

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- AGENTS.md apps/backend/AGENTS.md apps/backend/docs/architecture.md BACKLOG.md apps/web/src/lib/format.ts apps/backend/internal/operations/claims.go`
> If any changed, compare the "Current state" excerpts against the live files; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: docs
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

This repository is worked on by coding agents that are told to follow `AGENTS.md` exactly. Four statements in the
written rules have drifted from the code, each in a direction that weakens a safety property:

1. The list of exact confirmation phrases agents must "never weaken" omits three that the code enforces.
2. The list of operation kinds that run in parallel omits about fifteen kinds, so someone writing a restic or image
   handler is not warned that it shares state with concurrent operations.
3. The architecture document's security invariants say no API token scope reaches exec or credentials, while another
   section of the same document says the `admin` scope is "exec-level" (it can create scheduler jobs, write app
   files and change the app's start command).
4. `BACKLOG.md` says the real-Docker test suite does not run in GitHub Actions; the workflow runs it.

A fifth, small item is in the UI: the map of operation labels names kinds that do not exist and misses real ones.

## Current state

1. **Confirmations.**
   - `AGENTS.md` line 67: "- Never weaken exact confirmations (`delete <slug>`, `delete`, `replace <db>`, `export`, `delete <host>`)."
   - `apps/backend/AGENTS.md` rule 8 (line 74): "**Exact confirmations are enforced server-side** in `accept.go`
     (`delete <slug>`, `delete`, `replace <db>`, `export`, `delete <host>`). Never loosen them or move them only to the UI/CLI."
   - What the code enforces (every site that returns `ErrConfirmation`, all under
     `apps/backend/internal/operations/`):

     | Phrase | Action | File |
     |---|---|---|
     | `delete <slug>` | remove an app | `accept.go` |
     | `delete` | prune a removed app's retained data | `accept.go` |
     | `delete` | delete a Docker image | `images.go` |
     | `delete <host>` | delete an ingress host | `accept.go` |
     | `export` | export the stack | `transfer.go` |
     | `replace <db>` | restore a database from backup | `restic_restore_db.go` |
     | `clone <slug>` | create an app from a backup | `restic_clone.go`, `restic_remote.go` |
     | `disconnect <slug>` | point an app's backups at a different repository | `restic.go` |
     | `export <slug>` | export an app's backup encryption key | `restic.go` |

     Re-derive this table yourself with
     `grep -rn "ErrConfirmation" apps/backend/internal --include=*.go | grep -v _test` and read each site.
2. **Parallel kinds.** `apps/backend/AGENTS.md` line 84: "Handlers of parallel kinds
   (`app.reconcile|start|restart|update|deploy|stop`, `service.create|reconcile|configure|restart|remote`) share the
   edge, the network plan and image builds only through …". The source of truth is `claimsFor` in
   `apps/backend/internal/operations/claims.go`: every kind with a `case` there is parallel; at the planned commit
   that also includes `restic.backup|init|connect|refresh|key-add|key-remove|check|unlock|inspect|inspect-remote`,
   `app.clone-from-backup`, `app.restore-from-backup`, and `image.prepare|rebuild|rollout|rollout-stop|sweep`. The
   test `TestOnlyReviewedKindsRunInParallel` in `parallel_test.go` pins the set.
3. **Scope invariant.** `apps/backend/docs/architecture.md` line 883 (section listing security invariants):
   "The management API is loopback-only and requires a session, a peer-verified local socket, or a scoped API token.
   No token scope reaches deletes, restores, exec, terminals, or credentials." And line 710 (scheduler section):
   "A job is an arbitrary command as the app, so admin is exec-level here." The route-to-scope table is
   `routeScopes` in `apps/backend/internal/api/scopes.go`.
4. **Backlog.** `BACKLOG.md` (in "Decisions"): "- The real-Docker suite (`make -C apps/backend test-integration`) runs
   on the local dev host, not in GitHub Actions. CI (`.github/workflows/ci.yml`) runs only on tags and releases."
   `.github/workflows/ci.yml` has a step "Real Docker integration tests" that runs
   `sudo … make -C apps/backend test-integration`, on tags and published releases.
5. **UI labels.** `apps/web/src/lib/format.ts`, `describeOp`, has a `verbs` map with entries for `app.create`,
   `edge.update`, `proxy.upsert` and `proxy.remove`, none of which is an operation kind in the backend; real kinds
   such as `app.provision`, `app.deploy`, `edge.apply`, `tunnel.apply` have no entry and fall back to the raw kind
   with dots replaced by spaces. The label is rendered as `` `${verb} ${targetLabel ?? op.targetId}` ``.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Frontend checks (step 5 only) | `bun run fmt:check && bun run lint && bun run check` | exit 0 |
| Frontend tests, if present | `bun run test` | all pass |
| List confirmation sites | `grep -rn "ErrConfirmation" apps/backend/internal --include=*.go \| grep -v _test` | the definition in `accept.go` plus the enforcing sites in the table below |

## Scope

**In scope**:
- `AGENTS.md`, `apps/backend/AGENTS.md`, `apps/backend/docs/architecture.md`, `BACKLOG.md`
- `apps/web/src/lib/format.ts` (the `verbs` map only), and `apps/web/src/lib/format.test.ts` if it exists

**Out of scope**:
- Any Go code. This plan documents behaviour; it does not change scopes, confirmations or claims.
- Changing what the `admin` scope may do. Whether `admin` should lose file writes / job edits is a product decision
  recorded in `plan/README.md`, not part of this plan.
- `apps/docs/` (operator documentation).

## Git workflow

- Branch: `advisor/017-fix-drifted-agent-and-architecture-docs`
- One commit, plain imperative sentence, e.g. `Bring the agent rules and security invariants in line with the code`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Confirmations

- `AGENTS.md` line 67 → "- Never weaken exact confirmations (`delete <slug>`, `delete`, `delete <host>`, `export`,
  `export <slug>`, `replace <db>`, `clone <slug>`, `disconnect <slug>`)."
- `apps/backend/AGENTS.md` rule 8 → "**Exact confirmations are enforced server-side** by the acceptance functions in
  `internal/operations` (every site that returns `ErrConfirmation`): `delete <slug>`, `delete`, `delete <host>`,
  `export`, `export <slug>`, `replace <db>`, `clone <slug>`, `disconnect <slug>`. Never loosen them or move them only
  to the UI/CLI."

Keep line length and Markdown style consistent with the surrounding text (lines wrap at about 120 characters).

**Verify**: `grep -n "disconnect <slug>" AGENTS.md apps/backend/AGENTS.md` → one match in each file.

### Step 2: Parallel kinds

Read `claimsFor` in `claims.go` and list every kind constant that appears in a `case`, translated to its string
value (the constants are defined as `KindX = "a.b"` across `apps/backend/internal/operations/*.go`; find each with
`grep -rn 'Kind[A-Za-z]* *= *"' apps/backend/internal/operations --include=*.go | grep -v _test`).

In `apps/backend/AGENTS.md` replace the parenthesised list on line 84 so the sentence reads:
"Handlers of parallel kinds (every kind with a case in `claimsFor`, pinned by `TestOnlyReviewedKindsRunInParallel`;
today `app.reconcile|start|restart|update|deploy|stop|clone-from-backup|restore-from-backup`,
`service.create|reconcile|configure|restart|remote`, `restic.*` except `restic.recover` and `restic.restore-db`,
and `image.prepare|rebuild|rollout|rollout-stop|sweep`) share the edge, …" — but write the list **you derived**, not
this sentence verbatim: if your derivation differs from it (for example a kind listed here has no case, or
`restic.restore-db` does have one), your derivation wins.

**Verify**: every kind string in your new sentence appears as a `case` constant in `claimsFor`, and every `case`
constant appears in the sentence (check them off one by one; state the count in your report).

### Step 3: Scope invariant

In `apps/backend/docs/architecture.md`, replace the sentence "No token scope reaches deletes, restores, exec,
terminals, or credentials." with wording that is true for all three scopes. First read `routeScopes` in
`apps/backend/internal/api/scopes.go` to confirm what `admin` may call, then write, in the document's own plain style:

"No token scope reaches deletes, restores, the exec and terminal endpoints, or credential endpoints. The `read` and
`operate` scopes cannot run code. The `admin` scope can: it may create and edit scheduler jobs, write files under
`~/app`, and change an app's runtime command, each of which runs code as the app and so can read that app's secrets.
Treat an admin token as code execution on every app."

Adjust the three examples to what `routeScopes` actually grants `admin`; drop any that is not there.

**Verify**: `grep -n "Treat an admin token as code execution" apps/backend/docs/architecture.md` → one match; and
`grep -n "exec-level" apps/backend/docs/architecture.md` still matches line ~710 (unchanged).

### Step 4: Backlog

In `BACKLOG.md` replace the bullet quoted in "Current state" item 4 with:
"- CI (`.github/workflows/ci.yml`) runs only on tags and published releases, not on every push; it includes the
real-Docker suite (`make -C apps/backend test-integration`). Day to day, that suite runs on the local dev host."

**Verify**: `grep -n "not in GitHub" BACKLOG.md` → no matches.

### Step 5: UI operation labels

In `apps/web/src/lib/format.ts`, in the `verbs` map of `describeOp`: keep every existing entry (old kinds may exist
in a development stack's history), and add entries for real kinds that are missing. Derive the list from the kind
constants (same grep as step 2). Use a present participle that reads correctly when followed by the target
(`` `${verb} ${target}` ``), following the existing entries, for example:

```ts
"app.provision": "Creating",
"app.deploy": "Deploying",
"app.reconcile": "Repairing",
"app.prune": "Deleting retained data of",
"edge.apply": "Applying",
"tunnel.apply": "Applying",
"service.create": "Creating",
"service.restart": "Restarting",
"restic.backup": "Backing up",
```

Add only kinds that exist; do not invent any. For kinds you are unsure how to phrase, leave them to the fallback.
Copy rule from `AGENTS.md`: no emoji; plain words. If `apps/web/src/lib/format.test.ts` exists, add one assertion for
`describeOp({ kind: "app.deploy", targetId: "app_1" }, "shop")` → `"Deploying shop"`.

**Verify**: `bun run fmt:check && bun run lint && bun run check` → exit 0 (run `bun run fmt` first); `bun run test`
passes if the script exists.

## Test plan

Documentation changes have no tests. Step 5 adds one assertion when the test file exists.

## Done criteria

- [ ] The greps in steps 1, 3 and 4 give the stated results
- [ ] The report lists the derived parallel kinds and confirms they match `claimsFor` one to one
- [ ] `git diff --stat` shows no `.go` file changed
- [ ] `bun run fmt:check && bun run lint && bun run check` exits 0
- [ ] `plan/README.md` status row updated

## STOP conditions

- The confirmation grep finds a phrase not in the table above, or a table row has no code site — report the
  difference instead of documenting a guess.
- `routeScopes` does not grant `admin` any of the three code-execution paths (then the architecture sentence is
  right and line 710 is the stale one) — report.
- `bun` is unavailable: do steps 1–4, skip step 5's verification and say so.

## Maintenance notes

- The durable fix for list drift is to stop repeating lists: both agent files now point at the code site
  (`ErrConfirmation`, `claimsFor` + its test). Keep that when editing them.
- Open decision for the maintainer (not done here): whether hiding env values from `admin` callers is worth keeping
  given `admin` can run code as the app, or whether code-execution paths should move to a separate scope.
