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
	addEphemeral(t, h, "running-job-finished-op", job(op(true)), "running", 10*time.Minute)
	addEphemeral(t, h, "running-job-active-op", job(op(false)), "running", 10*time.Minute)
	addEphemeral(t, h, "running-volume-transfer", job("vol-abcde"), "running", 10*time.Minute)
	addEphemeral(t, h, "running-tool-finished-op", n.Labels(runtime.RoleTool, map[string]string{runtime.LabelOperation: op(true)}), "running", 10*time.Minute)
	// A backend killed mid-deploy leaves its tool container running the
	// script: once a new backend runs, no session can own it.
	addEphemeral(t, h, "running-tool-previous-backend", n.Labels(runtime.RoleTool, map[string]string{runtime.LabelOperation: "tddc3ddd465"}), "running", time.Hour)
	addEphemeral(t, h, "running-probe-previous-backend", n.Labels(runtime.RoleProbe, nil), "running", time.Hour)
	// A SQLite dump job is labelled with its tool session, not an operation.
	addEphemeral(t, h, "running-sqlite-job-previous-backend", job("t0a1b2c3d4e"), "running", time.Hour)
	r.started = time.Now().Add(-30 * time.Minute)

	if err := r.collectTools(ctx); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"running-job-finished-op": false, "running-job-active-op": true,
		"running-volume-transfer": true, "running-tool-finished-op": true,
		"running-tool-previous-backend": false, "running-probe-previous-backend": false,
		"running-sqlite-job-previous-backend": false,
	} {
		if got := exists(h, name); got != want {
			t.Errorf("%s exists=%v, want %v", name, got, want)
		}
	}
}

// A tool container created seconds before the backend restarted is still
// removed at once: the grace period protects containers of this process
// between Create and Start, never those of a previous one.
func TestCollectToolsSkipsGraceForPreviousBackend(t *testing.T) {
	h, r, _ := setup(t)
	n := h.C.Names
	addEphemeral(t, h, "tool-before-restart", n.Labels(runtime.RoleTool, nil), "running", 30*time.Second)
	addEphemeral(t, h, "tool-after-restart", n.Labels(runtime.RoleTool, nil), "running", 5*time.Second)
	r.started = time.Now().Add(-15 * time.Second)
	if err := r.collectTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	if exists(h, "tool-before-restart") || !exists(h, "tool-after-restart") {
		t.Fatalf("before restart exists=%v (want false), after restart exists=%v (want true)",
			exists(h, "tool-before-restart"), exists(h, "tool-after-restart"))
	}
}

// A removed app's target (its backoff and last error) is forgotten.
func TestPassForgetsRemovedApps(t *testing.T) {
	h, r, app := setup(t)
	ctx := t.Context()
	r.passMu.Lock()
	r.beginPass()
	r.get(app.ID, app.ConfigGeneration).LastError = "boom"
	r.get("a0000000gone0", 1).LastError = "left over"
	r.endPass()
	r.passMu.Unlock()
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.targets["a0000000gone0"]; ok {
		t.Fatal("the target of an app that no longer exists was kept")
	}
	if _, ok := r.targets[app.ID]; !ok {
		t.Fatal("a live app's target was dropped")
	}
	_ = h
}
