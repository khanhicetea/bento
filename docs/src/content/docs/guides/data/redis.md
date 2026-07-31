---
title: Use Redis
description: Understand Bento's private shared-prefix and ACL Redis identities and consume app connection metadata.
---

# Use Redis

Bento provides one durable private Redis service and writes app-specific connection metadata during provisioning. There are no standalone Redis management CLI subcommands.

## Find connection metadata

Bento writes protected metadata to host `homes/demo/credentials/app.env`, mapped under the app home as `/home/demo/credentials/app.env`. Configure the application to read that private file or copy only the needed values into its own protected configuration; do not invent host addresses or print the file in shared logs.

Relevant keys include `REDIS_HOST=redis`, `REDIS_PORT=6379`, `REDIS_PREFIX`, `REDIS_MODE`, and mode-specific credentials. Do not print these in shared logs or support requests.

## Modes

| Mode | Boundary | Application requirement |
| --- | --- | --- |
| `shared` | One stack password plus unique app prefix | Prefix every key/channel with `REDIS_PREFIX` |
| `acl` | Per-app username/password constrained to its key/channel namespace | Authenticate with the generated ACL identity and still use the prefix |

Shared mode is the compatibility default. A prefix is a naming rule, not a cryptographic barrier: application code must consistently apply it. ACL mode gives a stronger Redis-level boundary.

## Verify

Run a framework cache/queue health check through the app identity, then inspect `status` and Redis service logs:

```sh
bento status
bento compose -- logs --tail 100 redis
```

## Troubleshooting

Use hostname `redis`, never `localhost`; Redis is reachable only on the stack-private network. Authentication failures often mean an app cached old credentials or omitted the username in ACL mode. Connection success with cross-app key collisions usually means the application did not apply `REDIS_PREFIX`.

## Advanced

Redis uses AOF persistence in a named volume. Its volume is included in full stack export/import but not in relational logical backups. App prune removes the known retained app identity/data scope where supported; it never removes the shared Redis volume.

## Next steps

- [Understand app data ownership](/concepts/app-model/)
- [Safety and durability](/concepts/safety-and-durability/)
- [Export and import a stack](/guides/stacks/export-import/)
