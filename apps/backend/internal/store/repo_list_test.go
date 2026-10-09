package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// seedApps inserts n apps, each with a MySQL binding (two databases), a
// SQLite binding and two hosts, plus one upstream host that must not leak
// into any app.
func seedApps(tb testing.TB, s *Store, n int) {
	tb.Helper()
	ctx := context.Background()
	db := s.DB()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := range n {
		id := fmt.Sprintf("app%03d", i)
		slug := fmt.Sprintf("slug-%03d", i)
		uid, err := AllocateUID(ctx, db, domain.UIDRange{First: 20000, Last: 29999}, nil, id, slug)
		if err != nil {
			tb.Fatal(err)
		}
		a := domain.App{
			ID: id, Slug: slug, UID: uid, GID: uid,
			DesiredRuntime: domain.DesiredRunning, Ingress: domain.IngressManaged, Publication: domain.Unpublished,
			ConfigGeneration: 1, CredentialsGeneration: 1, Provisioned: true, CreatedAt: created, UpdatedAt: created,
		}
		if err := InsertApp(ctx, db, a); err != nil {
			tb.Fatal(err)
		}
		mysql := domain.Binding{
			ID: id + "-b1", AppID: id, Engine: domain.EngineMySQL, Service: "mysql",
			Username: "u_" + id, Password: "p" + id, CreatedAt: created,
		}
		sqlite := domain.Binding{
			ID: id + "-b2", AppID: id, Engine: domain.EngineSQLite, SQLiteFileID: id + "-file",
			Vacuum: &domain.VacuumSlot{}, CreatedAt: created,
		}
		for _, b := range []domain.Binding{mysql, sqlite} {
			if err := InsertBinding(ctx, db, b); err != nil {
				tb.Fatal(err)
			}
		}
		for _, name := range []string{"db_b", "db_a"} {
			if err := AddBindingDatabase(ctx, db, mysql.ID, name+"_"+id); err != nil {
				tb.Fatal(err)
			}
		}
		for _, name := range []string{slug + ".example.com", "www." + slug + ".example.com"} {
			h := domain.Host{Name: name, Target: domain.HostTargetApp, AppID: id, Enabled: true, CreatedAt: created,
				Route: domain.Route{TLS: domain.TLSACME}}
			if err := InsertHost(ctx, db, h); err != nil {
				tb.Fatal(err)
			}
		}
	}
	proxy := domain.Host{Name: "proxy.example.com", Target: domain.HostTargetUpstream,
		Upstreams: []string{"http://10.0.0.5:80"}, Enabled: true, Route: domain.Route{TLS: domain.TLSNone}}
	if err := InsertHost(ctx, db, proxy); err != nil {
		tb.Fatal(err)
	}
}

// TestListAppsMatchesGetApp pins ListApps' batched relation loading to the
// per-app loading GetApp does.
func TestListAppsMatchesGetApp(t *testing.T) {
	s := newStore(t)
	seedApps(t, s, 5)
	ctx := t.Context()
	apps, err := ListApps(ctx, s.DB())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 5 {
		t.Fatalf("got %d apps", len(apps))
	}
	for _, a := range apps {
		one, err := GetApp(ctx, s.DB(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, one) {
			t.Fatalf("ListApps and GetApp disagree for %s:\n%+v\n%+v", a.ID, a, one)
		}
		if len(a.Bindings) != 2 || len(a.Bindings[0].Databases) != 2 || len(a.Hosts) != 2 ||
			a.Hosts[0].Name != a.Slug+".example.com" {
			t.Fatalf("unexpected relations for %s: %+v", a.ID, a)
		}
	}
}

func BenchmarkListApps(b *testing.B) {
	s, err := Create(filepath.Join(b.TempDir(), "bento.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	seedApps(b, s, 50)
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ListApps(ctx, s.DB()); err != nil {
			b.Fatal(err)
		}
	}
}

func TestHostsCRUD(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	db := s.DB()
	seedApps(t, s, 2)
	up := domain.Host{Name: "proxy.example.com", Target: domain.HostTargetUpstream,
		Upstreams: []string{"http://10.0.0.1:3000"}, Enabled: true, Route: domain.Route{TLS: domain.TLSNone}}
	if err := InsertHost(ctx, db, up); err == nil {
		t.Fatal("an existing host name must conflict, never be overwritten")
	}
	up.Name = "grafana2.example.com"
	if err := InsertHost(ctx, db, up); err != nil {
		t.Fatal(err)
	}
	got, err := GetHost(ctx, db, up.Name)
	if err != nil || got.Target != domain.HostTargetUpstream || len(got.Upstreams) != 1 || got.AppID != "" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// Moving a host to another app places it after that app's hosts.
	moved := domain.Host{Name: "slug-000.example.com", Target: domain.HostTargetApp, AppID: "app001", Enabled: true,
		Route: domain.Route{TLS: domain.TLSSelfSigned}}
	if err := UpdateHost(ctx, db, moved); err != nil {
		t.Fatal(err)
	}
	a0, _ := GetApp(ctx, db, "app000")
	a1, _ := GetApp(ctx, db, "app001")
	if len(a0.Hosts) != 1 || len(a1.Hosts) != 3 || a1.Hosts[2].Name != moved.Name ||
		a1.Hosts[2].Route.TLS != domain.TLSSelfSigned {
		t.Fatalf("move: %+v / %+v", a0.Hosts, a1.Hosts)
	}
	// A disabled host sorts after enabled ones, so it is never the display host.
	first := a1.Hosts[0]
	first.Enabled = false
	if err := UpdateHost(ctx, db, first); err != nil {
		t.Fatal(err)
	}
	a1, _ = GetApp(ctx, db, "app001")
	if h, ok := a1.DisplayHost(); !ok || h.Name == first.Name || a1.Hosts[2].Name != first.Name {
		t.Fatalf("display host: %+v", a1.Hosts)
	}
	if err := DeleteHost(ctx, db, "missing.example.com"); err != ErrNotFound {
		t.Fatalf("delete missing: %v", err)
	}
	// Deleting an app deletes the hosts that target it.
	if err := DeleteApp(ctx, db, "app001"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetHost(ctx, db, moved.Name); err != ErrNotFound {
		t.Fatalf("host of a deleted app survived: %v", err)
	}
	all, err := ListHosts(ctx, db)
	if err != nil || len(all) != 3 {
		t.Fatalf("hosts after delete: %+v %v", all, err)
	}
}
