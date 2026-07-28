# proxy {{name}}
upstream {{upstreamName}} {
  {{#upstreamServers}}
  server {{.}};
  {{/upstreamServers}}
  include /etc/nginx/custom/proxies/{{name}}/upstream.d/*.conf;
  keepalive 32;
  keepalive_requests 1000;
  keepalive_timeout 60s;
}

server {
  listen 80;
  listen [::]:80;
  server_name {{serverNames}};

  # Additive operator-owned directives/locations; no full-template fork required.
  include /etc/nginx/custom/proxies/{{name}}/server.d/*.conf;
  include /etc/nginx/custom/proxies/{{name}}/http.d/*.conf;

  {{#redirectHttps}}
  location / {
    return 301 https://$host{{httpsPortSuffix}}$request_uri;
  }
  {{/redirectHttps}}
  {{^redirectHttps}}
  {{#accessLog}}
  access_log {{accessLogPath}} bento_access_log buffer=64k flush=1s;
  {{/accessLog}}
  location ~* \.(?:css|js|mjs|jpg|jpeg|gif|png|svg|ico|webp|avif|woff|woff2|ttf|eot)$ {
    expires 30d;
    proxy_cache proxy_assets;
    proxy_cache_valid 200 301 302 7d;
    include /etc/nginx/snippets/proxy-common.conf;
    proxy_pass {{upstreamScheme}}://{{upstreamName}}{{upstreamUri}};
  }
  location / {
    # Dynamic responses are deliberately uncached. Enable only with an app-aware
    # custom vhost or a more-specific location from the proxy server drop-ins.
    # proxy_cache proxy_cache;
    # proxy_cache_valid 200 7d;
    include /etc/nginx/snippets/proxy-common.conf;
    proxy_pass {{upstreamScheme}}://{{upstreamName}}{{upstreamUri}};
  }
  {{/redirectHttps}}
}

server {
  listen 443 ssl;
  listen [::]:443 ssl;
  {{#http3}}
  listen 443 quic;
  listen [::]:443 quic;
  {{/http3}}
  http2 on;
  server_name {{serverNames}};

  # Common proxy drop-ins apply to both protocols; HTTPS drop-ins apply only here.
  include /etc/nginx/custom/proxies/{{name}}/server.d/*.conf;
  include /etc/nginx/custom/proxies/{{name}}/https.d/*.conf;

  {{#sslCertificate}}
  ssl_certificate     {{sslCertificate}};
  ssl_certificate_key {{sslCertificateKey}};
  {{/sslCertificate}}
  include {{sslInclude}};
  {{#http3}}
  add_header Alt-Svc 'h3=":{{httpsAdvertisedPort}}"; ma=86400' always;
  {{/http3}}
  {{#accessLog}}
  access_log {{accessLogPath}} bento_access_log buffer=64k flush=1s;
  {{/accessLog}}
  location ~* \.(?:css|js|mjs|jpg|jpeg|gif|png|svg|ico|webp|avif|woff|woff2|ttf|eot)$ {
    expires 30d;
    proxy_cache proxy_assets;
    proxy_cache_valid 200 301 302 7d;
    include /etc/nginx/snippets/proxy-common.conf;
    proxy_pass {{upstreamScheme}}://{{upstreamName}}{{upstreamUri}};
  }
  location / {
    # Dynamic responses are deliberately uncached. Enable only with an app-aware
    # custom vhost or a more-specific location from the proxy server drop-ins.
    # proxy_cache proxy_cache;
    # proxy_cache_valid 200 7d;
    include /etc/nginx/snippets/proxy-common.conf;
    proxy_pass {{upstreamScheme}}://{{upstreamName}}{{upstreamUri}};
  }
}
