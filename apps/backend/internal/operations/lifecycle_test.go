package operations

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

type harness struct {
	t      *testing.T
	c      *Controller
	fake   *docker.Fake
	store  *store.Store
	layout platform.Layout
	cancel context.CancelFunc
}

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("lifecycle tests create app-owned directories and need root")
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	requireRoot(t)
	layout := platform.Layout{Root: t.TempDir()}
	for dir, mode := range layout.SkeletonDirs() {
		if err := platform.EnsureDir(dir, os.FileMode(mode), platform.RootOwner); err != nil {
			t.Fatal(err)
		}
	}
	s, err := store.Create(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for k, v := range map[string]string{"stack_id": "stest", "stack_name": "test"} {
		store.SetMeta(ctx, s.DB(), k, v)
	}
	fake := docker.NewFake()
	fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{Stdout: []byte("ready")} }
	c, err := NewController(Deps{
		Store: s, Engine: fake, Layout: layout, HostIDs: platform.NoHostIDs{},
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Probe:        func(context.Context, string) (int, error) { return 200, nil },
		ReadyTimeout: 2 * time.Second, PollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(ctx)
	c.Start(rctx)
	h := &harness{t: t, c: c, fake: fake, store: s, layout: layout, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		c.Shutdown(2 * time.Second)
		s.Close()
	})
	return h
}

func (h *harness) wait(op store.Operation) store.Operation {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := store.GetOperation(context.Background(), h.store.DB(), op.ID)
		if err != nil {
			h.t.Fatal(err)
		}
		if got.State.Terminal() {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("operation %s did not finish", op.ID)
	return op
}

func (h *harness) createApp(slug string) domain.App {
	h.t.Helper()
	app, op, err := h.c.CreateApp(context.Background(), CreateAppInput{
		Slug:    slug,
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}},
		Domains: []string{slug + ".example.com"}, Bindings: []BindingRequest{{Engine: domain.EngineSQLite}},
	}, "")
	if err != nil {
		h.t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpSucceeded {
		h.t.Fatalf("provision failed: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	app, _ = store.GetApp(context.Background(), h.store.DB(), app.ID)
	return app
}

func (h *harness) mustSucceed(op store.Operation, err error) store.Operation {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpSucceeded {
		h.t.Fatalf("%s failed: %s: %s", got.Kind, got.ErrorCode, got.ErrorMessage)
	}
	return got
}

func TestCreateIsStoppedUnpublishedAndDoesNotStart(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	if app.DesiredRuntime != domain.DesiredStopped || app.Publication != domain.Unpublished || !app.Provisioned {
		t.Fatalf("unexpected intent %+v", app)
	}
	if n := h.fake.CallCount("Create"); n != 0 {
		t.Fatalf("create must not start the app (Create calls: %d)", n)
	}
	if app.UID != 10000 || app.GID != app.UID {
		t.Fatalf("uid %d gid %d", app.UID, app.GID)
	}
	owner, _, err := platform.StatOwner(h.layout.AppHome("shop"))
	if err != nil || owner.UID != app.UID {
		t.Fatalf("home owner %v %v", owner, err)
	}
	codeOwner, mode, err := platform.StatOwner(h.layout.AppCode("shop"))
	if err != nil || !mode.IsDir() || codeOwner.UID != app.UID {
		t.Fatalf("code directory owner %v mode %v err %v", codeOwner, mode, err)
	}
}

func TestStartStopRestartSemantics(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	obs, _ := h.c.Observe(ctx, app)
	if !obs.Running || obs.RestartMode != "unless-stopped" {
		t.Fatalf("after start: %+v", obs)
	}
	app, _ = store.GetApp(ctx, h.store.DB(), app.ID)
	if app.Publication != domain.Unpublished {
		t.Fatal("start must not publish implicitly")
	}
	created := h.fake.CallCount("Create")
	h.mustSucceed(h.c.RestartApp(ctx, app.ID, ""))
	if h.fake.CallCount("Create") != created {
		t.Fatal("restart of an unchanged generation must not recreate")
	}
	h.mustSucceed(h.c.StopApp(ctx, app.ID, ""))
	obs, _ = h.c.Observe(ctx, app)
	if obs.Running || obs.RestartMode != "no" {
		t.Fatalf("stop must stop and disable restart: %+v", obs)
	}
	if _, err := h.c.RestartApp(ctx, app.ID, ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("restart of stopped app must be refused, got %v", err)
	}
	if _, err := h.c.PublishApp(ctx, app.ID, ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("publish of stopped app must be refused, got %v", err)
	}
}

func TestReadinessFailureIsBoundedAndDoesNotPublish(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	h.c.Probe = func(context.Context, string) (int, error) { return 503, nil }
	op, err := h.c.StartApp(context.Background(), app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || got.ErrorCode != "not-ready" || !strings.Contains(got.ErrorMessage, "503") {
		t.Fatalf("expected bounded not-ready failure, got %s %s", got.ErrorCode, got.ErrorMessage)
	}
	if n := h.fake.CallCount("Create"); n != 1 {
		t.Fatalf("readiness failure must not cause recreation storms (Create=%d)", n)
	}
}

// Fault injection at each external-effect boundary: the operation fails
// diagnosably, intent is kept, and a retry converges without duplicates.
func TestFaultInjectionEachBoundaryThenRecover(t *testing.T) {
	for _, method := range []string{"BuildImage", "Create", "Start", "Exec"} {
		t.Run(method, func(t *testing.T) {
			h := newHarness(t)
			app := h.createApp("shop")
			ctx := context.Background()
			h.fake.FailOn = map[string]error{method: errors.New("injected " + method + " failure")}
			if method == "BuildImage" {
				// Force a fresh build: the image was built during provisioning.
				h.fake.Images = map[string]string{}
			}
			op, err := h.c.StartApp(ctx, app.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			got := h.wait(op)
			if method != "Exec" && got.State != store.OpFailed {
				t.Fatalf("expected failure, got %s", got.State)
			}
			cur, _ := store.GetApp(ctx, h.store.DB(), app.ID)
			if cur.DesiredRuntime != domain.DesiredRunning {
				t.Fatal("intent must survive a failed operation")
			}
			h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
			list, _ := h.fake.List(ctx, map[string]string{"io.bento.app-id": app.ID})
			if len(list) != 1 {
				t.Fatalf("expected exactly one instance, got %d", len(list))
			}
		})
	}
}

func TestInterruptedOperationIsNotReplayed(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	// Simulate a crash mid-operation: a running row with no executor.
	op, _, err := store.InsertOperation(ctx, h.store.DB(), store.Operation{ID: platform.NewOperationID(), Kind: KindAppRemove, TargetKind: "app", TargetID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	store.MarkRunning(ctx, h.store.DB(), op.ID)
	if err := h.c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetOperation(ctx, h.store.DB(), op.ID)
	if got.State != store.OpInterrupted {
		t.Fatalf("state %s", got.State)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := store.GetApp(ctx, h.store.DB(), app.ID); err != nil {
		t.Fatal("interrupted destructive operation must not be replayed")
	}
}

func TestConcurrentStartsCreateOneInstance(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	var wg sync.WaitGroup
	ops := make(chan store.Operation, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, err := h.c.StartApp(ctx, app.ID, "")
			if err == nil {
				ops <- op
			}
		}()
	}
	wg.Wait()
	close(ops)
	for op := range ops {
		h.wait(op)
	}
	if n := h.fake.CallCount("Create"); n != 1 {
		t.Fatalf("serialized starts must create exactly one instance, got %d", n)
	}
}

func TestMissingHomeBlocksRecreation(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	h.fake.Delete(h.c.Names.AppContainer(app.ID))
	os.Rename(h.layout.AppHome("shop"), h.layout.AppHome("shop")+".moved")
	creates := h.fake.CallCount("Create")
	op, _, err := h.c.Submit(ctx, Submission{Kind: KindAppReconcile, TargetKind: "app", TargetID: app.ID, Origin: "reconciler"})
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || got.ErrorCode != "durable-state-missing" {
		t.Fatalf("expected durable-state-missing, got %s %s", got.ErrorCode, got.ErrorMessage)
	}
	if h.fake.CallCount("Create") != creates {
		t.Fatal("must not create an empty replacement")
	}
	os.Rename(h.layout.AppHome("shop")+".moved", h.layout.AppHome("shop"))
	op, _, _ = h.c.Submit(ctx, Submission{Kind: KindAppReconcile, TargetKind: "app", TargetID: app.ID, Origin: "reconciler"})
	if got := h.wait(op); got.State != store.OpSucceeded {
		t.Fatalf("recovery with intact data failed: %s", got.ErrorMessage)
	}
}

func TestMissingCodeDirectoryBlocksStart(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	if err := os.Remove(h.layout.AppCode("shop")); err != nil {
		t.Fatal(err)
	}

	op, err := h.c.StartApp(context.Background(), app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || got.ErrorCode != "durable-state-missing" {
		t.Fatalf("expected durable-state-missing, got %s %s", got.ErrorCode, got.ErrorMessage)
	}
	if h.fake.CallCount("Create") != 0 {
		t.Fatal("must not create an instance with a missing code directory")
	}
}

func TestRetainedHomeIsNeverAdopted(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	h.mustSucceed(h.c.RemoveApp(ctx, app.ID, "delete shop", ""))
	if _, err := h.c.RemoveApp(ctx, "shop", "delete shop", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("app should be gone")
	}
	if _, err := os.Stat(h.layout.AppHome("shop")); err != nil {
		t.Fatal("removal must retain the home")
	}
	_, _, err := h.c.CreateApp(ctx, CreateAppInput{Slug: "shop",
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"x"}}}}, "")
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("retained home must block a new incarnation, got %v", err)
	}
	ledger, _ := store.ListLedger(ctx, h.store.DB())
	if len(ledger) != 1 || ledger[0].State != "retired" {
		t.Fatalf("ledger %+v", ledger)
	}
}

func TestRemoveRequiresExactConfirmation(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	for _, bad := range []string{"", "delete", "delete Shop", "delete shop ", "yes"} {
		if _, err := h.c.RemoveApp(context.Background(), app.ID, bad, ""); !errors.Is(err, ErrConfirmation) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestIdempotentSubmissionReturnsSameOperation(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	a, err := h.c.StartApp(ctx, app.ID, "client-key-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.c.StartApp(ctx, app.ID, "client-key-1")
	if err != nil || a.ID != b.ID {
		t.Fatalf("retry produced %s vs %s (%v)", a.ID, b.ID, err)
	}
	h.wait(a)
	if n := h.fake.CallCount("Create"); n != 1 {
		t.Fatalf("duplicate work: %d creates", n)
	}
}
