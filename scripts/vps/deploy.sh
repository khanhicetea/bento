#!/bin/bash
# Write deploy scripts, then deploy, start and publish every e2e app.
set -uo pipefail
export BENTO_STACK_ROOT=/srv/bento/prod
ds() { bento app exec $1 -- bash -c "printf '%s\n' '#!/bin/bash' 'set -e' $2 > ~/deploy.sh && chmod 755 ~/deploy.sh" >/dev/null; }
ds e2e-node "'npm install --omit=dev'"
ds e2e-php "'date -u +%FT%TZ > deployed-at.txt'"
ds e2e-py "'[ -x .venv/bin/python ] || python3 -m venv .venv' '.venv/bin/pip install -q -r requirements.txt'"
for a in e2e-node e2e-php e2e-legacy e2e-bun e2e-py; do
  echo "== $a"
  bento app deploy $a 2>&1 | tail -2
  bento app start $a 2>&1 | tail -1
  bento app publish $a 2>&1 | tail -1
done
bento apps
