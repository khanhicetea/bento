---
title: Development
description: Build, test, and release Bento.
---

```bash
mise install
bun install --frozen-lockfile
bun run fmt:check && bun run lint && bun run check && bun run web:build
sudo make -C apps/backend ci                # gofmt, vet, race tests, tygo drift, build
sudo make -C apps/backend test-integration  # real Docker, disposable roots
make -C apps/backend release                # static amd64/arm64 binaries with the embedded UI
```

After changing API DTOs, run `make -C apps/backend generate-types` and commit the output. See `AGENTS.md`.
