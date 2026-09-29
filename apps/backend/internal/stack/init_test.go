package stack

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func treeHash(t *testing.T, root string) [32]byte {
	h := sha256.New()
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		h.Write([]byte(p + info.Mode().String()))
		if info.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			h.Write(b)
		}
		return nil
	})
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func TestInitRefusesForeignAndNonEmptyWithoutChanges(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	foreign := t.TempDir()
	os.WriteFile(filepath.Join(foreign, "state.db"), []byte("other"), 0o600)
	os.WriteFile(filepath.Join(foreign, ".env"), []byte("KEY=value\n"), 0o600)
	before := treeHash(t, foreign)
	if _, err := Init(t.Context(), InitOptions{Root: foreign, Name: "prod"}); err == nil {
		t.Fatal("foreign root accepted")
	}
	if treeHash(t, foreign) != before {
		t.Fatal("refusal modified the foreign root")
	}
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "notes.txt"), []byte("x"), 0o600)
	if _, err := Init(t.Context(), InitOptions{Root: other, Name: "prod"}); err == nil {
		t.Fatal("non-empty root accepted")
	}
}

func TestInitCreatesVersionedPrivateStack(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	root := filepath.Join(t.TempDir(), "stack")
	id, err := Init(t.Context(), InitOptions{Root: root, Name: "prod", MySQL: "8.4", Password: "a sufficiently long password"})
	if err != nil {
		t.Fatal(err)
	}
	if id.ID == "" || id.Name != "prod" {
		t.Fatal(id)
	}
	l := platform.Layout{Root: root}
	if err := store.CheckCompatible(l.Database()); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{l.SecretsDir(), l.RunDir(), l.BackupsDir()} {
		info, _ := os.Stat(d)
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %v", d, info.Mode().Perm())
		}
	}
	s, _ := store.Open(l.Database())
	defer s.Close()
	ops, _ := store.ListOperations(t.Context(), s.DB(), store.OpFilter{})
	if len(ops) != 2 {
		t.Fatalf("expected explicit init operations for redis and mysql, got %d", len(ops))
	}
	if _, err := Init(t.Context(), InitOptions{Root: root, Name: "prod"}); err == nil {
		t.Fatal("re-init of an existing stack accepted")
	}
}
