---
title: Add your first application
description: Create a PHP application with MySQL, verify local routing, and prepare code, DNS, and TLS.
---

# Add your first application

Add an app named `demo` to the running `production` stack, create its MySQL database, and verify the generated placeholder through Nginx. This guide keeps the first path on MySQL; PostgreSQL is an alternative for a later application.

## Before you begin

- Complete the [first-stack procedure](/start/first-stack/).
- Keep the stack root at `/var/lib/bento` and ports 80 and 443 assigned to its host-mode Nginx.
- Choose a domain you control. The examples use the reserved placeholder `demo.example.com`; replace it before configuring public DNS or ACME.
- Ensure `curl` is installed for the routing check.
- Ensure the operator can assign the app's numeric UID/GID on the app home, or can run a later permission repair with elevated privileges.

## Ensure the services are ready

Start any stopped roles and wait for their health checks:

```sh
bento --stack /var/lib/bento compose -- up -d --wait
```

Confirm that `mysql84`, `php85`, and `nginx` are running or healthy:

```sh
bento --stack /var/lib/bento compose -- ps
```

MySQL must be reachable before an explicit database request. Bento fails closed rather than recording a database that it could not create.

## Create the app and database

Create `demo` with front-controller routing, the `public` document root, and a MySQL 8.4 database named `demo`:

```sh
bento --stack /var/lib/bento app create demo \
  --domain demo.example.com \
  --docroot public \
  --front \
  --database-engine mysql \
  --mysql 8.4 \
  --database demo \
  --db
```

The command performs the live database grant before saving desired state, creates the app home and a stable Ed25519 deploy key, renders configuration, validates it, and reloads the affected running services. It also writes a placeholder at:

```text
/var/lib/bento/homes/demo/code/public/index.php
```

The app slug `demo` becomes a stable identity reused for its UID/GID, home, PHP pool and socket, database account, Redis prefix, and jobs. Do not treat the slug as a casual rename.

Check the initial filesystem policy:

```sh
bento --stack /var/lib/bento permissions check demo
```

If provisioning reported or routing later reveals ownership errors, run an explicitly recursive repair while the new app tree is still small:

```sh
sudo /usr/local/bin/bento --stack /var/lib/bento \
  permissions repair demo --recursive
```

Do not make recursive repair a routine startup action after the code tree grows.

## Inspect the result

Show the app with secrets redacted:

```sh
bento --stack /var/lib/bento app show demo
```

Confirm that it reports:

- `demo.example.com` as the primary domain;
- PHP service `php85`;
- document root `public` and front-controller routing;
- MySQL service `mysql84` with database `demo`;
- shared starter TLS.

Check the stack-wide view as well:

```sh
bento --stack /var/lib/bento status
```

The app and domain should appear under their respective sections.

## Register the deploy key when needed

Bento creates one deploy key per app and preserves it on later updates. Print only its public half through the app's ephemeral CLI identity:

```sh
bento --stack /var/lib/bento exec demo -- \
  cat /home/demo/.ssh/id_ed25519.pub
```

Add that public key as a read-only deploy key in your Git provider before cloning a private repository.

:::danger
Never copy, print, or upload `/home/demo/.ssh/id_ed25519`. It is the private key. Protect it with the rest of the app home and include it in your recovery plan.
:::

You do not need to register the key for a public repository or for the generated placeholder.

## Verify routing before DNS

Test the local HTTP route while forcing the correct hostname to loopback:

```sh
curl --resolve demo.example.com:80:127.0.0.1 http://demo.example.com/
```

The response should contain:

```text
bento app demo
```

This proves the local Nginx-to-PHP path without waiting for DNS. It does not prove that the host is reachable from the internet.

## Prepare the real application

The durable code directory is `/var/lib/bento/homes/demo/code/`, mounted in the PHP roles as `/home/demo/code/`. Replace the generated placeholder with your application using a deployment process that runs as the app identity. Keep the selected document root at `/home/demo/code/public`, or update the app deliberately if your framework uses another layout.

Bento writes database and Redis connection metadata to the private app file:

```text
/home/demo/credentials/app.env
```

Adapt those values into your framework's configuration without committing the credential file or printing its secrets. You can verify database access interactively as the app account:

```sh
bento --stack /var/lib/bento mysql shell --app demo --database demo
```

Exit the MySQL client with `quit` after the connection succeeds.

## Configure DNS and TLS

For public traffic, replace `demo.example.com` in the app configuration with your actual domain if necessary, then create DNS A and/or AAAA records pointing to this host. Verify resolution from outside your private network before enabling ACME.

The initial `shared` TLS mode uses a starter self-signed certificate and does not provide public domain validation. For public ACME certificates, first set `ACME_EMAIL` in the stack's private `/var/lib/bento/.env`, confirm that every app domain resolves to this host, and confirm that public TCP port 80 reaches Nginx. Then run:

```sh
bento --stack /var/lib/bento tls set --app demo --mode acme
```

:::caution
Do not enable ACME before DNS and public port 80 are correct. Issuance will fail, and repeated attempts can encounter certificate-authority rate limits.
:::

## Troubleshooting

**App creation says MySQL is unavailable:** run `compose -- ps` and inspect `mysql84` logs. Wait for MySQL to become healthy, then rerun the same `app create ... --db` command. The failed explicit request does not record the database or app state.

```sh
bento --stack /var/lib/bento compose -- logs --tail 100 mysql84
```

**The domain is already owned:** choose another domain or inspect its current owner with `status`. Bento refuses duplicate app and proxy domains.

**The local request returns `502 Bad Gateway`:** inspect `php85` and Nginx logs, then run `doctor`. Confirm that the app pool was generated and the PHP role is running.

```sh
bento --stack /var/lib/bento compose -- logs --tail 100 php85 nginx
```

**The request returns another site or a default response:** include the exact app hostname in `--resolve` and confirm that `app show demo` contains the same primary domain.

**The public key command fails:** confirm that the `php85-cli` image can be created and that the app home contains `.ssh/id_ed25519.pub`. Re-running app provisioning preserves an existing valid key pair.

## Advanced

Without `--db`, Bento may create the database account on a best-effort basis and defer that work while MySQL is unavailable. The first-app path uses `--db` so database creation is explicit and transactional with respect to desired-state recording.

An app binds to one database engine. A MySQL or PostgreSQL app also binds to one managed service. To use PostgreSQL for a different app, first add a supported PostgreSQL major, then select it with `--database-engine postgres --postgres <major> --db`. For a private file database and optional S3 replication, follow the [SQLite guide](/guides/data/sqlite/). Bento does not automatically move an existing app between engines.

The generated credential file is application metadata, not automatic framework configuration. Bento does not infer how Laravel, Symfony, WordPress, or another application loads environment variables.

## Next steps

- Return to the [documentation home](/) for current guides on TLS, deployment, and backups.
- [Review the stack startup and diagnostics flow](/start/first-stack/).
- [Review DNS, firewall, and host requirements](/start/requirements/).
