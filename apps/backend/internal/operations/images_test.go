package operations

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// gatedBuilds holds every BuildImage until released and tracks how many run
// at once. The fake engine's own BuildImage holds its lock for the whole
// call, so it cannot block there.
type gatedBuilds struct {
	docker.Engine
	gated          atomic.Bool
	release        chan struct{}
	once           sync.Once
	inflight, peak atomic.Int32
}

func (e *gatedBuilds) open() { e.once.Do(func() { close(e.release) }) }

func (e *gatedBuilds) BuildImage(
	ctx context.Context,
	tag string,
	r io.Reader,
	args, labels map[string]string,
	progress func(string),
) (string, error) {
	if !e.gated.Load() {
		return e.Engine.BuildImage(ctx, tag, r, args, labels, progress)
	}
	n := e.inflight.Add(1)
	for p := e.peak.Load(); n > p && !e.peak.CompareAndSwap(p, n); p = e.peak.Load() {
	}
	defer e.inflight.Add(-1)
	select {
	case <-e.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return e.Engine.BuildImage(ctx, tag, r, args, labels, progress)
}

func submitPrepare(t *testing.T, h *harness, key string) store.Operation {
	t.Helper()
	op, _, err := h.c.Submit(t.Context(), Submission{Kind: KindImagePrepare, TargetKind: "image", TargetID: key})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestParseImageKeyMatchesCatalogOnly(t *testing.T) {
	for _, key := range []domain.ImageKey{
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.4"},
		{Kind: domain.RuntimeHTTP, Toolchain: "node", Version: "24"},
		{Kind: domain.RuntimeHTTP, Toolchain: "bun", Version: "1.3"},
	} {
		if got, ok := domain.ParseImageKey(key.String()); !ok || got != key {
			t.Errorf("ParseImageKey(%q) = %+v, %v", key.String(), got, ok)
		}
	}
	for _, s := range []string{"", "php-9.9", "node", "ruby-3", "../php-8.4"} {
		if _, ok := domain.ParseImageKey(s); ok {
			t.Errorf("ParseImageKey(%q) accepted an unknown key", s)
		}
	}
}

func TestImagePrepareBuildsWithoutTouchingApps(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	op, err := h.c.StartApp(t.Context(), app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	h.wait(op)
	creates, stops := h.fake.CallCount("Create"), h.fake.CallCount("Stop")
	spec, err := runtime.PlanImage(app.Runtime.ImageKey())
	if err != nil {
		t.Fatal(err)
	}
	h.fake.SetImage(spec.Tag(), "")
	got := h.wait(submitPrepare(t, h, app.Runtime.ImageKey().String()))
	if got.State != store.OpSucceeded {
		t.Fatalf("prepare: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	if _, ok, _ := h.fake.ImageID(t.Context(), spec.Tag()); !ok {
		t.Fatal("prepare did not build the planned tag")
	}
	if h.fake.CallCount("Create") != creates || h.fake.CallCount("Stop") != stops {
		t.Fatal("prepare must not stop or create any container")
	}
	// Already built: a second prepare is a no-op success.
	builds := h.fake.CallCount("BuildImage")
	if got := h.wait(submitPrepare(t, h, app.Runtime.ImageKey().String())); got.State != store.OpSucceeded {
		t.Fatalf("repeat prepare: %s", got.ErrorMessage)
	}
	if h.fake.CallCount("BuildImage") != builds {
		t.Fatal("prepare rebuilt an image that exists")
	}
}

func TestImagePrepareFailures(t *testing.T) {
	h := newHarness(t)
	if got := h.wait(submitPrepare(t, h, "php-9.9")); got.State != store.OpFailed || got.ErrorCode != "image-key" {
		t.Fatalf("unknown key: %s %s", got.State, got.ErrorCode)
	}
	h.fake.FailOn = map[string]error{"BuildImage": errors.New("injected")}
	if got := h.wait(submitPrepare(t, h, "php-8.4")); got.State != store.OpFailed || got.ErrorCode != "image" {
		t.Fatalf("build failure: %s %s", got.State, got.ErrorCode)
	}
}

// At most two image.prepare run at once, and prepares waiting for a build
// slot neither hold executor slots nor block app operations.
func TestImagePrepareHonorsBuildPool(t *testing.T) {
	var eng *gatedBuilds
	h := newHarnessWith(t, func(d *Deps) {
		eng = &gatedBuilds{Engine: d.Engine, release: make(chan struct{})}
		d.Engine = eng
	})
	t.Cleanup(eng.open) // runs before the harness shuts the executor down
	app := h.createApp("shop")
	eng.gated.Store(true)
	var ops []store.Operation
	for _, key := range []string{"php-8.3", "php-8.4", "node-22", "bun-1.3"} {
		ops = append(ops, submitPrepare(t, h, key))
	}
	deadline := time.Now().Add(5 * time.Second)
	for eng.inflight.Load() < runtime.MaxConcurrentBuilds {
		if time.Now().After(deadline) {
			t.Fatalf("only %d builds started", eng.inflight.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.settle()
	third, err := store.GetOperation(t.Context(), h.store.DB(), ops[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if third.State != store.OpQueued || h.c.WaitingOn(third.ID) == "" {
		t.Fatalf("third prepare should wait for a build slot: state %s waitingOn %q", third.State, h.c.WaitingOn(third.ID))
	}
	start, err := h.c.StartApp(t.Context(), app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(start); got.State != store.OpSucceeded {
		t.Fatalf("app start blocked behind image builds: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	eng.open()
	for _, op := range ops {
		if got := h.wait(op); got.State != store.OpSucceeded {
			t.Fatalf("%s: %s %s", got.TargetID, got.ErrorCode, got.ErrorMessage)
		}
	}
	if got := eng.peak.Load(); got != runtime.MaxConcurrentBuilds {
		t.Fatalf("peak %d concurrent builds, want %d", got, runtime.MaxConcurrentBuilds)
	}
}
