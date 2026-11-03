package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestDeployToolLifetimeCoversFetchAndScript(t *testing.T) {
	if deployToolLifetime <= deployTimeout+deployScriptTimeout {
		t.Fatalf("tool lifetime %s does not cover fetch %s + script %s", deployToolLifetime, deployTimeout, deployScriptTimeout)
	}
}

func sqliteBinding(t *testing.T, app domain.App) domain.Binding {
	t.Helper()
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite {
			return b
		}
	}
	t.Fatalf("app %s has no sqlite binding", app.Slug)
	return domain.Binding{}
}

func TestRcloneTestOperation(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if _, err := h.c.SubmitRcloneTest(ctx, "", ""); err == nil {
		t.Fatal("test without any remote must be refused")
	}
	if err := os.MkdirAll(h.layout.RcloneDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.layout.RcloneDir(), "rclone.conf"), []byte("[s3]\ntype = s3\nsecret_access_key = hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	op, err := h.c.SubmitRcloneTest(ctx, "s3:bucket/bento", "rclone-test-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpSucceeded {
		t.Fatalf("op: %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}

	// Each external effect fails the test cleanly and leaves no container.
	for _, method := range []string{"Create", "Start"} {
		h.fake.FailOn = map[string]error{method: os.ErrPermission}
		op, err := h.c.SubmitRcloneTest(ctx, "s3:bucket", "rclone-test-"+method)
		if err != nil {
			t.Fatal(err)
		}
		if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "rclone-test-failed" {
			t.Fatalf("%s: %s %s %s", method, got.State, got.ErrorCode, got.ErrorMessage)
		}
		if len(h.fake.Containers) != 0 {
			t.Fatalf("%s: container left behind", method)
		}
	}

	op, err = h.c.SubmitRcloneTest(ctx, "gdrive:bento", "rclone-test-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || !strings.Contains(got.ErrorMessage, "not configured") {
		t.Fatalf("unknown remote: %s %s", got.State, got.ErrorMessage)
	}
}

// scheduleTestKind is a schedule kind for scheduler tests: it queues an
// rclone test of a remote that is not configured (the operation fails fast).
const scheduleTestKind = "test-schedule"

func addTestSchedule(t *testing.T, h *harness, id, cron string) store.Schedule {
	t.Helper()
	scheduleKinds[scheduleTestKind] = scheduleKind{Submit: func(ctx context.Context, c *Controller, s store.Schedule) (store.Operation, error) {
		op, _, err := c.Submit(ctx, Submission{Kind: KindRcloneTest, TargetKind: "backup", TargetID: s.ID,
			Request: RcloneTestRequest{Remote: "none:" + s.ID}, Origin: "schedule"})
		return op, err
	}}
	t.Cleanup(func() { delete(scheduleKinds, scheduleTestKind) })
	putTestSchedule(t, h, store.Schedule{ID: id, Kind: scheduleTestKind, Cron: cron, Enabled: true})
	return getSchedule(t, h, id)
}

func putTestSchedule(t *testing.T, h *harness, s store.Schedule) {
	t.Helper()
	now := platform.FormatTime(time.Now())
	s.LastSlot = now
	if err := store.PutSchedule(t.Context(), h.store.DB(), s, now); err != nil {
		t.Fatal(err)
	}
}

func getSchedule(t *testing.T, h *harness, id string) store.Schedule {
	t.Helper()
	row, err := store.GetSchedule(t.Context(), h.store.DB(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// Cron fields are server wall-clock time. LastSlot is stored in UTC, so a
// UTC+07 server must still fire "30 9 * * *" at 09:30 local, not 16:30.
func TestScheduleUsesServerLocalTime(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	ict := time.FixedZone("ICT", 7*3600)
	s := addTestSchedule(t, h, "daily", "30 9 * * *")
	before := time.Date(2026, 9, 28, 9, 29, 0, 0, ict)
	if got := NextRun(s, before); !got.Equal(time.Date(2026, 9, 28, 9, 30, 0, 0, ict)) {
		t.Fatalf("next run %s", got)
	}
	if !NextRun(store.Schedule{Cron: s.Cron}, before).IsZero() {
		t.Fatal("a disabled schedule has no next run")
	}
	setLastSlot(t, h, s.ID, before)
	h.c.checkSchedules(ctx, before.Add(30*time.Second))
	if st := getSchedule(t, h, s.ID); st.LastRunAt != "" {
		t.Fatalf("fired early: %+v", st)
	}
	h.c.checkSchedules(ctx, time.Date(2026, 9, 28, 9, 30, 20, 0, ict))
	st := getSchedule(t, h, s.ID)
	if st.LastState != "submitted" || st.LastOpID == "" || st.LastSlot != "2026-09-28T02:30:00.000Z" {
		t.Fatalf("did not fire at 09:30 local: %+v", st)
	}
	op, err := store.GetOperation(ctx, h.store.DB(), st.LastOpID)
	if err != nil || op.TargetID != s.ID || op.Origin != "schedule" {
		t.Fatalf("scheduled op %+v %v", op, err)
	}
	// The next slot is skipped, not stacked, while that run is still active.
	// The executor may already have finished it, so hold it running here.
	h.wait(op)
	if _, err := h.store.DB().ExecContext(ctx, "UPDATE operations SET state = 'running' WHERE id = ?", op.ID); err != nil {
		t.Fatal(err)
	}
	h.c.checkSchedules(ctx, time.Date(2026, 9, 29, 9, 30, 20, 0, ict))
	if st2 := getSchedule(t, h, s.ID); st2.LastState != "skipped" || st2.LastOpID != st.LastOpID || st2.Missed != 1 {
		t.Fatalf("overlapping run not skipped: %+v", st2)
	}
}

// Slots missed while the backend was down are recorded, not replayed, and an
// edit made while a check is in flight is never overwritten by bookkeeping.
func TestScheduleMissedSlotAndConcurrentEdit(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	sc := addTestSchedule(t, h, "hourly", "0 * * * *")
	start := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	setLastSlot(t, h, sc.ID, start)
	h.c.checkSchedules(ctx, start.Add(3*time.Hour+30*time.Minute))
	st := getSchedule(t, h, sc.ID)
	if st.LastState != "missed" || st.LastOpID != "" || st.Missed != 3 {
		t.Fatalf("missed slots: %+v", st)
	}
	putTestSchedule(t, h, store.Schedule{ID: sc.ID, Kind: scheduleTestKind, Cron: "0 5 * * *", Enabled: true})
	st.LastState = "stale"
	if ok, err := store.SaveScheduleState(ctx, h.store.DB(), st); err != nil || ok {
		t.Fatalf("stale bookkeeping overwrote an edit: %v %v", ok, err)
	}
}

func setLastSlot(t *testing.T, h *harness, id string, at time.Time) {
	t.Helper()
	if _, err := h.store.DB().ExecContext(t.Context(), "UPDATE schedules SET last_slot = ? WHERE id = ?",
		platform.FormatTime(at), id); err != nil {
		t.Fatal(err)
	}
}
