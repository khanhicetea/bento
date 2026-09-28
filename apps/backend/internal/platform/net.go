package platform

import (
	"net"
	"net/netip"
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
