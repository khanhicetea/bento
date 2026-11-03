package backup

import (
	"errors"
	"io/fs"
	"os"
	"sync"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

func TestEnsureStackKeyCreatesOnceAndKeeps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: the key is chowned to root")
	}
	l, err := platform.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStackKey(l); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no key yet: %v", err)
	}
	var wg sync.WaitGroup
	keys := make([]string, 8)
	created := make([]bool, 8)
	for i := range keys {
		wg.Go(func() {
			k, c, err := EnsureStackKey(l)
			if err != nil {
				t.Error(err)
			}
			keys[i], created[i] = k, c
		})
	}
	wg.Wait()
	n := 0
	for i, k := range keys {
		if k == "" || k != keys[0] {
			t.Fatalf("callers disagree on the key: %q", keys)
		}
		if created[i] {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d callers created the key, want 1", n)
	}
	info, err := os.Lstat(l.StackBackupKey())
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("key file mode: %v %v", info, err)
	}
	entries, _ := os.ReadDir(l.ResticKeyDir())
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
	if k, c, err := EnsureStackKey(l); err != nil || c || k != keys[0] {
		t.Fatalf("second call: %q %v %v", k, c, err)
	}
}

func TestReadStackKeyRefusesLinksAndBadContent(t *testing.T) {
	l, err := platform.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.ResticKeyDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	other := l.ResticKeyDir() + "/other"
	if err := os.WriteFile(other, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, l.StackBackupKey()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStackKey(l); err == nil {
		t.Fatal("a symlinked key must be refused")
	}
	for content, ok := range map[string]bool{"pw\n": true, "": false, "\n": false, "a\nb": false} {
		_ = os.Remove(l.StackBackupKey())
		if err := os.WriteFile(l.StackBackupKey(), []byte(content), 0o400); err != nil {
			t.Fatal(err)
		}
		k, err := ReadStackKey(l)
		if (err == nil) != ok || (ok && k != "pw") {
			t.Fatalf("%q: key %q err %v", content, k, err)
		}
	}
}
