# Plan 007: The web UI has a first unit-test layer and lint rules that enforce the repository's React rules

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- package.json eslint.config.js bun.lock .github/workflows apps/web/src/lib apps/web/src/api/client.ts apps/web/src/features/applications/EnvEditor.tsx`
> If any of these changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none (plans 008, 009, 015, 016 and 020 depend on this one)
- **Category**: tests / dx
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

`BACKLOG.md` states "The web UI has no automated tests". There is no `test` script and no test step in CI. The lint
configuration has no React hooks rules, so a missing effect dependency passes CI, and the repository's written ban on
`useMemo`, `useCallback` and `React.memo` holds only by convention. Two real UI bugs found in the same audit (a log
stream that drops lines, stale screens after operations) would have been flagged or testable with this baseline. This
plan adds the cheapest useful layer: Bun's built-in test runner (no new runtime dependency), three test files over
pure logic, the hooks lint rules, and CI wiring.

## Current state

- Toolchain: Bun 1.4.0 (`mise.toml`), root `package.json` scripts:

  ```json
  "lint": "bunx eslint apps/web/src",
  "check": "bunx tsc --noEmit --project apps/web/tsconfig.json",
  "ci": "bun install --frozen-lockfile && bun run fmt:check && bun run lint && bun run check && bun run web:build && make -C apps/backend ci"
  ```
  `@types/bun` 1.4.0 is already a root devDependency, and `tsconfig.json` has `"types": ["bun"]`. Dependency
  versions in this repository are pinned exactly (no `^` or `~`) and `bun.lock` is committed.
- `eslint.config.js` (whole file):

  ```js
  import js from "@eslint/js";
  import tseslint from "typescript-eslint";

  export default tseslint.config(
    { ignores: ["dist/**", "**/dist/**", "bento/**", "apps/web/src/api/generated/**"] },
    js.configs.recommended,
    ...tseslint.configs.recommended,
    {
      files: ["apps/web/**/*.{ts,tsx}"],
      rules: {
        "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      },
    },
  );
  ```
- `.github/workflows/ci.yml` frontend step runs: `bun install --frozen-lockfile`, `bun run fmt:check`, `bun run lint`,
  `bun run check`, `bun run web:build`. `.github/workflows/release.yml` "Gates" step runs
  `bun run fmt:check && bun run lint && bun run check`.
- Pure logic worth testing first:
  - `apps/web/src/lib/format.ts` — `formatRelative`, `formatBytes`, `formatDuration`, `describeOp` (and others; read
    the file for exact names and behaviour).
  - `apps/web/src/features/applications/EnvEditor.tsx` — exported `envToText(env)` and `envFromText(text)`:

    ```ts
    export function envToText(env: T.EnvVar[]): string {
      return env.filter((e) => e.key.trim() !== "").map((e) => `${e.key}=${e.value}`).join("\n");
    }
    /** Parse dotenv-style text: blank lines and # comments are skipped, `export ` and matching quotes are stripped. */
    export function envFromText(text: string): T.EnvVar[] { … }
    ```
  - `apps/web/src/api/client.ts` — `request` (not exported) behind the exported `api` object, and `ApiError`:
    a network failure throws `new ApiError(0, null, "The Bento backend is unreachable.")`; a 204 returns `undefined`;
    a non-OK response throws `ApiError(status, envelope?.error ?? null, …)`; non-GET requests send `X-CSRF-Token`;
    mutations made through `mutate` send an `Idempotency-Key` header starting with `web-`.
- Repository rules that apply (from `AGENTS.md`): "Function components and React 19 APIs. Do not add `useMemo`,
  `useCallback`, or `React.memo`." and "`apps/web/src/api/generated/types.ts` is generated — never edit or reformat it."
- Prettier covers `apps/web/src` (`bun run fmt:check`); new test files must be formatted (`bun run fmt`).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Install | `bun install --frozen-lockfile` | exit 0 |
| Format | `bun run fmt` then `bun run fmt:check` | exit 0 |
| Lint | `bun run lint` | exit 0 |
| Typecheck | `bun run check` | exit 0 |
| Tests | `bun run test` (added by this plan) | all pass |
| Build | `bun run web:build` | exit 0 |

## Scope

**In scope**:
- `package.json`, `bun.lock`, `eslint.config.js`
- `.github/workflows/ci.yml`, `.github/workflows/release.yml` (one added line each)
- `apps/web/src/lib/format.test.ts` (create)
- `apps/web/src/lib/env.ts` and `apps/web/src/lib/env.test.ts` (create)
- `apps/web/src/features/applications/EnvEditor.tsx` (only to import the two moved functions)
- `apps/web/src/api/client.test.ts` (create)
- Files that need an `eslint-disable-next-line` comment for a pre-existing hooks violation (step 4)
- `BACKLOG.md` (one line), `AGENTS.md` ("Tests and required checks" command line)

**Out of scope**:
- Fixing the behaviour the tests characterize. In particular `envFromText` strips quotes and whitespace from values;
  plan 020 fixes that. Here you only record current behaviour.
- Component/DOM tests (no `happy-dom`, no testing-library) — pure functions only.
- `apps/web/src/api/generated/types.ts`.

## Git workflow

- Branch: `advisor/007-web-test-baseline-and-hooks-lint`
- Commit per step is fine; plain imperative sentences, e.g. `Add unit tests for the web UI's pure logic`,
  `Lint React hooks and the banned memo APIs`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Add the test script

In root `package.json` add `"test": "bun test apps/web/src"` to `scripts`, and insert `&& bun run test` after
`bun run check` in the `ci` script.

**Verify**: `bun run test` → exits 0 or reports "no tests found" (either is acceptable at this point; note which).

### Step 2: Move the env helpers to a pure module and test them

Create `apps/web/src/lib/env.ts` containing `envToText` and `envFromText` moved verbatim from `EnvEditor.tsx`
(with the `T` type import they need — copy the import style used at the top of `EnvEditor.tsx`). In `EnvEditor.tsx`
delete the two function bodies and add `export { envToText, envFromText } from "../../lib/env.ts";` plus an import for
its own use. Run `grep -rn "envToText\|envFromText" apps/web/src` and make sure every importer still resolves.

Create `apps/web/src/lib/env.test.ts` using `import { describe, expect, test } from "bun:test";` with cases:
- `envToText` skips rows with a blank key and joins `KEY=value` with newlines.
- `envFromText` skips blank lines and `#` comments, strips a leading `export `, splits on the first `=`
  (`A=b=c` → value `b=c`), and treats a line without `=` as a key with empty value.
- Round trip of plain values: `envFromText(envToText(rows))` equals `rows` for values without quotes or
  leading/trailing spaces.
- Characterization of the known defect, clearly labelled:
  `test("KNOWN DEFECT (plan 020): quoted and padded values are altered", …)` asserting the **current** results:
  value `"x"` (with the quote characters) comes back as `x`; value ` padded ` comes back as `padded`.

**Verify**: `bun test apps/web/src/lib/env.test.ts` → all pass. `bun run check` → exit 0.

### Step 3: Test `format.ts` and `client.ts`

`apps/web/src/lib/format.test.ts`: read `format.ts` first and write 2–4 cases per exported pure function, asserting
what the code does today. For anything relative to "now", use `setSystemTime` from `bun:test` (and reset it with
`setSystemTime()` in `afterEach`). For `describeOp`, cover a known kind (`app.start` with a target label), an unknown
kind (falls back to the kind with dots replaced by spaces), and the no-target case.

`apps/web/src/api/client.test.ts`: stub `globalThis.fetch` per test (save the original and restore it in `afterEach`).
Cover:
- A 204 response resolves to `undefined` (use any `api` function that performs a non-GET request).
- A 200 JSON response resolves to the parsed body (e.g. `api.operations.get("op_1")`), and the request path is
  `/api/v1/operations/op_1`.
- A 409 with body `{"error":{"code":"conflict","message":"nope","fields":[]}}` rejects with an `ApiError` whose
  `status` is 409 and `message` is `nope` (check `T.ErrorCode` in `apps/web/src/api/generated/types.ts` for a real
  code value to use).
- A 502 with an HTML body rejects with `ApiError` status 502 and message `Request failed (HTTP 502)`.
- `fetch` rejecting with `new TypeError("failed")` rejects with `ApiError` status 0.
- `fetch` rejecting with `new DOMException("aborted", "AbortError")` rejects with that same `DOMException`.
- A GET sends no `X-CSRF-Token` header; a non-GET sends one, equal to the value given to `setCsrfToken`.
- A call that goes through `mutate` (for example `api.services.create(…)` — check the file) sends an
  `Idempotency-Key` header starting with `web-`; `api.operations.cancel("op_1")` (which uses `request` directly)
  sends none.

**Verify**: `bun run test` → all pass, 3 test files.

### Step 4: Lint rules

1. Add the plugin at an exact version: `bun add -d -E eslint-plugin-react-hooks` at the repository root. Confirm the
   version written to `package.json` has no `^`. Commit `bun.lock`.
2. In `eslint.config.js`, import it (`import reactHooks from "eslint-plugin-react-hooks";`) and extend the existing
   `files: ["apps/web/**/*.{ts,tsx}"]` block:

   ```js
   plugins: { "react-hooks": reactHooks },
   rules: {
     "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
     "react-hooks/rules-of-hooks": "error",
     "react-hooks/exhaustive-deps": "error",
     // AGENTS.md: React 19; do not add useMemo, useCallback or React.memo.
     "no-restricted-imports": [
       "error",
       { paths: [{ name: "react", importNames: ["useMemo", "useCallback", "memo"], message: "Banned by AGENTS.md." }] },
     ],
     "no-restricted-syntax": [
       "error",
       {
         selector: "MemberExpression[object.name='React'][property.name=/^(useMemo|useCallback|memo)$/]",
         message: "Banned by AGENTS.md.",
       },
     ],
   },
   ```
   Enable only these two hooks rules explicitly; do not spread the plugin's "recommended" preset (newer versions
   bundle additional compiler rules that are out of scope).
3. Run `bun run lint`. For each **pre-existing** `react-hooks/exhaustive-deps` error, do not change behaviour: add
   directly above the dependency array line
   `// eslint-disable-next-line react-hooks/exhaustive-deps -- pre-existing; see plan/README.md`
   One known site is the first effect in `apps/web/src/features/applications/LogsPanel.tsx` (it reads `lines.length`
   but lists only `[appId, follow]`; plan 008 fixes it properly). Keep a list of every site you suppress and put it in
   your report.

**Verify**: `bun run lint` → exit 0. `bun run fmt:check` → exit 0 (run `bun run fmt` first).

### Step 5: CI and docs

- `.github/workflows/ci.yml`: add a line `bun run test` after `bun run check` in the frontend step.
- `.github/workflows/release.yml`: in the "Gates" step change
  `bun run fmt:check && bun run lint && bun run check` to `bun run fmt:check && bun run lint && bun run check && bun run test`.
- `AGENTS.md`, "Tests and required checks": change the first command line to
  `bun run fmt:check && bun run lint && bun run check && bun run test && bun run web:build`.
- `BACKLOG.md`: change "- The web UI has no automated tests." to
  "- The web UI has unit tests for pure logic only (`bun run test`); no component or end-to-end tests."

**Verify**: `bun run fmt:check && bun run lint && bun run check && bun run test && bun run web:build` → exit 0.

## Test plan

Three new test files (`env.test.ts`, `format.test.ts`, `client.test.ts`), all using `bun:test`, no DOM.

## Done criteria

- [ ] `bun run test` exits 0 and reports tests from 3 files
- [ ] `bun run fmt:check && bun run lint && bun run check && bun run web:build` exits 0
- [ ] `grep -n '"eslint-plugin-react-hooks"' package.json` shows an exact version (no `^`/`~`)
- [ ] `git diff --stat` shows no change to `apps/web/src/api/generated/types.ts`
- [ ] No more than 8 `eslint-disable-next-line react-hooks/exhaustive-deps` comments were added
- [ ] `plan/README.md` status row updated

## STOP conditions

- `bun` is not installed, or `bun add` cannot reach the registry: report; do not hand-edit `bun.lock`.
- More than 8 pre-existing hooks-lint errors, or any `react-hooks/rules-of-hooks` error (that is a real bug — report
  it, do not suppress it).
- `bun test` cannot import `client.ts` or `format.ts` without a DOM. Report the error text.
- A characterization test fails because the code behaves differently from "Current state".

## Maintenance notes

- New pure helpers should live in `apps/web/src/lib/` with a sibling `.test.ts`.
- Each suppression comment added in step 4 is a small debt; plans 008 and 020 remove the known ones.
- If component tests are wanted later, that needs a DOM (`happy-dom`) and is a separate decision.
