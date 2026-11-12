package reconcile

import (
	"io"
	"log/slog"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/testutil"
)

// planOf runs PlanPass the way `bento upgrade plan` does: a controller of its
// own over a read-only engine, never started. It fails the test if planning
// changed Docker state or submitted an operation.
func planOf(t *testing.T, h *testutil.Harness) Plan {
	t.Helper()
	ctx := t.Context()
	h.WaitIdle()
	calls := len(h.Fake.Calls)
	ops, err := store.ListOperations(ctx, h.Store.DB(), store.OpFilter{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := operations.NewController(operations.Deps{Store: h.Store, Engine: docker.ReadOnly{E: h.Fake},
		Layout: h.Layout, HostIDs: platform.NoHostIDs{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: testutil.TailcatStubManager(h.Layout)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanPass(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Fake.Calls[calls:]; len(got) != 0 {
		t.Fatalf("planning changed Docker state: %v", got)
	}
	after, _ := store.ListOperations(ctx, h.Store.DB(), store.OpFilter{})
	if len(after) != len(ops) {
		t.Fatalf("planning submitted %d operations", len(after)-len(ops))
	}
	return p
}

func findChange(p Plan, kind, target string) (Change, bool) {
	for _, c := range p.Changes {
		if c.Kind == kind && c.Target == target {
			return c, true
		}
	}
	return Change{}, false
}

func TestPlanOfSteadyStackIsEmpty(t *testing.T) {
	h, _, _ := setup(t)
	p := planOf(t, h)
	if len(p.Changes) != 0 || len(p.Problems) != 0 {
		t.Fatalf("a converged stack plans nothing, got %+v", p)
	}
}

// TestPlanNamesImageBuildBeforeReplace covers the common upgrade: new image
// inputs, so the runtime is built and its running apps replaced afterwards;
// the next pass then does exactly that.
func TestPlanNamesImageBuildBeforeReplace(t *testing.T) {
	h, r, app := setup(t)
	staleImage(t, h, app)
	key := app.Runtime.ImageKey().String()
	p := planOf(t, h)
	img, ok := findChange(p, "image", key)
	if !ok || img.Action != ActionBuild || img.Detail != "for shop" {
		t.Fatalf("want a build of %s for shop, got %+v", key, p)
	}
	ac, ok := findChange(p, "app", "shop")
	if !ok || ac.Action != ActionReplace {
		t.Fatalf("want shop replaced after the build, got %+v", p)
	}
	if len(p.Problems) != 0 {
		t.Fatalf("problems: %+v", p.Problems)
	}
	pass(t, h, r)
	if n, _ := countOps(t, h, operations.KindImagePrepare, key); n != 1 {
		t.Fatalf("the pass must prepare the image the plan named, got %d", n)
	}
}

func TestPlanRepairsMissingAndStopsUnwanted(t *testing.T) {
	h, _, app := setup(t)
	other := runningApp(t, h, "blog", "24")
	h.Fake.Delete(h.C.Names.AppContainer(app.ID))
	op, err := h.C.StopApp(t.Context(), other.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	h.Wait(op.ID)
	h.Fake.SetRunning(h.C.Names.AppContainer(other.ID), true)
	p := planOf(t, h)
	if c, ok := findChange(p, "app", "shop"); !ok || c.Action != ActionStart {
		t.Fatalf("missing shop must be started, got %+v", p)
	}
	if c, ok := findChange(p, "app", "blog"); !ok || c.Action != ActionStop {
		t.Fatalf("blog runs but is desired stopped, got %+v", p)
	}
	if _, ok := findChange(p, "image", app.Runtime.ImageKey().String()); ok {
		t.Fatalf("a start on a built image needs no build: %+v", p)
	}
}
