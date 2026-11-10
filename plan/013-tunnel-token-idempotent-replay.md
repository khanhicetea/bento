# Plan 013: Replaying a tunnel-token request never changes the token file without an operation

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- apps/backend/internal/operations/accept.go apps/backend/internal/operations/edge_fault_test.go`
> If either changed, compare the "Current state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

Setting the Cloudflare tunnel token writes the token to a private file **before** submitting the `tunnel.apply`
operation, because the operation's handler reads that file. If the submission is rejected, the code restores the
previous file. But when the request carries an idempotency key that was already used, `Submit` returns the earlier
operation, reports no error, and does not run the settings change — and the file is *not* restored. A replayed or
reused key with a different token (or a "disable" replay of an "enable") therefore changes or deletes the token file
while the stored settings and token generation stay as they were: no operation runs, the reconciler sees no drift,
and the tunnel silently picks up the other token at some later unrelated recreate, or a later apply fails because
the file is missing. The API meanwhile reports success with the old operation.

## Current state

- `apps/backend/internal/operations/accept.go`, `SetTunnelToken` (near line 799):

  ```go
  prev, prevErr := os.ReadFile(path)
  hadPrev := prevErr == nil
  …
  // rollback is best effort: the Submit error is what the caller reports.
  rollback := func() {
      if hadPrev {
          _ = platform.AtomicWrite(path, prev, 0o440, owner)
      } else {
          _ = os.Remove(path)
      }
  }
  if enabled {
      if err := platform.AtomicWrite(path, []byte(token), 0o440, owner); err != nil {
          return store.Operation{}, err
      }
  } else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
      return store.Operation{}, err
  }
  op, _, err := c.Submit(ctx, Submission{
      Kind: KindTunnelApply, TargetKind: "tunnel", TargetID: "tunnel", IdempotencyKey: idem,
      Request: map[string]any{"enabled": enabled},
      Mutate: func(ctx context.Context, q store.Q) error { … s.Enabled = enabled; s.TokenGeneration++ … },
  })
  if err != nil {
      rollback()
  }
  return op, err
  ```
- `apps/backend/internal/operations/controller.go`, `Submit` returns `(store.Operation, bool, error)`; the `bool`
  (`existed`) is true when the idempotency key matched an earlier operation of the same kind and target, in which
  case `Mutate` is not run and no new operation is created.
- Tests: `apps/backend/internal/operations/edge_fault_test.go` (near line 115) uses
  `h.mustSucceed(h.c.SetTunnelToken(ctx, strings.Repeat("a", 40), ""))`. The token file is at
  `h.c.Layout.TunnelDir() + "/token"`. A valid test token is any 32–4096 character string without spaces or quotes.
- Secrets rule: never put the token in an error, log line or test failure message. In tests compare file contents
  with `bytes.Equal` and print only a description ("token file changed"), not the bytes.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Format check | `cd apps/backend && gofmt -l .` | prints nothing |
| Vet | `cd apps/backend && go vet ./...` | exit 0 |
| Narrow tests | `cd apps/backend && sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE go test -race ./internal/operations/ -run 'Tunnel'` | `ok` (as root) |
| Full gate | `sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci` | exit 0 |

## Scope

**In scope**:
- `apps/backend/internal/operations/accept.go` (`SetTunnelToken` only)
- `apps/backend/internal/operations/edge_fault_test.go`

**Out of scope**:
- `Submit` and the idempotency lookup in `controller.go`.
- Other acceptance functions. (If you notice another one that writes a file before `Submit`, list it in your report;
  do not change it.)

## Git workflow

- Branch: `advisor/013-tunnel-token-idempotent-replay`
- One commit, plain imperative sentence, e.g. `Leave the tunnel token file alone when a request is a replay`.
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Restore the file on a replay

Change the end of `SetTunnelToken` to:

```go
op, existed, err := c.Submit(ctx, Submission{ … unchanged … })
if err != nil || existed {
    // A replayed idempotency key creates no operation and changes no
    // settings: the token file must be what it was before this call.
    rollback()
}
return op, err
```

**Verify**: `cd apps/backend && gofmt -l . && go vet ./internal/operations/` → no output, exit 0.

### Step 2: Test

Add `TestTunnelTokenReplayLeavesTheFileAlone` to `edge_fault_test.go`, modelled on the tunnel test already there:

1. `first := h.mustSucceed(h.c.SetTunnelToken(ctx, strings.Repeat("a", 40), "key-1"))`; read the token file into
   `before`.
2. Replay with a different token and the same key:
   `again, err := h.c.SetTunnelToken(ctx, strings.Repeat("b", 40), "key-1")`; assert `err == nil` and
   `again.ID == first.ID`.
3. Read the file again; assert `bytes.Equal(before, after)` (failure message must not print the contents).
4. Replay a disable with the same key: `h.c.SetTunnelToken(ctx, "", "key-1")`; assert the file still exists and
   still equals `before`.
5. A genuinely new request still works: `h.mustSucceed(h.c.SetTunnelToken(ctx, strings.Repeat("c", 40), "key-2"))`
   and the file now differs from `before`.

**Verify**: narrow test command → `ok`; the new test PASSes; existing tunnel tests pass.

## Test plan

One new harness test as above.

## Done criteria

- [ ] `gofmt -l .` prints nothing; `go vet ./...` exits 0
- [ ] New test passes as root; `sudo … make -C apps/backend ci` exits 0
- [ ] No files outside the in-scope list are modified
- [ ] `plan/README.md` status row updated

## STOP conditions

- "Current state" excerpts do not match, or `Submit` no longer returns the `existed` flag.
- The replay in step 2 of the test returns an error (then the idempotency behaviour differs from what this plan
  assumes) — report the error.
- Go or root is unavailable: report "written but not run".

## Maintenance notes

- Narrow remaining window: if the first operation is still queued when a replay arrives, the file is briefly the
  replay's token between the write and the rollback. The handler reads the file when it runs; both happen in
  microseconds and the rollback restores the first request's token, so the outcome is still correct, but a cleaner
  design would check the idempotency key before touching the file. Not done here to keep the change minimal.
- Pattern to watch in review: any acceptance function that performs a filesystem write before `Submit` needs the
  same `existed` handling.
