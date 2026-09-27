---
title: Networks and trust
description: Which containers can talk to which, and which headers apps can trust.
sidebar:
  order: 3
---

Each stack has two bridge networks:

| Network | Members | Notes |
| --- | --- | --- |
| `bento-<stack>-apps` | apps, tooling containers, edge, cloudflared | Egress allowed. Apps reach each other here. |
| `bento-<stack>-data` | apps, MySQL, PostgreSQL, Redis | Docker `internal`: no egress. |

No app, database, or cache publishes a host port. Only the edge publishes the HTTP/HTTPS ports you choose.

Apps on the apps network can reach each other's listeners. This is **not** per-app network isolation; database grants
and Redis ACLs are the data boundary.

## Forwarding headers

The edge and the tunnel have fixed addresses on the apps network. PHP apps trust `X-Forwarded-For` and
`X-Forwarded-Proto` only from those addresses; a forged header from another app is ignored. HTTP apps receive the
trusted addresses in `BENTO_TRUSTED_PROXIES` and should configure their framework accordingly. The edge overwrites,
rather than appends to, forwarding headers.
