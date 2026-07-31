---
title: Diagnose a stack
description: Start with status, run deeper checks, inspect logs, and collect a support bundle.
---

# Diagnose a stack

Start with the cheapest checks, then go deeper until you locate the host, configuration, service, network, TLS, storage, or permission failure.

## Check status

```sh
bento status
bento --json status
bento compose -- ps
```

Status distinguishes running roles from configuration that is ready for a stopped service. JSON is suitable for scripts but remains operational output, not a stable remote API.

## Run diagnostics

```sh
bento doctor
bento --json doctor
```

Doctor checks Docker/Compose, host utilities, ingress and port risks, storage and modes, overlays, TLS, service health, volumes, and app permissions. Fix `fail` results first; review warnings against your topology.

Inspect service output without bypassing Bento's Compose file set:

```sh
bento compose -- logs --tail 200 nginx
bento compose -- logs --tail 200 php85
```

## Create a support bundle

```sh
bento support-bundle /tmp/bento-support.tar.gz
```

Known secrets are redacted. The bundle is still sensitive metadata: inspect its member list and extracted contents before sharing, then transfer it through an approved channel.

## Troubleshooting

- Docker errors: verify the daemon and your user's socket access.
- Validation errors: keep generated output untouched, fix desired state/custom input, and rerun `apply`.
- Down service: inspect `compose -- ps` and its logs, then start only the needed service.
- Routing/TLS: verify effective ingress, DNS, ports, and certificate mode.

See the [symptom-first troubleshooting index](/reference/troubleshooting/) for targeted checks.

## Advanced

`compose files` shows deterministic base, runtime/database fragments, and overlays. `compose --print -- config` prints the underlying argv before execution and helps confirm which stack is targeted.

## Next steps

- [Stack configuration reference](/reference/configuration/)
- [Stack layout reference](/reference/stack-layout/)
- [Troubleshooting](/reference/troubleshooting/)
