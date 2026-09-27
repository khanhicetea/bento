---
title: Several stacks on one host
description: Running independent stacks side by side.
sidebar:
  order: 2
---

Each stack needs its own root, a unique name (Docker resources are prefixed `bento-<name>-`), its own backend service
(`systemctl enable --now bento@<name>`) on its own loopback port, non-overlapping UID ranges, and — if both use the
managed edge — different host ports. Networks get separate private subnets automatically.
