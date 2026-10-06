package operations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestSweepPendingResticKeys(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	dir := h.c.resticKeyDir()
	write := func(name string, age time.Duration) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("secret"), 0o400); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	submit := func(pending string, finish bool) {
		t.Helper()
		req, _ := json.Marshal(resticKeyRequest{Pending: pending})
		op, _, err := store.InsertOperation(ctx, h.store.DB(), store.Operation{
			ID: platform.NewOperationID(), Kind: KindResticInit, TargetKind: "app", TargetID: "a1", Request: req,
		})
		if err != nil {
			t.Fatal(err)
		}
		if finish {
			if _, err := h.store.DB().ExecContext(ctx, "UPDATE operations SET state='cancelled' WHERE id=?", op.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	queued := "a1111.pending-aaaaaaaaaaaa.key"
	cancelled := "a1111.pending-bbbbbbbbbbbb.key"
	oldOrphan := "a1111.pending-cccccccccccc.key"
	freshOrphan := "a1111.pending-dddddddddddd.key"
	adopted := write("a1111.key", 72*time.Hour)
	write(queued, 72*time.Hour)
	write(cancelled, time.Minute)
	write(oldOrphan, 25*time.Hour)
	write(freshOrphan, time.Hour)
	submit(queued, false)
	submit(cancelled, true)

	n, err := h.c.SweepPendingResticKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
	if !exists(queued) || !exists(freshOrphan) || !exists("a1111.key") {
		t.Fatalf("sweep removed a key that must stay (adopted %s)", adopted)
	}
	if exists(cancelled) || exists(oldOrphan) {
		t.Fatal("stale pending keys were not removed")
	}
}

func TestSweepPendingResticKeysWithoutDirectory(t *testing.T) {
	h := newHarness(t)
	if n, err := h.c.SweepPendingResticKeys(t.Context()); err != nil || n != 0 {
		t.Fatalf("got %d, %v", n, err)
	}
}
