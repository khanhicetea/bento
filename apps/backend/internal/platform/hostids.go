package platform

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// HostIDs reports numeric IDs already used by host users and groups so the
// allocator can skip collisions.
type HostIDs interface {
	Taken(id int) bool
}

// FileHostIDs reads the host passwd and group databases.
type FileHostIDs struct {
	PasswdPath string
	GroupPath  string
}

func (h FileHostIDs) Taken(id int) bool {
	for _, p := range []string{h.passwd(), h.group()} {
		if idInColonFile(p, id) {
			return true
		}
	}
	return false
}

func (h FileHostIDs) passwd() string {
	if h.PasswdPath != "" {
		return h.PasswdPath
	}
	return "/etc/passwd"
}

func (h FileHostIDs) group() string {
	if h.GroupPath != "" {
		return h.GroupPath
	}
	return "/etc/group"
}

func idInColonFile(path string, id int) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	want := strconv.Itoa(id)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) >= 3 && fields[2] == want {
			return true
		}
	}
	return false
}

// NoHostIDs never reports a collision (tests).
type NoHostIDs struct{}

func (NoHostIDs) Taken(int) bool { return false }
