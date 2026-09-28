package store

import (
	"context"
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
	ctx := context.Background()
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
	ctx := context.Background()
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
	ctx := context.Background()
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
	if err := InsertBackupRun(ctx, db, BackupRun{ID: "b-old", Trigger: "manual", State: "succeeded", StartedAt: old}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE backup_runs SET finished_at=? WHERE id='b-old'", old); err != nil {
		t.Fatal(err)
	}
	if err := InsertBackupRun(ctx, db, BackupRun{ID: "b-running", Trigger: "manual", State: "running", StartedAt: old}); err != nil {
		t.Fatal(err)
	}
	ops, runs, err := PruneHistory(ctx, db, DefaultRetention)
	if err != nil || ops != 1 || runs != 1 {
		t.Fatalf("pruned ops=%d runs=%d err=%v", ops, runs, err)
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
	rs, _ := ListBackupRuns(ctx, db, 10)
	if len(rs) != 1 || rs[0].ID != "b-running" {
		t.Fatalf("backup runs after prune: %+v", rs)
	}
}
