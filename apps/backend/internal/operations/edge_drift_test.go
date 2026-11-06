package operations

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func submitEdgeApply(t *testing.T, h *harness) store.Operation {
	t.Helper()
	op, _, err := h.c.Submit(t.Context(), Submission{Kind: KindEdgeApply, TargetKind: "edge", TargetID: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	return h.wait(op)
}

func countCalls(h *harness, prefix string) int {
	return h.fake.CallCount(prefix)
}

// edgeControlCall is the fake engine's record of `nginx -s action` run in
// the edge container (the key FailOn matches).
func edgeControlCall(h *harness, action string) string {
	id := ""
	for _, c := range h.fake.Containers {
		if c.Name == h.c.Names.EdgeContainer() {
			id = c.ID
		}
	}
	return "Exec " + id + " nginx -e stderr -c " + edge.LiveConf + " -s " + action
}

// edgeControls counts `nginx -s action` runs in any container.
func edgeControls(h *harness, action string) int {
	n := 0
	for _, c := range h.fake.Calls {
		if strings.HasPrefix(c, "Exec ") && strings.HasSuffix(c, " nginx -e stderr -c "+edge.LiveConf+" -s "+action) {
			n++
		}
	}
	return n
}

// A reload lost after the new generation was promoted must not leave the
// edge serving the old configuration: the live generation was never loaded,
// which is drift, and the retry reloads even though nothing new renders.
func TestLostEdgeReloadIsDriftAndRetried(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	h.mustSucceed(h.c.PublishApp(ctx, app.ID, ""))
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift after a clean apply: %v %v", drift, err)
	}
	h.fake.FailOn = map[string]error{edgeControlCall(h, "reload"): errors.New("injected lost reload")}
	if got := submitUnpublish(t, h, app.ID); got.State != store.OpSucceeded &&
		!strings.Contains(got.ErrorMessage, "injected lost reload") &&
		!hasWarning(h, got.ID, "injected lost reload") {
		t.Fatalf("publish with a lost reload: %s %s", got.State, got.ErrorMessage)
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || !drift {
		t.Fatalf("a promoted but never loaded generation must be drift: %v %v", drift, err)
	}
	before := edgeControls(h, "reload")
	if got := submitEdgeApply(t, h); got.State != store.OpSucceeded {
		t.Fatalf("edge apply retry: %s %s", got.State, got.ErrorMessage)
	}
	if edgeControls(h, "reload") != before+1 {
		t.Fatal("the retry must reload the edge although no route changed")
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift after the retried reload: %v %v", drift, err)
	}
	// Once loaded, an apply with nothing new sends no reload.
	before = edgeControls(h, "reload")
	submitEdgeApply(t, h)
	if edgeControls(h, "reload") != before {
		t.Fatal("an unchanged, loaded generation must not be reloaded")
	}
}

func submitUnpublish(t *testing.T, h *harness, appID string) store.Operation {
	t.Helper()
	op, err := h.c.UnpublishApp(t.Context(), appID, "")
	if err != nil {
		t.Fatal(err)
	}
	return h.wait(op)
}

// relabel rewrites the generation label of the container named name, as if
// it had been created by an older Bento with another image pin.
func relabel(t *testing.T, h *harness, name, gen string) {
	t.Helper()
	c := h.fake.Containers[name]
	if c == nil {
		for _, fc := range h.fake.Containers {
			if fc.Name == name {
				c = fc
			}
		}
	}
	if c == nil {
		t.Fatalf("no container %s", name)
	}
	c.Spec.Config.Labels[runtime.LabelGeneration] = gen
}

func TestEdgeImagePinChangeIsDriftAndRecreates(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	if got := submitEdgeApply(t, h); got.State != store.OpSucceeded {
		t.Fatalf("edge apply: %s %s", got.State, got.ErrorMessage)
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift after apply: %v %v", drift, err)
	}
	relabel(t, h, h.c.Names.EdgeContainer(), "from-an-older-image-pin")
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || !drift {
		t.Fatalf("an edge created for another image must be drift: %v %v", drift, err)
	}
	created := countCalls(h, "Create")
	if got := submitEdgeApply(t, h); got.State != store.OpSucceeded {
		t.Fatalf("edge apply: %s %s", got.State, got.ErrorMessage)
	}
	if countCalls(h, "Create") <= created {
		t.Fatal("the edge must be recreated on the new image")
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift after recreate: %v %v", drift, err)
	}
}

func TestTunnelImagePinChangeIsDrift(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if drift, err := h.c.TunnelDrift(ctx); err != nil || drift {
		t.Fatalf("a disabled tunnel is never drift: %v %v", drift, err)
	}
	if got := h.wait(submitTunnelApply(t, h)); got.State != store.OpSucceeded {
		t.Fatalf("tunnel apply: %s %s", got.State, got.ErrorMessage)
	}
	if drift, err := h.c.TunnelDrift(ctx); err != nil || drift {
		t.Fatalf("drift after apply: %v %v", drift, err)
	}
	relabel(t, h, h.c.Names.TunnelContainer(), "token-1")
	if drift, err := h.c.TunnelDrift(ctx); err != nil || !drift {
		t.Fatalf("a tunnel from before the image pin changed must be drift: %v %v", drift, err)
	}
	op, _, err := h.c.Submit(ctx, Submission{Kind: KindTunnelApply, TargetKind: "tunnel", TargetID: "tunnel"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpSucceeded {
		t.Fatalf("tunnel apply: %s %s", got.State, got.ErrorMessage)
	}
	if drift, err := h.c.TunnelDrift(ctx); err != nil || drift {
		t.Fatalf("drift after recreate: %v %v", drift, err)
	}
	if !slices.ContainsFunc(h.fake.Calls, func(c string) bool { return strings.HasPrefix(c, "Remove") }) {
		t.Fatal("the outdated tunnel must be replaced")
	}
}
