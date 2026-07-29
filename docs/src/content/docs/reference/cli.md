---
title: CLI reference
description: Find Bento global flags and command groups, then open the task guide for safe procedures.
---

# CLI reference

Bento is a local command-line control plane. Run `bento --help` and `bento <group> --help` for the current exhaustive syntax.

## Global interface

```text
bento [--stack PATH] [--json] <command> [args]
```

| Option/environment | Purpose |
| --- | --- |
| `--stack PATH` / `BENTO_STACK_ROOT` | Select mutable stack root; default `./bento` |
| `--json` | Machine-readable output where supported |
| `--repo-root` | Test/source-mode repository override; not routine production use |
| `--help` | Current command help |

Put global flags before the command. Use explicit `--stack` for first use, multiple stacks, schedules, transfers, and destructive operations. Many state mutations accept `--no-apply`; follow the batch with `bento --stack PATH apply`.

## Command map

| Area | Commands | Guide |
| --- | --- | --- |
| Bootstrap/control | `version`, `tui`, `init`, `state migrate`, `render`, `apply` | [Start here](/start/first-stack/) |
| Diagnostics | `status`, `doctor`, `support-bundle`, `test-stack` | [Diagnostics](/guides/stacks/diagnostics/) |
| Apps/runtime | `app`, `php`, `exec` | [Apps](/guides/apps/manage/), [PHP](/guides/apps/php-runtimes/) |
| Data | `mysql`, `postgres`, `sqlite`, `backup`, `restore` | [Relational backup and restore](/guides/data/backup-restore/), [SQLite](/guides/data/sqlite/) |
| Traffic | `proxy`, `tls`, `logs` | [Reverse proxy](/guides/apps/reverse-proxy/), [TLS](/guides/apps/domains-tls/) |
| Background | `deploy`, `cron`, `worker` | [Deploy](/guides/apps/deploy/), [jobs](/guides/apps/schedules-workers/) |
| Stack | `compose`, `stack`, `maintenance` | [Stack management](/guides/stacks/manage/) |
| Safety/custom | `permissions`, `template` | [Permissions](/guides/apps/permissions/), [templates](/guides/customization/templates/) |

`app delete`/`remove` and `proxy delete`/`remove` need exact confirmation. `app prune` is interactive and permanently deletes listed retained parts only after typing `delete`. `compose` takes Docker Compose arguments after `--` and refuses volume-destructive `down -v` forms.

## Output and exits

Human output favors operational summaries; JSON is available only where advertised. Secrets are redacted from routine output. Nonzero exit means the requested operation or check did not complete cleanly; read structured error/recovery fields where present rather than matching human prose.

## Help convention

```sh
bento app --help
bento app create --help
bento compose --help
```

Source contributors may substitute `deno task run --`; production examples use the compiled `bento` binary.
