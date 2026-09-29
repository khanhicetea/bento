package backup

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// A corrupt artifact that fails mid-stream must not replace the database.
func TestRestoreSQLiteRefusesTruncatedArtifact(t *testing.T) {
	root := t.TempDir()
	layout, err := platform.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	b := domain.Binding{SQLiteFileID: "f1"}
	if err := os.MkdirAll(layout.SQLiteFileDir(b.SQLiteFileID), 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	payload := append([]byte("SQLite format 3\x00"), bytes.Repeat([]byte("page"), 64<<10)...)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(root, "sqlite-f1.db.gz")
	// Keep the header and part of the stream, drop the rest and the trailer.
	if err := os.WriteFile(art, buf.Bytes()[:buf.Len()/2], 0o600); err != nil {
		t.Fatal(err)
	}
	app := domain.App{Slug: "shop", UID: os.Getuid(), GID: os.Getgid()}
	if err := (Deps{Layout: layout}).RestoreSQLite(app, b, art); err == nil {
		t.Fatal("truncated artifact restored without error")
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
