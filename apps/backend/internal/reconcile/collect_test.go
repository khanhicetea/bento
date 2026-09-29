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
