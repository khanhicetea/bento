package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// makeV3DB builds a schema 3 database: apps own their domains (route_json,
// domains table) and reverse proxies are their own table.
func makeV3DB(t *testing.T, path string) {
	t.Helper()
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"DROP TABLE hosts",
		"ALTER TABLE apps DROP COLUMN access_log",
		`ALTER TABLE apps ADD COLUMN route_json TEXT NOT NULL DEFAULT '{"tls":"none"}'`,
		`CREATE TABLE proxies (
		  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, upstreams_json TEXT NOT NULL, route_json TEXT NOT NULL,
		  enabled INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL) STRICT`,
		`CREATE TABLE domains (
		  name TEXT PRIMARY KEY, owner_kind TEXT NOT NULL CHECK (owner_kind IN ('app','proxy')),
		  owner_id TEXT NOT NULL, is_primary INTEGER NOT NULL, created_at TEXT NOT NULL) STRICT`,
		"PRAGMA user_version = 3",
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// makeV2DB builds a schema 2 database: the v3 schema without apps.home_path.
func makeV2DB(t *testing.T, path string) {
	t.Helper()
	makeV3DB(t, path)
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("ALTER TABLE apps DROP COLUMN home_path"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
}

// seedV3 writes v3 rows: an app with a primary and a secondary domain and a
// proxy with one domain.
func seedV3(t *testing.T, path string, extra ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ts := "2026-01-02T03:04:05Z"
	for _, stmt := range append([]string{
		`INSERT INTO uid_ledger(uid, app_id, slug, state, allocated_at) VALUES(20000, 'a1', 'shop', 'active', '` + ts + `')`,
		`INSERT INTO apps(id, slug, uid, gid, runtime_json, resources_json, desired_runtime, ingress, publication,
		  route_json, redis_json, created_at, updated_at)
		  VALUES('a1', 'shop', 20000, 20000, '{"kind":"php-fpm"}', '{}', 'running', 'managed', 'published',
		  '{"tls":"acme","redirectHttps":true,"accessLog":true,"staticCache":true}', '{}', '` + ts + `', '` + ts + `')`,
		`INSERT INTO domains VALUES('www.shop.example.com', 'app', 'a1', 0, '` + ts + `')`,
		`INSERT INTO domains VALUES('shop.example.com', 'app', 'a1', 1, '` + ts + `')`,
		`INSERT INTO proxies VALUES('p1', 'grafana', '["http://10.0.0.5:3000"]', '{"tls":"self-signed"}', 0,
		  '` + ts + `', '` + ts + `')`,
		`INSERT INTO domains VALUES('grafana.example.com', 'proxy', 'p1', 1, '` + ts + `')`,
	}, extra...) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
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

func TestCheckCompatibleAcceptsTwoToFourOnly(t *testing.T) {
	dir := t.TempDir()
	for version, ok := range map[int]bool{1: false, 2: true, 3: true, 4: true, 5: false} {
		path := filepath.Join(dir, "v"+string(rune('0'+version))+".db")
		makeForeignDB(t, path, ApplicationID, version)
		if err := CheckCompatible(path); (err == nil) != ok {
			t.Errorf("version %d: err=%v, want ok=%v", version, err, ok)
		}
	}
}

func TestOpenMigratesV2ToCurrent(t *testing.T) {
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
	// Reopening a current database is a no-op.
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
	if _, err := s.db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db, 1); err == nil {
		t.Fatal("migration must run only from a supported version")
	}
}

func TestOpenMigratesDomainsAndProxiesToHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bento.db")
	makeV3DB(t, path)
	seedV3(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	app, err := GetApp(ctx, s.DB(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if !app.AccessLog {
		t.Fatal("the app keeps its access log setting")
	}
	if len(app.Hosts) != 2 || app.Hosts[0].Name != "shop.example.com" || app.Hosts[1].Name != "www.shop.example.com" {
		t.Fatalf("the former primary domain must stay the display host: %+v", app.Hosts)
	}
	want := domain.Route{TLS: domain.TLSACME, RedirectHTTPS: true, AccessLog: true, StaticCache: true}
	for _, h := range app.Hosts {
		if h.Target != domain.HostTargetApp || h.AppID != "a1" || !h.Enabled || h.Route != want {
			t.Fatalf("app host: %+v", h)
		}
	}
	g, err := GetHost(ctx, s.DB(), "grafana.example.com")
	if err != nil || g.Target != domain.HostTargetUpstream || g.Enabled || g.Route.TLS != domain.TLSSelfSigned ||
		len(g.Upstreams) != 1 || g.Upstreams[0] != "http://10.0.0.5:3000" {
		t.Fatalf("proxy host: %+v %v", g, err)
	}
	var n int
	if err := s.db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE name IN ('domains', 'proxies')",
	).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old tables left behind: %d %v", n, err)
	}
}

func TestMigrationRefusesOrphanDomains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bento.db")
	makeV3DB(t, path)
	seedV3(t, path, `INSERT INTO domains VALUES('lost.example.com', 'proxy', 'gone', 1, '2026-01-02T03:04:05Z')`)
	if _, err := Open(path); err == nil {
		t.Fatal("a domain without its owner must refuse the migration")
	}
	if v := userVersion(t, path); v != 3 {
		t.Fatalf("refused migration changed version to %d", v)
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
