# process app {{slug}}
upstream {{upstreamName}} {
  server unix:{{socketPath}};
  keepalive 32;
  keepalive_requests 1000;
  keepalive_timeout 60s;
}

server {
  listen 80;
  listen [::]:80;
  server_name {{serverNames}};

  include /etc/nginx/custom/apps/{{slug}}/server.d/*.conf;
  include /etc/nginx/custom/apps/{{slug}}/http.d/*.conf;

  {{#redirectHttps}}
  location / {
    return 301 https://$host{{httpsPortSuffix}}$request_uri;
  }
  {{/redirectHttps}}
  {{^redirectHttps}}
  {{#accessLog}}
  access_log {{accessLogPath}} bento_access_log buffer=64k flush=1s;
  {{/accessLog}}
  location / {
    include /etc/nginx/snippets/proxy-common.conf;
    proxy_pass http://{{upstreamName}};
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

  include /etc/nginx/custom/apps/{{slug}}/server.d/*.conf;
  include /etc/nginx/custom/apps/{{slug}}/https.d/*.conf;

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
  location / {
    include /etc/nginx/snippets/proxy-common.conf;
    proxy_pass http://{{upstreamName}};
  }
}
