package platform

import (
	"net"
	"net/netip"
	"os"
	"strings"
)

// HostAddrIn returns this host's interface address inside prefix, or "" when
// no interface has one (for example before a Docker bridge exists).
func HostAddrIn(prefix netip.Prefix) string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(n.IP); ok && prefix.Contains(ip.Unmap()) {
			return ip.Unmap().String()
		}
	}
	return ""
}

// HostIPv6 reports whether the host kernel has IPv6 enabled, which Docker
// needs for a dual-stack bridge. A host booted with ipv6.disable=1 has no
// /proc/sys/net/ipv6 at all.
func HostIPv6() bool {
	b, err := os.ReadFile("/proc/sys/net/ipv6/conf/all/disable_ipv6")
	return err == nil && strings.TrimSpace(string(b)) == "0"
}
