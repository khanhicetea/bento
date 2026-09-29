package reconcile

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
)

// gatedEngine blocks Inspect while armed, so a test can hold a pass in the
// middle of its Docker calls.
type gatedEngine struct {
	docker.Engine
	mu      sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
}

func (g *gatedEngine) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.armed = true
	g.entered = make(chan struct{})
	g.release = make(chan struct{})
}

func (g *gatedEngine) Inspect(ctx context.Context, nameOrID string) (*container.InspectResponse, error) {
	g.mu.Lock()
	armed, entered, release := g.armed, g.entered, g.release
	g.armed = false
	g.mu.Unlock()
	if armed {
		close(entered)
		<-release
	}
	return g.Engine.Inspect(ctx, nameOrID)
}

func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked behind an in-flight pass", what)
	}
}

// Status reads and budget resets must not wait for a pass's Docker and store
// calls, and a reset made during a pass must survive the pass.
func TestStatusDoesNotBlockOnInFlightPass(t *testing.T) {
	var gate *gatedEngine
	h, r, app := setupWith(t, func(d *operations.Deps) {
		gate = &gatedEngine{Engine: d.Engine}
		d.Engine = gate
	})
	// A stopped container makes the pass submit an app.reconcile, so the
	// pass would leave a pending operation on the app's target.
	h.Fake.SetRunning(h.C.Names.AppContainer(app.ID), false)
	gate.arm()
	passDone := make(chan error, 1)
	go func() { passDone <- r.Pass(t.Context()) }()
	<-gate.entered

	within(t, "Status", func() { r.Status(app.ID) })
	within(t, "Statuses", func() { r.Statuses() })
	within(t, "Passes", func() { r.Passes() })
	within(t, "ResetBudget", func() { r.ResetBudget(app.ID) })

	close(gate.release)
	if err := <-passDone; err != nil {
		t.Fatal(err)
	}
	h.WaitIdle()
	if st := r.Status(app.ID); st != (TargetStatus{}) {
		t.Fatalf("a reset during a pass must win over the pass's result: %+v", st)
	}
	if r.Passes() != 1 {
		t.Fatalf("passes %d", r.Passes())
	}
	// The next pass tracks the target again from a clean budget.
	pass(t, h, r)
	if st := r.Status(app.ID); st.Failures != 0 || st.Blocked {
		t.Fatalf("status after reset %+v", st)
	}
}

// Concurrent passes and readers exercise the locking under -race.
func TestConcurrentPassesAndReaders(t *testing.T) {
	h, r, app := setup(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 5 {
				if err := r.Pass(t.Context()); err != nil {
					t.Error(err)
				}
			}
		})
		wg.Go(func() {
			for range 50 {
				r.Status(app.ID)
				r.Statuses()
				r.ResetBudget("service:redis")
			}
		})
	}
	wg.Wait()
	h.WaitIdle()
	if r.Passes() != 20 {
		t.Fatalf("passes %d, want 20", r.Passes())
	}
}
