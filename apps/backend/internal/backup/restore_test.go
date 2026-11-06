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

// A restore replaces whatever the app left at the database's name (a link
// to a file elsewhere stays untouched), drops stale journals, and refuses a
// binding directory that is itself a link.
func TestRestoreSQLiteNeverFollowsLinks(t *testing.T) {
	root := t.TempDir()
	layout, err := platform.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	b := domain.Binding{SQLiteFileID: "f1"}
	dir := layout.SQLiteFileDir(b.SQLiteFileID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "shop.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shop.db-wal"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(root, "f1.db")
	if err := os.WriteFile(art, append([]byte("SQLite format 3\x00"), bytes.Repeat([]byte{0}, 84)...), 0o600); err != nil {
		t.Fatal(err)
	}
	app := domain.App{Slug: "shop", UID: os.Getuid(), GID: os.Getgid()}
	if err := (Deps{Layout: layout}).RestoreSQLite(app, b, art); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "keep me" {
		t.Fatal("the restore wrote through the app's link")
	}
	info, err := os.Lstat(filepath.Join(dir, "shop.db"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("restored database is not a regular file: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "shop.db-wal")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a stale journal survived the restore")
	}
	// The binding directory swapped for a link is refused.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if err := (Deps{Layout: layout}).RestoreSQLite(app, b, art); err == nil {
		t.Fatal("a binding directory that is a link was written into")
	}
}
