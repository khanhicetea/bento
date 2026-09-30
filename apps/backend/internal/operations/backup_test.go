package operations

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
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

func TestBackupContinuesPastFailingTarget(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	good, bad, empty := h.createApp("good"), h.createApp("bad"), h.createApp("empty")
	sched := saveSchedule(t, h, domain.BackupSchedule{Name: "nightly", Cron: "0 3 * * *", Retain: 1})
	for _, app := range []domain.App{good, bad} {
		b := sqliteBinding(t, app)
		if err := os.WriteFile(filepath.Join(h.layout.SQLiteFileDir(b.SQLiteFileID), app.Slug+".db"), []byte("db"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Seed an older artifact in each series.
		dir := filepath.Join(h.layout.BackupsDir(), app.Slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"-20200101T000000.000Z~" + backup.ScheduleTag(sched.ID) + ".db", "-20190101T000000.000Z.db"} {
			if err := os.WriteFile(filepath.Join(dir, "sqlite-"+b.SQLiteFileID+n), []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = empty // no database file on the host: must be skipped, not failed
	badDir := sqliteBinding(t, bad).SQLiteContainerDir()
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) == 0 || req.Cmd[0] != "sqlite3" {
			return docker.ExecResult{Stdout: []byte("ready")}
		}
		if strings.HasPrefix(req.Cmd[len(req.Cmd)-2], badDir+"/") {
			return docker.ExecResult{ExitCode: 1, Stderr: []byte("disk I/O error")}
		}
		c := h.fake.Containers[id]
		for _, m := range c.Spec.HostConfig.Mounts {
			if m.Target == "/bento-backup-out" {
				_ = os.WriteFile(filepath.Join(m.Source, "snapshot.db"), []byte("snap"), 0o600)
			}
		}
		return docker.ExecResult{}
	}

	op, err := h.c.SubmitBackup(ctx, BackupRequest{Scope: "all", Compression: "gzip", ScheduleID: sched.ID}, "backup-partial-1")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || got.ErrorCode != "backup-partial" || !strings.Contains(got.ErrorMessage, "bad/") || strings.Contains(got.ErrorMessage, "empty/") {
		t.Fatalf("op: %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	runs, err := store.ListBackupRuns(ctx, h.store.DB(), 5)
	if err != nil || len(runs) != 1 || runs[0].State != "partial" || len(runs[0].Artifacts) != 1 || !strings.HasPrefix(runs[0].Artifacts[0], "good/") {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	// Retention pruned only the successful series of this schedule; the
	// untagged (manual) artifacts are never pruned.
	goodFiles, _ := os.ReadDir(filepath.Join(h.layout.BackupsDir(), "good"))
	badFiles, _ := os.ReadDir(filepath.Join(h.layout.BackupsDir(), "bad"))
	if len(goodFiles) != 2 || !strings.Contains(goodFiles[0].Name(), "2019") || strings.Contains(goodFiles[1].Name(), "2020") ||
		!strings.HasSuffix(goodFiles[1].Name(), "~"+backup.ScheduleTag(sched.ID)+".db.gz") {
		t.Fatalf("good series not retained to newest: %v", goodFiles)
	}
	if len(badFiles) != 2 {
		t.Fatalf("failed series must keep its older artifact: %v", badFiles)
	}
}

func TestBackupAllTargetsFailed(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	app := h.createApp("solo")
	b := sqliteBinding(t, app)
	if err := os.WriteFile(filepath.Join(h.layout.SQLiteFileDir(b.SQLiteFileID), app.Slug+".db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.fake.FailOn = map[string]error{"Create": os.ErrPermission}
	op, err := h.c.SubmitBackup(ctx, BackupRequest{Scope: "all"}, "backup-fail-1")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || got.ErrorCode != "backup-failed" {
		t.Fatalf("op: %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	runs, _ := store.ListBackupRuns(ctx, h.store.DB(), 5)
	if len(runs) != 1 || runs[0].State != "failed" {
		t.Fatalf("runs: %+v", runs)
	}
}

func TestBackupValidation(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	app := h.createApp("shop")
	db := sqliteBinding(t, app).SQLiteFileID
	bad := []BackupRequest{
		{Scope: "all", Compression: "none"},
		{Scope: "all", Remote: "s3:b --config=/x"},
		{Scope: "database", AppID: app.ID},
		{Scope: "database", AppID: app.ID, Databases: []string{"other"}},
		{Scope: "app", AppID: "missing"},
		{Scope: "bogus"},
	}
	for _, req := range bad {
		if _, err := h.c.SubmitBackup(ctx, req, ""); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
	if _, err := h.c.SubmitBackup(ctx, BackupRequest{Scope: "database", AppID: app.ID, Databases: []string{db}}, ""); err != nil {
		t.Fatal(err)
	}
	for _, s := range []domain.BackupSchedule{
		{Name: "x", Cron: "0 3 * * *", Retain: 1, RcloneRemote: "s3:b --config=/x"},
		{Name: "x", Cron: "0 3 * * *", Retain: 1, Compression: "none"},
		{Name: "x", Cron: "0 3 * * *", Retain: 0},
		{Name: "", Cron: "0 3 * * *", Retain: 1},
		{Name: "x", Cron: "bad", Retain: 1},
		{Name: "x", Cron: "0 3 * * *", Retain: 1, Scope: "database", AppID: app.ID},
		{ID: "backup-missing", Name: "x", Cron: "0 3 * * *", Retain: 1},
	} {
		if _, err := h.c.SaveBackupSchedule(ctx, s); err == nil {
			t.Errorf("schedule accepted %+v", s)
		}
	}
}

// Several schedules coexist, each with its own scope, and can be toggled
// and deleted independently.
func TestMultipleBackupSchedules(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	app := h.createApp("shop")
	db := sqliteBinding(t, app).SQLiteFileID
	a := saveSchedule(t, h, domain.BackupSchedule{Name: "all", Enabled: true, Cron: "0 3 * * *", Retain: 7})
	b := saveSchedule(t, h, domain.BackupSchedule{Name: "shop db", Cron: "0 * * * *", Retain: 24, Scope: "database",
		AppID: app.ID, Databases: []string{db}, Compression: "gzip", RcloneRemote: "s3:bucket/shop"})
	list, err := h.c.BackupSchedules(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	if b.Scope != "database" || b.Databases[0] != db || b.Compression != "gzip" || a.Compression != "zstd" {
		t.Fatalf("saved %+v", b)
	}
	v, err := h.c.SetBackupScheduleEnabled(ctx, b.ID, true)
	if err != nil || !v.Enabled || v.RcloneRemote != "s3:bucket/shop" {
		t.Fatalf("enable %+v %v", v, err)
	}
	start := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	setLastSlot(t, h, b.ID, start)
	h.c.checkSchedules(ctx, start.Add(time.Hour+10*time.Second))
	v, _ = h.c.BackupSchedule(ctx, b.ID)
	op, err := store.GetOperation(ctx, h.store.DB(), v.State.LastOpID)
	if err != nil || !strings.Contains(string(op.Request), `"scope":"database"`) ||
		!strings.Contains(string(op.Request), `"remote":"s3:bucket/shop"`) {
		t.Fatalf("scheduled op %s %v", op.Request, err)
	}
	if err := h.c.DeleteBackupSchedule(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.c.DeleteBackupSchedule(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
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

// Cron fields are server wall-clock time. LastSlot is stored in UTC, so a
// UTC+07 server must still fire "30 9 * * *" at 09:30 local, not 16:30.
func TestScheduleUsesServerLocalTime(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	ict := time.FixedZone("ICT", 7*3600)
	s := saveSchedule(t, h, domain.BackupSchedule{Name: "daily", Enabled: true, Cron: "30 9 * * *", Retain: 1}).BackupSchedule
	before := time.Date(2026, 9, 28, 9, 29, 0, 0, ict)
	if got := NextBackup(s, before); !got.Equal(time.Date(2026, 9, 28, 9, 30, 0, 0, ict)) {
		t.Fatalf("next run %s", got)
	}
	if !NextBackup(domain.BackupSchedule{Cron: s.Cron}, before).IsZero() {
		t.Fatal("a disabled schedule has no next run")
	}
	setLastSlot(t, h, s.ID, before)
	h.c.checkSchedules(ctx, before.Add(30*time.Second))
	if v, _ := h.c.BackupSchedule(ctx, s.ID); v.State.LastRun != "" {
		t.Fatalf("fired early: %+v", v.State)
	}
	h.c.checkSchedules(ctx, time.Date(2026, 9, 28, 9, 30, 20, 0, ict))
	v, err := h.c.BackupSchedule(ctx, s.ID)
	st := v.State
	if err != nil || st.LastState != "submitted" || st.LastOpID == "" || st.LastSlot != "2026-09-28T02:30:00.000Z" {
		t.Fatalf("did not fire at 09:30 local: %+v %v", st, err)
	}
	op, err := store.GetOperation(ctx, h.store.DB(), st.LastOpID)
	if err != nil || !strings.Contains(string(op.Request), `"scheduleId":"`+s.ID+`"`) {
		t.Fatalf("scheduled op %s %v", op.Request, err)
	}
	// The next slot is skipped, not stacked, while that run is still queued.
	h.c.checkSchedules(ctx, time.Date(2026, 9, 29, 9, 30, 20, 0, ict))
	v2, _ := h.c.BackupSchedule(ctx, s.ID)
	if st2 := v2.State; st2.LastState != "skipped" || st2.LastOpID != st.LastOpID || st2.Missed != 1 {
		t.Fatalf("overlapping run not skipped: %+v", st2)
	}
}

// Slots missed while the backend was down are recorded, not replayed, and an
// edit made while a check is in flight is never overwritten by bookkeeping.
func TestScheduleMissedSlotAndConcurrentEdit(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	sc := saveSchedule(t, h, domain.BackupSchedule{Name: "hourly", Enabled: true, Cron: "0 * * * *", Retain: 1})
	start := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	setLastSlot(t, h, sc.ID, start)
	h.c.checkSchedules(ctx, start.Add(3*time.Hour+30*time.Minute))
	v, _ := h.c.BackupSchedule(ctx, sc.ID)
	st := v.State
	if st.LastState != "missed" || st.LastOpID != "" || st.Missed != 3 {
		t.Fatalf("missed slots: %+v", st)
	}
	row, err := store.GetSchedule(ctx, h.store.DB(), sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	sc.Cron = "0 5 * * *"
	saveSchedule(t, h, sc.BackupSchedule)
	row.LastState = "stale"
	if ok, err := store.SaveScheduleState(ctx, h.store.DB(), row); err != nil || ok {
		t.Fatalf("stale bookkeeping overwrote an edit: %v %v", ok, err)
	}
}

func saveSchedule(t *testing.T, h *harness, s domain.BackupSchedule) BackupScheduleView {
	t.Helper()
	v, err := h.c.SaveBackupSchedule(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func setLastSlot(t *testing.T, h *harness, id string, at time.Time) {
	t.Helper()
	if _, err := h.store.DB().ExecContext(t.Context(), "UPDATE schedules SET last_slot = ? WHERE id = ?",
		platform.FormatTime(at), id); err != nil {
		t.Fatal(err)
	}
}
