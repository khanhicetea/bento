---
title: Run Node.js, Bun, and Python projects
description: Supervise an HTTP process app behind Bento Nginx with shared databases and Redis.
---

# Run Node.js, Bun, and Python projects

A process app is a trusted Node.js, Bun, or Python HTTP project supervised by Bento. It keeps the normal app slug, UID/GID, home, domains, TLS, database bindings, Redis identity, backups, and diagnostics. Unlike PHP, each process app has one dedicated private container because these runtimes do not provide an FPM-style multi-pool server.

## Create a staged app

Provide an exact numeric runtime version and explicit argv. Repeat `--start` once per literal argument; Bento never passes the command through an implicit shell.

```sh
bento app create api \
  --domain api.example.com \
  --runtime node \
  --runtime-version 24 \
  --start node \
  --start dist/server.js \
  --health-path /health
```

Supported runtime values are `node`, `bun`, and `python`. The default private HTTP port is `8080`; override it with `--port`. Use `--workdir` for a path inside `/home/<slug>`.

A new process app is disabled. Its domain is reserved, home and credentials are materialized, and its private Compose/CLI roles are rendered, but Nginx does not publish the route yet.

## Prepare the Git project

Bento creates a stable Ed25519 key under `homes/api/.ssh`. Register its public key with the Git host, then check out and build the project as the app identity:

```sh
bento app shell api
# inside the ephemeral runtime shell:
git clone git@example.com:team/api.git /home/api/code
cd /home/api/code
npm ci
npm run build
exit
```

Use the corresponding Bun or Python package commands for those toolchains. Bento does not auto-detect a repository, choose a package manager, or run dependency installation during `render` or `apply`.

The generated `credentials/app.env` contains the app-scoped database and Redis metadata. The process entrypoint loads it without placing secret values in Compose configuration or host argv. Application-specific secrets remain operator-owned files under the protected app home.

## Start privately, then publish

Build and start only this process service:

```sh
bento app start api
bento status
```

The service publishes no host port. It listens on private loopback in its container, while a small adapter exposes `/run/bento-http/http.sock`. Stack Nginx consumes the corresponding socket under `/run/bento-apps/api/http.sock`.

After the service is running and its TCP or HTTP health check succeeds, publish the domain:

```sh
bento app enable api
curl -I -H 'Host: api.example.com' http://127.0.0.1/
```

Enablement refuses when the private service is not observed running. This prevents a newly created project from immediately publishing a known-broken route.

## Update and operate

Repeat the runtime selector when updating a process app:

```sh
bento app update api \
  --domain api.example.com \
  --runtime node \
  --runtime-version 24 \
  --start node --start dist/server.js \
  --health-path /health
```

If the app service is already running, Bento recreates only that service after saving and validating the new configuration. A stopped process app remains stopped.

Run one-off commands through the same image and UID/GID:

```sh
bento exec api --workdir /home/api/code -- node --version
bento app shell api
```

Disable removes the public route before stopping the private container:

```sh
bento app disable api
```

`bento app stop api` stops the private container without changing desired enablement. Use it for explicit maintenance; an enabled route will return an upstream error while the service is stopped.

## Security and limits

Process app services:

- publish no host ports and receive no Docker socket;
- mount only their own home, file-database directories, and socket directory;
- run app code as the stable app UID/GID;
- use a read-only container root, bounded Docker logs, and dropped capabilities;
- share the host kernel and private backend network, so they are not hostile-tenant sandboxes.

The curated images cover the language runtime and common Git/HTTP/SQLite tools. Projects requiring additional native or operating-system packages may need a future custom-image provider; Bento does not currently promise arbitrary repository compatibility or buildpack detection.

Process-app cron jobs, workers, and signed webhook deploy are intentionally refused in the initial runtime. They are not routed through a PHP runner. Use an operator-controlled deployment flow and `app update`/`app start` until language-neutral process supervision for those features ships.

## Next steps

- [Manage applications](/guides/apps/manage/)
- [Configure domains and TLS](/guides/apps/domains-tls/)
- [Back up and restore application data](/guides/data/backup-restore/)
- [Understand isolation and security](/advanced/isolation-security/)
