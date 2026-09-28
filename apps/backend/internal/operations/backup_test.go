package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
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
	ctx := context.Background()
	good, bad, empty := h.createApp("good"), h.createApp("bad"), h.createApp("empty")
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
		if err := os.WriteFile(filepath.Join(dir, "sqlite-"+b.SQLiteFileID+"-20200101T000000Z.db"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = empty // no database file on the host: must be skipped, not failed
	if err := h.c.SetBackupSchedule(ctx, domain.BackupSchedule{Cron: "0 3 * * *", Compression: "none", Retain: 1}); err != nil {
		t.Fatal(err)
	}
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

	op, err := h.c.SubmitBackup(ctx, BackupRequest{Scope: "all", Compression: "none"}, "backup-partial-1")
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
	// Retention pruned only the successful series.
	goodFiles, _ := os.ReadDir(filepath.Join(h.layout.BackupsDir(), "good"))
	badFiles, _ := os.ReadDir(filepath.Join(h.layout.BackupsDir(), "bad"))
	if len(goodFiles) != 1 || strings.Contains(goodFiles[0].Name(), "2020") {
		t.Fatalf("good series not retained to newest: %v", goodFiles)
	}
	if len(badFiles) != 1 || !strings.Contains(badFiles[0].Name(), "2020") {
		t.Fatalf("failed series must keep its older artifact: %v", badFiles)
	}
}

func TestBackupAllTargetsFailed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	app := h.createApp("solo")
	b := sqliteBinding(t, app)
	if err := os.WriteFile(filepath.Join(h.layout.SQLiteFileDir(b.SQLiteFileID), app.Slug+".db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.fake.FailOn = map[string]error{"Create": os.ErrPermission}
	op, err := h.c.SubmitBackup(ctx, BackupRequest{Scope: "all", Compression: "none"}, "backup-fail-1")
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
