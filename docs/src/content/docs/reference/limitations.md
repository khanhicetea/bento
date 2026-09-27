---
title: Limitations
description: Known limits of the current release.
---

- One host; no replicas, autoscaling, or zero-downtime replacement. Restart and config changes interrupt the app.
- Apps on the same stack network can reach each other's listeners.
- Not a sandbox for hostile tenants. Curated runtime images only.
- The management API is loopback-only; remote access needs your own secure tunnel.
- Verified end to end on linux/amd64 with PHP 8.4 and Node.js 24. arm64 builds, other runtime versions, live
  Cloudflare Tunnel, ACME, HTTP/3, and rclone uploads have not yet been exercised.
- No built-in deployment pipeline: deploy code into the app home yourself, then restart.
- App Nginx and FPM configuration is generated; customization is limited to edge drop-ins. Access logs go to the
  container's bounded log; there are no access-log reports.
