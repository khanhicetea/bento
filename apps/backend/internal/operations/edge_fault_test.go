package operations

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
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
