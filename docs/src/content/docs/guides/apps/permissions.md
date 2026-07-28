---
title: Check and repair app permissions
description: Audit app ownership and modes, preview repairs, and choose shallow or explicit recursive correction.
---

# Check and repair app permissions

Audit one app's filesystem policy and repair only the necessary scope without following symlink targets.

## Check first

```sh
bento --stack /var/lib/bento permissions check demo
```

Use `--recursive` to inspect the whole app tree. The default check focuses on core paths and is faster for large homes.

## Preview and repair

```sh
bento --stack /var/lib/bento permissions repair demo --dry-run
bento --stack /var/lib/bento permissions repair demo --shallow
```

Shallow repair is the routine path and fixes core directories. It avoids recursively rewriting a potentially large deployment.

:::caution
Recursive repair can change ownership and modes across many application files and may take significant time. Stop deployment/file-writing activity and review a dry run first.
:::

```sh
bento --stack /var/lib/bento permissions repair demo \
  --recursive --dry-run
bento --stack /var/lib/bento permissions repair demo --recursive
```

## Verify

```sh
bento --stack /var/lib/bento permissions check demo --recursive
bento --stack /var/lib/bento exec demo -- test -r public/index.php
```

## Troubleshooting

If repair reports permission denied, run Bento as an account with ownership-changing authority for this stack. If Nginx returns `403`, verify the configured document root exists and the public tree is group-readable/traversable. Recursive traversal does not follow symlink targets; repair a legitimate external target separately under its own ownership policy.

## Advanced

Web, CLI, cron, workers, and deploys use the same app UID/GID. Nginx mounts homes read-only and receives group access to public files and FPM sockets, not write access to private app directories. This protects routine ownership boundaries, not hostile co-tenancy.

## Next steps

- [Understand app identity](/concepts/app-model/)
- [Troubleshoot common symptoms](/reference/troubleshooting/)
- [Isolation and security](/advanced/isolation-security/)
