package operations

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// NetworkSettings is the persisted stack network plan.
//
// Membership matrix (tested in network_test.go):
//
//	apps network  (bridge, egress allowed): app instances, tools, edge, cloudflared
//	data network  (bridge, internal, no egress): app instances, tools, MySQL, PostgreSQL, Redis, backup jobs
//
// Edge and cloudflared never join the data network. Dynamic addresses on the
// apps network come from the upper half of the subnet; the edge and tunnel
// hold reserved static addresses so apps can trust forwarding headers only
// from them. This is not per-app isolation: apps can reach sibling listeners.
type NetworkSettings struct {
	AppsSubnet string `json:"appsSubnet"`
	DataSubnet string `json:"dataSubnet"`
	EdgeIP     string `json:"edgeIp"`
	TunnelIP   string `json:"tunnelIp"`
}

const networkSettingKey = "network"

func (n NetworkSettings) TrustedProxies() []string {
	var out []string
	for _, ip := range []string{n.EdgeIP, n.TunnelIP} {
		if ip != "" {
			out = append(out, ip)
		}
	}
	return out
}

// candidateSubnets yields /24s in 10.200.0.0/13 for stack networks.
func candidateSubnets() []netip.Prefix {
	var out []netip.Prefix
	for second := 200; second < 208; second++ {
		for third := 0; third < 256; third++ {
			out = append(out, netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(second), byte(third), 0}), 24))
		}
	}
	return out
}

func overlaps(p netip.Prefix, used []netip.Prefix) bool {
	for _, u := range used {
		if p.Overlaps(u) {
			return true
		}
	}
	return false
}

// PlanNetworks picks two unused subnets.
func PlanNetworks(used []netip.Prefix) (NetworkSettings, error) {
	var picked []netip.Prefix
	for _, c := range candidateSubnets() {
		if overlaps(c, used) || overlaps(c, picked) {
			continue
		}
		picked = append(picked, c)
		if len(picked) == 2 {
			break
		}
	}
	if len(picked) < 2 {
		return NetworkSettings{}, fmt.Errorf("no free /24 subnet available in 10.200.0.0/13")
	}
	base := picked[0].Addr().As4()
	return NetworkSettings{
		AppsSubnet: picked[0].String(),
		DataSubnet: picked[1].String(),
		EdgeIP:     netip.AddrFrom4([4]byte{base[0], base[1], base[2], 2}).String(),
		TunnelIP:   netip.AddrFrom4([4]byte{base[0], base[1], base[2], 3}).String(),
	}, nil
}

// NetworkPlan returns the persisted plan, choosing one on first use.
func (c *Controller) NetworkPlan(ctx context.Context) (NetworkSettings, error) {
	var ns NetworkSettings
	found, err := store.GetSetting(ctx, c.Store.DB(), networkSettingKey, &ns)
	if err != nil || found {
		return ns, err
	}
	used, err := c.Engine.UsedSubnets(ctx)
	if err != nil {
		return ns, err
	}
	if ns, err = PlanNetworks(used); err != nil {
		return ns, err
	}
	return ns, store.PutSetting(ctx, c.Store.DB(), networkSettingKey, ns)
}

// EnsureNetworks creates the stack networks if missing and refuses to use a
// same-named network that this stack does not own.
func (c *Controller) EnsureNetworks(ctx context.Context) (NetworkSettings, error) {
	ns, err := c.NetworkPlan(ctx)
	if err != nil {
		return ns, err
	}
	for _, n := range []struct {
		name     string
		subnet   string
		internal bool
		dynamic  bool
	}{
		{c.Names.AppsNetwork(), ns.AppsSubnet, false, true},
		{c.Names.DataNetwork(), ns.DataSubnet, true, false},
	} {
		existing, err := c.Engine.InspectNetwork(ctx, n.name)
		if err != nil {
			return ns, err
		}
		if existing != nil {
			if !c.Names.OwnedBy(existing.Labels, runtime.RoleNetwork, "") {
				return ns, Fail("foreign-network", "Remove or rename the conflicting network, or choose a different stack name.",
					"network %s exists but is not owned by this stack", n.name)
			}
			continue
		}
		subnet := netip.MustParsePrefix(n.subnet)
		spec := docker.NetworkSpec{Name: n.name, Internal: n.internal, Subnet: subnet, Labels: c.Names.Labels(runtime.RoleNetwork, nil)}
		if n.dynamic {
			a := subnet.Addr().As4()
			spec.IPRange = netip.PrefixFrom(netip.AddrFrom4([4]byte{a[0], a[1], a[2], 128}), 25)
		}
		if _, err := c.Engine.EnsureNetwork(ctx, spec); err != nil {
			return ns, err
		}
	}
	return ns, nil
}
