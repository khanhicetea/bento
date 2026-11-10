package remoteaccess

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// keyFileVersion is the format of tailcat/<service>.key.
const keyFileVersion = 1

// ErrKeyMissing means an enabled service has no key file. Bento never
// replaces a lost key on its own, because that would silently move the
// address; a rotate creates a new one.
var ErrKeyMissing = errors.New("the Tailcat key of this service is missing; rotate the address to create a new one")

// Key is a service's Tailcat server identity. Private and PresharedKey are
// secrets: together with the region they determine the address, and the
// address is what lets a client reach the engine port. It is stored only in a
// root-only 0600 file and handed to the child on stdin.
type Key struct {
	Version    int    `json:"version"`
	Service    string `json:"service"`
	Generation int64  `json:"generation"`
	// Private is the WireGuard node key ("privkey:…").
	Private key.NodePrivate `json:"private"`
	// PresharedKey is the WireGuard pre-shared key embedded in the address.
	PresharedKey tailcat.PresharedKey `json:"presharedKey"`
	// RegionID is the DERP region the server listens on. 0 until the first
	// child picks the nearest region; it is then recorded so the address
	// stays the same across restarts.
	RegionID int `json:"regionId"`
}

// NewKey generates a fresh identity for one generation of a service.
func NewKey(service string, generation int64) Key {
	return Key{
		Version:      keyFileVersion,
		Service:      service,
		Generation:   generation,
		Private:      key.NewNode(),
		PresharedKey: tailcat.NewPresharedKey(),
	}
}

// Address is the short Tailcat address of a server with this key in its
// recorded region: the region is referenced by ID (clients fetch the DERP
// map), so the address does not change when a relay's nodes do.
func (k Key) Address() (tailcat.Addr, error) {
	if k.Private.IsZero() || k.PresharedKey.IsZero() || k.RegionID <= 0 {
		return "", errors.New("incomplete Tailcat key")
	}
	ci := tailcat.ConnInfo{
		ServerPublic:      tailcat.NodePublic{NodePublic: k.Private.Public()},
		ServerDiscoPublic: tailcat.DiscoPublicForNode(k.Private),
		PresharedKey:      k.PresharedKey,
		RegionID:          tailcfgRegion(k.RegionID),
	}
	return ci.Addr(), nil
}

func (k Key) valid(service string) error {
	switch {
	case k.Version != keyFileVersion:
		return fmt.Errorf("unsupported Tailcat key file version %d", k.Version)
	case k.Service != service:
		return fmt.Errorf("the Tailcat key file belongs to service %q", k.Service)
	case k.Private.IsZero() || k.PresharedKey.IsZero():
		return errors.New("the Tailcat key file is incomplete")
	case k.RegionID < 0:
		return errors.New("the Tailcat key file names an invalid region")
	}
	return nil
}

// ReadKey loads a service's key file. A missing file is ErrKeyMissing; a
// file that is not a regular root-owned 0600 file, or that does not parse,
// is refused rather than replaced.
func ReadKey(layout platform.Layout, service string) (Key, error) {
	path := layout.RemoteKey(service)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Key{}, ErrKeyMissing
	}
	if err != nil {
		return Key{}, err
	}
	if !info.Mode().IsRegular() {
		return Key{}, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Key{}, fmt.Errorf("%s is readable by other users; refusing to use it", path)
	}
	if owner, _, err := platform.StatOwner(path); err != nil || owner != platform.RootOwner {
		return Key{}, fmt.Errorf("%s is not owned by root", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Key{}, err
	}
	var k Key
	if err := json.Unmarshal(b, &k); err != nil {
		// The error text of encoding/json can quote input; never echo it.
		return Key{}, fmt.Errorf("%s does not parse", path)
	}
	if err := k.valid(service); err != nil {
		return Key{}, err
	}
	return k, nil
}

// WriteKey replaces a service's key file atomically (root, 0600) inside the
// root-only key directory.
func WriteKey(layout platform.Layout, k Key) error {
	if err := platform.EnsureDir(layout.RemoteKeyDir(), 0o700, platform.RootOwner); err != nil {
		return err
	}
	b, err := json.Marshal(k)
	if err != nil {
		return err
	}
	return platform.AtomicWrite(layout.RemoteKey(k.Service), b, 0o600, platform.RootOwner)
}

// RemoveKey deletes a service's key file; a missing one is not an error.
func RemoveKey(layout platform.Layout, service string) error {
	err := os.Remove(layout.RemoteKey(service))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// EnsureKey returns the key for generation, creating it when the file is
// missing (create is true: an explicit enable) or holds an older generation
// (a persisted rotate). A key of a newer generation than asked is refused.
func EnsureKey(layout platform.Layout, service string, generation int64, create bool) (Key, bool, error) {
	k, err := ReadKey(layout, service)
	switch {
	case errors.Is(err, ErrKeyMissing) && !create:
		return Key{}, false, err
	case errors.Is(err, ErrKeyMissing):
	case err != nil:
		return Key{}, false, err
	case k.Generation == generation:
		return k, false, nil
	case k.Generation > generation:
		return Key{}, false, fmt.Errorf("the Tailcat key file is of generation %d, newer than this service's %d",
			k.Generation, generation)
	}
	k = NewKey(service, generation)
	if err := WriteKey(layout, k); err != nil {
		return Key{}, false, err
	}
	return k, true, nil
}

// LocalPort is the port suggested on the operator's machine for an engine
// port: 15432 for PostgreSQL, 13306 for MySQL, so a local server keeps its
// own.
func LocalPort(enginePort int) int { return 10000 + enginePort }

// ForwardCommand is the client command that maps LocalPort on 127.0.0.1 to
// the engine port through the tunnel.
func ForwardCommand(address string, enginePort int) string {
	return fmt.Sprintf("tailcat forward %s %d:%d", address, LocalPort(enginePort), enginePort)
}
