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
		"proxy_set_header X-Forwarded-For $remote_addr;", "proxy_set_header X-Forwarded-Proto $bento_proto;",
		"proxy_set_header X-Forwarded-Port $bento_port;",
		"acme_certificate bento_acme;", "return 301 https://$host$bento_https_suffix$request_uri;", "listen 443 quic;",
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
	if strings.Count(www, "return 301 https://shop.example.com$bento_https_suffix$request_uri;") != 2 {
		t.Fatal("a redirect to a served TLS host must use its scheme and port on both listeners")
	}
	if strings.Contains(www, "proxy_pass") || strings.Contains(www, "/_bento/") {
		t.Fatal("a redirect host proxies nothing")
	}
	if !strings.Contains(string(files["sites/host-old.example.com.conf"]),
		"return 301 $bento_proto://elsewhere.example.org$request_uri;") {
		t.Fatal("a redirect to an unknown host keeps the request scheme")
	}
	main := string(files["nginx.conf"])
	if !strings.Contains(main, "map $bento_from_tunnel $bento_https_suffix {\n    default \":8443\";\n    1       \"\";") ||
		!strings.Contains(main, "map $bento_from_tunnel $bento_http_suffix {\n    default \":8080\";\n    1       \"\";") {
		t.Fatal("redirect suffixes are the edge's ports, and none for tunnel visitors")
	}
	if !strings.Contains(main, "map $scheme $bento_published_port {\n    default 8080;\n    https   8443;") ||
		!strings.Contains(main, "map $scheme $bento_scheme_suffix {\n    default \":8080\";\n    https   \":8443\";") {
		t.Fatal("forwarded port and host suffix are the edge's published ports")
	}
	site := string(files["sites/host-shop.example.com.conf"])
	if !strings.Contains(site, "proxy_set_header Host $bento_forwarded_host;") ||
		!strings.Contains(site, "proxy_set_header X-Forwarded-Host $bento_forwarded_host;") ||
		strings.Contains(site, "proxy_set_header Host $http_host;") {
		t.Fatal("apps must see the host with the edge's published port, built from $host")
	}
}

func TestRenderTrustsForwardingOnlyFromTunnel(t *testing.T) {
	app := domain.App{ID: "a1", Slug: "shop", Ingress: domain.IngressManaged, Publication: domain.Published,
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Port: 3000}}}
	in := Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Apps:     []domain.App{app},
		Hosts:    []domain.Host{appHost("shop.example.com", "a1", domain.Route{TLS: domain.TLSACME, RedirectHTTPS: true})},
		Running:  map[string]bool{"a1": true},
		TunnelIP: "10.200.0.3",
	}
	files, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	main := string(files["nginx.conf"])
	for _, want := range []string{
		"set_real_ip_from 10.200.0.3;", "real_ip_header CF-Connecting-IP;",
		"geo $realip_remote_addr $bento_from_tunnel {\n    default 0;\n    10.200.0.3 1;\n  }",
		"map \"$bento_from_tunnel:$http_x_forwarded_proto\" $bento_proto {\n    default   $scheme;\n    \"1:https\" https;",
		"map \"$bento_from_tunnel:$http_x_forwarded_proto\" $bento_port {\n    default   $bento_published_port;\n    \"1:https\" 443;",
		"map $bento_from_tunnel $bento_forwarded_host {\n    default $host$bento_scheme_suffix;\n    1       $host;",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main config missing %q", want)
		}
	}
	if strings.Count(main, "set_real_ip_from") != 1 {
		t.Error("only the tunnel address may rewrite the client address")
	}
	site := string(files["sites/host-shop.example.com.conf"])
	if strings.Contains(site, "$scheme") || strings.Contains(site, "$server_port") {
		t.Error("sites must forward the visitor's scheme and port, not the edge's")
	}
	// Port 80 redirects plain HTTP but serves a tunnel visitor already on HTTPS.
	http := site[:strings.Index(site, "listen 443 ssl;")]
	if !strings.Contains(http, "if ($bento_proto = http) {") || !strings.Contains(http, "proxy_pass http://bento_app_shop;") {
		t.Error("port 80 must redirect only plain HTTP and serve tunnel HTTPS")
	}

	in.TunnelIP = ""
	files, err = Render(in)
	if err != nil {
		t.Fatal(err)
	}
	main = string(files["nginx.conf"])
	if strings.Contains(main, "set_real_ip_from") || strings.Contains(main, "real_ip_header") ||
		!strings.Contains(main, "geo $bento_from_tunnel {\n    default 0;\n  }") {
		t.Error("without a tunnel address no forwarding header is trusted")
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

func TestRenderProtectionOnlyForAppHosts(t *testing.T) {
	app := func(slug string, p domain.Protection) domain.App {
		return domain.App{ID: slug, Slug: slug, Ingress: domain.IngressManaged, Publication: domain.Published, Protection: p}
	}
	on := app("on", domain.DefaultProtection())
	partial := app("partial", domain.Protection{BlockDotFiles: true})
	off := app("off", domain.Protection{})
	cached := appHost("cache.example.com", "on", domain.Route{TLS: domain.TLSNone, StaticCache: true})
	files, err := Render(Input{
		Settings:      domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		UtilsUpstream: "10.0.0.2:7781",
		Apps:          []domain.App{on, partial, off},
		Hosts: []domain.Host{
			appHost("on.example.com", "on", domain.Route{TLS: domain.TLSNone}),
			appHost("partial.example.com", "partial", domain.Route{TLS: domain.TLSNone}),
			appHost("off.example.com", "off", domain.Route{TLS: domain.TLSNone}),
			cached,
			upstreamHost("up.example.com", true, "http://10.0.0.9:8080"),
			{Name: "go.example.com", Target: domain.HostTargetRedirect, RedirectTo: "on.example.com", Enabled: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	site := func(host string) string { return string(files["sites/"+RouteName(host)+".conf"]) }
	if c := site("on.example.com"); !strings.Contains(c, "wlwmanifest") || !strings.Contains(c, "composer") ||
		!strings.Contains(c, `/\.(?!well-known`) || !strings.Contains(c, "return 404;") || strings.Contains(c, "deny") {
		t.Fatalf("an app with everything on must carry every group:\n%s", c)
	}
	if c := site("partial.example.com"); !strings.Contains(c, "well-known") || strings.Contains(c, "wlwmanifest") || strings.Contains(c, "composer") {
		t.Fatalf("only the dot file group is on:\n%s", c)
	}
	for _, host := range []string{"off.example.com", "up.example.com", "go.example.com"} {
		if c := site(host); strings.Contains(c, "Bento protection") || strings.Contains(c, "well-known") {
			t.Errorf("%s must carry no protection:\n%s", host, c)
		}
	}
	// A blocked path must never reach the static cache location, which would
	// otherwise match .map before the protection does.
	c := site("cache.example.com")
	block, static := strings.Index(c, "# Bento protection"), strings.Index(c, "proxy_cache bento_static;")
	if block < 0 || static < 0 || block > static {
		t.Fatalf("protection at %d, static cache at %d:\n%s", block, static, c)
	}
	// The webhook prefix location (^~) outranks every regex location.
	if !strings.Contains(c, "location ^~ /_bento/webhook/") {
		t.Fatal("webhook location missing")
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

func TestRenderFrontProxy(t *testing.T) {
	app := domain.App{ID: "a1", Slug: "shop", Ingress: domain.IngressManaged, Publication: domain.Published,
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Port: 3000}}}
	in := Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 8443, HTTP3: true, ACMEURL: "https://acme.test/dir",
			FrontProxy: domain.FrontProxySettings{Enabled: true, TrustedProxies: []string{"100.64.0.1/32", "fd7a:115c:a1e0::/48"}}},
		Apps:          []domain.App{app},
		Hosts:         []domain.Host{appHost("shop.example.com", "a1", domain.Route{TLS: domain.TLSACME, RedirectHTTPS: true})},
		Running:       map[string]bool{"a1": true},
		UtilsUpstream: "10.200.0.1:7781",
		TunnelIP:      "10.200.0.3",
	}
	files, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	main := string(files["nginx.conf"])
	for _, want := range []string{
		"map $server_port $bento_via_front {\n    default 0;\n    8080 1;\n    8443 1;\n  }",
		// The peer, never a forwarded or PROXY-claimed address, decides trust.
		"geo $realip_remote_addr $bento_front_trusted {\n    default 0;\n    100.64.0.1/32 1;\n    fd7a:115c:a1e0::/48 1;\n  }",
		"map $bento_via_front $bento_client_addr {\n    default $remote_addr;\n    1       $proxy_protocol_addr;",
		"map \"$bento_via_front:$scheme\" $bento_published_port {\n    default   80;\n    \"0:https\" 8443;\n    \"1:http\"  80;\n    \"1:https\" 443;",
		"map $bento_std_ports $bento_https_suffix {\n    default \":8443\";\n    1       \"\";",
		"map $bento_std_ports $bento_forwarded_host {\n    default $host$bento_scheme_suffix;\n    1       $host;",
		"map $bento_via_front $bento_alt_svc {\n    default 'h3=\":8443\"; ma=86400';\n    1       \"\";",
		"listen 8080 proxy_protocol default_server;", "listen 8443 ssl proxy_protocol default_server;",
		"log_format bento_edge '$bento_client_addr - ",
		// ACME challenges are answered before any route sets $bento_route.
		"uninitialized_variable_warn off;",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main config missing %q", want)
		}
	}
	if strings.Contains(main, "real_ip_header proxy_protocol") {
		t.Error("the PROXY address must not replace $remote_addr: the tunnel owns real_ip_header")
	}
	site := string(files["sites/host-shop.example.com.conf"])
	for _, want := range []string{
		"listen 8080 proxy_protocol;", "listen [::]:8080 proxy_protocol;",
		"listen 8443 ssl proxy_protocol;", "listen [::]:8443 ssl proxy_protocol;",
		"add_header Alt-Svc $bento_alt_svc always;",
		"proxy_set_header X-Forwarded-For $bento_client_addr;", "proxy_set_header X-Real-IP $bento_client_addr;",
	} {
		if !strings.Contains(site, want) {
			t.Errorf("site missing %q", want)
		}
	}
	if n := strings.Count(site, "if ($bento_front_reject) {\n    return 444;"); n != 2 {
		t.Errorf("both server blocks must close untrusted front peers, found %d", n)
	}
	if strings.Contains(site, "X-Forwarded-For $remote_addr") {
		t.Error("forwarded client address must come from $bento_client_addr")
	}

	// Off: no PROXY listener or front-proxy variable anywhere.
	in.Settings.FrontProxy.Enabled = false
	files, err = Render(in)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		if strings.Contains(string(b), "proxy_protocol") || strings.Contains(string(b), "bento_front") ||
			strings.Contains(string(b), "bento_client_addr") {
			t.Errorf("%s mentions the front proxy while it is off", name)
		}
	}

	// Persisted settings are re-checked: nothing unparsed reaches nginx.
	for _, bad := range [][]string{nil, {"0.0.0.0/0"}, {"100.64.0.1 1; }"}} {
		in.Settings.FrontProxy = domain.FrontProxySettings{Enabled: true, TrustedProxies: bad}
		if _, err := Render(in); err == nil {
			t.Errorf("trusted %q must be refused at render", bad)
		}
	}
}

func TestRenderHTTPSUpstreamsVerifyByName(t *testing.T) {
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443},
		Hosts: []domain.Host{
			upstreamHost("named.example.com", true, "https://app.vercel.example/", "https://app.vercel.example:8443"),
			upstreamHost("port.example.com", false, "https://api.example.net:8443/v1"),
			upstreamHost("ip.example.com", false, "https://10.0.0.5:8443"),
			upstreamHost("plain.example.com", false, "http://grafana.internal:3000"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	named := string(files["sites/host-named.example.com.conf"])
	for _, want := range []string{
		"proxy_ssl_server_name on;", "proxy_ssl_name app.vercel.example;", "proxy_ssl_verify on;",
		"proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;", "proxy_ssl_verify_depth 4;",
		"proxy_set_header Host app.vercel.example;", "proxy_set_header X-Forwarded-Host $bento_forwarded_host;",
		// Resolved at run time, so an unresolvable name cannot fail nginx -t
		// for the whole edge.
		"zone bento_host_" + upstreamIdent("named.example.com") + " 64k;",
		"server app.vercel.example:443 resolve;", "server app.vercel.example:8443 resolve;",
	} {
		if !strings.Contains(named, want) {
			t.Errorf("named HTTPS upstream missing %q", want)
		}
	}
	// The cached static location proxies too: it must carry the same TLS.
	if strings.Count(named, "proxy_ssl_verify on;") != 2 {
		t.Error("both proxy locations of a cached HTTPS upstream must verify")
	}
	if strings.Contains(named, "proxy_set_header Host $bento_forwarded_host;") {
		t.Error("named HTTPS upstream must not receive the visitor's Host")
	}
	port := string(files["sites/host-port.example.com.conf"])
	if !strings.Contains(port, "proxy_set_header Host api.example.net:8443;") ||
		!strings.Contains(port, "proxy_ssl_name api.example.net;") {
		t.Error("a non-default port belongs in Host but not in the TLS name")
	}
	ip := string(files["sites/host-ip.example.com.conf"])
	if !strings.Contains(ip, "proxy_ssl_verify off;") || strings.Contains(ip, "proxy_ssl_server_name") ||
		!strings.Contains(ip, "proxy_set_header Host $bento_forwarded_host;") {
		t.Error("an HTTPS upstream named by IP keeps the visitor's Host and cannot verify a name")
	}
	plain := string(files["sites/host-plain.example.com.conf"])
	if strings.Contains(plain, "proxy_ssl") || !strings.Contains(plain, "proxy_set_header Host $bento_forwarded_host;") {
		t.Error("plain HTTP upstreams are unchanged")
	}
}

func TestUpstreamTLSName(t *testing.T) {
	for in, want := range map[string][2]string{
		"example.com:443":   {"example.com", "example.com"},
		"example.com:8443":  {"example.com", "example.com:8443"},
		"10.0.0.5:443":      {"", ""},
		"[2001:db8::1]:443": {"", ""},
		"[2001:db8::1]":     {"", ""},
	} {
		name, host := upstreamTLSName(in)
		if name != want[0] || host != want[1] {
			t.Errorf("%s -> %q %q, want %q %q", in, name, host, want[0], want[1])
		}
	}
}
