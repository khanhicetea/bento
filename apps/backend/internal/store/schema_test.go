package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestCheckCompatibleAcceptsOnlyCurrentSchema(t *testing.T) {
	dir := t.TempDir()
	for _, version := range []int{0, SchemaVersion, SchemaVersion + 1} {
		path := filepath.Join(dir, "v"+strconv.Itoa(version)+".db")
		makeForeignDB(t, path, ApplicationID, version)
		if err := CheckCompatible(path); (err == nil) != (version == SchemaVersion) {
			t.Errorf("version %d: err=%v", version, err)
		}
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

func TestServiceSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.db.Exec(`INSERT INTO data_services(name, engine, version, image, volume, initialized, created_at)
	  VALUES('mysql84', 'mysql', '8.4', 'mysql:8.4', 'v-mysql84', 1, '2026-01-02T03:04:05Z')`); err != nil {
		t.Fatal(err)
	}
	svc, err := GetService(ctx, s.DB(), "mysql84")
	if err != nil || !svc.Initialized || len(svc.Settings) != 0 {
		t.Fatalf("service %+v %v", svc, err)
	}
	want := domain.ServiceSettings{"innodb_buffer_pool_size": "1G"}
	if err := SetServiceSettings(ctx, s.DB(), "mysql84", want); err != nil {
		t.Fatal(err)
	}
	if svc, err = GetService(ctx, s.DB(), "mysql84"); err != nil || svc.Settings["innodb_buffer_pool_size"] != "1G" {
		t.Fatalf("settings %+v %v", svc.Settings, err)
	}
	if err := SetServiceSettings(ctx, s.DB(), "nope", want); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown service: %v", err)
	}
}

func TestAPITokens(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	at := time.Now().UTC()
	tok := APIToken{ID: "tok_1", Name: "ci", Scope: "read", TokenHash: "h1", CreatedAt: at}
	if err := InsertAPIToken(ctx, s.DB(), tok); err != nil {
		t.Fatal(err)
	}
	if err := InsertAPIToken(ctx, s.DB(), APIToken{ID: "tok_2", Name: "ci", Scope: "read", TokenHash: "h2",
		CreatedAt: at}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := InsertAPIToken(ctx, s.DB(), APIToken{ID: "tok_3", Name: "x", Scope: "root", TokenHash: "h3",
		CreatedAt: at}); err == nil {
		t.Fatal("unknown scope stored")
	}
	got, err := GetLiveAPIToken(ctx, s.DB(), "h1", at)
	if err != nil || got.Name != "ci" || !got.ExpiresAt.IsZero() {
		t.Fatalf("get %+v %v", got, err)
	}
	if err := DeleteAPIToken(ctx, s.DB(), "ci"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetLiveAPIToken(ctx, s.DB(), "h1", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted token: %v", err)
	}
}
