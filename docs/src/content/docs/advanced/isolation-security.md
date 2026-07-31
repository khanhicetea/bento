---
title: Isolation and security
description: Learn which app boundaries Bento provides and where you need stronger isolation.
---

# Isolation and security

Bento reduces accidental access between apps on one trusted host. It does not isolate hostile tenants as separate virtual machines would.

## Boundaries

| Area | Mechanism | Limit |
| --- | --- | --- |
| Process | Stable app UID/GID for FPM, CLI, cron, worker, deploy | Apps share version containers/kernel |
| Web | Dedicated FPM pool/socket and `open_basedir` | Shared FPM global capacity/image |
| Files | Private home; Nginx read-only with group traversal to public files | Host/root and shared container namespace remain privileged |
| Database | One service binding; MySQL grants or PostgreSQL role/database ownership | Services share a private network |
| Redis | Required prefix or per-app ACL identity | Shared mode depends on correct prefix use |
| Jobs | App workdir/identity, scoped s6 service, locks/timeouts | Runner is shared and singleton |

Use separate apps for codebases/trust boundaries and separate hosts or stronger isolation for untrusted tenants.

## Public surface

Only Nginx is public in the base Compose setup. It serves app and proxy domains, ACME challenges, and optional signed deploy endpoints.

FPM, runners, databases, Redis, s6 controls, and Bento management stay private or host-local. An overlay can expose them, so review the merged Compose configuration for unexpected ports.

## Secrets

Protect `.env`, `state.json`, app credentials, SSH keys, deploy secrets, database client files, certificate keys, ACME state, backups, logs, and stack exports.

Bento passes database passwords through protected files instead of host command arguments. It also redacts known secrets from routine output and support bundles. Always inspect an artifact before you share it.

## Filesystem and command safety

Bento keeps app working directories inside the app home. Recursive permission repair does not follow symlink targets.

Worker arguments do not invoke a shell implicitly. Cron `--cmd` does allow shell syntax, so treat it as trusted input. Bento also rejects duplicate domains and blocks Compose commands that would delete volumes.

## Residual risk

A compromised app can consume shared CPU or memory, probe private services, exploit a shared runtime or kernel flaw, or expose its credentials. Bento does not set per-app resource quotas or guarantee hostile-tenant isolation.

Patch the host, Docker, images, and apps. Keep overlays small, restrict access to Docker and the stack root, and maintain tested off-host recovery.

## Next steps

- [Safety and durability](/concepts/safety-and-durability/)
- [Limitations](/reference/limitations/)
- [Architecture](/advanced/architecture/)
