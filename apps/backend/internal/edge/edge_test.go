package edge

import (
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func appHost(name, appID string, route domain.Route) domain.Host {
	return domain.Host{Name: name, Target: domain.HostTargetApp, AppID: appID, Route: route, Enabled: true}
}

func upstreamHost(name string, cache bool, upstreams ...string) domain.Host {
	return domain.Host{Name: name, Target: domain.HostTargetUpstream, Upstreams: upstreams, Enabled: true,
		Route: domain.Route{TLS: domain.TLSNone, StaticCache: cache}}
}

func TestRenderOnlyManagedPublishedRoutes(t *testing.T) {
	mk := func(id, slug string, ingress domain.IngressMode, pub domain.Publication) domain.App {
		return domain.App{ID: id, Slug: slug, Ingress: ingress, Publication: pub,
			Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Port: 3000}}}
	}
	acme := domain.Route{TLS: domain.TLSACME, RedirectHTTPS: true}
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443, HTTP3: true, ACMEURL: "https://acme.test/dir"},
		Apps: []domain.App{
			mk("a1", "pub", domain.IngressManaged, domain.Published),
			mk("a2", "unpub", domain.IngressManaged, domain.Unpublished),
			mk("a3", "ext", domain.IngressExternal, domain.Unpublished),
		},
		Hosts: []domain.Host{
			appHost("pub.example.com", "a1", acme),
			appHost("unpub.example.com", "a2", acme),
			appHost("ext.example.com", "a3", acme),
		},
		Running:       map[string]bool{"a1": true},
		UtilsUpstream: "10.200.0.1:7781",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("expected main + 1 upstream + 1 site, got %d", len(files))
	}
	up := string(files["upstreams/app-pub.conf"])
	for _, want := range []string{"upstream bento_app_pub {", "server app-a1:3000 resolve;", "keepalive 16;"} {
		if !strings.Contains(up, want) {
			t.Errorf("upstream missing %q", want)
		}
	}
	site := string(files["sites/host-pub.example.com.conf"])
	for _, want := range []string{
		"server_name pub.example.com;", "proxy_pass http://bento_app_pub;",
		"proxy_set_header X-Forwarded-For $remote_addr;", "proxy_set_header X-Forwarded-Proto $scheme;",
		"acme_certificate bento_acme;", "return 301 https://$host$request_uri;", "listen 443 quic;",
		"location ^~ /_bento/webhook/ {", "proxy_pass http://bento_utils;",
		"include /etc/bento-edge-custom/routes/host-pub.example.com/*.conf;",
		"include /etc/bento-edge-custom/routes/app-pub/*.conf;",
	} {
		if !strings.Contains(site, want) {
			t.Errorf("site missing %q", want)
		}
	}
	if strings.Contains(site, "upstream bento_app_pub") {
		t.Error("app upstream must live in upstreams/, not in a host site")
	}
	main := string(files["nginx.conf"])
	if !strings.Contains(main, "resolver 127.0.0.11 valid=10s") ||
		!strings.Contains(main, "include upstreams/*.conf;\n  include sites/*.conf;") {
		t.Fatal("main config must re-resolve and use relative includes, upstreams before sites")
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

func TestRenderTLSIsPerHost(t *testing.T) {
	app := domain.App{ID: "a1", Slug: "shop", Ingress: domain.IngressManaged, Publication: domain.Published,
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Port: 3000}}}
	disabled := appHost("off.shop.test", "a1", domain.Route{TLS: domain.TLSNone})
	disabled.Enabled = false
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Apps:     []domain.App{app},
		Hosts: []domain.Host{
			appHost("shop.example.com", "a1", domain.Route{TLS: domain.TLSACME}),
			appHost("shop.internal.test", "a1", domain.Route{TLS: domain.TLSSelfSigned}),
			disabled,
		},
		Running: map[string]bool{"a1": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	pub := string(files["sites/host-shop.example.com.conf"])
	internal := string(files["sites/host-shop.internal.test.conf"])
	if !strings.Contains(pub, "acme_certificate bento_acme;") || strings.Contains(pub, "boot.crt") {
		t.Fatal("ACME host must use ACME only")
	}
	if !strings.Contains(internal, "ssl_certificate /etc/bento-edge-certs/boot.crt;") ||
		strings.Contains(internal, "acme_certificate") {
		t.Fatal("self-signed host must use the boot certificate only")
	}
	if _, ok := files["sites/host-off.shop.test.conf"]; ok {
		t.Fatal("a disabled host must not be served")
	}
	if len(files) != 4 {
		t.Fatalf("two hosts of one app share one upstream: got %d files", len(files))
	}
}

func TestRenderRedirectHosts(t *testing.T) {
	app := domain.App{ID: "a1", Slug: "shop", Ingress: domain.IngressManaged, Publication: domain.Published,
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Port: 3000}}}
	redirect := func(name, to string) domain.Host {
		return domain.Host{Name: name, Target: domain.HostTargetRedirect, RedirectTo: to, Enabled: true,
			Route: domain.Route{TLS: domain.TLSSelfSigned}}
	}
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 8080, HTTPSPort: 8443},
		Apps:     []domain.App{app},
		Hosts: []domain.Host{
			appHost("shop.example.com", "a1", domain.Route{TLS: domain.TLSACME}),
			redirect("www.shop.example.com", "shop.example.com"),
			redirect("old.example.com", "elsewhere.example.org"),
		},
		Running:       map[string]bool{"a1": true},
		UtilsUpstream: "10.200.0.1:7781",
	})
	if err != nil {
		t.Fatal(err)
	}
	www := string(files["sites/host-www.shop.example.com.conf"])
	if strings.Count(www, "return 301 https://shop.example.com:8443$request_uri;") != 2 {
		t.Fatal("a redirect to a served TLS host must use its scheme and port on both listeners")
	}
	if strings.Contains(www, "proxy_pass") || strings.Contains(www, "/_bento/") {
		t.Fatal("a redirect host proxies nothing")
	}
	if !strings.Contains(string(files["sites/host-old.example.com.conf"]),
		"return 301 $scheme://elsewhere.example.org$request_uri;") {
		t.Fatal("a redirect to an unknown host keeps the request scheme")
	}
}

func TestRenderWithoutUtilsListenerLeavesWebhookPathToUpstream(t *testing.T) {
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Hosts:    []domain.Host{upstreamHost("p.example.com", false, "http://10.0.0.9:8080")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files["sites/host-p.example.com.conf"]), "/_bento/") ||
		strings.Contains(string(files["nginx.conf"]), "bento_utils") {
		t.Fatal("without a utils listener on the apps network the edge must not reserve /_bento/webhook/")
	}
}

func TestRenderStaticCacheOnlyForOptedInApps(t *testing.T) {
	app := func(slug string, php bool) domain.App {
		a := domain.App{ID: slug, Slug: slug, Ingress: domain.IngressManaged, Publication: domain.Published}
		if php {
			a.Runtime.Kind = domain.RuntimePHP
		}
		return a
	}
	host := func(slug string, cache bool) domain.Host {
		return appHost(slug+".example.com", slug, domain.Route{TLS: domain.TLSNone, StaticCache: cache})
	}
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Apps:     []domain.App{app("on", true), app("off", true), app("http", false)},
		Hosts:    []domain.Host{host("on", true), host("off", false), host("http", true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["nginx.conf"]), "keys_zone=bento_static:") {
		t.Fatal("main config must define the static cache zone")
	}
	if !strings.Contains(string(files["sites/host-on.example.com.conf"]), "proxy_cache bento_static;") {
		t.Fatal("opted-in PHP app must cache static assets")
	}
	if !strings.Contains(string(files["sites/host-http.example.com.conf"]), "proxy_cache bento_static;") {
		t.Fatal("opted-in HTTP app must cache static assets")
	}
	if strings.Contains(string(files["sites/host-off.example.com.conf"]), "proxy_cache") {
		t.Fatal("app without opt-in must not cache")
	}
}

func TestRenderStaticCacheForOptedInUpstreams(t *testing.T) {
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Hosts: []domain.Host{
			upstreamHost("on.example.com", true, "http://10.0.0.9:8080"),
			upstreamHost("off.example.com", false, "http://10.0.0.9:8080"),
			upstreamHost("path.example.com", true, "http://10.0.0.9:8080/base"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	on := string(files["sites/host-on.example.com.conf"])
	onUp := "bento_host_" + upstreamIdent("on.example.com")
	if !strings.Contains(on, "proxy_cache bento_static;") || !strings.Contains(on, "upstream "+onUp+" {") {
		t.Fatal("opted-in upstream host must cache static assets through its keepalive upstream")
	}
	if strings.Contains(on, "proxy_ignore_headers") {
		t.Fatal("upstream cache must honor the external upstream's Cache-Control and Set-Cookie")
	}
	if strings.Contains(string(files["sites/host-off.example.com.conf"]), "proxy_cache") {
		t.Fatal("upstream host without opt-in must not cache")
	}
	path := string(files["sites/host-path.example.com.conf"])
	pathUp := "bento_host_" + upstreamIdent("path.example.com")
	// nginx rejects a proxy_pass URI inside a regex location.
	if !strings.Contains(path, "rewrite ^/(.*)$ /base$1 break;\n    proxy_pass http://"+pathUp+";") ||
		!strings.Contains(path, "proxy_pass http://"+pathUp+"/base;") {
		t.Fatal("cached upstream with a path must map it via rewrite in the regex location")
	}
}

func TestUpstreamIdentKeepsSimilarHostsApart(t *testing.T) {
	if upstreamIdent("a-b.example.com") == upstreamIdent("a.b-example.com") {
		t.Fatal("hosts that sanitize alike must get distinct upstream names")
	}
}
