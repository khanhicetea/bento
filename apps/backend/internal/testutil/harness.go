// Package testutil builds a controller over the fake engine for tests in
// packages that sit above operations.
package testutil

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

type Harness struct {
	T      *testing.T
	C      *operations.Controller
	Fake   *docker.Fake
	Store  *store.Store
	Layout platform.Layout
}

// New returns a started controller over a fake engine. Requires root because
// provisioning creates app-owned directories.
func New(t *testing.T) *Harness {
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
	c, err := operations.NewController(operations.Deps{
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
