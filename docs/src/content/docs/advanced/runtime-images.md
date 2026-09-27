---
title: Runtime images
description: How managed images are built and what they contain.
---

Bento builds one image per runtime version through the Docker Engine API (no Docker CLI or BuildKit). The build
context is embedded in the binary and hashed together with the build arguments; the tag
`bento-runtime/<toolchain>:<version>-<hash>` changes whenever either changes. Base images, s6-overlay, minicrond,
and Composer are pinned and checksum-verified.

Inside every image: s6-overlay as PID 1 via `bento-init` (takes the per-app instance lock), `bento-exec` for tooling
(never starts daemons), `bento-ready` for readiness, and a failure handler that stops the container after five
crashes of a component within two minutes so Docker's restart policy applies its own backoff.
