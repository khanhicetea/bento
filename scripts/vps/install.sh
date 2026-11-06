#!/bin/bash
# Operator install exactly as apps/docs/docs/get-started/install.mdx describes, on the VPS.
set -euo pipefail
install -m 0644 /root/src/deploy/systemd/bento@.service /etc/systemd/system/
systemctl daemon-reload
mkdir -p /srv/bento
bento --stack /srv/bento/prod init --name prod --mysql 8.4 --postgres 18
systemctl enable --now bento@prod
install -d -m 700 /root/.config/bento-test
[ -s /root/.config/bento-test/ui-password ] || (umask 077; head -c 24 /dev/urandom | base64 | tr -d '/+=' > /root/.config/bento-test/ui-password)
grep -q BENTO_STACK_ROOT /root/.bashrc || echo 'export BENTO_STACK_ROOT=/srv/bento/prod' >> /root/.bashrc
export BENTO_STACK_ROOT=/srv/bento/prod
for i in $(seq 1 30); do bento status >/dev/null 2>&1 && break; sleep 2; done
bento auth set-password < /root/.config/bento-test/ui-password
bento status
bento services
