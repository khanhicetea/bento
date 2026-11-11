package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

func TestTruncateIsRuneSafe(t *testing.T) {
	s := strings.Repeat("é", 10) // 2 bytes each
	for n := 0; n <= len(s); n++ {
		got := truncate(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("n=%d produced invalid UTF-8 %q", n, got)
		}
		if n < len(s) && len(strings.TrimSuffix(got, "…")) > n {
			t.Fatalf("n=%d exceeded limit: %q", n, got)
		}
	}
	if truncate("abc", 5) != "abc" {
		t.Fatal("short strings must be unchanged")
	}
}

func TestAppendEventKeepsTailWithMarker(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	op, _, err := InsertOperation(ctx, s.DB(), Operation{ID: platform.NewOperationID(), Kind: "app.start", TargetKind: "app", TargetID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	total := maxEventsPerOperation + 50
	for i := 1; i <= total; i++ {
		msg := "event"
		if i == total {
			msg = "final error"
		}
		if err := AppendEvent(ctx, s.DB(), op.ID, "info", msg); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := ListEvents(ctx, s.DB(), op.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != maxEventsPerOperation {
		t.Fatalf("kept %d events", len(evs))
	}
	if !strings.HasPrefix(evs[0].Message, TruncatedEventMessage) {
		t.Fatalf("first event should be truncation marker, got %q", evs[0].Message)
	}
	last := evs[len(evs)-1]
	if last.Message != "final error" || last.Seq != total {
		t.Fatalf("last event %+v", last)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatal("seqs must be increasing")
		}
	}
}

func TestNextQueuedIsInsertionOrder(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	ids := []string{"op-zzz", "op-aaa", "op-mmm"}
	for _, id := range ids {
		if _, _, err := InsertOperation(ctx, s.DB(), Operation{ID: id, Kind: "k", TargetKind: "app", TargetID: id}); err != nil {
			t.Fatal(err)
		}
	}
	// Force identical timestamps so only insertion order can decide.
	if _, err := s.DB().ExecContext(ctx, "UPDATE operations SET created_at='2026-01-01T00:00:00.000Z'"); err != nil {
		t.Fatal(err)
	}
	for _, want := range ids {
		o, err := NextQueued(ctx, s.DB())
		if err != nil || o.ID != want {
			t.Fatalf("want %s got %s %v", want, o.ID, err)
		}
		if err := FinishOperation(ctx, s.DB(), o.ID, OpSucceeded, nil, "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPruneHistory(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	db := s.DB()
	old := platform.FormatTime(time.Now().Add(-40 * 24 * time.Hour))
	mk := func(id string, state OpState, finished string) {
		if _, _, err := InsertOperation(ctx, db, Operation{ID: id, Kind: "k", TargetKind: "app", TargetID: id}); err != nil {
			t.Fatal(err)
		}
		if err := AppendEvent(ctx, db, id, "info", "x"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE operations SET state=?, finished_at=NULLIF(?, '') WHERE id=?", state, finished, id); err != nil {
			t.Fatal(err)
		}
	}
	mk("old-done", OpSucceeded, old)
	mk("new-done", OpFailed, platform.FormatTime(time.Now()))
	mk("old-queued", OpQueued, "")
	ops, err := PruneHistory(ctx, db, DefaultRetention)
	if err != nil || ops != 1 {
		t.Fatalf("pruned ops=%d err=%v", ops, err)
	}
	if _, err := GetOperation(ctx, db, "old-done"); err == nil {
		t.Fatal("old finished op should be pruned")
	}
	var n int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM operation_events WHERE operation_id='old-done'").Scan(&n)
	if n != 0 {
		t.Fatal("events of pruned op must be deleted")
	}
	for _, id := range []string{"new-done", "old-queued"} {
		if _, err := GetOperation(ctx, db, id); err != nil {
			t.Fatalf("%s must be kept: %v", id, err)
		}
	}
}

// TestOperationTargetQueryPlans records how SQLite answers the per-target
// operation lookups and pins that the hot reconciler query (ActiveForTarget)
// never degrades to a full table scan. Timings are logged, not asserted.
func TestOperationTargetQueryPlans(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	const targets, terminal = 50, 5000
	err := s.Tx(ctx, func(tx Q) error {
		for i := 0; i < terminal+3; i++ {
			op, _, err := InsertOperation(ctx, tx, Operation{
				ID:         platform.NewOperationID(),
				Kind:       "app.reconcile",
				TargetKind: "app",
				TargetID:   fmt.Sprintf("app%02d", i%targets),
			})
			if err != nil {
				return err
			}
			state := OpSucceeded
			switch {
			case i >= terminal:
				state = OpQueued
				if i == terminal+2 {
					state = OpRunning
				}
			case i%7 == 0:
				state = OpFailed
			}
			if _, err := tx.ExecContext(ctx, "UPDATE operations SET state = ? WHERE id = ?", state, op.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, "ANALYZE"); err != nil {
		t.Fatal(err)
	}

	const target = "app01"
	statements := []struct{ name, sql string }{
		{"ActiveForTarget", "SELECT COUNT(*) FROM operations WHERE target_id = ? AND state IN ('queued','running')"},
		{"ListOperations{TargetID, Limit:100}", "SELECT " + opColumns + " FROM operations WHERE 1=1 AND target_id = ? ORDER BY created_at DESC, id DESC LIMIT ?"},
		{"ListOperations{TargetID, States:[succeeded], Limit:1}", "SELECT " + opColumns + " FROM operations WHERE 1=1 AND target_id = ? AND state IN (?) ORDER BY created_at DESC, id DESC LIMIT ?"},
	}
	argsFor := []([]any){{target}, {target, 100}, {target, OpSucceeded, 1}}
	for i, st := range statements {
		rows, err := s.DB().QueryContext(ctx, "EXPLAIN QUERY PLAN "+st.sql, argsFor[i]...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s plan: %s", st.name, strings.Join(details, " | "))
		if i == 0 {
			for _, d := range details {
				if strings.Contains(d, "SCAN operations") && !strings.Contains(d, "USING") {
					t.Fatalf("ActiveForTarget scans the whole operations table: %q", d)
				}
			}
		}
	}

	start := time.Now()
	for i := 0; i < 1000; i++ {
		if _, err := ActiveForTarget(ctx, s.DB(), target); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("ActiveForTarget mean over 1000 calls: %v", time.Since(start)/1000)
	start = time.Now()
	for i := 0; i < 100; i++ {
		if _, err := ListOperations(ctx, s.DB(), OpFilter{TargetID: target, Limit: 100}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("ListOperations{TargetID, Limit:100} mean over 100 calls: %v", time.Since(start)/100)
	start = time.Now()
	for i := 0; i < 100; i++ {
		if _, err := ListOperations(ctx, s.DB(), OpFilter{TargetID: target, States: []OpState{OpSucceeded}, Limit: 1}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("ListOperations{TargetID, States:[succeeded], Limit:1} mean over 100 calls: %v", time.Since(start)/100)
}
