package reconcile

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
)

// imageIDCounter counts ImageID lookups, a Docker round trip on the real
// engine.
type imageIDCounter struct {
	docker.Engine
	n atomic.Int64
}

func (e *imageIDCounter) ImageID(ctx context.Context, ref string) (string, bool, error) {
	e.n.Add(1)
	return e.Engine.ImageID(ctx, ref)
}

// TestSteadyPassResolvesImageOncePerApp pins the per-app cost of a pass over
// a healthy running app: one image lookup covers both the fingerprint and the
// config-drift check.
func TestSteadyPassResolvesImageOncePerApp(t *testing.T) {
	var counter *imageIDCounter
	h, r, _ := setupWith(t, func(d *operations.Deps) {
		counter = &imageIDCounter{Engine: d.Engine}
		d.Engine = counter
	})
	creates := h.Fake.CallCount("Create")
	counter.n.Store(0)
	pass(t, h, r)
	if got := counter.n.Load(); got != 1 {
		t.Fatalf("a steady pass over one running app made %d image lookups, want 1", got)
	}
	if h.Fake.CallCount("Create") != creates {
		t.Fatal("a steady pass must not recreate the healthy app")
	}
}
