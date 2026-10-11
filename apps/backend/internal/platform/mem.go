package platform

import (
	"os"
	"strconv"
	"strings"
)

// HostMemory returns total host memory in bytes from /proc/meminfo, or false
// when it cannot be read.
func HostMemory() (int64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return kb * 1024, true
		}
	}
	return 0, false
}
