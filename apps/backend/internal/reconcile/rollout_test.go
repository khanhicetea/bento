package reconcile

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/testutil"
)

func runningApp(t *testing.T, h *testutil.Harness, slug, node string) domain.App {
	t.Helper()
	ctx := t.Context()
	app, op, err := h.C.CreateApp(ctx, operations.CreateAppInput{Slug: slug,
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: node, Argv: []string{"node", "s.js"}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.Wait(op.ID)
	op, err = h.C.StartApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Wait(op.ID); got.State != store.OpSucceeded {
		t.Fatal(got.ErrorMessage)
	}
	app, _ = store.GetApp(ctx, h.Store.DB(), app.ID)
	return app
}

// upgrade rebuilds the image of runtime node-<v> under its tag, so every
// running app on it drifts, as after a Bento upgrade whose image is built.
func upgrade(t *testing.T, h *testutil.Harness, node string) {
	t.Helper()
	spec, err := runtime.PlanImage(domain.ImageKey{Kind: domain.RuntimeHTTP, Toolchain: "node", Version: node})
	if err != nil {
		t.Fatal(err)
	}
	h.Fake.SetImage(spec.Tag(), "sha256:upgraded-"+node)
}

func containerOf(t *testing.T, h *testutil.Harness, app domain.App) string {
	t.Helper()
	obs, err := h.C.Observe(t.Context(), app)
	if err != nil || !obs.Running {
		t.Fatalf("%s not running: %v", app.Slug, err)
	}
	return obs.ContainerID
}

func replaced(t *testing.T, h *testutil.Harness, apps []domain.App, before map[string]string) int {
	t.Helper()
	n := 0
	for _, a := range apps {
		obs, _ := h.C.Observe(t.Context(), a)
		if obs.ContainerID != before[a.ID] {
			n++
		}
	}
	return n
}

func TestUpgradeRollsOutOneAppAtATime(t *testing.T) {
	h := testutil.New(t)
	var apps []domain.App
	for _, slug := range []string{"alpha", "beta", "gamma"} {
		apps = append(apps, runningApp(t, h, slug, "24"))
	}
	before := map[string]string{}
	for _, a := range apps {
		before[a.ID] = containerOf(t, h, a)
	}
	upgrade(t, h, "24")
	r := New(h.C, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.BaseBackoff = time.Millisecond
	for i := 1; i <= len(apps); i++ {
		pass(t, h, r)
		if n := replaced(t, h, apps, before); n != i {
			t.Fatalf("after pass %d, %d apps replaced; want exactly %d", i, n, i)
		}
	}
	pass(t, h, r)
	if n := replaced(t, h, apps, before); n != len(apps) {
		t.Fatalf("%d of %d apps replaced", n, len(apps))
	}
}

func TestFailedReplacementPausesItsRuntimeOnly(t *testing.T) {
	h := testutil.New(t)
	alpha := runningApp(t, h, "alpha", "24")
	beta := runningApp(t, h, "beta", "24")
	other := runningApp(t, h, "other", "22")
	apps := []domain.App{alpha, beta, other}
	before := map[string]string{}
	for _, a := range apps {
		before[a.ID] = containerOf(t, h, a)
	}
	upgrade(t, h, "24")
	upgrade(t, h, "22")
	r := New(h.C, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.BaseBackoff = time.Hour // the failed app is not retried by itself
	h.Fake.FailOn = map[string]error{"Create": errors.New("injected: the new image does not start")}

	pass(t, h, r) // alpha's replacement fails
	if _, op := countOps(t, h, operations.KindAppReconcile, alpha.ID); op.State != store.OpFailed {
		t.Fatalf("alpha's replacement: %s", op.State)
	}
	pass(t, h, r) // settles the failure; beta pauses, other rolls out
	pass(t, h, r)
	if obs, _ := h.C.Observe(t.Context(), beta); obs.ContainerID != before[beta.ID] || !obs.Running {
		t.Fatal("beta must keep its working instance while alpha's replacement on the same runtime is failing")
	}
	if !strings.Contains(r.Status(beta.ID).LastError, "rollout paused") {
		t.Fatalf("beta's pause must be observable: %+v", r.Status(beta.ID))
	}
	if obs, _ := h.C.Observe(t.Context(), other); obs.ContainerID == before[other.ID] {
		t.Fatal("an app on another runtime image must still roll out")
	}

	// The operator fixes alpha; the rollout resumes.
	op, err := h.C.StartApp(t.Context(), alpha.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Wait(op.ID); got.State != store.OpSucceeded {
		t.Fatalf("operator start of alpha: %s", got.ErrorMessage)
	}
	pass(t, h, r)
	pass(t, h, r)
	if obs, _ := h.C.Observe(t.Context(), beta); obs.ContainerID == before[beta.ID] {
		t.Fatal("beta must roll out once alpha recovered")
	}
	if st := r.Status(beta.ID); st.LastError != "" {
		t.Fatalf("beta still reports %q", st.LastError)
	}
}
