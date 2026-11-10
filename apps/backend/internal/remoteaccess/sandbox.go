package remoteaccess

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlockNetABI is the first Landlock ABI that can restrict TCP connect
// (Linux 6.7).
const landlockNetABI = 4

// landlockRuleNetPort is LANDLOCK_RULE_NET_PORT (not in x/sys yet).
const landlockRuleNetPort = 2

// landlockNetPortAttr is struct landlock_net_port_attr.
type landlockNetPortAttr struct {
	AllowedAccess uint64
	Port          uint64
}

// restrictTCPConnect limits every thread of the process to outbound TCP on
// the given ports, using Landlock. The child runs in the host network
// namespace and could otherwise open TCP to the data network; this keeps it
// to the DERP relays. UDP (WireGuard, STUN) and Unix sockets are not
// affected. It reports false, without error, when the kernel has no Landlock
// network support or the process cannot apply it to all threads (cgo builds);
// no_new_privs must already be set.
func restrictTCPConnect(ports []uint16) (bool, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 || int(abi) < landlockNetABI {
		return false, nil
	}
	attr := unix.LandlockRulesetAttr{Access_net: unix.LANDLOCK_ACCESS_NET_CONNECT_TCP}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return false, errno
	}
	defer unix.Close(int(fd))
	seen := map[uint16]bool{}
	for _, p := range ports {
		if p == 0 || seen[p] {
			continue
		}
		seen[p] = true
		rule := landlockNetPortAttr{AllowedAccess: unix.LANDLOCK_ACCESS_NET_CONNECT_TCP, Port: uint64(p)}
		if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, landlockRuleNetPort,
			uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
			return false, errno
		}
	}
	// landlock_restrict_self applies to the calling thread only; the Go
	// runtime has many, so apply it to all of them at once.
	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		if errors.Is(errno, syscall.ENOTSUP) {
			return false, nil
		}
		return false, errno
	}
	return true, nil
}

// setNoNewPrivsAllThreads sets no_new_privs on every thread: prctl applies to
// the calling thread only, the Go runtime already runs several, and Landlock
// refuses to restrict a thread without it. cgo builds cannot reach all
// threads and fall back to the calling one.
func setNoNewPrivsAllThreads() error {
	_, _, errno := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0)
	if errno == 0 {
		return nil
	}
	if !errors.Is(errno, syscall.ENOTSUP) {
		return errno
	}
	return unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
}
