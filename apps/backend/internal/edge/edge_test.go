package edge

import (
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestRenderOnlyManagedPublishedRoutes(t *testing.T) {
	mk := func(id, slug string, ingress domain.IngressMode, pub domain.Publication) domain.App {
		return domain.App{ID: id, Slug: slug, Ingress: ingress, Publication: pub,
			Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Port: 3000}},
			Domains: []domain.DomainLink{{Name: slug + ".example.com", Primary: true}}, Route: domain.Route{TLS: domain.TLSACME, RedirectHTTPS: true}}
	}
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443, HTTP3: true, ACMEURL: "https://acme.test/dir"},
		Apps: []domain.App{
			mk("a1", "pub", domain.IngressManaged, domain.Published),
			mk("a2", "unpub", domain.IngressManaged, domain.Unpublished),
			mk("a3", "ext", domain.IngressExternal, domain.Unpublished),
		},
		Running:       map[string]bool{"a1": true},
		UtilsUpstream: "10.200.0.1:7781",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected main + 1 site, got %d", len(files))
	}
	site := string(files["sites/app-pub.conf"])
	for _, want := range []string{
		"upstream bento_app_pub {", "server app-a1:3000 resolve;", "keepalive 16;", "proxy_pass http://bento_app_pub;",
		"proxy_set_header X-Forwarded-For $remote_addr;", "proxy_set_header X-Forwarded-Proto $scheme;",
		"acme_certificate bento_acme;", "return 301 https://$host$request_uri;", "listen 443 quic;",
		"location ^~ /_bento/webhook/ {", "proxy_pass http://bento_utils;",
	} {
		if !strings.Contains(site, want) {
			t.Errorf("site missing %q", want)
		}
	}
	main := string(files["nginx.conf"])
	if !strings.Contains(main, "resolver 127.0.0.11 valid=10s") || !strings.Contains(main, "include sites/*.conf;") {
		t.Fatal("main config must re-resolve and use relative includes")
	}
	if !strings.Contains(main, "upstream bento_utils {\n    server 10.200.0.1:7781;\n    keepalive 2;") {
		t.Fatal("main config must pool keepalive connections to the utils listener")
	}
	for _, f := range files {
		if strings.Contains(string(f), "/home/") || strings.Contains(string(f), "fastcgi_pass") {
			t.Fatal("edge must never reference app homes or FPM")
		}
	}
}

func TestRenderWithoutUtilsListenerLeavesWebhookPathToUpstream(t *testing.T) {
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Proxies: []domain.Proxy{{Name: "p", Enabled: true, Upstreams: []string{"http://10.0.0.9:8080"},
			Domains: []domain.DomainLink{{Name: "p.example.com", Primary: true}}, Route: domain.Route{TLS: domain.TLSNone}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files["sites/proxy-p.conf"]), "/_bento/") || strings.Contains(string(files["nginx.conf"]), "bento_utils") {
		t.Fatal("without a utils listener on the apps network the edge must not reserve /_bento/webhook/")
	}
}

func TestRenderStaticCacheOnlyForOptedInApps(t *testing.T) {
	app := func(slug string, php bool, cache bool) domain.App {
		a := domain.App{ID: slug, Slug: slug, Ingress: domain.IngressManaged, Publication: domain.Published,
			Domains: []domain.DomainLink{{Name: slug + ".example.com", Primary: true}},
			Route:   domain.Route{TLS: domain.TLSNone, StaticCache: cache}}
		if php {
			a.Runtime.Kind = domain.RuntimePHP
		}
		return a
	}
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Apps:     []domain.App{app("on", true, true), app("off", true, false), app("http", false, true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["nginx.conf"]), "keys_zone=bento_static:") {
		t.Fatal("main config must define the static cache zone")
	}
	if !strings.Contains(string(files["sites/app-on.conf"]), "proxy_cache bento_static;") {
		t.Fatal("opted-in PHP app must cache static assets")
	}
	if !strings.Contains(string(files["sites/app-http.conf"]), "proxy_cache bento_static;") {
		t.Fatal("opted-in HTTP app must cache static assets")
	}
	if strings.Contains(string(files["sites/app-off.conf"]), "proxy_cache") {
		t.Fatal("app without opt-in must not cache")
	}
}

func TestRenderStaticCacheForOptedInProxies(t *testing.T) {
	proxy := func(name string, cache bool) domain.Proxy {
		return domain.Proxy{Name: name, Enabled: true, Upstreams: []string{"http://10.0.0.9:8080"},
			Domains: []domain.DomainLink{{Name: name + ".example.com", Primary: true}},
			Route:   domain.Route{TLS: domain.TLSNone, StaticCache: cache}}
	}
	withPath := proxy("path", true)
	withPath.Upstreams = []string{"http://10.0.0.9:8080/base"}
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Proxies:  []domain.Proxy{proxy("on", true), proxy("off", false), withPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	on := string(files["sites/proxy-on.conf"])
	if !strings.Contains(on, "proxy_cache bento_static;") || !strings.Contains(on, "upstream bento_proxy_on {") {
		t.Fatal("opted-in proxy must cache static assets through its keepalive upstream")
	}
	if strings.Contains(on, "proxy_ignore_headers") {
		t.Fatal("proxy cache must honor the external upstream's Cache-Control and Set-Cookie")
	}
	if strings.Contains(string(files["sites/proxy-off.conf"]), "proxy_cache") {
		t.Fatal("proxy without opt-in must not cache")
	}
	path := string(files["sites/proxy-path.conf"])
	// nginx rejects a proxy_pass URI inside a regex location.
	if !strings.Contains(path, "rewrite ^/(.*)$ /base$1 break;\n    proxy_pass http://bento_proxy_path;") ||
		!strings.Contains(path, "proxy_pass http://bento_proxy_path/base;") {
		t.Fatal("cached proxy with an upstream path must map it via rewrite in the regex location")
	}
}
