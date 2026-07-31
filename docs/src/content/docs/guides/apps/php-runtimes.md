---
title: Manage PHP runtimes
description: Add PHP versions, assign one to an app, and remove versions that no app uses.
---

# Manage PHP runtimes

Run several PHP versions at the same time. Assign each app one version and one FPM capacity profile.

## List and add runtimes

```sh
bento php list
bento php add 8.4
```

Adding a version creates FPM, a singleton runner, and temporary CLI roles from the same image. Bento applies the new configuration as needed. To batch changes, use `--no-apply` and finish with `bento apply`.

Update an app by repeating its domain and selecting the managed version/profile:

```sh
bento app update demo \
  --domain demo.example.com --php 8.4 --fpm medium
```

Omitted runtime choices preserve the app's current values. Run application tests and migrations through the app CLI identity before shifting production traffic.

## Verify

```sh
bento app show demo
bento status
bento exec demo -- php -v
```

## Remove an unused runtime

```sh
bento php remove 8.4
```

Removal is refused if an app uses the version, if it is the default, or if it would remove the final managed PHP version. It removes managed configuration, not application homes or database data.

## Troubleshooting

If a build fails, inspect the PHP service build logs and host disk space. If Bento reports excess capacity, reduce app profiles or spread apps across versions; profiles share a global FPM process cap rather than reserving isolated CPU/memory.

## Advanced

Apps on one version share the image, container namespace, network, and global capacity. The runner must remain a singleton because scaling it duplicates schedules and workers. A runtime move changes the app socket and background role; Bento targets FPM/Nginx/runner reconciliation rather than treating it as a slug rename.

## Next steps

- [Understand the app model](/concepts/app-model/)
- [Run app commands](/guides/apps/manage/)
- [Runtime supervision](/advanced/runtime-supervision/)
