---
title: Auto deploy
description: Deploy an app on every push with a webhook.
sidebar:
  order: 6
---

Auto deploy runs the same deploy as `bento app deploy` whenever your git host reports a push to the app's branch:
fetch the branch head, run `~/deploy.sh` if present, then reload the app.

```text
git push ──► git host ──POST /_webhook/deploy/<id>──► your ingress ──► Bento public listener
                                                                          │ verify secret
                                                                          │ push to configured branch?
                                                                          ▼
                                           app.deploy: fetch ─► ~/deploy.sh ─► record ─► reload
```

## 1. Prerequisites

- The app has a git source and deploys by hand: see [Git deploy](/guides/apps/deploy/).
- Optional: a [`~/deploy.sh`](/guides/apps/deploy-scripts/) for installs, builds, and migrations.

## 2. Enable the webhook

```bash
bento app webhook shop --enable
```

```text
url:    https://hooks.example.com/_webhook/deploy/f371135742d0b9169e93273dbeeacdf3
secret: c312c9c4…                     # shown only once
```

Or use the app's **Deploy** tab → **Enable webhook**. Copy the secret now: it is never shown again. If you lose it,
rotate it (`--rotate`), which keeps the URL.

The URL path is not a secret and may appear in logs; every request must also prove the secret.

## 3. Expose `/_webhook/*`

Webhooks are served by Bento's **public listener**, a port separate from the management UI that serves nothing but
routes that authenticate themselves. `bento serve` listens by default on:

| Address | Reachable from |
| --- | --- |
| `127.0.0.1:7781` | the host (nginx, Caddy on the host) |
| `apps:7781` — the host's address on the stack's apps network, e.g. `10.200.0.128:7781` | the edge, cloudflared, other containers |

See them under **Ingress → Public URL**. Route the path with the ingress you already use:

**Managed edge** — nothing to do: every domain the edge routes forwards `/_webhook/*` to Bento.

**Host nginx or Caddy**

```nginx
location /_webhook/ {
  proxy_pass http://127.0.0.1:7781;
  client_max_body_size 8m;
}
```

**Cloudflare Tunnel** — in the tunnel's public hostnames, add a rule **above** the app's own rule:

| Field | Value |
| --- | --- |
| Hostname | `shop.example.com` (or a dedicated `hooks.example.com`) |
| Path | `^/_webhook/` |
| Service | `HTTP` · the apps-network address, e.g. `10.200.0.128:7781` |

cloudflared runs in a container, so it cannot use `127.0.0.1`. If a host firewall (for example ufw) drops traffic
from containers to the host, allow it: `ufw allow from 10.200.0.0/24 to any port 7781 proto tcp`.

Then set **Ingress → Public URL** to the origin you exposed (for example `https://hooks.example.com`). Bento only uses
it to show the full webhook URL; routing stays yours.

To change the listener, start the backend with `--public-listen` (repeatable; `IP:PORT`, `apps:PORT`, or `off`).

## 4. Configure the git host

Use the full URL from step 2 and the secret. Send **push** events only.

| Host | Where | Secret field |
| --- | --- | --- |
| GitHub | Repository → Settings → Webhooks → Add webhook. Content type `application/json`. | Secret |
| GitLab | Project → Settings → Webhooks. Trigger: Push events (branch filter optional). | Secret token |
| Gitea / Forgejo | Repository → Settings → Webhooks → Gitea/Forgejo. Trigger on push. | Secret |
| Bitbucket Cloud | Repository settings → Webhooks. Trigger: Repository push. | Secret |
| CI / scripts | `curl -fsS -X POST -H "Authorization: Bearer $BENTO_WEBHOOK_SECRET" "$URL"` | bearer token |

GitHub and Bitbucket send a ping when the webhook is created; it should show as **ping** in the Deploy tab.

## 5. What triggers a deploy

| Delivery | Result |
| --- | --- |
| Push to the app's configured branch | **deployed**: a deploy of the branch head is queued |
| Push while a deploy is already queued | **coalesced**: joins the queued deploy (it fetches the newest head anyway) |
| Same delivery sent again (provider redelivery) | **duplicate**: returns the original deploy |
| Push to another branch, a tag, or a branch deletion | **ignored-ref**, nothing runs |
| Other events (issues, pull requests, …) | **ignored-event** |
| Bearer request from CI (no provider headers) | **deployed**: CI decides when to call it |
| Unknown URL or wrong secret | `404`, not recorded |

The payload never chooses the code: Bento always fetches the configured branch from the configured repository.
Changing the branch (`bento app git shop --branch release`) takes effect for the next push; the URL and secret stay.

## 6. Watch and trace deliveries

The app's **Deploy** tab lists the last 20 deliveries as a timeline. Expand one to see:

- when it arrived, the provider's delivery id, and who pushed;
- which credential verified it (for example `X-Hub-Signature-256 (HMAC-SHA256)`);
- Bento's decision and why (for example "ignored-ref — configured branch is main");
- for deploys, the live deploy log: git output, every `deploy.sh` line, and the reload.

From the CLI: `bento app webhook shop` lists deliveries, and `bento op <operationId>` prints a deploy's log.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Git host reports timeout or 502 | the path does not reach the public listener: check the ingress rule and, for tunnel/edge, the host firewall |
| Git host reports `404` | wrong URL, wrong secret, webhook disabled, or the path was sent to the app instead of Bento |
| Delivery shows **ignored-ref** | the push was to a different branch than `bento app git shop` shows |
| Delivery **deployed** but the operation failed | expand it: the git error or the failing `deploy.sh` line is in the log |
| `413` | payload over 8 MiB; send push events only |

## Security notes

- The secret is 256 random bits, stored in Bento's database, and returned only by enable/rotate.
- GitHub, Gitea, Forgejo, and Bitbucket sign the body (HMAC-SHA256); GitLab and bearer requests send the secret
  itself, so expose the URL over HTTPS only.
- The public listener never serves the UI or management API. The worst a valid request can do is queue a deploy of
  the configured branch.
- Disabling the webhook, removing the git source, or removing the app destroys the URL and secret.
