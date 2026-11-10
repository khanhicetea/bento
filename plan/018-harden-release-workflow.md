# Plan 018: The release workflow builds with read-only permissions, pins its actions, and scans for known vulnerabilities

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected
> result before moving to the next step. If anything in the "STOP conditions" section occurs, stop and report — do not
> improvise. When done, update the status row for this plan in `plan/README.md` — unless a reviewer dispatched you and
> told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 012bba0..HEAD -- .github/workflows apps/backend/Makefile`
> If any changed, compare the "Current state" excerpts against the live files; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S–M
- **Risk**: LOW (but workflows cannot be run locally; see STOP conditions)
- **Depends on**: none (if plan 007 landed, keep its added `bun run test` lines)
- **Category**: dx / security
- **Planned at**: commit `012bba0`, 2026-11-09

## Why this matters

`release.yml` builds the binaries operators install as root. Today the whole job — including `bun install` (which
runs third-party package code) and the full test gate — runs with `contents: write`, so a compromised npm package or
action could alter the release. Actions are referenced by mutable tags (`@v4`), the tag name is interpolated directly
into a shell line, both workflows trigger on tag pushes **and** on published releases (so one release can start two
runs that upload the same assets), and there is no scan for known vulnerabilities in the Go dependencies, which
include a pre-release `tailscale.com` linked into the root binary.

Decision already recorded in `BACKLOG.md` and **not** changed by this plan: CI runs only on tags and published
releases, not on every push.

## Current state

`.github/workflows/release.yml` (whole file at the planned commit):

```yaml
name: release-binaries

on:
  push:
    tags:
      - "*"
  release:
    types: [published]

permissions:
  contents: write

jobs:
  build:
    name: Build Linux binaries
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event_name == 'release' && github.event.release.tag_name || github.ref }}
      - uses: actions/setup-go@v5
        with:
          go-version: "1.27.3"
      - uses: oven-sh/setup-bun@v2
        with:
          bun-version: "1.4.2"
      - name: Gates
        run: |
          bun install --frozen-lockfile
          bun run fmt:check && bun run lint && bun run check
          sudo --preserve-env=PATH,HOME,GOPATH,GOCACHE,GOMODCACHE make -C apps/backend ci
          # ci ran as root; hand dist/ and build caches back to the runner user.
          sudo chown -R "$(id -u):$(id -g)" "$GITHUB_WORKSPACE" "$(go env GOCACHE)" "$(go env GOMODCACHE)"
      - name: Cross-build amd64 and arm64 with the embedded UI
        run: make -C apps/backend release VERSION=${{ github.event_name == 'release' && github.event.release.tag_name || github.ref_name }}
      - uses: softprops/action-gh-release@v2
        with:
          tag_name: ${{ github.event_name == 'release' && github.event.release.tag_name || github.ref_name }}
          files: |
            dist/bento-linux-amd64
            dist/bento-linux-arm64
            dist/SHA256SUMS
          fail_on_unmatched_files: true
```

`.github/workflows/ci.yml` has the same two triggers, `permissions: contents: read`, one job `checks` using
`actions/checkout@v4`, `actions/setup-go@v5` (go `1.27.3`), `oven-sh/setup-bun@v2` (bun `1.4.2`), a frontend step, a
`make -C apps/backend ci` step and a "Real Docker integration tests" step.

Other facts:
- `mise.toml` and `package.json` (`packageManager`) say Bun `1.4.0`; the workflows install `1.4.2`.
- `apps/backend/Makefile` has targets `fmt-check`, `vet`, `test-race`, `check-generated`, `build`, `ci`, `release`
  (which writes `dist/bento-linux-amd64`, `dist/bento-linux-arm64`, `dist/SHA256SUMS`). `GO ?= go`.
- The Go module is at `apps/backend/` (`apps/backend/go.sum`), not at the repository root, so `actions/setup-go`'s
  default cache key (root `go.sum`) never matches.
- Repository rule: "Keep dependency versions exact".

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Resolve an action tag to a commit | `git ls-remote https://github.com/<owner>/<repo> 'refs/tags/<tag>' 'refs/tags/<tag>^{}'` | prints the commit SHA (use the `^{}` line if present) |
| YAML syntax | `python3 -c "import sys,yaml; [yaml.safe_load(open(f)) for f in sys.argv[1:]]" .github/workflows/ci.yml .github/workflows/release.yml` | exit 0 |
| Workflow lint (if installed) | `actionlint` | exit 0 |
| Vulnerability scan (step 4) | `make -C apps/backend vuln` | exit 0, or a list of findings to report |

## Scope

**In scope**:
- `.github/workflows/release.yml`, `.github/workflows/ci.yml`
- `apps/backend/Makefile` (add a `vuln` target)
- `.github/dependabot.yml` (create)

**Out of scope**:
- Adding `pull_request` / branch triggers — a recorded decision keeps CI on tags and releases only.
- Signing or build-provenance attestation — worth doing, but needs a maintainer decision on the mechanism; listed in
  `plan/README.md`.
- Fixing vulnerabilities the scan reports — report them; upgrades are separate work.

## Git workflow

- Branch: `advisor/018-harden-release-workflow`
- Commit per step is fine; plain imperative sentences, e.g. `Build releases with read-only permissions`,
  `Pin workflow actions to commits`.
- Do NOT push, tag, or publish a release. Never trigger these workflows yourself.

## Steps

### Step 1: Pin every action to a commit SHA

For each `uses:` in both workflows (`actions/checkout@v4`, `actions/setup-go@v5`, `oven-sh/setup-bun@v2`,
`softprops/action-gh-release@v2`, and the upload/download-artifact actions you add in step 2), resolve the tag to its
commit with the `git ls-remote` command above and write `uses: owner/repo@<40-hex SHA> # v4` (keep the tag as a
trailing comment). Requires network access.

**Verify**: `grep -n "uses:" .github/workflows/*.yml | grep -v "@[0-9a-f]\{40\}"` → no output.

### Step 2: Split build (read-only) from publish (write) in `release.yml`

Restructure to:

```yaml
permissions:
  contents: read

concurrency:
  # A tag push and the release published for it would otherwise both build and upload.
  group: release-${{ github.event_name == 'release' && github.event.release.tag_name || github.ref_name }}
  cancel-in-progress: false

env:
  RELEASE_TAG: ${{ github.event_name == 'release' && github.event.release.tag_name || github.ref_name }}

jobs:
  build:
    name: Build Linux binaries
    runs-on: ubuntu-latest
    steps:
      … checkout / setup-go / setup-bun as today (SHA-pinned) …
      - name: Gates
        run: | … unchanged …
      - name: Cross-build amd64 and arm64 with the embedded UI
        run: make -C apps/backend release VERSION="$RELEASE_TAG"
      - uses: actions/upload-artifact@<SHA> # v4
        with:
          name: bento-dist
          path: |
            dist/bento-linux-amd64
            dist/bento-linux-arm64
            dist/SHA256SUMS
          if-no-files-found: error

  publish:
    name: Publish release assets
    needs: build
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/download-artifact@<SHA> # v4
        with:
          name: bento-dist
          path: dist
      - name: Verify checksums
        run: cd dist && sha256sum -c SHA256SUMS
      - uses: softprops/action-gh-release@<SHA> # v2
        with:
          tag_name: ${{ env.RELEASE_TAG }}
          files: |
            dist/bento-linux-amd64
            dist/bento-linux-arm64
            dist/SHA256SUMS
          fail_on_unmatched_files: true
```

Points that matter:
- The tag reaches the shell through the environment (`"$RELEASE_TAG"`), never through `${{ }}` inside `run:`.
- Only `publish` has `contents: write`, and it runs no repository or package code.
- The `checkout` step's `ref:` expression stays as it is.

Add the same `concurrency` idea to `ci.yml` (`group: ci-<same expression>`, `cancel-in-progress: false`).

**Verify**: YAML syntax command → exit 0; `actionlint` → exit 0 if available.

### Step 3: Small consistency fixes in both workflows

- `actions/setup-go`: add `cache-dependency-path: apps/backend/go.sum` under `with:`.
- Bun version: change `bun-version: "1.4.2"` to `"1.4.0"` in both workflows so CI uses the version `mise.toml` and
  `package.json` pin.
- Add `timeout-minutes: 90` to the `checks` job in `ci.yml` and `timeout-minutes: 60` to `build` in `release.yml`.

**Verify**: `grep -n "bun-version\|cache-dependency-path\|timeout-minutes" .github/workflows/*.yml` → shows the new
values in both files.

### Step 4: Vulnerability scan

In `apps/backend/Makefile` add `vuln` to `.PHONY` and a target:

```make
# Known vulnerabilities reachable from the module's code. Pinned so the gate
# does not change under us; bump deliberately.
GOVULNCHECK_VERSION = <exact version, see below>
vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
```

Choose the newest released `govulncheck` version (`go list -m -versions golang.org/x/vuln`) and write it exactly
(for example `v1.x.y`), not `latest`. (The recipe line must start with a tab.)

Do **not** add `vuln` to the `ci` target (a newly published advisory must not block an unrelated release build
without a human decision). Instead add to `ci.yml`, after the Go gate step:

```yaml
      - name: Known vulnerabilities (govulncheck)
        run: make -C apps/backend vuln
```

Run `make -C apps/backend vuln` locally. If it reports findings, do not try to fix them: paste the summary (module,
advisory id, fixed version) into your report and into a new bullet under "Hardening (low)" in `BACKLOG.md`.

**Verify**: `make -C apps/backend vuln` → exit 0, or findings recorded as described.

### Step 5: Dependabot for visibility

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /apps/backend
    schedule: { interval: weekly }
    open-pull-requests-limit: 5
  - package-ecosystem: github-actions
    directory: /
    schedule: { interval: weekly }
```

(No npm entry: the JavaScript toolchain is Bun with exact pins and a `bun.lock`; add one only if the maintainer wants
it.)

**Verify**: YAML syntax command on `.github/dependabot.yml` → exit 0.

## Test plan

Workflows cannot be executed locally. Verification is syntax (`yaml.safe_load`, `actionlint` if available), the greps
above, and a careful diff read. The first real run happens when the maintainer next pushes a tag; say this plainly in
your report.

## Done criteria

- [ ] No `uses:` line without a 40-hex SHA in either workflow
- [ ] `release.yml` top-level `permissions` is `contents: read`; only the `publish` job has `contents: write`
- [ ] `grep -n 'run:.*\${{' .github/workflows/release.yml` → no output
- [ ] Both workflow files and `dependabot.yml` parse as YAML
- [ ] `make -C apps/backend vuln` ran, with its result stated in the report
- [ ] No files outside the in-scope list are modified (plus `BACKLOG.md` only if step 4 found advisories)
- [ ] `plan/README.md` status row updated, noting that the workflows have not yet run on GitHub

## STOP conditions

- No network access (cannot resolve action SHAs or fetch govulncheck): do not guess SHAs — report.
- The workflow files differ from "Current state" (for example plan 007 added `bun run test`; that is expected — keep
  it — but anything else unexpected is a STOP).
- You are asked or tempted to push a tag to "test" the workflow: do not.

## Maintenance notes

- Pinned SHAs go stale; the Dependabot `github-actions` entry proposes updates. Review each bump's release notes.
- The integration suite still runs only in `ci.yml`, not as a gate of `release.yml`: binaries can be published while
  the Docker suite for the same tag is failing. Making `release.yml` wait for it (a reusable workflow or one combined
  workflow) is the natural next step once the maintainer decides how long a release may take.
- Provenance (GitHub artifact attestations or cosign) is the remaining gap: `SHA256SUMS` comes from the same job as
  the binaries and is unsigned.
