# Plan 015: The UI retries when the backend is briefly unreachable, and a rejected session on a write returns to sign-in

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/web/src/main.tsx apps/web/src/api/client.ts apps/web/src/features/session/useSession.ts`
> If any changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: plan/007-web-test-baseline-and-hooks-lint.md (for the tests; without it, skip step 4)
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

Operators reach the UI through an SSH tunnel to a loopback listener, and the backend restarts on every upgrade. The
query retry rule is meant to skip client errors (4xx) and retry the rest, but a network failure is represented as
status `0`, and `0 < 500`, so an unreachable backend is **never** retried: every query without polling shows an
error panel until the operator clicks Retry. Second, an expired or revoked session is handled only for queries; a
401 on a mutation (Save, Start, Delete) just shows an inline error and leaves the old CSRF token and cached data in
place until some query also fails. Third, an HTTP error with a non-JSON body (for example a proxy's HTML 502) gets
the error code `"network"`, the same as "unreachable", which makes the two indistinguishable.

## Current state

- `apps/web/src/main.tsx` (whole query client setup):

  ```tsx
  const queryClient: QueryClient = new QueryClient({
    queryCache: new QueryCache({
      onError(error) {
        // An expired or revoked session sends the operator back to sign-in.
        if (error instanceof ApiError && error.status === 401) {
          queryClient.setQueryData(keys.session, { authenticated: false });
        }
      },
    }),
    defaultOptions: {
      queries: {
        retry: (count, error) => !(error instanceof ApiError && error.status < 500) && count < 2,
        refetchOnWindowFocus: true,
      },
    },
  });
  ```
- `apps/web/src/api/client.ts`:

  ```ts
  export class ApiError extends Error {
    readonly status: number;
    readonly code: T.ErrorCode | "network";
    readonly fields: T.FieldError[];

    constructor(status: number, body: T.ErrorBody | null, fallback: string) {
      super(body?.message ?? fallback);
      this.status = status;
      this.code = body?.code ?? "network";
      this.fields = body?.fields ?? [];
    }
  }
  ```
  A network failure throws `new ApiError(0, null, "The Bento backend is unreachable.")`; a non-OK response throws
  `new ApiError(response.status, envelope?.error ?? null, \`Request failed (HTTP ${response.status})\`)`.
  At the planned commit nothing else in `apps/web/src` compares against the string `"network"`
  (`grep -rn '"network"' apps/web/src` shows only these two lines).
- `apps/web/src/features/session/useSession.ts`, what logout does on success:

  ```ts
  setCsrfToken("");
  // Update the mounted session query in place (clearing the cache would
  // detach its observer), then drop every other cached server response.
  queryClient.setQueryData(keys.session, { authenticated: false });
  queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== keys.session[0] });
  ```
- Repository rule (`AGENTS.md`): "Keep one `QueryClientProvider` at the entry point."

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Install | `bun install --frozen-lockfile` | exit 0 |
| Format | `bun run fmt` then `bun run fmt:check` | exit 0 |
| Lint | `bun run lint` | exit 0 |
| Typecheck | `bun run check` | exit 0 |
| Tests | `bun run test` (exists once plan 007 landed) | all pass |
| Build | `bun run web:build` | exit 0 |

## Scope

**In scope**:
- `apps/web/src/main.tsx`
- `apps/web/src/api/client.ts` (`ApiError` only)
- `apps/web/src/features/session/useSession.ts` (to share the "signed out" cleanup)
- `apps/web/src/lib/retry.ts` and `apps/web/src/lib/retry.test.ts` (create)
- `apps/web/src/api/client.test.ts` (extend, if it exists from plan 007)

**Out of scope**:
- Retrying mutations automatically — never; a write must not be repeated without the operator.
- The sign-in screen and session query.

## Git workflow

- Branch: `advisor/015-retry-network-errors-and-handle-401-on-mutations`
- One commit, plain imperative sentence, e.g. `Retry queries when the backend is unreachable and sign out on a rejected write`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: A pure retry rule

Create `apps/web/src/lib/retry.ts`:

```ts
import { ApiError } from "../api/client.ts";

/**
 * Query retry rule. Client errors (4xx) are final: retrying cannot change
 * them. Everything else is worth two more tries: server errors (5xx), an
 * unreachable backend (status 0: SSH tunnel blip, backend restart), and
 * non-API errors.
 */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false;
  return failureCount < 2;
}
```

In `main.tsx` replace the inline `retry:` arrow with `retry: shouldRetryQuery`.

**Verify**: `bun run check` → exit 0.

### Step 2: Tell "unreachable" from "non-JSON HTTP error"

In `client.ts` change the `code` type to `T.ErrorCode | "network" | "http"` and the constructor line to:

```ts
// "network": no response at all. "http": a response without Bento's error
// envelope (a proxy's HTML error page, a truncated body).
this.code = body?.code ?? (status === 0 ? "network" : "http");
```

Then run `grep -rn "\.code ===\|\.code !==\|code:" apps/web/src --include=*.tsx --include=*.ts | grep -v generated | grep -v test`
and check no caller depended on `"network"` for HTTP errors. Expected: none.

**Verify**: `bun run check` → exit 0.

### Step 3: One "signed out" cleanup, used by queries, mutations and logout

In `useSession.ts` export a function containing exactly what logout's `onSuccess` does today:

```ts
/** Forget the session locally: clear the CSRF token and every cached server response. */
export function signedOut(queryClient: QueryClient) {
  setCsrfToken("");
  // Update the mounted session query in place (clearing the cache would
  // detach its observer), then drop every other cached server response.
  queryClient.setQueryData(keys.session, { authenticated: false });
  queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== keys.session[0] });
}
```

and make `useLogout`'s `onSuccess` call `signedOut(queryClient)`.

In `main.tsx`:

```tsx
function onAuthError(error: unknown) {
  // An expired or revoked session sends the operator back to sign-in.
  if (error instanceof ApiError && error.status === 401) signedOut(queryClient);
}

const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({ onError: onAuthError }),
  mutationCache: new MutationCache({ onError: onAuthError }),
  …
});
```

(import `MutationCache` from `@tanstack/react-query`; function declarations are hoisted, so `onAuthError` may refer
to `queryClient` declared after it — it only runs later.)

Check one thing before finishing: the sign-in request itself. Find the login mutation
(`grep -rn "session.login\|api.session" apps/web/src`). If a wrong password makes the backend answer **401**, the new
mutation handler would run `signedOut` on a failed login — harmless (already signed out), but it must not hide the
form's inline error. Read the login component and confirm its error display comes from the mutation's own `error`
state (which is unaffected). If the login form instead depends on cached queries that `removeQueries` would drop,
exclude it: give that mutation `meta: { skipAuthHandler: true }` and make `MutationCache.onError` receive
`(error, _vars, _ctx, mutation)` and return early when `mutation.meta?.skipAuthHandler` is set.

**Verify**: `bun run lint && bun run check` → exit 0.

### Step 4: Tests (if plan 007 landed)

`apps/web/src/lib/retry.test.ts`:
- `shouldRetryQuery(0, new ApiError(0, null, "x"))` → true (the bug this plan fixes);
- 404 and 401 and 409 → false at count 0;
- 500 and 503 → true at counts 0 and 1, false at count 2;
- a plain `new Error("x")` → true at count 0, false at count 2.

In `apps/web/src/api/client.test.ts` add: a 502 with an HTML body rejects with `code === "http"`; a rejected `fetch`
rejects with `code === "network"`.

**Verify**: `bun run test` → all pass. Then `bun run fmt && bun run fmt:check && bun run web:build` → exit 0.

## Test plan

As step 4. The cache wiring in `main.tsx` is not unit-tested (it is the app entry point); it is covered by
typecheck and by reading.

## Done criteria

- [ ] `bun run fmt:check && bun run lint && bun run check && bun run web:build` exits 0
- [ ] `grep -n "status < 500" apps/web/src/main.tsx` → no matches
- [ ] `grep -n "MutationCache" apps/web/src/main.tsx` → at least one match
- [ ] `grep -c "QueryClientProvider" apps/web/src/main.tsx` is unchanged (still one provider)
- [ ] `bun run test` passes (if plan 007 landed)
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match.
- Some caller depends on `code === "network"` for HTTP errors (step 2 grep).
- The login flow breaks under the new mutation handler and the `meta` escape does not fit how it is written.
- `bun` is unavailable: report "written but not run".

## Maintenance notes

- Polling queries (operation status, app list) already retried implicitly by polling again; this change matters for
  one-shot queries (backup tab, hosts, services, webhook settings).
- Retries use React Query's default backoff (1 s, 2 s): an unreachable backend now takes about 3 s longer to show
  its error panel. That is the intended trade.
- Never add `retry` to mutations globally.
