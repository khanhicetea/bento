package platform

import (
	"os"
	"strconv"
	"strings"
)

// HostMemory returns total host memory in bytes from /proc/meminfo, or false
// when it cannot be read.
func HostMemory() (int64, bool) { return meminfo("MemTotal:") }

// HostMemAvailable returns the memory the kernel estimates is available for
// new work without swapping (MemAvailable) plus free swap, where idle pages
// can go, or false when it cannot be read.
func HostMemAvailable() (int64, bool) {
	avail, ok := meminfo("MemAvailable:")
	if !ok {
		return 0, false
	}
	swap, _ := meminfo("SwapFree:")
	return avail + swap, true
}

func meminfo(field string) (int64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == field {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return kb * 1024, true
		}
	}
	return 0, false
}
