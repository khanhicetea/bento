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
// SQLite binding and two domains, plus one proxy domain that must not leak
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
		links := []domain.DomainLink{{Name: slug + ".example.com", Primary: true}, {Name: "www." + slug + ".example.com"}}
		if err := ReplaceDomains(ctx, db, "app", id, links); err != nil {
			tb.Fatal(err)
		}
	}
	if err := ReplaceDomains(ctx, db, "proxy", "app000", []domain.DomainLink{{Name: "proxy.example.com", Primary: true}}); err != nil {
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
		if len(a.Bindings) != 2 || len(a.Bindings[0].Databases) != 2 || len(a.Domains) != 2 || !a.Domains[0].Primary {
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

func TestListProxiesMatchesGetProxy(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	db := s.DB()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, name := range []string{"beta", "alpha", "gamma"} {
		p := domain.Proxy{ID: fmt.Sprintf("px%d", i), Name: name, Upstreams: []string{"10.0.0.1:80"}, Enabled: true, CreatedAt: created}
		if err := UpsertProxy(ctx, db, p); err != nil {
			t.Fatal(err)
		}
		if name == "gamma" {
			continue // a proxy without domains keeps nil Domains
		}
		links := []domain.DomainLink{{Name: "z." + name + ".example.com"}, {Name: name + ".example.com", Primary: true}}
		if err := ReplaceDomains(ctx, db, "proxy", p.ID, links); err != nil {
			t.Fatal(err)
		}
	}
	// An app domain whose owner id collides with a proxy id must not leak.
	if err := ReplaceDomains(ctx, db, "app", "px0", []domain.DomainLink{{Name: "app.example.com", Primary: true}}); err != nil {
		t.Fatal(err)
	}
	proxies, err := ListProxies(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 3 {
		t.Fatalf("got %d proxies", len(proxies))
	}
	for _, p := range proxies {
		one, err := GetProxy(ctx, db, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p, one) {
			t.Fatalf("ListProxies and GetProxy disagree for %s:\n%+v\n%+v", p.Name, p, one)
		}
	}
	if len(proxies[0].Domains) != 2 || !proxies[0].Domains[0].Primary || proxies[2].Domains != nil {
		t.Fatalf("unexpected domains: %+v", proxies)
	}
}
