#!/bin/bash
# S3 endpoint for backup tests on the VPS: `rclone serve s3` on the docker0 address (never public), bucket "bento",
# and the rclone remote "e2es3" in the stack's rclone.conf. MinIO no longer publishes community images.
set -euo pipefail
C=${1:-/srv/bento/prod}/rclone/rclone.conf
install -d -m 700 /root/.config/bento-test
[ -s /root/.config/bento-test/s3-pass ] || (umask 077; head -c 24 /dev/urandom | base64 | tr -d '/+=' > /root/.config/bento-test/s3-pass)
P=$(cat /root/.config/bento-test/s3-pass)
docker volume create e2e-s3 >/dev/null
docker ps -q --filter name=^e2e-s3$ | grep -q . || docker run -d --name e2e-s3 --restart unless-stopped -v e2e-s3:/data \
  -p 172.17.0.1:9000:9000 rclone/rclone:latest serve s3 --auth-key "e2eadmin,$P" --addr :9000 /data >/dev/null
sleep 2; docker exec e2e-s3 mkdir -p /data/bento
grep -q '^\[e2es3\]' "$C" 2>/dev/null || printf '\n[e2es3]\ntype = s3\nprovider = Other\naccess_key_id = e2eadmin\nsecret_access_key = %s\nendpoint = http://172.17.0.1:9000\nno_check_bucket = true\n' "$P" >> "$C"
chmod 600 "$C"
echo "remote e2es3:bento ready"
