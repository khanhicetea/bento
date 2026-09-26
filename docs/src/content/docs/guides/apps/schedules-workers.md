---
title: Operate schedules and workers
description: Manage app-owned minicrond schedules and workers.
---

# Operate schedules and workers

Each enabled PHP app has its own minicrond registry, socket, jobs, runs, logs, and supervised workers under its UID/GID. Bento does **not** store user job definitions or offer `bento cron` / `bento worker` commands.

Use the app-scoped CLI to invoke the shipped minicrond binary inside the selected runner as the app user. Everything after `--` is passed as argv, not interpreted by Bento:

```sh
bento app minicrond demo -- status
bento app minicrond demo -- list
bento app minicrond demo -- --help
```

Use minicrond's own CLI help for the installed version's job, worker, run, and log commands. With `WEB_BASIC_AUTH` configured on a fixed local Bento port, `bento serve` also offers a protected **Open scheduler** link for each enabled PHP app. It defaults to loopback: tunnel to the server rather than exposing an unauthenticated listener. The browser proxy uses the app's private socket; it never receives scheduler credentials. See [runtime supervision](/advanced/runtime-supervision/).

Check the selected runner with `bento compose -- logs --tail 100 php85-runner` if a daemon is not running. App schedulers are independent: changes in one registry do not trigger `bento apply` or modify another app. The runner must stay a singleton to avoid duplicate firings. Back up each app's registry and WAL consistently when transferring a stack; a live stack archive is not a SQLite snapshot.

Host `backup schedule` remains a separate host-crontab operation. Bento's deploy drain, SQLite VACUUM, and root logrotate are reserved config-owned internal tasks; do not reuse their names for user jobs.
