---
title: Render and apply internals
description: Understand Bento's staged transaction, rollback, recovery, validation, and scoped reload behavior.
---

# Render and apply internals

The operator consequence is direct: `render` changes generated files without signaling services; `apply` adds validation and targeted reload.

## Transaction

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

The transaction includes generated service files and protected database client material. Candidate failure before promotion leaves live output unchanged. Interruption during promotion is recovered deterministically by the next render/apply.

## Reload scope

| Change | Typical target |
| --- | --- |
| Domain, proxy, TLS, access log, vhost | Nginx |
| App identity, PHP version/profile, pool | PHP-FPM and sometimes Nginx |
| Cron, deploy drain, worker | Matching PHP runner |
| Database creation, backup, restore | No web/runtime reload |
| Full `apply` | Relevant Nginx, FPM, runners |

Stopped services do not fail merely because they cannot be signaled; configuration is ready for startup. A running target must validate before reload.

## Failure semantics

- Validation failure restores previous generated content and modes.
- Reload-signal failure occurs after validation, so valid new files stay promoted. Restore service health and retry apply/signal.
- State mutation and data-plane side effects are not one distributed transaction; follow command-specific recovery guidance.
- Custom templates/drop-ins/overlays are trusted inputs but generated output remains managed.

## Operational use

Batch mutations with `--no-apply`, inspect state/rendered Compose, then apply once:

```sh
bento --stack /var/lib/bento render
bento --stack /var/lib/bento compose -- config
bento --stack /var/lib/bento apply
```

Do not manually delete staging, journal, or lock paths during an active command. If a process died, preserve evidence and let the next render recover before intervening.

## Design rationale

Complete generation avoids mixing old and new fragments; same-filesystem replacement supplies atomic file publication; managed markers distinguish stale output; scoped reloads reduce unrelated disruption. It is not continuous reconciliation or a zero-downtime guarantee.

## Next steps

- [Desired state concept](/concepts/desired-state/)
- [Troubleshooting](/reference/troubleshooting/)
- [Architecture](/advanced/architecture/)
