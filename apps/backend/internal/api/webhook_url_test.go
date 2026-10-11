package api

import (
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestPublicHostOrigin(t *testing.T) {
	plain := domain.Host{Name: "shop.example.com", Route: domain.Route{TLS: domain.TLSNone}}
	tls := domain.Host{Name: "shop.example.com", Route: domain.Route{TLS: domain.TLSACME}}
	edge := func(bind string, http, https int) domain.EdgeSettings {
		return domain.EdgeSettings{Enabled: true, Bind: bind, HTTPPort: http, HTTPSPort: https}
	}
	cases := []struct {
		name   string
		es     domain.EdgeSettings
		tunnel bool
		host   domain.Host
		want   string
	}{
		{"public edge, default ports, plain", edge("0.0.0.0", 80, 443), false, plain, "http://shop.example.com"},
		{"public edge, default ports, tls", edge("0.0.0.0", 80, 443), false, tls, "https://shop.example.com"},
		{"public edge, custom port is reachable", edge("0.0.0.0", 8080, 8443), false, plain, "http://shop.example.com:8080"},
		{"public edge, custom tls port", edge("::", 8080, 8443), false, tls, "https://shop.example.com:8443"},
		// The e2e finding: edge on 127.0.0.1:18080 behind a Cloudflare Tunnel.
		{"loopback edge behind tunnel", edge("127.0.0.1", 18080, 18443), true, plain, "https://shop.example.com"},
		{"loopback edge behind host proxy", edge("127.0.0.1", 18080, 18443), false, plain, "https://shop.example.com"},
		{"ipv6 loopback edge", edge("::1", 8080, 8443), false, tls, "https://shop.example.com"},
		{"localhost edge", edge("localhost", 8080, 8443), false, plain, "https://shop.example.com"},
		{"tunnel with public edge", edge("0.0.0.0", 8080, 8443), true, plain, "https://shop.example.com"},
	}
	for _, c := range cases {
		if got := publicHostOrigin(c.es, c.tunnel, c.host); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
