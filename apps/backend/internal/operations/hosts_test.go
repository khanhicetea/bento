package operations

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestHostAcceptValidatesTargets(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.createApp("shop")
	ext := h.createApp("ext")
	external := domain.IngressExternal
	h.mustSucceed(func() (store.Operation, error) {
		_, op, err := h.c.UpdateApp(ctx, ext.ID, UpdateAppInput{Ingress: &external}, "")
		return op, err
	}())
	for name, in := range map[string]HostInput{
		"unknown app":      {Name: "a.example.com", Target: domain.HostTargetApp, App: "nope"},
		"external ingress": {Name: "a.example.com", Target: domain.HostTargetApp, App: "ext"},
		"no app":           {Name: "a.example.com", Target: domain.HostTargetApp},
		"no upstream":      {Name: "a.example.com", Target: domain.HostTargetUpstream},
		"bad upstream":     {Name: "a.example.com", Target: domain.HostTargetUpstream, Upstreams: []string{"http://x;y"}},
		"self redirect":    {Name: "a.example.com", Target: domain.HostTargetRedirect, RedirectTo: "A.example.com"},
		"mixed target":     {Name: "a.example.com", Target: domain.HostTargetUpstream, App: "shop", Upstreams: []string{"http://10.0.0.1"}},
		"bad kind":         {Name: "a.example.com", Target: "proxy"},
		"bad name":         {Name: "*.example.com", Target: domain.HostTargetRedirect, RedirectTo: "b.example.com"},
		"redirect no tls":  {Name: "a.example.com", Target: domain.HostTargetRedirect, RedirectTo: "b.example.com", Route: domain.Route{RedirectHTTPS: true}},
	} {
		if _, _, err := h.c.CreateHost(ctx, in, ""); !errors.As(err, new(domain.ValidationErrors)) {
			t.Errorf("%s: expected a validation error, got %v", name, err)
		}
	}
	// An existing name is never silently moved by a create.
	_, _, err := h.c.CreateHost(ctx, HostInput{Name: "shop.example.com", Target: domain.HostTargetRedirect,
		RedirectTo: "b.example.com", Enabled: true}, "")
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	// Updating a missing host does not create it.
	if _, _, err := h.c.UpdateHost(ctx, HostInput{Name: "new.example.com", Target: domain.HostTargetRedirect,
		RedirectTo: "b.example.com"}, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestHostLifecycleRendersAndRemoves(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	h.mustSucceed(h.c.PublishApp(ctx, app.ID, ""))
	live := func(host string) bool {
		_, err := os.Stat(filepath.Join(h.c.Layout.EdgeConfDir(), "live", "sites", edge.RouteName(host)+".conf"))
		return err == nil
	}
	if !live("shop.example.com") {
		t.Fatal("the seeded app host must be live after publish")
	}
	_, op, err := h.c.CreateHost(ctx, HostInput{Name: "WWW.Shop.example.com", Target: domain.HostTargetRedirect,
		RedirectTo: "shop.example.com", Enabled: true, Route: domain.Route{TLS: domain.TLSSelfSigned}}, "")
	h.mustSucceed(op, err)
	if !live("www.shop.example.com") {
		t.Fatal("a created host is rendered under its normalized name")
	}
	// Re-pointing the app host to an upstream changes its route, not its name.
	_, op, err = h.c.UpdateHost(ctx, HostInput{Name: "shop.example.com", Target: domain.HostTargetUpstream,
		Upstreams: []string{"http://10.0.0.9:8080"}, Enabled: true}, "")
	h.mustSucceed(op, err)
	site, _ := os.ReadFile(filepath.Join(h.c.Layout.EdgeConfDir(), "live", "sites", "host-shop.example.com.conf"))
	if !strings.Contains(string(site), "server 10.0.0.9:8080;") {
		t.Fatalf("host not re-pointed:\n%s", site)
	}
	if got, _ := store.GetApp(ctx, h.store.DB(), app.ID); len(got.Hosts) != 0 {
		t.Fatalf("a re-pointed host must leave the app: %+v", got.Hosts)
	}
	for _, confirm := range []string{"", "delete", "delete WWW.shop.example.com", "delete www.shop.example.com "} {
		if _, err := h.c.DeleteHost(ctx, "www.shop.example.com", confirm, ""); !errors.Is(err, ErrConfirmation) {
			t.Fatalf("confirm %q: %v", confirm, err)
		}
	}
	h.mustSucceed(h.c.DeleteHost(ctx, "www.shop.example.com", "delete www.shop.example.com", ""))
	if live("www.shop.example.com") {
		t.Fatal("a deleted host must leave the edge")
	}
}

func TestPublishNeedsAnEnabledHost(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	_, op, err := h.c.UpdateHost(ctx, HostInput{Name: "shop.example.com", Target: domain.HostTargetApp, App: "shop"}, "")
	h.mustSucceed(op, err)
	op, err = h.c.PublishApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "no-domain" {
		t.Fatalf("publish without an enabled host: %s %s", got.State, got.ErrorCode)
	}
}
