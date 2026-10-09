---
title: Your first app
description: Create, deploy, start, and publish a PHP app.
sidebar:
  order: 3
---

```bash
export BENTO_STACK_ROOT=/srv/bento/prod
cat > shop.json <<'JSON'
{
  "slug": "shop",
  "runtime": { "kind": "php-fpm",
    "php": { "version": "8.4", "documentRoot": "public", "routing": "front-controller", "mode": "standard", "uploadLimitMb": 64 } },
  "hosts": [{ "name": "shop.example.com", "route": { "tls": "acme", "redirectHttps": true, "accessLog": false } }],
  "bindings": [{ "engine": "mysql", "service": "mysql84" }]
}
JSON
bento app create --json shop.json
```

The app now has a UID, a home at `/home/shop` inside its container (`homes/shop` in the stack root), and an empty code
directory at `/home/shop/app`. It also has a MySQL user and database and a Redis ACL user. It is **stopped and
unpublished**. `hosts` creates Ingress hosts that point at the new app; you can add, re-point, or remove them later
with `bento host` (see [Edge](/guides/ingress/edge/)).

1. **Deploy code** as the app user: `bento app shell shop` opens `/home/shop/app` in a throwaway tooling container with
   the app's identity and credentials in the environment. Clone directly into the empty code directory, then install:
   `git clone <repository> . && composer install`.
2. **Start:** `bento app start shop`. Bento waits until FPM, local Nginx, the scheduler, and a real HTTP request all
   succeed.
3. **Enable the edge** once per stack (see [Edge](/guides/ingress/edge/)), then **publish:** `bento app publish shop`.

Database credentials are environment variables inside the app: `DB_CONNECTION`, `DB_HOST`, `DB_PORT`,
`DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD` for the first binding, `BENTO_DB_<n>_*` for all bindings, and `REDIS_*`.
