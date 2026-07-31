---
title: Render and apply internals
description: Follow a render or apply from staging through validation, reload, and recovery.
---

# Render and apply internals

`render` writes generated files but does not signal services. `apply` also validates the new configuration and reloads only the affected services.

## Transaction

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/render-apply-transaction.svg
Alt: Bento apply transaction from lock and staging through validation, targeted reload, rollback, or retry.
Show: A left-to-right flow with one decision at validation. The failure branch restores previous files and skips reload. The success branch reloads only affected roles. Add a second failure marker after reload signaling to show that valid new files stay in place for retry.
-->

```text
exclusive lock
  -> recover abandoned journal
  -> render complete same-filesystem staging tree
  -> build managed-file manifest
  -> snapshot and atomically promote candidates
  -> remove stale managed files last
  -> validate running targets
       failure: restore previous bytes/modes; no reload
       success: signal targeted roles; finalize journal
```

The transaction covers generated service files and protected database client files. If candidate generation fails, Bento leaves the live files unchanged. If the process stops during publication, the next `render` or `apply` recovers the interrupted transaction.

## Reload scope

| Change | Typical target |
| --- | --- |
| Domain, proxy, TLS, access log, vhost | Nginx |
| App identity, PHP version/profile, pool | PHP-FPM and sometimes Nginx |
| Cron, deploy drain, worker | Matching PHP runner |
| Database creation, backup, restore | No web/runtime reload |
| Full `apply` | Relevant Nginx, FPM, runners |

Bento does not treat a stopped service as a signal failure. The configuration waits for the next start. A running service must pass validation before Bento reloads it.

## Failure semantics

- Validation failure restores previous generated content and modes.
- Reload-signal failure occurs after validation, so valid new files stay promoted. Restore service health and retry apply/signal.
- State mutation and data-plane side effects are not one distributed transaction; follow command-specific recovery guidance.
- Custom templates/drop-ins/overlays are trusted inputs but generated output remains managed.

## Operational use

Batch mutations with `--no-apply`, inspect state/rendered Compose, then apply once:

```sh
bento render
bento compose -- config
bento apply
```

Do not manually delete staging, journal, or lock paths during an active command. If a process died, preserve evidence and let the next render recover before intervening.

## Design rationale

Bento generates a complete file set so it never mixes old and new fragments. Same-filesystem replacement publishes each file atomically, and managed markers identify stale output. Targeted reloads reduce unrelated disruption.

This design does not continuously reconcile the stack, and it does not guarantee zero downtime.

## Next steps

- [Desired state concept](/concepts/desired-state/)
- [Troubleshooting](/reference/troubleshooting/)
- [Architecture](/advanced/architecture/)
