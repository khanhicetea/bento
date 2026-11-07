# Real VPS test guide

Testing on a fresh, public amd64 host. This covers what the dev host and [`e2e-live.md`](e2e-live.md) cannot: a
clean-distro install as `install.mdx` describes, the edge on public ports 80/443 (IPv4 and IPv6, HTTP/3), real ACME
issuance, reboots and Docker restarts, and the real-Docker suite on a machine with nothing else on it. Results go in
[`evidence.md`](evidence.md).

The dev host only edits code and builds; every test, including `make ci`, runs on the VPS.

## Host

| Item | Value |
| --- | --- |
| Provider | Linode |
| Address | `104.64.207.10`, `2600:3c15::2000:86ff:fe39:93f1` (`ssh root@104.64.207.10`) |
| OS | Debian GNU/Linux 13 (trixie), x86_64 |
| Size | 2 vCPU, 3.8 GiB RAM, 79 GB disk |
| Docker | 29.9.3 (API 1.56), overlayfs, cgroup v2 (systemd), Compose v5.7.0; installed 2026-11-06 via get.docker.com |
| Tooling | Go 1.27.0 (`/usr/local/go`), Bun 1.4.0, gcc (race tests), git, jq, rsync |

The arm64 host is a second, equally disposable box:

| Item | Value |
| --- | --- |
| Provider | Hetzner (`ssh bento-arm`, same key and `~/.ssh/config` entry shape as `bento-test`) |
| Address | `2.31.150.195`, `2a01:4f8:1c1c:b3ca::1` |
| OS | Debian GNU/Linux 13 (trixie), aarch64 |
| Size | 2 vCPU, 3.7 GiB RAM, 38 GB disk |
| Docker | 29.9.3 (API 1.56), overlayfs, cgroup v2 (systemd); installed 2026-11-07 via get.docker.com |
| Tooling | Go 1.27.0 (`go1.27.0.linux-arm64`), gcc, git, jq, rsync; no Bun (the web checks run on `bento-test`) |

The scripts target it with `BENTO_VPS=bento-arm` (and `BENTO_ARCH=arm64` for `ship.sh`); build the test binaries
with `GOARCH=arm64`. Everything else in this guide applies as written.

## Access

From the dev host, the key `~/.ssh/bento_test_ed25519` (comment `claude-bento-test`) is authorized for `root`. The
private key never leaves the dev host and is never committed. `~/.ssh/config` contains:

```
Host bento-test
  HostName 104.64.207.10
  User root
  IdentityFile ~/.ssh/bento_test_ed25519
  IdentitiesOnly yes
  StrictHostKeyChecking accept-new
  ServerAliveInterval 30
```

Run everything non-interactively: `ssh -o BatchMode=yes bento-test '...'`. The stack is `/srv/bento/prod`
(`bento@prod`); root's `~/.bashrc` exports `BENTO_STACK_ROOT`, but a non-interactive `ssh` command does not read it.

## Rules

- **Disposable.** The VPS is a throwaway test box. An agent may install packages, change the firewall, create and wipe
  stacks, reboot, or reinstall it without asking. If the box breaks, rebuild it from Linode and re-add the key.
- **The management listener stays on loopback.** Reach the UI and API only through an SSH tunnel. Never bind the control
  plane to a public address, not even on this box.
- **Test services stay private.** Gitea and the S3 endpoint listen on loopback and the docker0 address `172.17.0.1`
  only, which app tool containers reach and the internet does not.
- **No secrets in this file or in the repo.** UI, Gitea, S3 and stack backup passwords live on the VPS under
  `/root/.config/bento-test/` (mode 0600); the scripts read them from there.
- **Report honestly.** A check counts as run only if it ran on one of these hosts; arm64 counts only on `bento-arm`, never
  under emulation.

## Scripts

`scripts/vps/` holds the helpers. `sync.sh` and `ship.sh` run on the dev host; the rest are copied to `/root` on the
VPS (`scp scripts/vps/*.sh bento-test:/root/`).

| Script | Where | What |
| --- | --- | --- |
| `sync.sh` | dev host | rsync the repo (with `.git`, without `node_modules`/`dist`) to `/root/src` |
| `ship.sh [web]` | dev host | build the amd64 binary (`web`: rebuild and embed the UI first) and install it as `/usr/local/bin/bento` |
| `install.sh` | VPS | `bento@.service`, `init --name prod --mysql 8.4 --postgres 18`, password, status |
| `reset.sh` | VPS | stop `bento@prod`, remove the stack's containers, networks and volumes, delete `/srv/bento/prod` |
| `gitea.sh` | VPS | private Gitea (`172.17.0.1:3000`, SSH `:2222`) with the five fixture repos from `scripts/e2e/apps` |
| `s3.sh` | VPS | `rclone serve s3` on `172.17.0.1:9000` and the `e2es3` rclone remote |
| `apps.sh` | VPS | the five e2e apps on `<slug>.104.64.207.10.nip.io` (ACME, HTTPS redirect), deploy keys added to Gitea |
| `deploy.sh` | VPS | deploy scripts, then deploy, start and publish each app |
| `push.sh REPO BRANCH SED FILE` | VPS | edit one file in a Gitea repo and push (webhook tests) |
| `lara.sh` | VPS | the Laravel fixture of `e2e-live.md` section 8, pushed to Gitea |
| `final.sh` | VPS | the smoke run: reset, install, edge, apps, certificates, HTTPS on IPv4/IPv6, a Gitea push-to-deploy |

## 1. Bootstrap the host

```bash
ssh bento-test 'apt-get update && apt-get install -y curl ca-certificates rsync jq git make gcc libc6-dev &&
  curl -fsSL https://get.docker.com | sh &&
  curl -fsSL https://go.dev/dl/go1.27.0.linux-amd64.tar.gz | tar -C /usr/local -xz &&
  ln -sf /usr/local/go/bin/go /usr/local/go/bin/gofmt /usr/local/bin/ && git config --global --add safe.directory /root/src'
```

## 2. Tests

```bash
scripts/vps/sync.sh && ssh bento-test 'cd /root/src && bun install --frozen-lockfile &&
  bun run fmt:check && bun run lint && bun run check && bun run web:build && make -C apps/backend ci'
```

Real-Docker suite: build the test binaries on the dev host, run them on the VPS from the package directories (the
runtime tests read `templates/` by relative path).

```bash
cd apps/backend
for p in integration runtime; do CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o ../../dist/$p.test ./internal/$p/; done
ssh bento-test 'mkdir -p /root/bento/apps/backend/internal/{integration,runtime}'
rsync -az templates bento-test:/root/bento/apps/backend/
rsync -z ../../dist/integration.test bento-test:/root/bento/apps/backend/internal/integration/
rsync -z ../../dist/runtime.test bento-test:/root/bento/apps/backend/internal/runtime/
ssh bento-test 'cd /root/bento/apps/backend/internal/integration && BENTO_DOCKER_TESTS=1 ./integration.test -test.count=1 -test.v &&
  cd ../runtime && BENTO_DOCKER_TESTS=1 ./runtime.test -test.count=1 -test.v'
```

A cold runtime-image build takes about 25 minutes on this host; with warm caches the whole suite takes about 15.

## 3. Stack, edge and apps

```bash
scripts/vps/ship.sh web && scp scripts/vps/*.sh bento-test:/root/
ssh bento-test 'bash /root/gitea.sh && bash /root/final.sh'
```

UI from the dev host (then open `http://127.0.0.1:7780`; the password is in `/root/.config/bento-test/ui-password`):

```bash
ssh -N -L 7780:127.0.0.1:7780 bento-test
```

Test hostnames come from [nip.io](https://nip.io/): any `<name>.104.64.207.10.nip.io` resolves to the VPS. The edge
uses the Let's Encrypt **staging** directory. nip.io is shared by everyone, so a production issuance can fail on Let's
Encrypt rate limits that have nothing to do with Bento; record that as an external failure. Staging certificates are
untrusted: check the issuer (`openssl s_client -servername …`), not the chain. HTTP/3 needs a curl built with it:
`docker run --rm --network host ymuski/curl-http3 curl -sk --http3-only https://<host>/`.

The rest of [`e2e-live.md`](e2e-live.md) (deploy cases, ingress modes, backups with `s3.sh`, removal, Laravel, Cachet)
runs the same way, with Gitea repos (`ssh://git@172.17.0.1:2222/e2e/<repo>.git`) in place of GitHub and nip.io hosts
in place of the tunnel.

### Tunnel and GitHub

The e2e Cloudflare tunnel and the GitHub test account of [`e2e-live.md`](e2e-live.md) work here too. Their credentials
stay on the dev host (`~/.config/bento-e2e/`, `gh`); only the tunnel token goes to the VPS, on stdin. Make sure no
other stack runs the `bento-e2e` connector, then:

```bash
set -a; . ~/.config/bento-e2e/env; set +a; TID=$(cat ~/.config/bento-e2e/tunnel-id)
curl -fsS -H "Authorization: Bearer $CF_API_TOKEN" \
  https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/cfd_tunnel/$TID/token | jq -r .result |
  ssh bento-test 'BENTO_STACK_ROOT=/srv/bento/prod bento tunnel set-token'
E2E_STACK=prod scripts/e2e/cf-host.sh add e2e-node      # routes to bento-prod-edge:80
```

Then add `e2e-node.aitea.app` as a host (`tls: none`, or `acme` with `redirectHttps`), and for GitHub follow
`e2e-live.md` section 4 with `gh` on the dev host. `tunnel disable` on the VPS releases the tunnel for the dev host.

## Reset

```bash
ssh bento-test 'bash /root/reset.sh'
```

Gitea and the S3 endpoint are separate containers and survive a stack reset. For a full reset, rebuild the Linode and
re-add the public key.

## Log

- **2026-11-06:** Key access verified. Debian 13 x86_64; Docker Engine 29.9.3 installed (step 1).
- **2026-11-06/07:** First real-server run, recorded in `evidence.md` ("Real VPS run"). Seven fixes, one commit each
  (IPv6 edge, edge restartability, orphaned deploy scripts, deploy summary order, webhook delivery outcome, ACME
  challenge warnings, the placeholder certificate during first issuance), then the schema merged into one baseline and
  a final run on a fresh stack. Bun 1.4.0 is installed too, so the web checks run here as well.
- **2026-11-07:** Every open backlog finding fixed or decided, verified here (`evidence.md`, "Backlog closure"),
  including a production Let's Encrypt certificate and Invoice Ninja. Gitea also hosts `e2e-laravel`, `e2e-cachet`
  and `e2e-invoiceninja`.
- **2026-11-07:** The Cloudflare Tunnel and GitHub cases ran here with the e2e tunnel and the test account (one fix:
  repository-switch guidance). The tunnel token is set on `bento@prod`; `e2e-php` deploys from GitHub. Then Cachet
  from the GitHub fork through the tunnel (all of `e2e-live.md` section 9) and one more fix (app Valkey writable after
  a failed snapshot). The Cachet admin password and API token are in `/root/.config/bento-test/`.
- **2026-11-07:** arm64 host `bento-arm` added: unit (race), integration and runtime suites, then the `final.sh` smoke
  run and a reboot, all on real arm64 hardware (`evidence.md`, "arm64 on real hardware").
- **2026-11-07:** Both hosts joined a tailnet (ephemeral key from the user): `bento-test` became a public nginx front
  (its Bento edge off) for the stack on `bento-arm`, in PROXY-protocol and CDN-style modes. Four fixes (`evidence.md`,
  "Front proxy over Tailscale"). `bento-arm` keeps `tailscale set --snat-subnet-routes=false`.
