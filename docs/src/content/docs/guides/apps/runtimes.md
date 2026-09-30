---
title: Runtimes
description: PHP-FPM apps and HTTP-process apps.
sidebar:
  order: 2
---

## PHP (`php-fpm`)

```json
{ "kind": "php-fpm", "php": { "version": "8.4", "documentRoot": "current/public", "routing": "front-controller",
  "mode": "standard", "uploadLimitMb": 64, "readyPath": "/up", "releaseSymlink": "current" } }
```

- Versions: 7.4, 8.0, 8.1, 8.2, 8.3, 8.4, 8.5 (7.4 and 8.0 are end-of-life, bullseye-based). Images include bash completion, git, grep, jq, and vim.
- `mode` is `standard` (default) or `high-concurrency`. Both run one PHP-FPM `ondemand` pool: workers start per request
  (about 1 ms) and exit after 10 s idle, so an idle app holds none. Worker count comes from the app memory limit,
  which FPM shares with nginx, cron jobs and workers: `standard` gets 60% of memory at about 48 MB per worker (6 at
  512 MB, 12 at 1 GB), never more than half the PID limit; `high-concurrency` gets 3× that with a smaller per-request
  memory limit, for apps that mostly wait on external APIs or slow queries. To serve more, raise the app memory.

  | Mode | Web memory limit | Max execution time | Jobs & workers memory limit |
  | --- | --- | --- | --- |
  | `standard` | 128 MB | 60 s | 256 MB |
  | `high-concurrency` | 48 MB | 30 s | 256 MB |

- Optional overrides (omit for the mode default): `maxWorkers` (1–200), `webMemoryLimitMb` (16–4096),
  `cliMemoryLimitMb` (16–8192, cron jobs, workers and shells), `maxExecutionSeconds` (1–280), `maxInputVars`
  (100–100000). Web limits are pool defaults, so the app can still raise them with `ini_set()` or a `.user.ini` in
  the document root, which is also where other PHP settings go. A long-running worker picks up a new jobs memory limit
  when it restarts.
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
