# First-class SQLite with one Litestream directory watcher

Status: implemented architecture decision

Research and experiment date: 2026-07-29

Scope: provide first-class app SQLite files and stack-wide continuous S3-compatible replication with Litestream v0.5.15.

## Decision

Bento uses one dedicated Litestream container per stack:

1. Every SQLite app stores one file at `sqlite/<app-slug>_<10-random-hex-chars>/<app-slug>.sqlite`.
2. `BENTO_LITESTREAM_ENABLED=true` permits one stack-wide backup policy.
3. Litestream watches `/sqlite` recursively for files matching `*.sqlite`.
4. Every matching valid SQLite database is backed up. Backup is not a per-app opt-in.
5. The container runs as root with all capabilities dropped except `DAC_OVERRIDE`, `CHOWN`, and `FOWNER`.
6. Bento uses ordinary ownership and modes, not a shared backup UID or POSIX ACLs.
7. Litestream receives only the SQLite root, durable metadata, generated config, volatile control socket, S3 environment, and a private egress network. It receives no app homes or code.
8. RPO, snapshot interval, retention, destination, and credentials are stack-wide.
9. Remote replication is asynchronous disaster recovery/PITR, not high availability, synchronous durability, or a complete stack backup.

This deliberately accepts that compromise of the Litestream process exposes every SQLite database and replica in the stack. The mount and network boundary keeps credentials and process state away from PHP applications.

## Why directory watcher

Litestream v0.5.15 supports `dir`, `pattern`, `recursive`, `watch`, and `meta-dir` configuration. The watcher:

- scans existing databases at startup;
- accepts an empty directory;
- discovers new valid SQLite files through fsnotify;
- removes deleted files from active replication without deleting remote history;
- automatically namespaces each replica by the database's relative path.

For Bento:

```text
/sqlite/demo_a1b2c3d4e5/demo.sqlite
```

maps beneath the stack replica root as:

```text
bento/<stack>/demo_a1b2c3d4e5/demo.sqlite/
```

The immutable file ID combines a readable app slug with a random suffix, preventing app slug reuse from joining old history. Exactly one watcher process must write each stack replica root.

Directory mode replaces:

- generated per-database entries;
- runtime `register` and `unregister` calls;
- the fixed runtime-registration sync interval;
- policy-dependent hot registration versus recreation;
- per-database backup UIDs and ACL reconciliation.

## Generated configuration

The generated configuration is equivalent to:

```yaml
logging:
  level: info
  type: json

socket:
  enabled: true
  path: /run/litestream/control.sock
  permissions: 0600

snapshot:
  interval: 1h
  retention: 168h

l0-retention: 24h
validation:
  interval: 6h
verify-compaction: true
shutdown-sync-timeout: 30s

dbs:
  - dir: /sqlite
    pattern: "*.sqlite"
    recursive: true
    watch: true
    meta-dir: /var/lib/litestream
    monitor-interval: 1s
    checkpoint-interval: 1m
    busy-timeout: 5s
    replica:
      sync-interval: 60s
      url: s3://${S3_BUCKET_NAME}/bento/${COMPOSE_PROJECT_NAME}?endpoint=${S3_ENDPOINT}&region=${S3_REGION}
```

The metadata root is durable. It must not be stored only under volatile `runtime/`. Litestream still opens and checkpoints SQLite WAL files, so `/sqlite` must be writable.

## Container boundary

The generated Compose service is equivalent to:

```yaml
services:
  litestream:
    image: litestream/litestream:0.5.15
    user: "0:0"
    command: [replicate, -config, /etc/litestream/litestream.yml]
    restart: unless-stopped
    read_only: true
    cap_drop: [ALL]
    cap_add: [DAC_OVERRIDE, CHOWN, FOWNER]
    security_opt: [no-new-privileges:true]
    stop_grace_period: 45s
    networks: [backup-egress]
    env_file: [./generated/secrets/litestream/stack-s3.env]
    volumes:
      - ./sqlite:/sqlite
      - ./litestream-meta:/var/lib/litestream
      - ./generated/litestream:/etc/litestream:ro
      - ./runtime/litestream:/run/litestream
```

Capability purposes:

- `DAC_OVERRIDE`: traverse and open app-owned mode-`0700` directories and writable SQLite files;
- `CHOWN`: preserve the source database UID/GID on files Litestream creates;
- `FOWNER`: finish metadata/timestamp operations after ownership is transferred.

Rootless Docker and user-namespace remapping are not supported for this mode. Bento must fail clearly if the ownership contract cannot be met.

Running every PHP app under one common UID was rejected. Per-app UIDs continue to provide useful accidental filesystem isolation. Constrained root is limited to the narrow backup container instead.

## State model

Backup policy belongs to the stack:

```ts
type SqliteBackupPolicy = {
  provider: "litestream";
  destination: string;
  syncInterval: "1s" | "10s" | "60s";
  snapshotInterval: string;
  snapshotRetention: string;
  l0Retention: string;
  enabled: boolean;
};
```

An app SQLite binding contains only the immutable local file identity and its latest restore-verification timestamp. S3 credentials are derived from the stack `.env` into `generated/secrets/litestream/stack-s3.env`, not stored in desired state.

A new SQLite database is automatically covered while the policy is enabled. Removing an app retains its SQLite directory, so backup continues. Prune deletes the local directory; the watcher unregisters it automatically while remote objects remain.

## Operations

### Enable

```sh
bento sqlite backup enable demo --rpo 60s --retention 168h
```

The app argument selects the database used for initial proof. Enablement:

1. requires the environment gate and S3 settings;
2. validates managed SQLite paths;
3. writes the protected stack S3 environment;
4. records one stack policy;
5. renders one watcher configuration;
6. gracefully recreates the watcher so policy changes take effect;
7. forces synchronization of the selected app;
8. restores from its direct replica URL into `litestream-meta/`;
9. runs a full integrity check and removes the proof file;
10. records the app verification timestamp.

### Status and synchronization

Watcher-managed databases are runtime entries, so Bento uses daemon IPC:

```sh
litestream list -socket /run/litestream/control.sock -json
litestream info -socket /run/litestream/control.sock -json
litestream sync -socket /run/litestream/control.sock -wait /sqlite/<id>/<app-slug>.sqlite
```

Do not use configuration-based `litestream status` for watcher health. In v0.5.15 it does not report dynamic directory entries correctly, and `meta-dir` can produce a misleading validation error in that command path.

### Restore verification

Directory-managed databases are not individually defined in configuration. Restore therefore uses the replica URL directly:

```sh
litestream restore -o /var/lib/litestream/verify.sqlite \
  -integrity-check full \
  s3://<bucket>/bento/<stack>/<file-id>/<app-slug>.sqlite
```

Bento currently verifies restore-to-new only. A guarded production replacement restore remains separate work.

## Experimental evidence

The reviewed v0.5.15 image was tested on native Linux/rootful Docker with two mode-`0700` directories owned by distinct app UIDs.

Observed:

- an initially empty recursive watcher started successfully;
- two databases created after startup were discovered and replicated;
- dynamic `sync -wait` succeeded through the control socket;
- `list` and `info` reported both runtime databases;
- deleting the dedicated backup UID and all ACLs was viable with constrained root;
- `cap_drop: [ALL]` alone could not traverse app directories;
- `DAC_OVERRIDE` alone replicated but recreated WAL/SHM as root, after which the app received `attempt to write a readonly database`;
- `DAC_OVERRIDE` plus `CHOWN` but without `FOWNER` produced `chtimes ... operation not permitted` on ownership-matched files;
- `DAC_OVERRIDE`, `CHOWN`, and `FOWNER` preserved app ownership and allowed subsequent app writes;
- the official source intentionally attempts to match created file/directory UID, GID, and mode to the source database.

The ownership behavior is important enough to remain an automated native-Linux integration test rather than an untested implementation assumption.

## Required invariants

1. Exactly one Litestream process writes a stack replica root.
2. Every managed SQLite file uses local Linux storage with working SQLite locking semantics.
3. Enabling the watcher means every valid managed `<app-slug>.sqlite` is in scope.
4. App database directories remain private to their app UID; they are not world-readable or world-writable.
5. Litestream receives only the mounts and capabilities listed above.
6. PHP containers cannot read Litestream credentials, configuration, metadata, process environment, or control socket.
7. Replica paths derive from immutable stack and SQLite file identities, not mutable slugs.
8. Backup success means confirmed remote synchronization; recovery confidence means successful full-integrity restore.
9. App removal and prune do not delete remote history.
10. Stack clone/import must not start a second writer against an existing replica root.

## Acceptance tests

- State permits one stack backup policy and no per-app policy duplication.
- One generated directory entry contains `recursive: true`, `watch: true`, and durable `meta-dir`.
- One generated service runs as root with exactly the reviewed capability allowlist.
- Generated service mounts no app homes/code and joins no application-private network.
- Two private app UIDs can write concurrently while one watcher replicates both databases.
- A database created after watcher startup is discovered without process recreation.
- An empty placeholder is ignored until it becomes a valid SQLite database.
- After Litestream starts with missing WAL/SHM files, their ownership permits the app UID to write.
- `list`, `info`, and database-scoped `sync -wait` operate through IPC.
- Restore verification uses the direct replica URL and a full integrity check.
- Removing the last app from desired state does not stop protection of retained SQLite directories.
- Prune removes the runtime database but leaves remote history.
- Rootless/userns-remapped operation fails clearly.
- Routine output and support bundles reveal no S3 secret.

## Non-goals

- per-app backup enablement or policy;
- one Unix identity shared by all PHP apps;
- multi-primary SQLite or automatic failover;
- synchronous remote commits or zero-data-loss guarantees;
- automatic remote deletion during prune;
- a public production replacement-restore command;
- treating Litestream as a backup of app code, homes, certificates, or desired state.

## Sources

Official sources reviewed:

- Directory watcher: https://litestream.io/guides/directory-watcher/
- Configuration and directory metadata: https://litestream.io/reference/config/
- Docker topology: https://litestream.io/guides/docker/
- Restore: https://litestream.io/reference/restore/
- Sync and IPC commands: https://litestream.io/reference/sync/
- Litestream v0.5.15 release: https://github.com/benbjohnson/litestream/releases/tag/v0.5.15
