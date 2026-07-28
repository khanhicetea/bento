---
title: Operate access logs
description: Enable, rotate, analyze, and disable per-app Nginx access logs while controlling sensitive retention.
---

# Operate access logs

Enable request logs for one app, rotate them without a configuration reload, and create an ad hoc report.

## Enable and verify

:::caution
Access logs can contain client addresses, URLs, query strings, referrers, and user agents. Set retention and sharing policy before enabling them.
:::

```sh
bento --stack /var/lib/bento logs access enable --app demo
bento --stack /var/lib/bento app show demo
```

Generate a request, then inspect files beneath `logs/nginx/`. Enabling/disabling targets an Nginx-only configuration reload.

## Rotate and report

```sh
bento --stack /var/lib/bento logs access rotate --app demo
bento --stack /var/lib/bento logs access report \
  --app demo --output /var/lib/bento/logs/reports/demo.html
```

The one-shot GoAccess container reads the log and writes HTML. Use `--dry-run` to inspect its Docker argv or `--attach` for an interactive terminal dashboard.

Disable future logging while retaining existing files:

```sh
bento --stack /var/lib/bento logs access disable --app demo
```

## Troubleshooting

If the log stays empty, verify the app is enabled, traffic reaches this stack/domain, and `app show` reports access logging enabled. If a report fails, confirm Docker access and that the log exists. Avoid sharing raw logs or reports without redaction.

## Advanced

Rotation renames the active file and asks Nginx to reopen logs; it does not reload configuration. Host maintenance prunes recognized rotated logs/reports by age. Runner file logs and Docker service logs have separate retention mechanisms.

## Next steps

- [Run stack maintenance](/guides/stacks/maintenance/)
- [Diagnose a stack](/guides/stacks/diagnostics/)
- [Safety and durability](/concepts/safety-and-durability/)
