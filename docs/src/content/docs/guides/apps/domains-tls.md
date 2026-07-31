---
title: Manage domains and TLS
description: Configure app or proxy domains and choose shared, private-CA, ACME, or external TLS safely.
---

# Manage domains and TLS

Choose and activate the correct TLS mode for a Bento app or reverse proxy, verify its certificate path, and understand who owns renewal and client trust.

## Before you begin

- Use an existing app or proxy and confirm the stack root. Examples target `/var/lib/bento` and app `demo`.
- Ensure Nginx is running before testing live HTTP or HTTPS.
- Ensure OpenSSL and `curl` are installed for certificate and HTTP checks. For ACME, also install a DNS lookup tool such as `dig`.
- For public certificates, control every primary domain and alias and be able to change its DNS.
- Back up the stack's `certs/` directory as sensitive durable data.

Inspect the current domain and TLS mode:

```sh
bento --stack /var/lib/bento app show demo
bento --stack /var/lib/bento status
```

## Choose a TLS mode

Bento stores one TLS mode per app or proxy:

| Mode | Certificate owner | HTTP redirects to HTTPS | Use when |
| --- | --- | ---: | --- |
| `shared` | Bento's shared starter certificate | No | Bootstrapping or local HTTP checks |
| `self-ca` | Bento's stack-private CA | Yes | Internal clients where you can install the CA |
| `acme` | Nginx's shared native ACME issuer | Yes | Public DNS names reachable on port 80 |
| `external` | You or an external certificate system | Yes | Another system supplies files under the stack `certs/` directory |

`shared` is the initial mode. Its one self-signed certificate is reused by every shared-mode site and does not validate the requested domain. It keeps HTTP available without forcing an HTTPS redirect; do not treat it as production identity.

The other three modes turn on HTTP-to-HTTPS redirects. Configure and verify the selected certificate path before depending on that redirect.

## Update app domains

Domains are authoritative link records, globally unique across apps and proxies. Each target has one primary link and any number of additional links. Change an app's links with `app update`; pass the complete alias list because it replaces the previous additional links:

:::caution
Changing a primary domain or alias changes routing immediately after apply. Confirm DNS and the intended certificate coverage first. `app update` also requires you to repeat `--access-log` if enabled access logging must remain enabled.
:::

```sh
bento --stack /var/lib/bento app update demo \
  --domain demo.example.com \
  --alias www.demo.example.com
```

Bento rejects a domain already owned by another app or proxy. For `self-ca`, the next apply replaces the site's leaf certificate when its SAN list changes. Native ACME obtains certificates for the current primary domain and aliases. With `external`, you must replace the supplied certificate with one covering the new names before changing routing.

Review the update behavior in [Manage applications](/guides/apps/manage/#update-an-app). Reverse-proxy domains are selected when the proxy is created; they participate in the same TLS commands below.

## Use the shared starter certificate

Switch an app back to the startup mode:

```sh
bento --stack /var/lib/bento tls set --app demo --mode shared
```

For a proxy, replace `--app demo` with `--proxy <name>`. Pass exactly one target.

Verify HTTP without certificate trust:

```sh
curl -I -H 'Host: demo.example.com' http://127.0.0.1/
```

If you inspect HTTPS during bootstrap, expect certificate verification to fail unless you explicitly bypass trust. Bypassing trust is a diagnostic action, not a production client configuration.

## Use the Bento private CA

`self-ca` creates one private CA for the stack and a separate certificate for the app's primary domain and aliases.

:::caution
Clients do not trust the Bento private CA automatically. Export and distribute only its public certificate. Protect `/var/lib/bento/certs/private-ca/ca.key`; losing it prevents Bento from signing replacement leaf certificates, while replacing it requires redistributing trust to every client.
:::

Enable private-CA TLS:

```sh
bento --stack /var/lib/bento tls set --app demo --mode self-ca
```

Bento creates or verifies the CA, issues the site certificate, applies configuration, and reloads Nginx. It renews a leaf certificate on a later render or apply when its domain list changes or it has less than 30 days remaining.

Export only the public CA certificate to a path outside the managed CA directory:

```sh
bento --stack /var/lib/bento tls ca export \
  --output ./bento-ca.crt
```

The command refuses to overwrite an existing destination unless you add `--force`. Inspect the exported certificate:

```sh
openssl x509 -in ./bento-ca.crt -noout -subject -issuer -dates
```

Verify the site locally while trusting that exported CA:

```sh
curl --cacert ./bento-ca.crt \
  --resolve demo.example.com:443:127.0.0.1 \
  -I https://demo.example.com/
```

Install `bento-ca.crt` in client trust stores only through the operating system or application's documented CA process. Some long-running clients require a restart or CA-bundle reload afterward. Never distribute `ca.key` or a per-site private key.

## Use public ACME certificates

Bento uses Nginx's native ACME module and a single issuer named `bento_acme`. Nginx performs HTTP-01 issuance and renewal; there is no `bento tls renew` or Certbot step.

:::caution
Before enabling ACME, every A and AAAA record for the primary domain and all aliases must point to this host, and public TCP port 80 must reach this stack's Nginx. A stale AAAA record can cause validation failure even when the A record is correct. Repeated failed issuance can encounter certificate-authority rate limits.
:::

Edit the private stack environment at `/var/lib/bento/.env` and set:

```text
ACME_EMAIL=ops@example.com
ACME_URL=https://acme-v02.api.letsencrypt.org/directory
```

`ACME_URL` already defaults to the Let's Encrypt production directory when blank. Use a staging directory URL while testing repeated issuance, then deliberately switch to the production URL. Preserve the other `.env` values and its restricted permissions.

Check public DNS from outside the host's private network:

```sh
dig +short A demo.example.com
dig +short AAAA demo.example.com
```

Check that the public HTTP route reaches Bento Nginx. Run this from an external network when possible:

```sh
curl -I http://demo.example.com/
```

Enable ACME only after those checks pass:

```sh
bento --stack /var/lib/bento tls set --app demo --mode acme
```

Certificate issuance can continue after the command returns. Nginx stores durable issuer state below `/var/lib/bento/certs/acme-state/`; include it in stack backups.

Verify the certificate served publicly:

```sh
openssl s_client \
  -connect demo.example.com:443 \
  -servername demo.example.com </dev/null 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
```

Also test every alias over HTTPS. A successful `status` confirms the selected mode, not that the certificate authority completed issuance.

## Use external certificate files

External mode reads operator-managed certificate and key files from the stack's mounted `certs/` tree. Bento does not obtain or renew them.

Prepare a certificate chain and private key, for example:

```sh
sudo install -d -m 0700 /var/lib/bento/certs/external
sudo install -m 0644 /path/to/fullchain.pem \
  /var/lib/bento/certs/external/demo-fullchain.pem
sudo install -m 0600 /path/to/privkey.pem \
  /var/lib/bento/certs/external/demo-privkey.pem
```

:::caution
Confirm that the certificate is current, matches the private key, and covers the primary domain and every alias before switching. Bento verifies that both paths exist under the stack `certs/` directory and rejects a group- or world-accessible key, but it does not manage external renewal for you.
:::

Select paths relative to `/var/lib/bento/certs/`:

```sh
bento --stack /var/lib/bento tls set \
  --app demo \
  --mode external \
  --cert external/demo-fullchain.pem \
  --key external/demo-privkey.pem
```

Verify the live certificate with `openssl s_client` as shown in the ACME procedure. When the external system renews the files, replace them atomically if possible and run:

```sh
bento --stack /var/lib/bento apply
```

That causes Nginx to load the current files. Monitor certificate expiry outside Bento because no external-file renewal scheduler is provided.

## Verify a TLS change

A direct `tls set` applies an Nginx-only reload; it does not reload PHP-FPM or runner roles. Check the selected state and Nginx process:

```sh
bento --stack /var/lib/bento status
bento --stack /var/lib/bento compose -- ps nginx
```

Then verify both behaviors:

1. HTTP redirects for `self-ca`, `acme`, and `external`, but remains available without forced redirect for `shared`.
2. HTTPS presents the expected certificate and succeeds with the appropriate public or private trust root.

Use `--no-apply` only when batching changes. It records the TLS mode without changing live Nginx; run `bento --stack /var/lib/bento apply` afterward.

## Troubleshooting

**Bento says to provide `--app` or `--proxy`:** select exactly one existing site target. Confirm its slug or name with `app list`, `status`, or the proxy list command.

**ACME remains unavailable:** verify all A and AAAA answers externally, public port 80, firewall/NAT rules, and Nginx logs. Confirm that `ACME_URL` is valid and inspect the issuer state directory without exposing its contents:

```sh
bento --stack /var/lib/bento compose -- logs --tail 200 nginx
```

**The site redirects to HTTPS before a usable certificate exists:** switch temporarily to `shared` to restore non-redirected HTTP while correcting ACME, private-CA trust, or external files.

**Private-CA creation fails:** install OpenSSL, check that `/var/lib/bento/certs/` is writable, and inspect whether only one of `ca.crt` or `ca.key` exists. Restore a matching pair from backup; do not casually generate a replacement for an already trusted CA.

**CA export says the destination exists:** choose another output or add `--force` only when you intend to replace that copy. The destination must remain outside `certs/private-ca/`.

**External mode rejects a path:** place both files under `/var/lib/bento/certs/` and pass either relative paths from that directory or absolute paths inside it. Paths outside the tree are refused.

**External mode rejects key permissions:** restrict the key and retry:

```sh
sudo chmod 0600 /var/lib/bento/certs/external/demo-privkey.pem
```

**Nginx reload fails after a TLS change:** inspect Nginx logs and the certificate files. The newly generated configuration remains live after a reload-signal failure; correct the certificate or mode and run `apply` again.

## Advanced

Certificates, private keys, and ACME state are durable runtime data, not generated configuration. Generated Nginx snippets only reference those files. Stack export archives therefore contain private keys and must be encrypted and handled as sensitive material.

Private-CA leaf certificates contain the exact sorted SAN set from current desired state. Bento uses RSA 4096 for a newly created ten-year CA and RSA 2048 for 825-day leaf certificates, verifies CA/key matching, and keeps leaf keys at mode `0600`. These implementation lifetimes do not remove the need to back up and monitor the CA.

Native ACME configuration is shared at stack scope: `ACME_EMAIL` and `ACME_URL` affect every ACME site. Issuer state survives Nginx container recreation through the `certs/acme-state/` bind mount. HTTP-01 makes public HTTP reachability a continuing operational dependency for renewal.

The `tls set` path validates external file location and key mode before recording state, then uses an Nginx-scoped apply. It does not prove public DNS, client trust, certificate SAN coverage, or end-to-end application behavior; the verification steps above remain operator responsibilities.

## Next steps

- [Manage the app lifecycle and domain updates](/guides/apps/manage/).
- [Review host ports, DNS, and firewall requirements](/start/requirements/).
- [Inspect stack status and Nginx logs](/guides/stacks/manage/).
