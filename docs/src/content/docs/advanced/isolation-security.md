---
title: Isolation and security
description: Understand Bento identity, filesystem, socket, data, secret, and public-surface boundaries and their limits.
---

# Isolation and security

Bento protects routine application ownership boundaries on one trusted host; it does not isolate mutually hostile tenants like separate VMs.

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

Only Nginx is public in the base Compose topology. It exposes configured app/proxy domains, ACME challenges, and optional signed deploy endpoints. FPM, runners, databases, Redis, s6 controls, and Bento management remain host-local/private. Overlays can weaken this—review merged Compose for unintended ports.

## Secrets

Sensitive material includes `.env`, `state.json`, app credential files, SSH keys, deploy HMAC secrets, root client files, certificate keys, ACME state, backups, logs, and stack exports. Root/app database passwords are staged through protected files rather than host argv. Routine output and support bundles redact known fields, but operators must still inspect artifacts before sharing.

## Filesystem and command safety

Working directories cannot escape the app home through ordinary path resolution. Recursive permission repair does not follow symlink targets. Worker argv avoids implicit shell evaluation; cron `--cmd` deliberately permits shell syntax. Domain uniqueness prevents ambiguous generated routing. The Compose wrapper blocks volume-destructive down.

## Residual risk

A compromised app may consume shared CPU/memory, probe private network services, exploit a shared runtime/kernel vulnerability, or expose its own credentials. Bento has no per-app resource quota or hostile tenancy guarantee. Keep host/Docker/images/apps patched, minimize overlays, restrict stack-root and Docker access, and maintain tested off-host recovery.

## Next steps

- [Safety and durability](/concepts/safety-and-durability/)
- [Limitations](/reference/limitations/)
- [Architecture](/advanced/architecture/)
