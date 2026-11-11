package operations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// A panic while dispatching must not escape the executor goroutine, where it
// would stop the backend.
func TestSafeDispatchRecoversPanic(t *testing.T) {
	// A nil store makes dispatch panic on its first query.
	c := &Controller{Deps: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	started, err := c.safeDispatch(t.Context())
	if started || err == nil || !strings.Contains(err.Error(), "operation dispatch panicked") {
		t.Fatalf("started %v err %v", started, err)
	}
	if c.passes.Load() != 1 {
		t.Fatalf("the pass must still be counted, got %d", c.passes.Load())
	}
	if len(c.running) != 0 {
		t.Fatal("nothing may be left running")
	}
}

// failingFinish makes the first n final-state writes fail (n < 0: all).
func failingFinish(h *harness, n int32) *atomic.Int32 {
	var calls atomic.Int32
	real := h.c.finishOp
	h.c.finishOp = func(ctx context.Context, q store.Q, id string, state store.OpState, result json.RawMessage, code, message, guidance string) error {
		if c := calls.Add(1); n < 0 || c <= n {
			return errors.New("disk full")
		}
		return real(ctx, q, id, state, result, code, message, guidance)
	}
	return &calls
}

// A final state that cannot be written is retried, not dropped: a row left
// running would block its target's reconciliation until the next start.
func TestFinishWriteIsRetriedUntilRecorded(t *testing.T) {
	h := newHarnessWith(t, func(d *Deps) { d.FinishRetry = 5 * time.Millisecond })
	app := h.createApp("shop")
	calls := failingFinish(h, 3)
	op, _, err := h.c.Submit(t.Context(), Submission{Kind: KindAppReconcile, TargetKind: "app", TargetID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if !got.State.Terminal() || calls.Load() < 4 {
		t.Fatalf("state %s after %d finish attempts", got.State, calls.Load())
	}
}

func TestFinishRetryStopsWhenBackendStops(t *testing.T) {
	h := newHarnessWith(t, func(d *Deps) { d.FinishRetry = 5 * time.Millisecond })
	app := h.createApp("shop")
	calls := failingFinish(h, -1)
	op, _, err := h.c.Submit(t.Context(), Submission{Kind: KindAppReconcile, TargetKind: "app", TargetID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the final write was not retried")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.cancel()
	h.c.Shutdown(2 * time.Second)
	got, err := store.GetOperation(context.Background(), h.store.DB(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.OpRunning {
		t.Fatalf("state %s, want running (recovery interrupts it on the next start)", got.State)
	}
}
