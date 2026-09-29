package operations

import (
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/moby/moby/api/types/container"
)

func TestRemoveCollectsExitedOrphanBackupJob(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	job := h.c.Names.Labels(runtime.RoleBackup, map[string]string{runtime.LabelAppID: app.ID, runtime.LabelOperation: "op_crashed"})
	if _, err := h.fake.Create(ctx, docker.ContainerSpec{Name: h.c.Names.BackupContainer("sqlite-orphan"), Config: &container.Config{Labels: job}}); err != nil {
		t.Fatal(err)
	}
	h.mustSucceed(h.c.RemoveApp(ctx, app.ID, "delete shop", ""))
	if left, _ := h.fake.List(ctx, map[string]string{runtime.LabelAppID: app.ID}); len(left) != 0 {
		t.Fatalf("%d containers remain", len(left))
	}
}

func TestRemoveRefusesWhileBackupJobRuns(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	job := h.c.Names.Labels(runtime.RoleBackup, map[string]string{runtime.LabelAppID: app.ID})
	id, err := h.fake.Create(ctx, docker.ContainerSpec{Name: h.c.Names.BackupContainer("sqlite-live"), Config: &container.Config{Labels: job}})
	if err != nil {
		t.Fatal(err)
	}
	h.fake.SetRunning(id, true)
	op, err := h.c.RemoveApp(ctx, app.ID, "delete shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "containers-remain" {
		t.Fatalf("state %s code %s", got.State, got.ErrorCode)
	}
}
