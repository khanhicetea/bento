package operations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// hungPulls never finishes a pull until its context ends, like a stalled
// registry behind an Engine that ignores the backend's deadlines.
type hungPulls struct {
	docker.Engine
	started chan struct{}
}

func (e *hungPulls) PullImage(ctx context.Context, _ string, _ func(string)) error {
	select {
	case e.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func submitTunnelApply(t *testing.T, h *harness) store.Operation {
	t.Helper()
	op, _, err := h.c.Submit(t.Context(), Submission{
		Kind: KindTunnelApply, TargetKind: "tunnel", TargetID: "tunnel",
		Mutate: func(ctx context.Context, q store.Q) error {
			return store.PutSetting(ctx, q, tunnelSettingKey, domain.TunnelSettings{Enabled: true, TokenGeneration: 1})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestCancelForcesAStuckOperationAfterTheGrace(t *testing.T) {
	eng := &hungPulls{started: make(chan struct{}, 1)}
	h := newHarnessWith(t, func(d *Deps) {
		eng.Engine = d.Engine
		d.Engine = eng
		d.CancelGrace, d.CancelPoll = 300*time.Millisecond, 20*time.Millisecond
	})
	stuck := submitTunnelApply(t, h)
	select {
	case <-eng.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel apply never reached its pull")
	}
	// A global operation queued behind the stuck one.
	behind, _, err := h.c.Submit(t.Context(), Submission{Kind: KindEdgeApply, TargetKind: "edge", TargetID: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got, _ := store.GetOperation(t.Context(), h.store.DB(), stuck.ID); got.State != store.OpRunning {
		t.Fatalf("stuck operation is %s before any cancellation", got.State)
	}
	requested := time.Now()
	if _, err := store.RequestCancel(t.Context(), h.store.DB(), stuck.ID); err != nil {
		t.Fatal(err)
	}
	got := h.wait(stuck)
	if got.State != store.OpCancelled || !strings.Contains(got.ErrorMessage, "while an effect was in progress") {
		t.Fatalf("stuck operation ended %s: %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	if waited := time.Since(requested); waited < 300*time.Millisecond {
		t.Fatalf("cancellation forced after %s, before the grace", waited)
	}
	events, err := store.ListEvents(t.Context(), h.store.DB(), stuck.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var forced bool
	for _, e := range events {
		forced = forced || strings.Contains(e.Message, "cancellation forced")
	}
	if !forced {
		t.Fatal("the forced cancellation is not in the operation journal")
	}
	if got := h.wait(behind); got.State != store.OpSucceeded {
		t.Fatalf("operation queued behind the stuck one: %s %s", got.State, got.ErrorMessage)
	}
}

func TestCancelWatcherLeavesUncancelledOperationsAlone(t *testing.T) {
	h := newHarnessWith(t, func(d *Deps) { d.CancelGrace, d.CancelPoll = time.Hour, 10*time.Millisecond })
	op := submitTunnelApply(t, h)
	got := h.wait(op)
	if got.State != store.OpSucceeded {
		t.Fatalf("tunnel apply: %s %s", got.State, got.ErrorMessage)
	}
	if strings.Contains(got.ErrorMessage, "effect") {
		t.Fatal("an operation that was never cancelled must not be marked forced")
	}
}
