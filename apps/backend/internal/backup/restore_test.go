package backup

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// A corrupt artifact that fails mid-stream must not replace the database.
func TestRestoreSQLiteRefusesNonSQLiteFile(t *testing.T) {
	root := t.TempDir()
	layout, err := platform.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	b := domain.Binding{SQLiteFileID: "f1"}
	if err := os.MkdirAll(layout.SQLiteFileDir(b.SQLiteFileID), 0o700); err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(root, "f1.db")
	if err := os.WriteFile(art, bytes.Repeat([]byte("not sqlite"), 64), 0o600); err != nil {
		t.Fatal(err)
	}
	app := domain.App{Slug: "shop", UID: os.Getuid(), GID: os.Getgid()}
	if err := (Deps{Layout: layout}).RestoreSQLite(app, b, art); err == nil {
		t.Fatal("a file that is not SQLite was restored")
	}
	target := filepath.Join(layout.SQLiteFileDir(b.SQLiteFileID), "shop.db")
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target exists after failed restore: %v", err)
	}
	entries, _ := os.ReadDir(layout.SQLiteFileDir(b.SQLiteFileID))
	if len(entries) != 0 {
		t.Fatalf("leftover files: %v", entries)
	}
}
