---
title: Operate schedules and workers
description: Run app schedules and long-lived workers with locks, timeouts, and scoped controls.
---

# Operate schedules and workers

Run scheduled commands and long-lived workers as the app user. Bento supervises them through the app's selected PHP runner.

## Add a schedule

Prefer an explicit shell command only when you need redirects or pipelines:

```sh
bento cron add \
  --app demo --name scheduler \
  --schedule '* * * * *' --timezone UTC \
  --lock scheduler --timeout 300 \
  --cmd 'php artisan schedule:run >> logs/scheduler.log 2>&1'
bento cron list demo
```

Working directories and locks remain inside the app boundary. Edit omitted options without changing them:

```sh
bento cron edit demo scheduler --timeout 600
```

`cron reload demo` signals only that app's Supercronic service after generated crontab validation.

## Add and control a worker

Prefer argv after `--` to avoid unintended shell evaluation:

```sh
bento worker add \
  --app demo --name queue -- php artisan queue:work
bento worker inspect demo queue
bento worker restart demo queue
```

Other scoped controls are `start`, `stop`, and `signal --signal HUP|ALRM|INT|QUIT|USR1|USR2|TERM|KILL`.

:::caution
Removing a worker stops supervision for that definition. Confirm the application can tolerate interrupted in-flight work before running `worker remove demo queue`.
:::

## Verify

```sh
bento worker list demo
bento compose -- logs --tail 100 php85-runner
```

## Troubleshooting

If no scheduler/worker starts, verify the app is enabled, its PHP runner is running, and generated command/workdir paths exist. Inspect the individual worker and runner logs. Use `--no-apply` only when intentionally batching changes, followed by `apply`.

## Advanced

One runner per PHP version uses s6 to reconcile flat per-app services. Adding/removing one definition does not restart sibling workers or Nginx. Scaling runners is unsupported because it duplicates schedules and work.

## Next steps

- [Configure webhook deployment](/guides/apps/deploy/)
- [Run app commands](/guides/apps/manage/)
- [Runtime supervision](/advanced/runtime-supervision/)
