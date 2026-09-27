---
title: Runtimes
description: PHP-FPM apps and HTTP-process apps.
sidebar:
  order: 2
---

## PHP (`php-fpm`)

```json
{ "kind": "php-fpm", "php": { "version": "8.4", "documentRoot": "current/public", "routing": "front-controller",
  "pool": "small", "uploadLimitMb": 64, "readyPath": "/up", "releaseSymlink": "current" } }
```

- Versions: 8.3, 8.4, 8.5. Pool profiles: `tiny`, `small`, `medium`, `large`, `xlarge`, `ondemand`.
- `front-controller` executes only `index.php` (with `PATH_INFO`); `legacy` executes any `.php` file under the root.
- App code lives at `/home/<slug>/app`. `documentRoot` is relative to that directory.
- Local Nginx listens on port 8080 inside the container, serves static files, denies dotfiles (except
  `.well-known`), and refuses symlinks inside the document root. Set `releaseSymlink` (for example `current`) to let a
  deployment symlink be traversed deliberately; the document root must then live under it.
- Composer and Node.js are included for builds in `app shell`.

## HTTP process (`http-process`)

```json
{ "kind": "http-process", "http": { "toolchain": "node", "version": "24",
  "argv": ["node", "server.js"], "workdir": "app", "port": 3000, "readyPath": "/health" } }
```

- Toolchains: Node.js 20/22/24, Bun 1.2/1.3, Python 3.12/3.13.
- App code lives at `/home/<slug>/app`. `argv` runs directly, never through a shell, and `workdir` is relative to the
  code directory.
- Listen on `0.0.0.0:$PORT` (Bento sets `PORT` and `HOST`). The app serves its own static files and limits.
- There is no Bento-reserved URL path.

## Resources

`{"memoryMb": 512, "cpuMillis": 1000, "pids": 256}` by default, enforced by Docker. Idle apps typically use 20–35 MiB.
