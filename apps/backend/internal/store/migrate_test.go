package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// makeV2DB builds a schema 2 database: the v3 schema without apps.home_path.
func makeV2DB(t *testing.T, path string) {
	t.Helper()
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE apps DROP COLUMN home_path"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func userVersion(t *testing.T, path string) int64 {
	t.Helper()
	_, v, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckCompatibleAcceptsTwoAndThreeOnly(t *testing.T) {
	dir := t.TempDir()
	for version, ok := range map[int]bool{1: false, 2: true, 3: true, 4: false} {
		path := filepath.Join(dir, "v"+string(rune('0'+version))+".db")
		makeForeignDB(t, path, ApplicationID, version)
		if err := CheckCompatible(path); (err == nil) != ok {
			t.Errorf("version %d: err=%v, want ok=%v", version, err, ok)
		}
	}
}

func TestOpenMigratesV2ToV3(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bento.db")
	makeV2DB(t, path)
	if err := CheckCompatible(path); err != nil {
		t.Fatal(err)
	}
	if v := userVersion(t, path); v != 2 {
		t.Fatalf("CheckCompatible must not migrate; version %d", v)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int64
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("version after open %d %v", v, err)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM pragma_table_info('apps') WHERE name='home_path'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("home_path column missing: %d %v", n, err)
	}
	// Reopening a v3 database is a no-op.
	s.Close()
	if v := userVersion(t, path); v != SchemaVersion {
		t.Fatalf("version on disk %d", v)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Pre-migration apps read back with the default home.
	err = s.Tx(ctx, func(q Q) error {
		uid, err := AllocateUID(ctx, q, domain.UIDRange{First: 20000, Last: 29999}, nil, "a1", "shop")
		if err != nil {
			return err
		}
		return InsertApp(ctx, q, domain.App{ID: "a1", Slug: "shop", UID: uid, GID: uid, DesiredRuntime: domain.DesiredStopped,
			Ingress: domain.IngressNone, Publication: domain.Unpublished, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err := GetApp(ctx, s.DB(), "a1")
	if err != nil || app.HomePath != "" || app.ContainerHome() != "/home/shop" {
		t.Fatalf("app %+v %v", app, err)
	}
}

func TestFailedMigrationLeavesV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bento.db")
	makeV2DB(t, path)
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The column already exists, so the ALTER fails and the version must stay 2.
	if _, err := db.Exec("ALTER TABLE apps ADD COLUMN home_path TEXT"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db, 2); err == nil {
		t.Fatal("expected migration failure")
	}
	if v := userVersion(t, path); v != 2 {
		t.Fatalf("failed migration changed version to %d", v)
	}
}

func TestMigrateRefusesOtherVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bento.db")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := migrate(s.db, 2); err == nil {
		t.Fatal("migration must run only from v2")
	}
}

func TestHomePathRoundTripAndValidation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	insert := func(id, slug, home string) error {
		return s.Tx(ctx, func(q Q) error {
			uid, err := AllocateUID(ctx, q, domain.UIDRange{First: 20000, Last: 29999}, nil, id, slug)
			if err != nil {
				return err
			}
			return InsertApp(ctx, q, domain.App{ID: id, Slug: slug, UID: uid, GID: uid, HomePath: home,
				DesiredRuntime: domain.DesiredStopped, Ingress: domain.IngressNone, Publication: domain.Unpublished,
				CreatedAt: time.Now(), UpdatedAt: time.Now()})
		})
	}
	if err := insert("a1", "shop-copy", "/home/shop"); err != nil {
		t.Fatal(err)
	}
	app, err := GetApp(ctx, s.DB(), "a1")
	if err != nil || app.HomePath != "/home/shop" || app.ContainerHome() != "/home/shop" {
		t.Fatalf("app %+v %v", app, err)
	}
	// An update never rewrites the home path.
	app.HomePath = "/home/other"
	if err := s.Tx(ctx, func(q Q) error { return UpdateApp(ctx, q, app) }); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetApp(ctx, s.DB(), "a1"); got.HomePath != "/home/shop" {
		t.Fatalf("home path edited: %q", got.HomePath)
	}
	for _, bad := range []string{"/home/", "/home/../etc", "/etc/shop", "/home/Shop", "/home/shop/app", "home/shop", "/home/a--b"} {
		if err := insert("b"+bad, "other", bad); err == nil {
			t.Errorf("home path %q accepted", bad)
		}
	}
}
