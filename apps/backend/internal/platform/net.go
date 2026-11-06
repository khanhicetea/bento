package platform

import (
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
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

// DiskSpace reports the bytes available and the size of dir's filesystem.
func DiskSpace(dir string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}
