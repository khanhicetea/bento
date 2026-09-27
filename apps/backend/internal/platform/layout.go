// Package platform owns host-side effects: stack-root layout, symlink-safe
// filesystem helpers, locks, identifiers, and clock access.
package platform

import (
	"fmt"
	"path/filepath"
)

// Layout resolves every path under one external stack root. Runtime state is
// never written beside the source tree or the compiled binary.
type Layout struct {
	Root string
}

func NewLayout(root string) (Layout, error) {
	if root == "" {
		return Layout{}, fmt.Errorf("stack root is required (--stack or BENTO_STACK_ROOT)")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, err
	}
	return Layout{Root: filepath.Clean(abs)}, nil
}

func (l Layout) Database() string       { return filepath.Join(l.Root, "bento.db") }
func (l Layout) RunDir() string         { return filepath.Join(l.Root, "run") }
func (l Layout) ControlSocket() string  { return filepath.Join(l.Root, "run", "bento.sock") }
func (l Layout) RelayDir() string       { return filepath.Join(l.Root, "run", "relay") }
func (l Layout) LockDir() string        { return filepath.Join(l.Root, "locks") }
func (l Layout) ControllerLock() string { return filepath.Join(l.Root, "locks", "controller.lock") }
func (l Layout) BackupLock() string     { return filepath.Join(l.Root, "locks", "backup.lock") }
func (l Layout) SecretsDir() string     { return filepath.Join(l.Root, "secrets") }
func (l Layout) HomesDir() string       { return filepath.Join(l.Root, "homes") }
func (l Layout) SQLiteDir() string      { return filepath.Join(l.Root, "sqlite") }
func (l Layout) AppsDir() string        { return filepath.Join(l.Root, "apps") }
func (l Layout) ServicesDir() string    { return filepath.Join(l.Root, "services") }
func (l Layout) EdgeDir() string        { return filepath.Join(l.Root, "edge") }
func (l Layout) EdgeConfDir() string    { return filepath.Join(l.Root, "edge", "conf") }
func (l Layout) EdgeCertsDir() string   { return filepath.Join(l.Root, "edge", "certs") }
func (l Layout) EdgeACMEDir() string    { return filepath.Join(l.Root, "edge", "acme") }
func (l Layout) EdgeCustomDir() string  { return filepath.Join(l.Root, "edge", "custom") }
func (l Layout) TunnelDir() string      { return filepath.Join(l.Root, "cloudflared") }
func (l Layout) BackupsDir() string     { return filepath.Join(l.Root, "backups") }
func (l Layout) RcloneDir() string      { return filepath.Join(l.Root, "rclone") }
func (l Layout) CacheDir() string       { return filepath.Join(l.Root, "cache") }
func (l Layout) StagingDir() string     { return filepath.Join(l.Root, "staging") }

func (l Layout) Secret(name string) string  { return filepath.Join(l.Root, "secrets", name) }
func (l Layout) AppHome(slug string) string { return filepath.Join(l.Root, "homes", slug) }
func (l Layout) AppDir(appID string) string { return filepath.Join(l.Root, "apps", appID) }
func (l Layout) SQLiteFileDir(id string) string {
	return filepath.Join(l.Root, "sqlite", id)
}

// AppConfigDir is mounted read-only at /etc/bento. The directory (not single
// files) is mounted so atomic renames inside it are visible to the container.
func (l Layout) AppConfigDir(appID string) string {
	return filepath.Join(l.Root, "apps", appID, "config")
}

// AppIdentityDir holds boot-static passwd/group files mounted as single files.
func (l Layout) AppIdentityDir(appID string) string {
	return filepath.Join(l.Root, "apps", appID, "identity")
}

func (l Layout) RelaySocket(appID string) string {
	return filepath.Join(l.Root, "run", "relay", appID+".sock")
}

// HomeSidecar records stack/app identity for consistency checks. It is not an
// unforgeable credential.
func (l Layout) HomeSidecar(slug string) string {
	return filepath.Join(l.Root, "homes", slug, ".bento-identity.json")
}

// SkeletonDirs are created by init with the listed modes (owned by root).
func (l Layout) SkeletonDirs() map[string]uint32 {
	return map[string]uint32{
		l.Root:            0o711,
		l.RunDir():        0o700,
		l.RelayDir():      0o700,
		l.LockDir():       0o700,
		l.SecretsDir():    0o700,
		l.HomesDir():      0o711,
		l.SQLiteDir():     0o711,
		l.AppsDir():       0o711,
		l.ServicesDir():   0o700,
		l.EdgeDir():       0o700,
		l.EdgeConfDir():   0o755,
		l.EdgeCertsDir():  0o700,
		l.EdgeACMEDir():   0o700,
		l.EdgeCustomDir(): 0o755,
		l.TunnelDir():     0o700,
		l.BackupsDir():    0o700,
		l.RcloneDir():     0o700,
		l.CacheDir():      0o700,
		l.StagingDir():    0o700,
	}
}
