#!/bin/bash
# wp-up.sh [SLUG] [MEMORY_MB] [CPU_MILLIS] : WordPress benchmark fixture on the running stack.
# PHP 8.4, MySQL 8.4 (service mysql84), front-controller routing, HTTP-only host wp.<ip>.nip.io through the
# edge, 1,000 posts of ~1,200 words, Yoast SEO and Contact Form 7 active, /%postname%/ permalinks.
# Writes $BENCH_DIR/<slug>-paths.txt (one URL path per line) for wordpress.lua. Refuses a slug that exists or
# was used before (Bento never reuses one); pick another slug for a fresh fixture.
set -euo pipefail
. "$(dirname "$0")/env.sh"
SLUG=${1:-wpbench}
MEM=${2:-1024}
CPU=${3:-1000}
HOST=$SLUG.$IP4.nip.io

if bento --json app show "$SLUG" >/dev/null 2>&1; then
  echo "$SLUG already exists; pick another slug" >&2
  exit 1
fi

cat >"$BENCH_DIR/$SLUG.json" <<J
{"slug":"$SLUG",
 "runtime":{"kind":"php-fpm","php":{"version":"8.4","documentRoot":"","routing":"front-controller","mode":"standard","uploadLimitMb":64}},
 "resources":{"memoryMb":$MEM,"cpuMillis":$CPU,"pids":512},
 "ingress":"managed",
 "hosts":[{"name":"$HOST","route":{"tls":"none","redirectHttps":false,"accessLog":false}}],
 "bindings":[{"engine":"mysql","service":"mysql84"}]}
J
bento app create --json "$BENCH_DIR/$SLUG.json" | tail -1

# Everything below runs as the app user in a tool container, in the app code directory.
bento app exec "$SLUG" -- bash -euo pipefail -c '
  curl -fsSL -o ~/wp-cli.phar https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar
  wp() { php -d memory_limit=512M ~/wp-cli.phar "$@"; }
  wp core download --quiet --skip-content --version=latest
  mkdir -p wp-content/plugins wp-content/themes wp-content/uploads
  wp config create --quiet --dbname="$DB_DATABASE" --dbuser="$DB_USERNAME" --dbpass="$DB_PASSWORD" \
    --dbhost="$DB_HOST:$DB_PORT" --skip-check
  wp core install --quiet --url="http://'"$HOST"'" --title="Bento bench" --admin_user=bench \
    --admin_password="$(head -c 18 /dev/urandom | base64)" --admin_email=bench@bento.invalid --skip-email
  # wp core install guesses the site address from the code directory (".../app"); set it.
  wp option update home "http://'"$HOST"'" --quiet
  wp option update siteurl "http://'"$HOST"'" --quiet
  wp theme install twentytwentyfive --activate --quiet
  wp plugin install wordpress-seo contact-form-7 --activate --quiet
  wp rewrite structure "/%postname%/" --quiet
  for i in $(seq 1 10); do wp term create category "Topic $i" --quiet >/dev/null; done
  para="Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum."
  body=""; for i in $(seq 1 16); do body="$body<!-- wp:paragraph --><p>$para $para</p><!-- /wp:paragraph -->"; done
  printf "%s" "$body" | wp post generate --count=1000 --post_content --quiet
  wp eval "\$c = get_terms([\"taxonomy\" => \"category\", \"hide_empty\" => false, \"fields\" => \"ids\", \"exclude\" => [1]]);
    \$i = 0; foreach (get_posts([\"numberposts\" => -1, \"fields\" => \"ids\"]) as \$id) {
      wp_set_post_categories(\$id, [\$c[\$i++ % count(\$c)]]); }"
  form=$(wp post list --post_type=wpcf7_contact_form --field=ID | head -n1)
  wp post create --post_type=page --post_title=Contact --post_name=contact --post_status=publish \
    --post_content="[contact-form-7 id=\"$form\"]" --quiet
  wp option update blog_public 1 --quiet
  { echo /; echo /contact/; echo "/?s=lorem";
    wp term list category --field=url --exclude=1 | sed "s#^http://[^/]*##";
    wp post list --post_type=post --field=url --posts_per_page=-1 | sed "s#^http://[^/]*##"; } > ~/bench-paths.txt
  wc -l < ~/bench-paths.txt
'
bento app exec "$SLUG" -- bash -c 'cat ~/bench-paths.txt' >"$BENCH_DIR/$SLUG-paths.txt"
bento app start "$SLUG" | tail -1
bento app publish "$SLUG" | tail -1
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: $HOST" "http://127.0.0.1:$EDGE_HTTP/")
echo "$SLUG ready: http://$HOST (edge port $EDGE_HTTP) -> $code; $(wc -l <"$BENCH_DIR/$SLUG-paths.txt") paths"
