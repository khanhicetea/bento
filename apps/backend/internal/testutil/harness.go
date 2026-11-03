// Package testutil builds a controller over the fake engine for tests in
// packages that sit above operations.
package testutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

type Harness struct {
	T      *testing.T
	C      *operations.Controller
	Fake   *docker.Fake
	Store  *store.Store
	Layout platform.Layout

	builds int
}

// New returns a started controller over a fake engine. Requires root because
// provisioning creates app-owned directories.
func New(t *testing.T) *Harness {
	t.Helper()
	return NewWith(t, nil)
}

// NewWith is New with a hook to adjust the controller deps (for example to
// wrap the engine) before the executor starts.
func NewWith(t *testing.T, mutate func(*operations.Deps)) *Harness {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
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
	store.SetMeta(ctx, s.DB(), "stack_id", "stest")
	store.SetMeta(ctx, s.DB(), "stack_name", "test")
	fake := docker.NewFake()
	deps := operations.Deps{
		Store: s, Engine: fake, Layout: layout, HostIDs: platform.NoHostIDs{},
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Probe:        func(context.Context, string) (int, error) { return 200, nil },
		ReadyTimeout: 2 * time.Second, PollInterval: 20 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&deps)
	}
	c, err := operations.NewController(deps)
	if err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(ctx)
	c.Start(rctx)
	t.Cleanup(func() {
		cancel()
		c.Shutdown(2 * time.Second)
		s.Close()
	})
	return &Harness{T: t, C: c, Fake: fake, Store: s, Layout: layout}
}

// Wait blocks until an operation is terminal.
func (h *Harness) Wait(id string) store.Operation {
	h.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		op, err := store.GetOperation(h.T.Context(), h.Store.DB(), id)
		if err != nil {
			h.T.Fatal(err)
		}
		if op.State.Terminal() {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.T.Fatalf("operation %s did not finish", id)
	return store.Operation{}
}

// WaitIdle waits until no operation is queued or running.
func (h *Harness) WaitIdle() {
	h.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ops, _ := store.ListOperations(
			h.T.Context(),
			h.Store.DB(),
			store.OpFilter{States: []store.OpState{store.OpQueued, store.OpRunning}},
		)
		if len(ops) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.T.Fatal("operations did not drain")
}

// StaleBuilds makes every recorded build of key predate the current image
// inputs, as after a Bento upgrade that changed them: apps then need a new
// build of the key.
func (h *Harness) StaleBuilds(t *testing.T, key domain.ImageKey) {
	t.Helper()
	if _, err := h.Store.DB().ExecContext(t.Context(),
		"UPDATE runtime_builds SET context_hash = 'stale-' || context_hash WHERE image_key = ?", key.String()); err != nil {
		t.Fatal(err)
	}
}

// NewBuild records a new current build of key's current inputs whose image is
// imageID, as an operator rebuild that changed something does.
func (h *Harness) NewBuild(t *testing.T, key domain.ImageKey, imageID string) store.RuntimeBuild {
	t.Helper()
	spec, err := runtime.PlanImage(key)
	if err != nil {
		t.Fatal(err)
	}
	h.builds++
	b := store.RuntimeBuild{ImageKey: key.String(), ContextHash: spec.Hash,
		Tag: fmt.Sprintf("%s-test%d", spec.BuildTag(time.Now()), h.builds), ImageID: imageID,
		Manifest: map[string]string{"build": imageID}, BuiltAt: time.Now()}
	if b.ID, err = store.InsertCurrentRuntimeBuild(t.Context(), h.Store.DB(), b); err != nil {
		t.Fatal(err)
	}
	h.Fake.SetImage(b.Tag, imageID)
	return b
}
