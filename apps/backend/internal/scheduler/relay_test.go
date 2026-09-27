package scheduler

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

func TestTargetSocketValidation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	root := t.TempDir()
	m := NewRelayManager(platform.Layout{Root: root}, "/proc/self/exe", nil)
	app := domain.App{ID: "a1", Slug: "shop", UID: 12345, GID: 12345}
	dir := filepath.Join(root, "homes/shop/.local/share/minicron")
	os.MkdirAll(dir, 0o700)
	if _, err := m.TargetSocket(app); err == nil {
		t.Fatal("missing socket accepted")
	}
	sock := filepath.Join(dir, "minicron.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := m.TargetSocket(app); err == nil {
		t.Fatal("socket owned by another uid accepted")
	}
	os.Lchown(sock, 12345, 12345)
	if _, err := m.TargetSocket(app); err != nil {
		t.Fatal(err)
	}
	// A symlinked path component is refused.
	os.Rename(filepath.Join(root, "homes/shop/.local"), filepath.Join(root, "homes/shop/.real"))
	os.Symlink(filepath.Join(root, "homes/shop/.real"), filepath.Join(root, "homes/shop/.local"))
	if _, err := m.TargetSocket(app); err == nil {
		t.Fatal("symlinked path accepted")
	}
}
