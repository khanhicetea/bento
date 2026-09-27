package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishRefusesEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".partial-x")
	os.WriteFile(p, nil, 0o600)
	if _, err := publish(p, filepath.Join(dir, "final.sql")); err == nil {
		t.Fatal("empty artifact must not be published")
	}
	if _, err := os.Stat(filepath.Join(dir, "final.sql")); err == nil {
		t.Fatal("final artifact exists")
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("partial not cleaned")
	}
}

func TestRetentionKeepsNewestPerDatabase(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "shop"), 0o700)
	for _, n := range []string{
		"mysql-shop-20250101T000000Z.sql.zst", "mysql-shop-20250102T000000Z.sql.zst", "mysql-shop-20250103T000000Z.sql.zst",
		"mysql-other-20250101T000000Z.sql.zst", ".partial-abc",
	} {
		os.WriteFile(filepath.Join(dir, "shop", n), []byte("x"), 0o600)
	}
	removed, err := Retain(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "shop/mysql-shop-20250101T000000Z.sql.zst" {
		t.Fatalf("removed %v", removed)
	}
}

func TestResolveArtifactContainment(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "shop"), 0o700)
	os.WriteFile(filepath.Join(dir, "shop", "mysql-shop-20250101T000000Z.sql"), []byte("x"), 0o600)
	os.Symlink("/etc", filepath.Join(dir, "evil"))
	if _, err := ResolveArtifact(dir, "shop/mysql-shop-20250101T000000Z.sql"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "evil/passwd", "shop/.partial", "shop"} {
		if _, err := ResolveArtifact(dir, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
