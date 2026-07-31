---
title: Customize app templates
description: Take ownership of an app template, track upstream changes, or return to Bento's version.
---

# Customize app templates

Use a complete custom `vhost` or `pool` template when a small additive change is not enough. Once selected, you own that app's template and must review upstream changes.

## Select a template

Create/open a copy in the stack custom area using `$EDITOR`:

```sh
bento template select \
  --app demo --kind vhost
```

Or import an existing file:

```sh
bento template select \
  --app demo --kind pool --source /path/to/pool.conf.tpl
```

By default Bento copies imported input under `custom/`. `--no-copy` records the source in place; keep that path durable and protected.

Bento records template provenance, renders, validates, and targets the affected Nginx or PHP-FPM role.

## Check drift

```sh
bento template drift --app demo
```

A `DRIFT` result means the upstream template changed since selection. Review and merge intentionally; Bento does not overwrite your source.

## Return to upstream

```sh
bento template return \
  --app demo --kind vhost
```

Returning changes desired state but preserves the custom source on disk.

## Verify

Run `app show`, `template drift`, and an application request.

## Troubleshooting

If validation fails, correct the trusted custom source or return to upstream; do not edit generated output. Use `--no-apply` only for a deliberate batch followed by `apply`.

## Advanced

A complete template can override safety and routing assumptions that drop-ins preserve. Keep required socket paths, identity, TLS markers, and includes. Prefer [Nginx drop-ins](/guides/customization/nginx/) for additive changes.

## Next steps

- [Customize Nginx with drop-ins](/guides/customization/nginx/)
- [Desired state and generated files](/concepts/desired-state/)
- [Troubleshooting](/reference/troubleshooting/)
