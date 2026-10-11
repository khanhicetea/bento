# Live end-to-end test guide

A repeatable, hands-off run of a real Bento stack against real external services: GitHub (public and private repos,
deploy keys, push webhooks), a Cloudflare Tunnel with real DNS, and S3-compatible storage (Cloudflare R2) for database
dumps and restic app backups. It complements the Docker integration suite (`make -C apps/backend test-integration`),
which never leaves the host.

Written for an agent to follow end to end. The operator only provides the accounts once (see
[Prerequisites](#prerequisites)).

## Rules

- **Disposable only.** Use the dedicated GitHub test account, the e2e tunnel and `e2e-*` hostnames, the test bucket,
  and the `/srv/bento-e2e` stack. Within these you may create, wipe, and rebuild without asking.
- **Never touch** anything else on the host or in the accounts: other Bento stacks (for example `bento-dev` on
  `/tmp/dev-stack`, ports 7780/7781/8880/8843), unrelated containers, other tunnels, or other DNS records in the zone.
- **Never print secrets.** Source `~/.config/bento-e2e/env` and pass values by variable; mask account IDs in output.
- **Report honestly.** ACME needs public ports 80/443 and arm64 needs an arm64 host; report them as skipped unless
  they actually ran.

## Prerequisites

| Item | What | Used for |
| --- | --- | --- |
| Host | Linux amd64, root, Docker 25+, Go and Bun toolchains from `mise.toml` | build and run the stack |
| GitHub | `gh auth login -h github.com -p https -w -s repo,admin:repo_hook,delete_repo` as a **test account**, then `gh auth setup-git` | repos, deploy keys, webhooks |
| Cloudflare | account API token: zone policy (DNS Edit, Zone Read) on the test zone + account policy **Argo Tunnel (Legacy) Edit** | tunnel and hostnames |
| S3 | bucket + key scoped to it (Get/Put/Delete/List) | DB dumps and restic repos |

`~/.config/bento-e2e/env` (mode 0600, outside the repo):

```bash
E2E_DOMAIN=aitea.app              # zone on Cloudflare; test hosts are e2e-*.$E2E_DOMAIN
CF_API_TOKEN=                     # account-owned token: /user/tokens/verify fails, /accounts/$ID/tokens/verify works
CF_ACCOUNT_ID=
CF_ZONE_ID=
S3_ENDPOINT=https://<account_id>.r2.cloudflarestorage.com
S3_REGION=auto
S3_BUCKET=
S3_ACCESS_KEY_ID=                 # R2: 32 hex chars
S3_SECRET_ACCESS_KEY=             # R2: 64 hex chars
```

Validate before anything else. R2 IDs, keys, and endpoints are lowercase hex: a letter `l` in them is a typo for `1`
(this happened on the first run and showed up as a TLS handshake failure, then `SignatureDoesNotMatch`).

```bash
set -a; . ~/.config/bento-e2e/env; set +a
H="Authorization: Bearer $CF_API_TOKEN"
curl -s -H "$H" https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/tokens/verify | jq -c '{success}'
curl -s -H "$H" https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID | jq -c '{success, name: .result.name}'
curl -s -H "$H" "https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/cfd_tunnel?is_deleted=false" | jq -c '{success}'
gh auth status
[[ $S3_SECRET_ACCESS_KEY =~ ^[0-9a-f]{64}$ ]] && echo "secret format ok"
curl -s -o /dev/null -w 'r2 %{http_code}\n' "$S3_ENDPOINT"     # 400 = reachable; 000 = bad endpoint
```

## Helpers

| Path | Purpose |
| --- | --- |
| `scripts/e2e/cf-host.sh add\|rm NAME [SERVICE]` | add/remove `NAME.$E2E_DOMAIN` on the e2e tunnel plus its proxied CNAME; default service is the e2e edge |
| `scripts/e2e/api.sh METHOD PATH [JSON]` | REST call over the control socket; waits for accepted operations (`finalState`) |
| `scripts/e2e/q.php "SQL"` | run SQL against an app's first MySQL binding (PHP images have no `mysql` client) |
| `scripts/e2e/apps/*` | sample apps pushed to the test GitHub account |

Common shell setup for every step below:

```bash
export BENTO_STACK_ROOT=/srv/bento-e2e
B=/opt/bento-e2e/bento
E=$PWD/scripts/e2e          # from the repo root
set -a; . ~/.config/bento-e2e/env; set +a
GU=$(gh api user --jq .login)
R='{"tls":"none","redirectHttps":false,"accessLog":true}'   # cloudflared talks plain HTTP to the edge
```

## 1. Build and init

```bash
make -C apps/backend web
(cd apps/backend && CGO_ENABLED=0 go build -trimpath -o /opt/bento-e2e/bento ./cmd/bento)
umask 077; head -c 24 /dev/urandom | base64 | tr -d '/+=' > ~/.config/bento-e2e/ui-password
$B --stack /srv/bento-e2e init --name e2e --mysql 8.4 --postgres 18 --uid-first 30000 --uid-last 39999 \
  --password-stdin < ~/.config/bento-e2e/ui-password
nohup $B --stack /srv/bento-e2e serve --listen 127.0.0.1:7790 --utils-listen 127.0.0.1:7791 --utils-listen apps:7791 \
  > /srv/bento-e2e-serve.log 2>&1 &
$B status; $B services          # expect mysql84, postgres18, redis healthy
```

The separate UID range and ports keep the stack clear of any other stack on the host. Expect: `init` refuses an
existing root.

## 2. Edge and tunnel

```bash
echo '{"enabled":true,"bind":"127.0.0.1","httpPort":18080,"httpsPort":18443,"http3":false,"acmeEmail":"",
  "acmeUrl":"https://acme-staging-v02.api.letsencrypt.org/directory"}' > /tmp/edge.json
$B edge set --json /tmp/edge.json             # expect state healthy, ports 127.0.0.1:18080/18443

TS=$(head -c 32 /dev/urandom | base64)
TID=$(curl -s -X POST -H "Authorization: Bearer $CF_API_TOKEN" -H 'Content-Type: application/json' \
  https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/cfd_tunnel \
  -d "{\"name\":\"bento-e2e\",\"config_src\":\"cloudflare\",\"tunnel_secret\":\"$TS\"}" | jq -r .result.id)
echo "$TID" > ~/.config/bento-e2e/tunnel-id
curl -s -H "Authorization: Bearer $CF_API_TOKEN" \
  https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/cfd_tunnel/$TID/token | jq -r .result | $B tunnel set-token
$B tunnel                                     # expect enabled, healthy
```

Reuse an existing `bento-e2e` tunnel instead of creating a second one when `tunnel-id` already exists.

## 3. Apps

Push each fixture to the test account (`gh repo create NAME --public|--private --source DIR --push` from a copy with
`git init`). Then for each row: write the create JSON, `$B app create --json`, `$B app git`, write `~/deploy.sh` if
listed, `$B app deploy`, `$B app start`, `$B app publish`, `$E/cf-host.sh add SLUG`.

| Slug | Repo | Runtime JSON (`runtime`) | Binding | `~/deploy.sh` |
| --- | --- | --- | --- | --- |
| e2e-node | public, HTTPS URL | `http-process`, node 22, argv `["node","server.js"]`, port 3000, readyPath `/health` | postgres18 | `npm install --omit=dev` |
| e2e-php | **private**, SSH URL + deploy key | `php-fpm` 8.4, docroot `public`, `front-controller` | mysql84 (+ Redis, automatic) | write `deployed-at.txt` |
| e2e-legacy | public | `php-fpm` 7.4, docroot `""`, `legacy` | mysql84 | none |
| e2e-bun | public | `http-process`, bun 1.3, argv `["bun","server.ts"]`, port 3000 | sqlite | none |
| e2e-py | public | `http-process`, python 3.13, argv `[".venv/bin/python","app.py"]`, port 8000 | postgres18 | `python3 -m venv .venv` (once) + `pip install -r requirements.txt` |

Create JSON shape:

```json
{"slug":"e2e-node","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"22","argv":["node","server.js"],
 "workdir":"","port":3000,"readyPath":"/health"}},"ingress":"managed",
 "hosts":[{"name":"e2e-node.aitea.app","route":{"tls":"none","redirectHttps":false,"accessLog":true}}],
 "bindings":[{"engine":"postgres","service":"postgres18"}]}
```

Private repo deploy key:

```bash
$B --json app git e2e-php --repo git@github.com:$GU/e2e-php.git --branch main | jq -r .publicKey > /tmp/k.pub
gh repo deploy-key add /tmp/k.pub -R $GU/e2e-php -t bento-e2e     # read-only by default
```

Writing `~/deploy.sh`: `$B app exec SLUG -- bash -c 'printf "#!/bin/bash\n..." > ~/deploy.sh && chmod 755 ~/deploy.sh'`.
To put a file in an app home from the host, copy it under `/srv/bento-e2e/homes/SLUG/tmp/` and `chown UID:UID`; inside
the container it is `/home/SLUG/tmp/...`. `app exec` runs in a separate tool container, so the app container's
`/tmp` is not shared.

**Expect** each `https://SLUG.$E2E_DOMAIN/` to answer 200 with JSON naming its runtime version and a growing `hits`
counter (the DB write works); e2e-php also returns `redisCounter`.

## 4. Deploy cases

| Case | How | Expect |
| --- | --- | --- |
| Push to deploy | `$B --json app webhook e2e-php --enable`; register its `url` (`https://e2e-php.$E2E_DOMAIN<path>`) on GitHub (`gh api -X POST repos/$GU/e2e-php/hooks`, `content_type json`, the secret); push a commit | GitHub ping 200, push 202; `app webhook` lists `push deployed`; new marker live |
| Wrong branch | push to `feature` | delivery `ignored-ref` |
| Forged calls | POST with no signature, a wrong `X-Hub-Signature-256`, a wrong bearer, a wrong path, and a GET | all 404 |
| CI style | `curl -X POST -H "Authorization: Bearer $SECRET" URL` | 202, delivery `generic trigger deployed` |
| Failing script | add a `FAIL` file the script checks for, push, deploy | op fails with the script's exit code; old version keeps serving; next good deploy reloads |

With the edge bound to loopback and a tunnel configured, the webhook URL that Bento prints is
`https://host/_bento/webhook/deploy/<id>`, without the edge port; `path` is always printed as well.

## 5. Ingress modes

| Case | How | Expect |
| --- | --- | --- |
| External | `$B app update e2e-bun --json` with `{"ingress":"external"}`; `$E/cf-host.sh add e2e-bun <ingressInfo.internalUrl>` | 200 through the tunnel; edge Host-header request 404; stopping the app gives 502 |
| Private | `{"ingress":"none"}` on e2e-py | 404 through the tunnel and the edge; app stays healthy |
| Unpublish/publish | `$B app unpublish|publish e2e-legacy` | 404, then 200 |
| Stop/start | `$B app stop|start` | stop unpublishes first; start does **not** republish (documented) |
| Legacy docroot | request `/.env`, `/.git/config`, `/.hidden.php` | 403 |

## 6. Backups

Add an rclone remote by appending to `/srv/bento-e2e/rclone/rclone.conf` (mode 0600; no config password):

```ini
[e2es3]
type = s3
provider = Cloudflare
access_key_id = ...
secret_access_key = ...
endpoint = https://<account_id>.r2.cloudflarestorage.com
region = auto
no_check_bucket = true
```

| Case | How | Expect |
| --- | --- | --- |
| Remote test | `$E/api.sh POST /api/v1/backups/rclone/test '{"remote":"e2es3:BUCKET/bento-e2e/db"}'`; repeat with a broken endpoint | succeeded within seconds; the broken one fails within seconds (at most 60s) with rclone's error |
| DB dump + upload | `$B backup run --app e2e-php --upload e2es3:BUCKET/bento-e2e/db` (also `--compression gzip` on e2e-node) | `upload: succeeded`; artifacts in `backup list` and in the bucket |
| DB restore | drop `hits` with `q.php`, then `echo "replace e2e_php_main" \| $B backup restore --artifact PATH --app e2e-php --database e2e_php_main` | wrong text refused; table back with the dump's row count |
| App backup | `api.sh PUT /api/v1/apps/ID/restic` (repository `e2es3:BUCKET/bento-e2e/apps/e2e-php`, paths `["."]`), `POST .../restic/init` (save `.key` to `~/.config/bento-e2e/*.restic.key`), `POST .../restic/backup` | snapshot listed, `lastBackup.ok` |
| Clone on stack | `POST .../restic/inspect` then `.../restic/clone` with `{"snapshot":"latest","slug":"e2e-php-clone","confirm":"clone e2e-php-clone"}`; add a host (`$B host add`), start, publish | code, untracked files, DB rows restored; new DB name and user; Redis empty (not backed up) |
| Disaster restore | `echo "clone e2e-php-dr" \| $B app restore-from-backup --repo e2es3:... --key-file KEY --slug e2e-php-dr --snapshot latest` | stopped app plus a checklist (DB user, deploy key, hosts); a random key fails with `wrong key` |

`$B backup list --json` (or `$B --json backup list`) prints the artifact list as JSON. The REST inspect and clone
endpoints accept a full or short snapshot ID or `latest`, like the CLI.

## 7. Removal and retained data

| Case | How | Expect |
| --- | --- | --- |
| Remove | `echo "delete SLUG" \| $B app remove SLUG` | wrong text refused; containers gone; `retired` lists the home and databases |
| Slug reuse | create the same slug again | `conflict: a retained home ... exists` |
| Prune | `echo delete \| $B retired prune APP_ID` | databases, user, and home dropped; entry stays with `prunedAt` |
| UID non-reuse | create the slug again | gets the next UID, never the retired one |

## Teardown

```bash
for a in $($B --json apps | jq -r '.apps[].slug'); do echo "delete $a" | $B app remove $a; done
for id in $($B retired | jq -r '.retired[] | select(.prunedAt == null) | .appId'); do echo delete | $B retired prune $id; done
SID=$($B --json status | jq -r .stackId)          # read before stopping the backend
kill "$(pgrep -fx '/opt/bento-e2e/bento --stack /srv/bento-e2e serve.*')"
docker ps -a --filter label=io.bento.stack-id=$SID -q | xargs -r docker rm -f    # full reset only: also data services
for h in e2e-node e2e-php e2e-legacy e2e-bun e2e-py e2e-php-clone; do $E/cf-host.sh rm $h; done
for r in e2e-node e2e-php e2e-legacy e2e-bun e2e-py; do gh repo delete $GU/$r --yes; done
```

Keep the tunnel and the bucket for the next run unless asked; delete the stack root only for a full reset.

## Last run: 2026-10-11

Commit `7ca4f46`, Docker 29.8.1, amd64. Every case in sections 1–7 passed. Skipped: ACME issuance (no public 80/443),
arm64, GitLab/Gitea/Bitbucket webhooks, scheduled (cron) backups, Object Lock buckets.

### Known findings

1. ~~**Webhook URL wrong behind a tunnel or with a non-default edge port.**~~ **Fixed:** with a loopback-bound edge
   or a configured tunnel the URL is `https://<host>/_bento/webhook/...` (no edge port); a public edge keeps its
   scheme and port. The CLI and UI always show the path too. Not yet re-run live.
2. ~~**rclone Test with an unreachable endpoint** ran for about 2.5 minutes, then `context deadline exceeded`.~~
   **Fixed:** the test adds `--low-level-retries 1` (rclone's default 10 retries with backoff caused the delay),
   is bounded to 60s, and reports rclone's last error line. Not yet re-run live.
3. ~~**`backup list --json`** ignored `--json`.~~ **Fixed:** `backup list` and `backup schedule list` honor `--json`,
   and every listing also accepts `--json` after the command.
4. ~~**REST restic inspect/clone** rejected `"snapshot":"latest"`.~~ **Fixed:** the API accepts `latest` and the
   operation resolves it to the source app's newest snapshot in the repository, shared with restore-from-backup.
5. **Legacy PHP with docroot = repo root** serves non-dot files such as `composer.json`, `config.inc`, `vendor/*`.
   Dotfiles are denied. Expected nginx behaviour, but worth a warning in the UI or docs.
6. **No ready path on a PHP app** means readiness probes request `/` and run app code a few times before the first
   visitor.
7. **Failed deploy** leaves the new code on disk; a later manual restart serves it without the deploy step that
   failed (documented, but easy to trip over).
8. **`retired`** keeps pruned entries (with `prunedAt`) next to live ones in the same list.
