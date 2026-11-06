#!/bin/bash
# push.sh REPO BRANCH SED_EXPR FILE : clone from the e2e Gitea, edit one file, push.
set -e
T=$(cat /root/.config/bento-test/gitea-token); d=$(mktemp -d)
git clone -q -b main "http://e2e:$T@127.0.0.1:3000/e2e/$1.git" $d/r 2>/dev/null
cd $d/r; git checkout -q -B "$2"; sed -i "$3" "$4"
git -c user.name=e2e -c user.email=e2e@bento.invalid commit -qam "e2e: $3"
git push -q -f origin "$2" 2>/dev/null; git rev-parse --short HEAD; rm -rf $d
