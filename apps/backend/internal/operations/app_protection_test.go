package operations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// New apps filter hostile requests by default; a create can turn a group off,
// and an update changes only the groups it names.
func TestAppProtectionDefaultsAndPatch(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	app := h.createApp("shop")
	if app.Protection != domain.DefaultProtection() {
		t.Fatalf("default protection %+v", app.Protection)
	}

	off := false
	wp, op, err := h.c.CreateApp(ctx, CreateAppInput{
		Slug: "blog", Protection: &domain.ProtectionPatch{BlockPHPProbes: &off},
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{
			Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}},
		Bindings: []BindingRequest{{Engine: domain.EngineSQLite}},
	}, "")
	h.mustSucceed(op, err)
	if want := (domain.Protection{BlockDotFiles: true, BlockPHPProbes: false, BlockSensitiveFiles: true}); wp.Protection != want {
		t.Fatalf("create with a patch: %+v", wp.Protection)
	}

	before, err := store.GetApp(ctx, h.store.DB(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, op, err = h.c.UpdateApp(ctx, app.ID, UpdateAppInput{Protection: &domain.ProtectionPatch{BlockDotFiles: &off}}, "")
	h.mustSucceed(op, err)
	got, err := store.GetApp(ctx, h.store.DB(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := (domain.Protection{BlockDotFiles: false, BlockPHPProbes: true, BlockSensitiveFiles: true}); got.Protection != want {
		t.Fatalf("a partial patch must change only its own field: %+v", got.Protection)
	}
	if got.ConfigGeneration != before.ConfigGeneration+1 {
		t.Fatalf("generation %d -> %d", before.ConfigGeneration, got.ConfigGeneration)
	}

	// Backups record the setting.
	if sp := h.c.appSpec(ctx, got, nil); sp.Protection == nil || *sp.Protection != got.Protection {
		t.Fatalf("app.json protection %+v", sp.Protection)
	}

	// An update that does not mention protection keeps it.
	valkey := true
	_, op, err = h.c.UpdateApp(ctx, app.ID, UpdateAppInput{Valkey: &valkey}, "")
	h.mustSucceed(op, err)
	if got, _ = store.GetApp(ctx, h.store.DB(), app.ID); got.Protection.BlockDotFiles {
		t.Fatalf("an unrelated update must not reset protection: %+v", got.Protection)
	}
}

// A protection change reaches the published edge route without a restart,
// and an app with a group off is served without that group's blocks.
func TestAppProtectionReachesEdge(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	h.mustSucceed(h.c.PublishApp(ctx, app.ID, ""))
	site := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(h.c.Layout.EdgeConfDir(), "live", "sites", edge.RouteName("shop.example.com")+".conf"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if c := site(); !strings.Contains(c, "wlwmanifest") || !strings.Contains(c, "composer") || !strings.Contains(c, "well-known") {
		t.Fatalf("the published route must carry every group:\n%s", c)
	}
	off := false
	_, op, err := h.c.UpdateApp(ctx, app.ID, UpdateAppInput{
		Protection: &domain.ProtectionPatch{BlockPHPProbes: &off, BlockSensitiveFiles: &off}}, "")
	h.mustSucceed(op, err)
	if c := site(); strings.Contains(c, "wlwmanifest") || strings.Contains(c, "composer") || !strings.Contains(c, "well-known") {
		t.Fatalf("only the dot file group stays on:\n%s", c)
	}
}

// A snapshot written before the setting existed clones with every filter on;
// one that recorded it keeps it.
func TestCloneRestoresProtection(t *testing.T) {
	h := newHarness(t)
	addMySQLService(t, h)
	spec := mysqlSpec("shop", "shop_main")
	man := ResticManifest{FormatVersion: 2, Slug: "shop", AppID: "asrc", HomePath: "/home/shop", Dumps: []ResticDump{
		{File: "db/a.sql", Engine: domain.EngineMySQL, Database: "shop_main", Binding: 0, Suffix: "main", Username: "uasrc"}}}
	plan := func(s AppSpec) domain.Protection {
		t.Helper()
		p, err := h.c.planClone(t.Context(), cloneInput{Man: man, Spec: s, Slug: "shop-copy", AppID: "anew"})
		must(t, err)
		return p.App.Protection
	}
	if got := plan(spec); got != domain.DefaultProtection() {
		t.Fatalf("a snapshot without the setting: %+v", got)
	}
	kept := domain.Protection{BlockDotFiles: true}
	spec.Protection = &kept
	if got := plan(spec); got != kept {
		t.Fatalf("a snapshot that recorded it: %+v", got)
	}
}
