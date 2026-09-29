package operations

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/transfer"
)

// exportFixture is a harness with one running app and one running Redis
// service, the state every export path starts from.
func exportFixture(t *testing.T) (*harness, domain.App) {
	t.Helper()
	h := newHarness(t)
	ctx := t.Context()
	// "ready" satisfies app readiness, "OK" the Redis ACL reload.
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult {
		return docker.ExecResult{Stdout: []byte("ready OK")}
	}
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	_, op, err := h.c.CreateService(ctx, domain.EngineRedis, "", "")
	h.mustSucceed(op, err)
	return h, app
}

// phases returns the phase events of an operation in order.
func (h *harness) phases(id string) []string {
	h.t.Helper()
	evs, err := store.ListEvents(h.t.Context(), h.store.DB(), id, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if p, ok := strings.CutPrefix(e.Message, "phase: "); ok {
			out = append(out, p)
		}
	}
	return out
}

func (h *harness) assertResumed(app domain.App, serviceRunning bool) {
	h.t.Helper()
	ctx := h.t.Context()
	obs, err := h.c.Observe(ctx, app)
	if err != nil || !obs.Running {
		h.t.Fatalf("app not resumed after export: %+v %v", obs, err)
	}
	ins, err := h.fake.Inspect(ctx, h.c.Names.ServiceContainer("redis"))
	if err != nil || ins == nil || ins.State.Running != serviceRunning {
		h.t.Fatalf("service running after export: want %v (%v)", serviceRunning, err)
	}
}

func TestExportQuiescesArchivesAndResumes(t *testing.T) {
	h, app := exportFixture(t)
	ctx := t.Context()
	stops := h.fake.CallCount("Stop")
	dest := filepath.Join(t.TempDir(), "export")
	got := h.mustSucceed(h.c.SubmitExport(ctx, dest, "export", ""))

	var res map[string]any
	if err := json.Unmarshal(got.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res["destination"] != dest || res["services"] != float64(1) || res["apps"] != float64(1) {
		t.Fatalf("result %v", res)
	}
	m, err := transfer.ReadManifest(dest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != transfer.FormatName || m.StackID != "stest" || m.StackName != "test" || m.Arch != "amd64" ||
		m.StateFile != "state.db" || m.RootArchive != "stack.tar.zst" || len(m.Services) != 1 {
		t.Fatalf("manifest %+v", m)
	}
	if s := m.Services[0]; s.Name != "redis" || s.VolumeFile != "volume-redis.tar" || s.Image != domain.RedisImage {
		t.Fatalf("service entry %+v", s)
	}
	for _, f := range []string{"state.db", "stack.tar.zst", "manifest.json"} {
		info, err := os.Stat(filepath.Join(dest, f))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, info, err)
		}
	}
	if n := h.fake.CallCount("Stop") - stops; n != 2 {
		t.Fatalf("export must stop exactly the running app and service, stopped %d", n)
	}
	want := []string{"quiesce", "snapshot-state", "archive-root", "archive-volume redis", "resume"}
	if p := h.phases(got.ID); !slices.Equal(p[:len(want)], want) {
		t.Fatalf("phases %v", p)
	}
	h.assertResumed(app, true)
}

func TestExportFailureStillResumes(t *testing.T) {
	cases := map[string]struct {
		inject func(h *harness)
		code   string
		// A service whose volume vanished is not restarted: ensureService
		// refuses to recreate durable data.
		serviceResumes bool
	}{
		"volume job fails": {
			inject:         func(h *harness) { h.fake.FailOn = map[string]error{"Create": errors.New("boom")} },
			code:           "internal",
			serviceResumes: true,
		},
		"volume missing": {
			inject: func(h *harness) {
				if err := h.fake.VolumeRemove(h.t.Context(), h.c.Names.ServiceVolume("redis")); err != nil {
					h.t.Fatal(err)
				}
			},
			code: "volume-missing",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, app := exportFixture(t)
			tc.inject(h)
			op, err := h.c.SubmitExport(t.Context(), filepath.Join(t.TempDir(), "export"), "export", "")
			if err != nil {
				t.Fatal(err)
			}
			got := h.wait(op)
			if got.State != store.OpFailed {
				t.Fatalf("want failure, got %s", got.State)
			}
			if tc.code != "internal" && got.ErrorCode != tc.code {
				t.Fatalf("want %s, got %s %s", tc.code, got.ErrorCode, got.ErrorMessage)
			}
			if p := h.phases(got.ID); !slices.Contains(p, "resume") || slices.Index(p, "resume") < slices.Index(p, "quiesce") {
				t.Fatalf("resume must run after a failed export: %v", p)
			}
			h.assertResumed(app, tc.serviceResumes)
		})
	}
}

func TestExportRefusesDestinationInsideRoot(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if _, err := h.c.SubmitExport(ctx, "/tmp/x", "Export", ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("confirmation must be exact, got %v", err)
	}
	_, err := h.c.SubmitExport(ctx, filepath.Join(h.layout.Root, "out"), "export", "")
	var verrs domain.ValidationErrors
	if !errors.As(err, &verrs) || verrs[0].Field != "destination" {
		t.Fatalf("destination inside the root must be refused, got %v", err)
	}
}
