package platform

import (
	"bufio"
	"errors"
	"io/fs"
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

// idInColonFile reports whether id appears in the third field of path. A
// missing file has no IDs; a file that cannot be read fully counts as a
// collision, so the allocator skips the ID instead of risking a clash.
func idInColonFile(path string, id int) bool {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
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
	return sc.Err() != nil
}

// NoHostIDs never reports a collision (tests).
type NoHostIDs struct{}

func (NoHostIDs) Taken(int) bool { return false }
