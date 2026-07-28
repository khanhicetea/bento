load_module /usr/lib/nginx/modules/ngx_http_acme_module.so;

worker_processes auto;
worker_shutdown_timeout 10s;
error_log /var/log/nginx/error.log warn;
pid /var/run/nginx.pid;

# Operator-owned main-context drop-ins, loaded lexically.
include /etc/nginx/custom/main.d/*.conf;

events {
  worker_connections {{workerConnections}};
  multi_accept on;

  # Optional event-context tuning.
  include /etc/nginx/custom/events.d/*.conf;
}

http {
  resolver 1.1.1.1 8.8.8.8 valid=300s ipv6=off;

  acme_shared_zone zone=ngx_acme_shared:10M;

  {{acmeIssuers}}

  include /etc/nginx/mime.types;
  default_type application/octet-stream;
  types_hash_max_size 4096;
  server_tokens off;

  # Efficient socket and file delivery defaults for mixed static/PHP traffic.
  sendfile on;
  tcp_nopush on;
  tcp_nodelay on;
  keepalive_timeout 30s;
  keepalive_requests 1000;
  client_body_timeout 30s;
  client_header_timeout 15s;
  send_timeout 30s;
  reset_timedout_connection on;
  client_max_body_size 64m;

  # Keep normal reverse-proxy connections reusable while preserving upgrades.
  map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      '';
  }

  # Shared cache zones (cache data is kept on disk; keys use bounded shared memory).
  fastcgi_cache_path /var/cache/nginx/app_cache levels=1:2 keys_zone=app_cache:10m max_size=1g inactive=1d use_temp_path=off;
  proxy_cache_path /var/cache/nginx/proxy_assets levels=1:2 keys_zone=proxy_assets:20m max_size=2g inactive=7d use_temp_path=off;
  proxy_cache_path /var/cache/nginx/proxy_cache levels=1:2 keys_zone=proxy_cache:10m max_size=1g inactive=7d use_temp_path=off;

  # Collapse cache stampedes and serve stale cache entries during brief failures.
  fastcgi_cache_lock on;
  fastcgi_cache_background_update on;
  fastcgi_cache_use_stale error timeout updating http_500 http_503;
  proxy_cache_lock on;
  proxy_cache_background_update on;
  proxy_cache_use_stale error timeout updating http_500 http_502 http_503 http_504;

  log_format bento_access_log '$remote_addr - $remote_user [$time_local] '
                         '"$request" $status $body_bytes_sent '
                         '"$http_referer" "$http_user_agent" '
                         'rt=$request_time urt=$upstream_response_time';

  gzip on;
  gzip_vary on;
  gzip_min_length 1024;
  gzip_comp_level 5;
  gzip_types text/plain text/css text/javascript text/xml application/json application/javascript application/manifest+json application/wasm application/xml application/xml+rss image/svg+xml font/ttf;

  # zstd can be enabled from http.d when a compatible module is installed.

  # Operator-owned HTTP directives load before managed sites.
  include /etc/nginx/custom/http.d/*.conf;

  include /etc/nginx/sites/*.conf;

  # Operator-owned server blocks load after managed sites.
  include /etc/nginx/custom/sites.d/*.conf;
}
