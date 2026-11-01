package operations

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// breakEdge makes applyEdge fail: either a persisted invalid bind (settings
// written before validation was strict) or an injected engine fault.
func breakEdge(t *testing.T, h *harness, mode string) {
	t.Helper()
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	switch mode {
	case "bad-bind":
		s.Bind = "1.2.3.4x"
	case "pull-fails":
		h.fake.Images = map[string]string{}
		h.fake.FailOn = map[string]error{"PullImage": errors.New("injected PullImage failure")}
	}
	if err := store.PutSetting(t.Context(), h.store.DB(), edgeSettingKey, s); err != nil {
		t.Fatal(err)
	}
}

func hasWarning(h *harness, opID, substr string) bool {
	events, _ := store.ListEvents(h.t.Context(), h.store.DB(), opID, 0)
	return slices.ContainsFunc(events, func(e store.OpEvent) bool {
		return e.Level == "warn" && strings.Contains(e.Message, substr)
	})
}

func TestStopAndRemoveSucceedWithBrokenEdge(t *testing.T) {
	for _, mode := range []string{"bad-bind", "pull-fails"} {
		t.Run(mode+"/stop", func(t *testing.T) {
			h := newHarness(t)
			app := h.createApp("shop")
			ctx := t.Context()
			h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
			breakEdge(t, h, mode)
			op := h.mustSucceed(h.c.StopApp(ctx, app.ID, ""))
			if !hasWarning(h, op.ID, "edge route removal failed") {
				t.Fatal("stop must warn about the edge failure")
			}
			list, _ := h.fake.List(ctx, map[string]string{"io.bento.app-id": app.ID})
			for _, c := range list {
				if c.State == "running" {
					t.Fatal("instance must be stopped despite the broken edge")
				}
			}
		})
		t.Run(mode+"/remove", func(t *testing.T) {
			h := newHarness(t)
			app := h.createApp("shop")
			ctx := t.Context()
			h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
			breakEdge(t, h, mode)
			op := h.mustSucceed(h.c.RemoveApp(ctx, app.ID, "delete shop", ""))
			if !hasWarning(h, op.ID, "edge route removal failed") {
				t.Fatal("remove must warn about the edge failure")
			}
			list, _ := h.fake.List(ctx, map[string]string{"io.bento.app-id": app.ID})
			if len(list) != 0 {
				t.Fatalf("expected no containers after remove, got %d", len(list))
			}
		})
	}
}

func TestConfigureEdgeRejectsInvalidBind(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	for _, bind := range []string{"1.2.3.4x", "010.0.0.1", "1.2.3", "256.0.0.1", "::1", "fe80::1%eth0", " 1.2.3.4", "localhost"} {
		s := domain.DefaultEdgeSettings()
		s.Bind = bind
		_, err := h.c.ConfigureEdge(ctx, s, "")
		if _, ok := errors.AsType[domain.ValidationErrors](err); !ok {
			t.Fatalf("bind %q: expected validation error, got %v", bind, err)
		}
	}
	for _, bind := range []string{"0.0.0.0", "127.0.0.1", "192.168.1.10"} {
		if _, err := parseEdgeBind(bind); err != nil {
			t.Fatalf("bind %q should be valid: %v", bind, err)
		}
	}
}

func TestEdgeSpecRejectsPersistedBadBind(t *testing.T) {
	h := newHarness(t)
	s := domain.DefaultEdgeSettings()
	s.Bind = "010.0.0.1"
	_, err := h.c.edgeSpec(s, NetworkSettings{EdgeIP: "10.0.0.2"})
	if oe, ok := errors.AsType[*OpError](err); !ok || oe.Code != "edge-settings-invalid" {
		t.Fatalf("expected edge-settings-invalid, got %v", err)
	}
}

// The tunnel pulls its image only when missing, recreates only its own
// container on token rotation, and is removed when disabled.
func TestTunnelApplyPullsRotatesAndRemoves(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	name := h.c.Names.TunnelContainer()
	h.mustSucceed(h.c.SetTunnelToken(ctx, strings.Repeat("a", 40), ""))
	if n := h.fake.CallCount("PullImage " + domain.TunnelImage); n != 1 {
		t.Fatalf("missing tunnel image must be pulled once, pulled %d", n)
	}
	first, _ := h.fake.Inspect(ctx, name)
	if first == nil || !first.State.Running {
		t.Fatal("tunnel container not running")
	}
	h.mustSucceed(h.c.SetTunnelToken(ctx, strings.Repeat("b", 40), ""))
	second, _ := h.fake.Inspect(ctx, name)
	if second == nil || second.ID == first.ID || !second.State.Running {
		t.Fatal("token rotation must recreate the tunnel container")
	}
	if n := h.fake.CallCount("PullImage"); n != 1 {
		t.Fatalf("present image must not be pulled again, pulled %d", n)
	}
	h.mustSucceed(h.c.SetTunnelToken(ctx, "", ""))
	if gone, _ := h.fake.Inspect(ctx, name); gone != nil {
		t.Fatal("disabling the tunnel must remove its container")
	}
}

// A failed tunnel image pull fails the operation before any container exists.
func TestTunnelApplyPullFailure(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.fake.FailOn = map[string]error{"PullImage": errors.New("registry down")}
	op, err := h.c.SetTunnelToken(ctx, strings.Repeat("a", 40), "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || !strings.Contains(got.ErrorMessage, "registry down") {
		t.Fatalf("want pull failure, got %s %s", got.State, got.ErrorMessage)
	}
	if h.fake.CallCount("Create") != 0 {
		t.Fatal("no container may be created without the image")
	}
}

// startEdge configures and starts the edge and returns its container id.
func startEdge(t *testing.T, h *harness) string {
	t.Helper()
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	h.mustSucceed(h.c.ConfigureEdge(t.Context(), s, ""))
	ins, _ := h.fake.Inspect(t.Context(), h.c.Names.EdgeContainer())
	if ins == nil || !ins.State.Running {
		t.Fatal("edge not running")
	}
	return ins.ID
}

// simulateEdgeUpgrade makes the running edge look as if an older Bento made
// it (a different generation label) and removes the newly pinned image, as
// on a host that has just installed a release bumping the edge image pin.
func simulateEdgeUpgrade(t *testing.T, h *harness, id string) {
	t.Helper()
	c := h.fake.Containers[id]
	labels := map[string]string{}
	for k, v := range c.Spec.Config.Labels {
		labels[k] = v
	}
	labels[runtime.LabelGeneration] = "from-an-older-release"
	cfg := *c.Spec.Config
	cfg.Labels = labels
	c.Spec.Config = &cfg
	delete(h.fake.Images, domain.EdgeImage)
	h.fake.RequireImages = true
}

func reapplyEdge(t *testing.T, h *harness) (store.Operation, error) {
	t.Helper()
	s, err := h.c.EdgeSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	op, err := h.c.ConfigureEdge(t.Context(), s, "")
	if err != nil {
		return op, err
	}
	return h.wait(op), nil
}

func assertNoPreviousEdge(t *testing.T, h *harness) {
	t.Helper()
	if prev, _ := h.fake.Inspect(t.Context(), h.c.Names.EdgePreviousContainer()); prev != nil {
		t.Fatal("previous edge container left behind")
	}
}

// An upgrade that only bumps the edge image pin renders identical routes; the
// edge must still pull the new image before recreating, or Create fails and
// ingress stays down.
func TestEdgeImageBumpPullsBeforeRecreate(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	oldID := startEdge(t, h)
	simulateEdgeUpgrade(t, h, oldID)
	pulls := h.fake.CallCount("PullImage " + domain.EdgeImage)
	op, err := reapplyEdge(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if op.State != store.OpSucceeded {
		t.Fatalf("edge apply failed: %s: %s", op.ErrorCode, op.ErrorMessage)
	}
	if h.fake.CallCount("PullImage "+domain.EdgeImage) != pulls+1 {
		t.Fatal("the new edge image must be pulled")
	}
	ins, _ := h.fake.Inspect(ctx, h.c.Names.EdgeContainer())
	if ins == nil || ins.ID == oldID || !ins.State.Running {
		t.Fatal("edge must be recreated and running")
	}
	if ins.Config.Labels[runtime.LabelGeneration] == "from-an-older-release" {
		t.Fatal("recreated edge must carry the new generation")
	}
	if old, _ := h.fake.Inspect(ctx, oldID); old != nil {
		t.Fatal("old edge must be removed after a successful recreate")
	}
	assertNoPreviousEdge(t, h)
}

// A recreate that fails at any step puts the old edge back, running, under
// the edge name.
func TestEdgeRecreateFailureRestoresPreviousEdge(t *testing.T) {
	for _, tc := range []struct{ fault, code string }{
		{"Create", "edge-create-failed"},
		{"Start", "edge-start-failed"},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			h := newHarness(t)
			ctx := t.Context()
			oldID := startEdge(t, h)
			simulateEdgeUpgrade(t, h, oldID)
			h.fake.FailOn = map[string]error{
				tc.fault + " " + h.c.Names.EdgeContainer(): errors.New("port is already allocated"),
			}
			op, err := reapplyEdge(t, h)
			if err != nil {
				t.Fatal(err)
			}
			if op.State != store.OpFailed || op.ErrorCode != tc.code {
				t.Fatalf("want %s failure, got %s %s: %s", tc.code, op.State, op.ErrorCode, op.ErrorMessage)
			}
			ins, _ := h.fake.Inspect(ctx, h.c.Names.EdgeContainer())
			if ins == nil || ins.ID != oldID || !ins.State.Running {
				t.Fatal("the previous edge must be restored and running")
			}
			assertNoPreviousEdge(t, h)
			list, _ := h.fake.List(ctx, map[string]string{"io.bento.role": "edge"})
			if len(list) != 1 {
				t.Fatalf("the failed new edge must be removed, %d edge containers remain", len(list))
			}
		})
	}
}

// A recreate interrupted after the old edge was renamed aside is settled by
// the next apply: the leftover is restored when the edge is missing and
// removed when a new edge already exists.
func TestEdgeApplySettlesInterruptedRecreate(t *testing.T) {
	t.Run("restore", func(t *testing.T) {
		h := newHarness(t)
		ctx := t.Context()
		oldID := startEdge(t, h)
		if err := h.fake.Rename(ctx, oldID, h.c.Names.EdgePreviousContainer()); err != nil {
			t.Fatal(err)
		}
		h.fake.Stop(ctx, oldID, 0)
		if op, _ := reapplyEdge(t, h); op.State != store.OpSucceeded {
			t.Fatalf("edge apply failed: %s: %s", op.ErrorCode, op.ErrorMessage)
		}
		ins, _ := h.fake.Inspect(ctx, h.c.Names.EdgeContainer())
		if ins == nil || ins.ID != oldID || !ins.State.Running {
			t.Fatal("the previous edge must be restored under the edge name and started")
		}
		assertNoPreviousEdge(t, h)
	})
	t.Run("remove", func(t *testing.T) {
		h := newHarness(t)
		ctx := t.Context()
		oldID := startEdge(t, h)
		if err := h.fake.Rename(ctx, oldID, h.c.Names.EdgePreviousContainer()); err != nil {
			t.Fatal(err)
		}
		// The new edge was created before the interruption.
		s, _ := h.c.EdgeSettings(ctx)
		ns, _ := h.c.NetworkPlan(ctx)
		spec, err := h.c.edgeSpec(s, ns)
		if err != nil {
			t.Fatal(err)
		}
		newID, _ := h.fake.Create(ctx, spec)
		if op, _ := reapplyEdge(t, h); op.State != store.OpSucceeded {
			t.Fatalf("edge apply failed: %s: %s", op.ErrorCode, op.ErrorMessage)
		}
		ins, _ := h.fake.Inspect(ctx, h.c.Names.EdgeContainer())
		if ins == nil || ins.ID != newID || !ins.State.Running {
			t.Fatal("the new edge must be kept and started")
		}
		if old, _ := h.fake.Inspect(ctx, oldID); old != nil {
			t.Fatal("the leftover previous edge must be removed")
		}
	})
}

// A previous-edge name held by something this stack does not own is refused.
func TestEdgeApplyRefusesForeignPreviousEdge(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	startEdge(t, h)
	h.fake.Create(ctx, docker.ContainerSpec{
		Name:   h.c.Names.EdgePreviousContainer(),
		Config: &container.Config{Image: "busybox", Labels: map[string]string{"owner": "someone-else"}},
	})
	op, _ := reapplyEdge(t, h)
	if op.State != store.OpFailed || op.ErrorCode != "foreign-container" {
		t.Fatalf("want foreign-container, got %s %s", op.State, op.ErrorCode)
	}
	if foreign, _ := h.fake.Inspect(ctx, h.c.Names.EdgePreviousContainer()); foreign == nil {
		t.Fatal("a foreign container must not be touched")
	}
}
