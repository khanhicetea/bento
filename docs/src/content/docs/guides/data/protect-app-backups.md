---
title: Protect app backups from deletion
description: Let restic delete what it needs while keeping a compromised host from destroying the backups, using bucket versioning, lifecycle rules, and Object Lock.
sidebar:
  order: 5
---

[App backups](/guides/data/app-backups/) need a remote credential that can **delete**: restic removes its lock file
after every command, and `forget --prune` deletes expired data. The same credential lives on the Bento host
(`rclone/rclone.conf`), so anyone who takes over the host (ransomware, a leaked config) could use it to delete every
backup too.

The fix is to let the credential delete while making deletes **reversible** on the storage side. The bucket keeps
every overwritten or deleted object as an old version for a fixed number of days, and the Bento credential is not
allowed to touch those old versions or change the bucket's rules.

| Threat | Without protection | With this setup |
| --- | --- | --- |
| restic deletes a lock / prunes old packs | object gone | object becomes a noncurrent version, removed after the window |
| Attacker on the host runs `rclone purge` / `restic forget --prune` | backups gone | only delete markers; every version restorable for the window |
| Attacker tries to delete old versions or disable versioning | possible | denied by IAM (and by Object Lock, if enabled) |
| Attacker silently stops backups | not noticed | the Backups page shows failed/overdue runs; keep watching it |

## S3 (AWS and compatible)

### 1. Enable versioning

```sh
aws s3api put-bucket-versioning --bucket backup-example \
  --versioning-configuration Status=Enabled
```

With versioning on, `DeleteObject` without a version id only adds a **delete marker**. The data stays as a
noncurrent version.

### 2. Expire old versions with a lifecycle rule

Pick a window longer than the time it would take you to notice an incident (30 days is a common choice). Old
versions older than that are removed by the bucket itself, not by a credential:

```json
{
  "Rules": [
    {
      "ID": "bento-restic-versions",
      "Filter": { "Prefix": "bento/" },
      "Status": "Enabled",
      "NoncurrentVersionExpiration": { "NoncurrentDays": 30 },
      "Expiration": { "ExpiredObjectDeleteMarker": true },
      "AbortIncompleteMultipartUpload": { "DaysAfterInitiation": 7 }
    }
  ]
}
```

```sh
aws s3api put-bucket-lifecycle-configuration --bucket backup-example \
  --lifecycle-configuration file://lifecycle.json
```

Storage cost: pruned data is billed for the extra window. With restic's deduplication this is usually small.

### 3. Give Bento a key that can delete objects but not versions

Attach this policy to the IAM user whose keys are in the Bento rclone remote. Narrow the prefix to the paths your apps
use:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ListRepositoryPaths",
      "Effect": "Allow",
      "Action": ["s3:ListBucket", "s3:GetBucketLocation"],
      "Resource": "arn:aws:s3:::backup-example",
      "Condition": { "StringLike": { "s3:prefix": ["bento/*", "bento"] } }
    },
    {
      "Sid": "ReadWriteDeleteCurrentObjects",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::backup-example/bento/*"
    },
    {
      "Sid": "NeverTouchHistoryOrBucketRules",
      "Effect": "Deny",
      "Action": [
        "s3:DeleteObjectVersion",
        "s3:PutBucketVersioning",
        "s3:PutLifecycleConfiguration",
        "s3:PutBucketPolicy",
        "s3:DeleteBucketPolicy",
        "s3:PutBucketObjectLockConfiguration",
        "s3:PutObjectRetention",
        "s3:BypassGovernanceRetention",
        "s3:DeleteBucket"
      ],
      "Resource": ["arn:aws:s3:::backup-example", "arn:aws:s3:::backup-example/*"]
    }
  ]
}
```

`s3:ListAllMyBuckets` is not needed. `rclone lsd remote:` fails with this policy, but `rclone lsf remote:bucket/path`
works, and that's what Bento uses.

### 4. Optional: Object Lock

Versioning plus the deny statement protects you as long as the IAM policy itself is safe. **Object Lock** adds a
retention period that the storage service enforces on every version:

- **Governance mode**: users with `s3:BypassGovernanceRetention` (an admin, never the Bento key) can still remove
  versions. Good default.
- **Compliance mode**: nobody, including the account root, can delete a version before its retention ends. Use it
  only if you accept that cost and that commitment.

Enable Object Lock when creating the bucket, or later on a versioned bucket (`put-object-lock-configuration`), with a
default retention:

```sh
aws s3api create-bucket --bucket backup-example --object-lock-enabled-for-bucket
aws s3api put-object-lock-configuration --bucket backup-example \
  --object-lock-configuration '{"ObjectLockEnabled":"Enabled","Rule":{"DefaultRetention":{"Mode":"GOVERNANCE","Days":30}}}'
```

restic works with a default retention: a delete still only adds a delete marker, and the locked version expires
through the lifecycle rule once its retention ends. Keep the lifecycle `NoncurrentDays` ≥ the lock days.

### 5. Check it

From the rclone shell (**Backups → Schedules → Open rclone shell**):

```sh
rclone touch  remote:backup-example/bento/.check     # write: ok
rclone deletefile remote:backup-example/bento/.check # delete: ok (adds a delete marker)
rclone lsf --s3-versions remote:backup-example/bento/ | grep check   # the old version is still listed
```

Then try to remove the version (it must fail with `AccessDenied`):

```sh
rclone deletefile --s3-versions "remote:backup-example/bento/.check-v2026-10-01-000000-000"
```

In Bento, **Create repository** passes its write/delete probe, backups run their retention, and **Remove stale locks**
works.

## Other providers

| Provider | Versioning / history | Notes |
| --- | --- | --- |
| Backblaze B2 | Keeps all versions by default; set the bucket lifecycle to "Keep prior versions for N days" | Through the S3 API a delete hides the file (like a delete marker). Object Lock is available. Restrict the application key to the bucket and prefix. |
| Wasabi | Versioning + Object Lock (S3 API) | Same S3 policy; Wasabi bills a minimum storage duration. |
| Cloudflare R2 | No object versioning | Use bucket lock rules (retention) on the prefix, or choose another provider for protected backups. |
| MinIO / Garage / self-hosted S3 | Versioning and Object Lock in MinIO | Same S3 policy. |
| SFTP / local disk | None | Use filesystem snapshots (ZFS, btrfs) on the storage host, which the Bento host can't reach. |

## Recovering after deletion

If someone deleted objects from the repository, restore the previous versions of everything under the repository
path to just before the incident. For example, with rclone, copy the bucket as it was at a point in time into a new
location:

```sh
rclone copy --s3-version-at "2026-10-01 12:00:00" remote:backup-example/bento/apps/shop remote:backup-example/recovered/shop
```

Then point the app's repository at `remote:backup-example/recovered/shop`, **Connect** with the app's key, and
restore a snapshot as usual. Run **Verify** first.

## What this does not cover

- An attacker can still stop new backups, or upload junk. Watch **Backups → App backups** for failed or overdue runs.
- Whoever has the repository key can read the backups. Store handover keys like passwords.
- Protection lasts only for the window you chose. Pick it longer than your detection time.
