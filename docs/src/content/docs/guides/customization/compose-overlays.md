---
title: Customize Compose with overlays
description: Extend Bento's Compose setup with ordered, operator-owned overlay files.
---

# Customize Compose with overlays

Add or override Compose settings with files under the stack's `overlays/` directory. Bento loads these operator-owned files in a predictable order.

<!-- DIAGRAM PLACEHOLDER
Asset: /diagrams/compose-overlay-order.svg
Alt: Bento-managed Compose files followed by ordered operator overlays and the final merged Compose model.
Show: A stack of managed files, then overlays named 10-, 20-, and 90- in lexical order, all merging into one effective Compose model. Add validation before apply and a separate container-recreate path for mounts, environment, networks, or image changes.
-->

## Add an overlay

Use a lexical filename such as `/var/lib/bento/overlays/20-local.yml`:

```yaml
services:
  nginx:
    environment:
      EXAMPLE_FLAG: "1"
```

Inspect the deterministic file order and merged model:

```sh
bento compose files
bento compose -- config
bento doctor
```

Apply after validation:

```sh
bento apply
```

Some Compose changes require explicit service recreation because a configuration reload cannot change container mounts, environment, networks, or image settings:

```sh
bento compose -- up -d
```

:::caution
Overlays are trusted and can bypass Bento's base safety model. Back up durable data before changing volumes, service identities, or networking.
:::

## Preserve invariants

Do not remove or break:

- the stack-private network for PHP, runners, databases, and Redis;
- app home and FPM socket mounts;
- app UID/GID execution paths;
- singleton runner cardinality;
- database/Redis named volumes;
- protected credential mounts;
- Nginx certificate/custom/generated mounts.

Never add public database or Redis ports as a routine workaround. The wrapper still refuses `down -v`.

## Troubleshooting

If `doctor` reports an invalid overlay, run `compose -- config`, fix YAML/merge semantics, and apply again. A valid merged model can still violate runtime invariants; remove the newest overlay and recreate only affected services to recover.

## Advanced

Generated base/runtime fragments load first; `.yml` and `.yaml` overlays load last in lexical order. Upgrades may change the underlying service model, so review every overlay after upgrading Bento.

## Next steps

- [Manage stack services](/guides/stacks/manage/)
- [Stack layout reference](/reference/stack-layout/)
- [Architecture](/advanced/architecture/)
