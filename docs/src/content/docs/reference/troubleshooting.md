---
title: Troubleshooting
description: Start from a Bento symptom and follow targeted, non-destructive checks.
---

# Troubleshooting

Start with explicit stack targeting and preserve evidence before changing data:

```sh
bento status
bento doctor
bento compose -- ps
```

| Symptom | Checks and destination |
| --- | --- |
| Docker unavailable/permission denied | Start Docker; verify socket access and Compose v2. See [requirements](/start/requirements/). |
| Port already in use | Run `stack ingress show`; identify the listener; use distinct bridge ports for additional stacks. |
| Docker cannot create a network | Check address-pool exhaustion and stale unused networks; do not remove active stack networks. |
| Nginx validation fails | Inspect the reported file/context; fix `custom/` or desired state, never `generated/`. Previous config is restored before reload. |
| App gives 404/403 | Check domain ownership, enabled state, document root, files, and [permissions](/guides/apps/permissions/). |
| App/proxy gives 502 | Check PHP/service health and the upstream address in the correct [network namespace](/concepts/networking/). |
| DNS/ACME pending | Ensure every A/AAAA record reaches this host and public TCP port 80 reaches Nginx. See [TLS](/guides/apps/domains-tls/). |
| MySQL/PostgreSQL unhealthy | Inspect service logs and `.env` credential consistency; do not "fix" initialized volumes by changing passwords casually. |
| Cron/worker/deploy not running | Check app enabled state, selected PHP runner, scoped inspect/status, workdir, hook, and runner logs. |
| Backup missing | Check database reachability and free space. Failed/empty dumps are not published; scheduled copies remain on-host. |
| Restore failed | Destination may be partial. Preserve logs, inspect/drop only the target with authorization, and retry from a verified dump. |
| Import failed | Use an empty root, complete archives, no destination volumes, and compatible architecture/database images. |

## Collect evidence

```sh
bento compose -- logs --tail 200 <service>
bento support-bundle /tmp/bento-support.tar.gz
```

Support bundles redact known secrets but remain sensitive. Inspect before sharing. Use `--json` with `status`/`doctor` for automation.

## Safe recovery rules

Do not edit generated files, use `compose down -v`, delete unknown volumes, or prune an app while troubleshooting. If apply validation fails, correct the input and retry. If signaling fails after successful validation, valid new files remain live; retry apply/reload after restoring service health.

For architecture-level failure semantics, see [render and apply](/advanced/render-apply/) and [storage recovery](/advanced/storage-recovery/).
