---
title: Configure webhook deployment
description: Enable authenticated queued deployment, replace the no-op hook, and inspect or recover jobs.
---

# Configure webhook deployment

Enable a signed HTTPS webhook that queues one app-owned deployment job for its PHP runner.

## Before you begin

- The app, HTTPS route, FPM service, and singleton runner must work.
- Choose `latest` coalescing (default) or bounded FIFO.
- Prepare an application-specific deployment and rollback plan.

## Enable and configure

```sh
bento --stack /var/lib/bento deploy enable demo
bento --stack /var/lib/bento deploy instructions demo
```

The enable output includes the HMAC secret once. Store it immediately in the provider's secret field; do not put it in source control or chat.

Edit `/var/lib/bento/homes/demo/.bento/deploy.sh`. The generated hook intentionally performs no deployment and exits `99` (`skipped`). A real hook should fail on errors, update code/dependencies, run migrations deliberately, and return `0` only after verification.

Configure the provider to send HTTPS `POST` requests to `https://demo.example.com/_bento/deploy` with a GitHub-compatible HMAC signature. Bodies above 256 KiB are rejected.

## Verify and operate

Trigger a test delivery, then run:

```sh
bento --stack /var/lib/bento deploy status demo
bento --stack /var/lib/bento deploy drain demo
```

The runner normally drains each minute. Logs are under `homes/demo/logs/`. Exit `0` is success, `99` skipped, and other exits failed. A finished attempt requests an app-pool OPcache reset; reset failure is logged without changing the hook result.

## Rotate or disable

:::caution
Rotation invalidates the old provider secret as soon as the applied configuration changes. Coordinate both sides.
:::

```sh
bento --stack /var/lib/bento deploy rotate demo
bento --stack /var/lib/bento deploy disable demo
```

## Troubleshooting

`401` means a signature mismatch, `413` an oversized body, and FIFO `429` a full queue. Confirm exact raw-body signing and provider headers. For timeouts or stale jobs, inspect status/logs and runner health; the next drain reclaims interrupted jobs after timeout plus grace.

## Advanced

`latest` supersedes older queued work while allowing the running job to finish. FIFO accepts at most 20 queued jobs. One app has one active drain; webhook handling only enqueues and returns `202`.

## Next steps

- [Operate schedules and workers](/guides/apps/schedules-workers/)
- [Inspect app status](/guides/apps/manage/)
- [Runtime supervision](/advanced/runtime-supervision/)
