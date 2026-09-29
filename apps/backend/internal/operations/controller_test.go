package operations

import (
	"io"
	"log/slog"
	"strings"
	"testing"
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
