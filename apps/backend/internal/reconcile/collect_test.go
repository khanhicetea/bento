package reconcile

import (
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/testutil"
	"github.com/moby/moby/api/types/container"
)

func addEphemeral(t *testing.T, h *testutil.Harness, name string, labels map[string]string, state string, age time.Duration) {
	t.Helper()
	id, err := h.Fake.Create(t.Context(), docker.ContainerSpec{Name: name, Config: &container.Config{Labels: labels}})
	if err != nil {
		t.Fatal(err)
	}
	c := h.Fake.Containers[id]
	c.State = state
	c.Created = time.Now().Add(-age)
}

func exists(h *testutil.Harness, name string) bool {
	for _, c := range h.Fake.Containers {
		if c.Name == name {
			return true
		}
	}
	return false
}

func TestCollectToolsHonoursGraceAndActiveOperations(t *testing.T) {
	h, r, app := setup(t)
	ctx := t.Context()
	n := h.C.Names
	active, _, err := store.InsertOperation(ctx, h.Store.DB(), store.Operation{ID: platform.NewOperationID(), Kind: "app.exec", TargetKind: "app", TargetID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkRunning(ctx, h.Store.DB(), active.ID); err != nil {
		t.Fatal(err)
	}
	tool := func(op string) map[string]string {
		return n.Labels(runtime.RoleTool, map[string]string{runtime.LabelAppID: app.ID, runtime.LabelOperation: op})
	}
	foreign := runtime.Names{StackID: "sother", StackName: "x"}
	addEphemeral(t, h, "young-created", tool("op_gone"), "created", 10*time.Second)
	addEphemeral(t, h, "old-created-active-op", tool(active.ID), "created", time.Hour)
	addEphemeral(t, h, "old-exited-tool", tool("op_gone"), "exited", time.Hour)
	addEphemeral(t, h, "old-probe", n.Labels(runtime.RoleProbe, nil), "created", time.Hour)
	addEphemeral(t, h, "young-probe", n.Labels(runtime.RoleProbe, nil), "created", time.Second)
	addEphemeral(t, h, "old-backup-job", n.Labels(runtime.RoleBackup, map[string]string{runtime.LabelOperation: "op_gone"}), "exited", time.Hour)
	addEphemeral(t, h, "foreign-probe", foreign.Labels(runtime.RoleProbe, nil), "exited", time.Hour)
	addEphemeral(t, h, "unmanaged-probe", map[string]string{runtime.LabelStackID: n.StackID, runtime.LabelRole: "probe"}, "exited", time.Hour)

	if err := r.collectTools(ctx); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]bool{
		"young-created": true, "old-created-active-op": true, "young-probe": true,
		"foreign-probe": true, "unmanaged-probe": true,
		"old-exited-tool": false, "old-probe": false, "old-backup-job": false,
	} {
		if got := exists(h, name); got != want {
			t.Errorf("%s exists=%v, want %v", name, got, want)
		}
	}
	if !exists(h, n.AppContainer(app.ID)) {
		t.Fatal("app runtime container must never be collected")
	}
}

// Restic and SQLite snapshot jobs idle while running; a crashed backend
// leaves them running, so a running backup job of a finished operation is
// collected. Running jobs of active operations, and labels that are not
// operation ids (volume transfers), are left alone.
func TestCollectToolsRemovesRunningJobsOfFinishedOperations(t *testing.T) {
	h, r, _ := setup(t)
	ctx := t.Context()
	n := h.C.Names
	op := func(finish bool) string {
		o, _, err := store.InsertOperation(ctx, h.Store.DB(), store.Operation{ID: platform.NewOperationID(), Kind: "restic.backup", TargetKind: "app", TargetID: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkRunning(ctx, h.Store.DB(), o.ID); err != nil {
			t.Fatal(err)
		}
		if finish {
			if err := store.FinishOperation(ctx, h.Store.DB(), o.ID, store.OpInterrupted, nil, "interrupted", "x", ""); err != nil {
				t.Fatal(err)
			}
		}
		return o.ID
	}
	job := func(id string) map[string]string {
		return n.Labels(runtime.RoleBackup, map[string]string{runtime.LabelOperation: id})
	}
	addEphemeral(t, h, "running-job-finished-op", job(op(true)), "running", time.Hour)
	addEphemeral(t, h, "running-job-active-op", job(op(false)), "running", time.Hour)
	addEphemeral(t, h, "running-volume-transfer", job("vol-abcde"), "running", time.Hour)
	addEphemeral(t, h, "running-tool-finished-op", n.Labels(runtime.RoleTool, map[string]string{runtime.LabelOperation: op(true)}), "running", time.Hour)

	if err := r.collectTools(ctx); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"running-job-finished-op": false, "running-job-active-op": true,
		"running-volume-transfer": true, "running-tool-finished-op": true,
	} {
		if got := exists(h, name); got != want {
			t.Errorf("%s exists=%v, want %v", name, got, want)
		}
	}
}
