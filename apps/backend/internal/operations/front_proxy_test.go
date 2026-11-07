package operations

import (
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"testing"

	"github.com/moby/moby/api/types/network"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func frontSettings(trusted ...string) domain.EdgeSettings {
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	s.FrontProxy.Enabled = true
	s.FrontProxy.TrustedProxies = trusted
	return s
}

func validationFields(err error) []string {
	ve, ok := errors.AsType[domain.ValidationErrors](err)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range ve {
		out = append(out, e.Field)
	}
	return out
}

func TestConfigureEdgeRejectsBadFrontProxy(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := store.PutSetting(ctx, h.c.Store.DB(), networkSettingKey,
		NetworkSettings{AppsSubnet: "10.200.0.0/24", DataSubnet: "10.200.1.0/24"}); err != nil {
		t.Fatal(err)
	}
	sameBind := frontSettings("100.64.0.1")
	sameBind.FrontProxy.HTTPSPort = 443
	clash := frontSettings("100.64.0.1")
	clash.FrontProxy.HTTPPort, clash.FrontProxy.HTTPSPort = 9000, 9000
	badBind := frontSettings("100.64.0.1")
	badBind.FrontProxy.Bind = "::1"
	cases := []struct {
		name  string
		s     domain.EdgeSettings
		field string
	}{
		{"no trusted proxy", frontSettings(), "frontProxy.trustedProxies"},
		{"catch-all v4", frontSettings("0.0.0.0/0"), "frontProxy.trustedProxies"},
		{"catch-all v6", frontSettings("::/0"), "frontProxy.trustedProxies"},
		{"not an address", frontSettings("vps.example.com"), "frontProxy.trustedProxies"},
		{"nginx injection", frontSettings("100.64.0.1 1; } server {"), "frontProxy.trustedProxies"},
		{"zone", frontSettings("fe80::1%eth0"), "frontProxy.trustedProxies"},
		{"overlaps apps network", frontSettings("10.200.0.0/16"), "frontProxy.trustedProxies"},
		{"inside data network", frontSettings("10.200.1.7"), "frontProxy.trustedProxies"},
		{"port used by main listener", sameBind, "frontProxy.httpsPort"},
		{"http and https port equal", clash, "frontProxy.httpsPort"},
		{"bind not ipv4", badBind, "frontProxy.bind"},
	}
	for _, c := range cases {
		_, err := h.c.ConfigureEdge(ctx, c.s, "")
		if !slices.Contains(validationFields(err), c.field) {
			t.Errorf("%s: expected a %s validation error, got %v", c.name, c.field, err)
		}
	}
}

func TestConfigureEdgeCanonicalisesFrontProxy(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	s := frontSettings(" 100.64.0.1", "100.64.0.1/32", "fd7a:115c:a1e0::5/48", "")
	// Different specific binds may share a port with the main listeners.
	s.Bind, s.FrontProxy.Bind = "192.168.1.10", "100.64.0.5"
	s.FrontProxy.HTTPPort, s.FrontProxy.HTTPSPort = 80, 443
	if _, err := h.c.ConfigureEdge(ctx, s, ""); err != nil {
		t.Fatal(err)
	}
	got, err := h.c.EdgeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"100.64.0.1/32", "fd7a:115c:a1e0::/48"}; !slices.Equal(got.FrontProxy.TrustedProxies, want) {
		t.Fatalf("trusted proxies = %v, want %v", got.FrontProxy.TrustedProxies, want)
	}
}

// Clients that predate the front proxy send no frontProxy object.
func TestConfigureEdgeFillsFrontProxyDefaults(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	s := domain.DefaultEdgeSettings()
	s.FrontProxy = domain.FrontProxySettings{}
	if _, err := h.c.ConfigureEdge(ctx, s, ""); err != nil {
		t.Fatal(err)
	}
	got, err := h.c.EdgeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fp := got.FrontProxy
	if fp.Enabled || fp.Bind != "0.0.0.0" || fp.HTTPPort != 8080 || fp.HTTPSPort != 8443 || fp.TrustedProxies == nil {
		t.Fatalf("unexpected front proxy defaults: %+v", fp)
	}
}

// Adding the front proxy settings must not change the fingerprint of an
// existing edge, or every edge would be recreated on upgrade.
func TestEdgeFingerprintStableWithoutFrontProxy(t *testing.T) {
	ns := NetworkSettings{EdgeIP: "10.200.0.2"}
	s := domain.EdgeSettings{Enabled: true, Bind: "0.0.0.0", HTTPPort: 80, HTTPSPort: 443, HTTP3: true,
		ACMEEmail: "ops@example.com", ACMEURL: "https://acme.test/dir"}
	type legacySettings struct {
		Enabled   bool   `json:"enabled"`
		Bind      string `json:"bind"`
		HTTPPort  int    `json:"httpPort"`
		HTTPSPort int    `json:"httpsPort"`
		HTTP3     bool   `json:"http3"`
		ACMEEmail string `json:"acmeEmail"`
		ACMEURL   string `json:"acmeUrl"`
	}
	b, _ := json.Marshal(struct {
		S  legacySettings
		IP string
		I  string
		V  int
	}{legacySettings{true, "0.0.0.0", 80, 443, true, "ops@example.com", "https://acme.test/dir"}, ns.EdgeIP, domain.EdgeImage,
		5}) // shape version 5: memory and PID limits
	legacy := platform.SHA256Hex(b)[:20]
	s.FrontProxy = domain.DefaultEdgeSettings().FrontProxy
	if got := edgeFingerprint(s, ns); got != legacy {
		t.Fatalf("fingerprint changed for an edge without a front proxy: %s != %s", got, legacy)
	}
	s.FrontProxy.TrustedProxies = []string{"100.64.0.1/32"}
	if edgeFingerprint(s, ns) != legacy {
		t.Fatal("a disabled front proxy must not affect the fingerprint")
	}
	s.FrontProxy.Enabled = true
	on := edgeFingerprint(s, ns)
	if on == legacy {
		t.Fatal("enabling the front proxy publishes ports, so it must recreate the edge")
	}
	s.FrontProxy.TrustedProxies = []string{"100.64.0.2/32"}
	if edgeFingerprint(s, ns) != on {
		t.Fatal("changing trusted proxies only re-renders config; it must not recreate the edge")
	}
}

// stubHostAddrs makes addrs the only specific addresses this host has.
func stubHostAddrs(t *testing.T, addrs ...string) {
	t.Helper()
	have := map[netip.Addr]bool{}
	for _, a := range addrs {
		have[netip.MustParseAddr(a)] = true
	}
	prev := hostHasAddr
	hostHasAddr = func(a netip.Addr) bool { return a.IsUnspecified() || have[a] }
	t.Cleanup(func() { hostHasAddr = prev })
}

func TestEdgeSpecPublishesFrontProxyPorts(t *testing.T) {
	h := newHarness(t)
	stubHostAddrs(t, "100.101.102.103")
	s := frontSettings("100.64.0.1/32")
	s.FrontProxy.Bind, s.FrontProxy.HTTPPort, s.FrontProxy.HTTPSPort = "100.101.102.103", 18080, 18443
	spec, err := h.c.edgeSpec(s, NetworkSettings{EdgeIP: "10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	for container, host := range map[string]string{"8080/tcp": "18080", "8443/tcp": "18443"} {
		p := network.MustParsePort(container)
		b := spec.HostConfig.PortBindings[p]
		if len(b) != 1 || b[0].HostIP.String() != "100.101.102.103" || b[0].HostPort != host {
			t.Errorf("%s bound as %+v", container, b)
		}
		if _, ok := spec.Config.ExposedPorts[p]; !ok {
			t.Errorf("%s not exposed", container)
		}
	}
	s.FrontProxy.Enabled = false
	spec, err = h.c.edgeSpec(s, NetworkSettings{EdgeIP: "10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.HostConfig.PortBindings) != 2 {
		t.Fatalf("a disabled front proxy publishes nothing: %v", spec.HostConfig.PortBindings)
	}
}

func TestConfigureEdgeValidatesHTTPProxies(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := store.PutSetting(ctx, h.c.Store.DB(), networkSettingKey,
		NetworkSettings{AppsSubnet: "10.200.0.0/24", DataSubnet: "10.200.1.0/24"}); err != nil {
		t.Fatal(err)
	}
	with := func(p ...string) domain.EdgeSettings {
		s := domain.DefaultEdgeSettings()
		s.Enabled, s.HTTPProxies = true, p
		return s
	}
	tooMany := make([]string, maxHTTPProxies+1)
	for i := range tooMany {
		tooMany[i] = "192.0.2." + strconv.Itoa(i)
	}
	for name, s := range map[string]domain.EdgeSettings{
		"catch-all v4":       with("0.0.0.0/0"),
		"catch-all v6":       with("::/0"),
		"not an address":     with("cdn.example.com"),
		"nginx injection":    with("173.245.48.0/20 1; } server {"),
		"overlaps apps":      with("10.200.0.0/16"),
		"tunnel address":     with("10.200.0.3"),
		"more than the list": with(tooMany...),
	} {
		_, err := h.c.ConfigureEdge(ctx, s, "")
		if !slices.Contains(validationFields(err), "httpProxies") {
			t.Errorf("%s: expected an httpProxies validation error, got %v", name, err)
		}
	}
	// Cloudflare's published ranges fit, canonicalised.
	if _, err := h.c.ConfigureEdge(ctx, with(" 173.245.48.1/20", "2400:cb00::/32", "173.245.48.0/20", "198.51.100.7"), ""); err != nil {
		t.Fatal(err)
	}
	got, err := h.c.EdgeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"173.245.48.0/20", "2400:cb00::/32", "198.51.100.7/32"}; !slices.Equal(got.HTTPProxies, want) {
		t.Fatalf("HTTP proxies = %v, want %v", got.HTTPProxies, want)
	}
}

// Trusting HTTP proxies only changes rendered config: the edge is reloaded,
// never recreated.
func TestEdgeFingerprintIgnoresHTTPProxies(t *testing.T) {
	ns := NetworkSettings{EdgeIP: "10.200.0.2"}
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	before := edgeFingerprint(s, ns)
	s.HTTPProxies = []string{"173.245.48.0/20"}
	if edgeFingerprint(s, ns) != before {
		t.Fatal("HTTP proxies must not recreate the edge")
	}
}

// A front proxy bound to a VPN address that is not up yet (a reboot, the VPN
// restarting) must not keep the main listeners from starting: its ports are
// left out until the address appears, which changes the fingerprint.
func TestFrontProxyPortsWaitForTheirBindAddress(t *testing.T) {
	h := newHarness(t)
	ns := NetworkSettings{EdgeIP: "10.0.0.2"}
	s := frontSettings("100.64.0.1/32")
	s.FrontProxy.Bind = "100.101.102.103"
	off := s
	off.FrontProxy.Enabled = false
	stubHostAddrs(t)
	spec, err := h.c.edgeSpec(s, ns)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.HostConfig.PortBindings) != 2 || edgeFingerprint(s, ns) != edgeFingerprint(off, ns) {
		t.Fatalf("without its bind address the front proxy publishes nothing: %v", spec.HostConfig.PortBindings)
	}
	stubHostAddrs(t, "100.101.102.103")
	spec, err = h.c.edgeSpec(s, ns)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.HostConfig.PortBindings) != 4 || edgeFingerprint(s, ns) == edgeFingerprint(off, ns) {
		t.Fatalf("with its bind address the front proxy ports are published: %v", spec.HostConfig.PortBindings)
	}
}

func TestFrontProxyAddressAppearingRecreatesTheEdge(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	s := frontSettings("100.64.0.1/32")
	s.FrontProxy.Bind = "100.101.102.103"
	if err := store.PutSetting(ctx, h.store.DB(), edgeSettingKey, s); err != nil {
		t.Fatal(err)
	}
	stubHostAddrs(t)
	if got := submitEdgeApply(t, h); got.State != store.OpSucceeded {
		t.Fatalf("edge apply without the VPN: %s %s", got.State, got.ErrorMessage)
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift while the address is missing: %v %v", drift, err)
	}
	stubHostAddrs(t, "100.101.102.103")
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || !drift {
		t.Fatalf("the address appearing must be drift: %v %v", drift, err)
	}
	if got := submitEdgeApply(t, h); got.State != store.OpSucceeded {
		t.Fatalf("edge apply with the VPN: %s %s", got.State, got.ErrorMessage)
	}
	ins, err := h.fake.Inspect(ctx, h.c.Names.EdgeContainer())
	if err != nil || ins == nil {
		t.Fatal(err)
	}
	if _, ok := ins.HostConfig.PortBindings[network.MustParsePort("8443/tcp")]; !ok {
		t.Fatal("the recreated edge must publish the front proxy ports")
	}
}

// Docker can start an edge whose port setup failed with no ports at all; it
// serves nothing, so it is drift and is recreated.
func TestEdgeRunningWithoutItsPortsIsRecreated(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	if got := submitEdgeApply(t, h); got.State != store.OpSucceeded {
		t.Fatalf("edge apply: %s %s", got.State, got.ErrorMessage)
	}
	for _, c := range h.fake.Containers {
		if c.Name == h.c.Names.EdgeContainer() {
			c.NoPorts = true
		}
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || !drift {
		t.Fatalf("an edge without its published ports must be drift: %v %v", drift, err)
	}
	created := countCalls(h, "Create")
	if got := submitEdgeApply(t, h); got.State != store.OpSucceeded {
		t.Fatalf("edge apply: %s %s", got.State, got.ErrorMessage)
	}
	if countCalls(h, "Create") <= created {
		t.Fatal("the edge must be recreated")
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift after recreate: %v %v", drift, err)
	}
}
