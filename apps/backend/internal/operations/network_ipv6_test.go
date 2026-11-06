package operations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/network"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
)

func TestPlanNetworksRecordsIPv6(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		ns, err := PlanNetworks(nil, v6)
		if err != nil {
			t.Fatal(err)
		}
		if ns.AppsIPv6 != v6 {
			t.Fatalf("ipv6=%v planned AppsIPv6=%v", v6, ns.AppsIPv6)
		}
	}
}

// Docker publishes 0.0.0.0 on IPv4 only: an edge bound to all addresses must
// also publish [::] on a dual-stack plan, and never on an IPv4-only one
// (where every IPv6 visitor would arrive as the bridge gateway).
func TestEdgeSpecPublishesIPv6OnDualStack(t *testing.T) {
	h := newHarness(t)
	s := domain.DefaultEdgeSettings()
	s.Enabled, s.HTTP3 = true, true
	hosts := func(bind string, v6 bool) map[string][]string {
		s.Bind = bind
		spec, err := h.c.edgeSpec(s, NetworkSettings{EdgeIP: "10.0.0.2", AppsIPv6: v6})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for _, p := range []string{"80/tcp", "443/tcp", "443/udp"} {
			for _, b := range spec.HostConfig.PortBindings[network.MustParsePort(p)] {
				out[p] = append(out[p], b.HostIP.String())
			}
		}
		return out
	}
	for p, got := range hosts("0.0.0.0", true) {
		if len(got) != 2 || got[0] != "0.0.0.0" || got[1] != "::" {
			t.Errorf("dual-stack, all addresses: %s published on %v", p, got)
		}
	}
	for _, c := range []struct {
		bind string
		v6   bool
	}{{"0.0.0.0", false}, {"127.0.0.1", true}, {"203.0.113.7", true}} {
		for p, got := range hosts(c.bind, c.v6) {
			if len(got) != 1 || got[0] != c.bind {
				t.Errorf("bind %s ipv6=%v: %s published on %v", c.bind, c.v6, p, got)
			}
		}
	}
}

// ACME hosts present the pending placeholder until their first issuance, so
// the edge mounts it read-only; preparing the edge creates it.
func TestEdgeSpecMountsPendingCertificate(t *testing.T) {
	h := newHarness(t)
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	spec, err := h.c.edgeSpec(s, NetworkSettings{EdgeIP: "10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range spec.HostConfig.Mounts {
		if m.Target == edge.PendingMount {
			found = m.Source == h.c.Layout.EdgePendingDir() && m.ReadOnly
		}
	}
	if !found {
		t.Fatalf("pending certificate not mounted read-only: %+v", spec.HostConfig.Mounts)
	}
	if err := h.c.prepareEdgeDirs(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"pending.crt", "pending.key"} {
		if _, err := os.Stat(filepath.Join(h.c.Layout.EdgePendingDir(), f)); err != nil {
			t.Fatalf("prepareEdgeDirs did not create %s: %v", f, err)
		}
	}
}
