package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/testutil"
)

func setup(t *testing.T) (*testutil.Harness, *Reconciler, domain.App) {
	h := testutil.New(t)
	ctx := context.Background()
	app, op, err := h.C.CreateApp(ctx, operations.CreateAppInput{Slug: "shop",
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}}}, "")
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
	r := New(h.C, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.BaseBackoff = time.Millisecond
	app, _ = store.GetApp(ctx, h.Store.DB(), app.ID)
	return h, r, app
}

func pass(t *testing.T, h *testutil.Harness, r *Reconciler) {
	t.Helper()
	if err := r.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.WaitIdle()
}

func TestHealthyStateIsLeftAlone(t *testing.T) {
	h, r, _ := setup(t)
	creates, starts := h.Fake.CallCount("Create"), h.Fake.CallCount("Start")
	pass(t, h, r)
	pass(t, h, r)
	if h.Fake.CallCount("Create") != creates || h.Fake.CallCount("Start") != starts {
		t.Fatal("reconciliation of an intact stack must not restart healthy apps")
	}
}

func TestMissedEventRepairedByResync(t *testing.T) {
	h, r, app := setup(t)
	h.Fake.Delete(h.C.Names.AppContainer(app.ID)) // no event delivered by the fake
	pass(t, h, r)
	obs, _ := h.C.Observe(context.Background(), app)
	if !obs.Running {
		t.Fatal("full resync must recreate a missing desired-running instance")
	}
}

func TestStopIntentIsNeverResurrected(t *testing.T) {
	h, r, app := setup(t)
	ctx := context.Background()
	op, _ := h.C.StopApp(ctx, app.ID, "")
	h.Wait(op.ID)
	// Something starts the container behind Bento's back.
	h.Fake.SetRunning(h.C.Names.AppContainer(app.ID), true)
	pass(t, h, r)
	obs, _ := h.C.Observe(ctx, app)
	if obs.Running {
		t.Fatal("stopped intent must be converged back to stopped")
	}
	pass(t, h, r)
	obs, _ = h.C.Observe(ctx, app)
	if obs.Running {
		t.Fatal("stopped app resurrected")
	}
}

func TestRetryBudgetIsBoundedAndObservable(t *testing.T) {
	h, r, app := setup(t)
	// The backoff must outlast a whole pass under -race, or the settling pass
	// resubmits before the next failure is injected and the attempt succeeds.
	r.BaseBackoff = 20 * time.Millisecond
	h.Fake.Delete(h.C.Names.AppContainer(app.ID))
	for range MaxAttempts + 3 {
		h.Fake.FailOn = map[string]error{"Create": errors.New("injected")}
		pass(t, h, r)
		pass(t, h, r)
		time.Sleep(time.Until(r.Status(app.ID).NextAttempt) + time.Millisecond)
	}
	st := r.Status(app.ID)
	if !st.Blocked || st.Failures != MaxAttempts {
		t.Fatalf("expected blocked after %d failures, got %+v", MaxAttempts, st)
	}
	ops, _ := store.ListOperations(context.Background(), h.Store.DB(), store.OpFilter{TargetID: app.ID})
	n := 0
	for _, o := range ops {
		if o.Kind == operations.KindAppReconcile {
			n++
		}
	}
	if n != MaxAttempts {
		t.Fatalf("expected exactly %d reconcile attempts, got %d", MaxAttempts, n)
	}
	// An explicit operator action resets the budget.
	h.Fake.FailOn = nil
	r.ResetBudget(app.ID)
	pass(t, h, r)
	obs, _ := h.C.Observe(context.Background(), app)
	if !obs.Running {
		t.Fatal("recovery after reset failed")
	}
}

// M7: a blocked non-app target recovers once any operation on it succeeds,
// and its status is observable.
func TestBlockedServiceRecoversAfterSuccessfulOperation(t *testing.T) {
	h, r, _ := setup(t)
	ctx := context.Background()
	_, op, err := h.C.CreateService(ctx, domain.EngineRedis, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Wait(op.ID); got.State != store.OpSucceeded {
		t.Fatal(got.ErrorMessage)
	}
	name := h.C.Names.ServiceContainer("redis")
	// Wait out the actual backoff rather than a fixed guess, which -race can outrun.
	r.BaseBackoff = 20 * time.Millisecond
	for range MaxAttempts + 2 {
		h.Fake.SetRunning(name, false)
		h.Fake.FailOn = map[string]error{"Start": errors.New("injected")}
		pass(t, h, r)
		pass(t, h, r)
		time.Sleep(time.Until(r.Statuses()["service:redis"].NextAttempt) + time.Millisecond)
	}
	if st := r.Statuses()["service:redis"]; !st.Blocked {
		t.Fatalf("expected blocked, got %+v", st)
	}
	h.Fake.FailOn = nil
	op, _, err = h.C.Submit(ctx, operations.Submission{Kind: operations.KindServiceEnsure, TargetKind: "service", TargetID: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Wait(op.ID); got.State != store.OpSucceeded {
		t.Fatal(got.ErrorMessage)
	}
	pass(t, h, r)
	if st, ok := r.Statuses()["service:redis"]; ok {
		t.Fatalf("a successful ensure must clear the budget, got %+v", st)
	}
}

// After a host reboot every app needs a reconcile; one slow readiness wait
// must not queue the others behind it.
func TestDownAppsAreReconciledInParallel(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	var apps []domain.App
	for _, slug := range []string{"alpha", "beta", "gamma"} {
		app, op, err := h.C.CreateApp(ctx, operations.CreateAppInput{Slug: slug,
			Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}}}, "")
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
		apps = append(apps, app)
	}
	for _, app := range apps {
		h.Fake.Delete(h.C.Names.AppContainer(app.ID))
	}
	var inflight, peak atomic.Int32
	h.Fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) > 0 && strings.HasSuffix(req.Cmd[0], "bento-ready") {
			n := inflight.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(60 * time.Millisecond)
			inflight.Add(-1)
		}
		return docker.ExecResult{}
	}
	r := New(h.C, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pass(t, h, r)
	for _, app := range apps {
		if obs, _ := h.C.Observe(ctx, app); !obs.Running {
			t.Fatalf("%s was not recreated", app.Slug)
		}
	}
	if peak.Load() < 2 {
		t.Fatalf("reconcile operations for different apps never overlapped (peak %d)", peak.Load())
	}
}
