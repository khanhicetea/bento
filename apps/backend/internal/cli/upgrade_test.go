package cli

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/reconcile"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// TestUpgradePlanBlocksStateThisBuildCannotOpen covers the refusals that come
// before any Docker call, and that the stack's database is never written.
func TestUpgradePlanBlocksStateThisBuildCannotOpen(t *testing.T) {
	cases := map[string]string{
		"newer schema": fmt.Sprintf("PRAGMA user_version = %d", store.SchemaVersion+1),
		"foreign file": "PRAGMA application_id = 7",
	}
	for name, pragma := range cases {
		t.Run(name, func(t *testing.T) {
			layout := platform.Layout{Root: t.TempDir()}
			s, err := store.Create(layout.Database())
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			db, err := sql.Open("sqlite", layout.Database())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(pragma); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before, _ := os.ReadFile(layout.Database())
			_, err = PlanUpgrade(t.Context(), layout, "test", 0)
			if !errors.Is(err, ErrUpgradeBlocked) {
				t.Fatalf("err %v, want ErrUpgradeBlocked", err)
			}
			after, _ := os.ReadFile(layout.Database())
			if !bytes.Equal(before, after) {
				t.Fatal("planning must not write the stack's database")
			}
			if entries, _ := os.ReadDir(layout.Root); len(entries) != 1 {
				t.Fatalf("planning must not add files to the stack root: %v", entries)
			}
		})
	}
	if _, err := PlanUpgrade(t.Context(), platform.Layout{Root: filepath.Join(t.TempDir(), "none")}, "test", 0); err == nil {
		t.Fatal("a root without state must be refused")
	}
}

func TestPrintUpgradePlan(t *testing.T) {
	p := UpgradePlan{Version: "0.4.0", Stack: "prod", Root: "/srv/bento/prod", Docker: "Engine 29, API 1.56",
		Changes: []reconcile.Change{
			{Kind: "app", Target: "blog", Action: reconcile.ActionReplace, Detail: "container definition changed"},
			{Kind: "image", Target: "php-8.4", Action: reconcile.ActionBuild, Detail: "for blog"},
			{Kind: "edge", Target: "edge", Action: reconcile.ActionReload, Detail: "routes or templates changed"},
		},
		Problems: []reconcile.Problem{{Kind: "app", Target: "shop", Error: "boom"}},
	}
	p.Schema.From, p.Schema.To, p.Schema.Migrated = 1, 2, true
	var b bytes.Buffer
	printUpgradePlan(&b, p)
	// Column widths depend on content; compare with runs of spaces collapsed.
	var lines []string
	for l := range strings.Lines(b.String()) {
		lines = append(lines, strings.Join(strings.Fields(l), " "))
	}
	out := strings.Join(lines, "\n")
	for _, want := range []string{"SCHEMA 1 → 2", "IMAGES build php-8.4", "APPS", "replace blog", "EDGE",
		"reload edge", "UNKNOWN", "app shop", "1 app(s) change one at a time", "1 image(s) build first"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "IMAGES") > strings.Index(out, "APPS") {
		t.Errorf("images must be listed before the apps waiting for them:\n%s", out)
	}
}
