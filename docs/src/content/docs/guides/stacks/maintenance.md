---
title: Run stack maintenance
description: Apply bounded log retention manually or through host cron without implying database backup.
---

# Run stack maintenance

Prune eligible rotated access logs and reports while retaining active logs, app homes, state, and database volumes.

## Run now

```sh
bento maintenance run --retain-days 14
```

Review the reported removed and retained paths. Unknown filenames are retained rather than deleted.

:::note
Maintenance is not a backup. It does not create or replicate database dumps, and in-runner log rotation is a separate supervised task.
:::

## Schedule maintenance

Register the stack-qualified job in the current user's crontab:

```sh
bento maintenance register \
  --schedule '15 3 * * *' --bin /usr/local/bin/bento
```

The command preserves unrelated crontab lines and replaces only Bento's marked maintenance block. Verify with:

```sh
crontab -l
```

Remove only that block when no longer needed:

```sh
bento maintenance unregister
```

## Troubleshooting

If `crontab` is missing or access is denied, install the host cron utility or run as the same unprivileged operator account that owns the schedule. Ensure the absolute binary path and stack root are available to non-interactive cron. Run the exact maintenance command manually before registering it.

## Advanced

The retention pass keeps active `*.access.log` files and uses timestamps in known rotated names. Database dump retention belongs to successful backup batches, not maintenance. Docker's `local` log driver separately limits service stdout/stderr to 10 MiB across three files.

## Next steps

- [Operate access logs](/guides/apps/access-logs/)
- [Schedule logical backups](/guides/data/backup-restore/)
- [Daily operations](/start/daily-operations/)
