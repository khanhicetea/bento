package operations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Valkey is off and minicrond on by default; toggling either on a running
// app is a boot-level change that replaces its instance.
func TestAppServiceTogglesDefaultsAndReplace(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	app := h.createApp("shop")
	if app.Valkey || !app.Minicrond {
		t.Fatalf("defaults valkey=%v minicrond=%v", app.Valkey, app.Minicrond)
	}
	runtimeEnv := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(h.c.Layout.AppConfigDir(app.ID), "runtime.env"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if env := runtimeEnv(); !strings.Contains(env, "BENTO_MINICROND=1\n") || !strings.Contains(env, "BENTO_VALKEY=0\n") {
		t.Fatalf("runtime.env %s", env)
	}
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	creates := h.fake.CallCount("Create")

	on, off := true, false
	_, op, err := h.c.UpdateApp(ctx, app.ID, UpdateAppInput{Valkey: &on, Minicrond: &off}, "")
	h.mustSucceed(op, err)
	got, err := store.GetApp(ctx, h.store.DB(), app.ID)
	if err != nil || !got.Valkey || got.Minicrond {
		t.Fatalf("persisted valkey=%v minicrond=%v %v", got.Valkey, got.Minicrond, err)
	}
	if env := runtimeEnv(); !strings.Contains(env, "BENTO_MINICROND=0\n") || !strings.Contains(env, "BENTO_VALKEY=1\n") ||
		!strings.Contains(env, "REDIS_HOST=127.0.0.1\n") {
		t.Fatalf("runtime.env %s", env)
	}
	if h.fake.CallCount("Create") == creates {
		t.Fatal("toggling app services must replace the running instance")
	}
}

func TestCreateAppHonorsServiceToggles(t *testing.T) {
	h := newHarness(t)
	off := false
	app, op, err := h.c.CreateApp(t.Context(), CreateAppInput{
		Slug: "shop", Valkey: true, Minicrond: &off,
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{
			Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}},
	}, "")
	h.mustSucceed(op, err)
	if !app.Valkey || app.Minicrond {
		t.Fatalf("valkey=%v minicrond=%v", app.Valkey, app.Minicrond)
	}
}

// Valkey listens on the app container's loopback, so a tool container for a
// running Valkey app shares that network namespace; otherwise it keeps its
// own networking.
func TestToolSharesAppNetworkForValkey(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	appContainer := func() string {
		t.Helper()
		ins, err := h.fake.Inspect(ctx, h.c.Names.AppContainer(app.ID))
		if err != nil || ins == nil {
			t.Fatalf("app container: %v", err)
		}
		return ins.ID
	}

	open := func() docker.ContainerSpec {
		t.Helper()
		got, err := store.GetApp(ctx, h.store.DB(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		tool, err := h.c.OpenTool(ctx, got, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		defer tool.Close()
		return h.fake.Containers[tool.ContainerID].Spec
	}
	ownNetwork := func(spec docker.ContainerSpec) bool {
		return spec.HostConfig.NetworkMode == "" && spec.Networking != nil && spec.Config.Hostname == "shop-tool"
	}

	if spec := open(); !ownNetwork(spec) {
		t.Fatalf("valkey off: tool must keep its own network: %+v", spec.HostConfig.NetworkMode)
	}

	on := true
	_, op, err := h.c.UpdateApp(ctx, app.ID, UpdateAppInput{Valkey: &on}, "")
	h.mustSucceed(op, err)
	appID := appContainer()
	spec := open()
	if spec.HostConfig.NetworkMode != container.NetworkMode("container:"+appID) || spec.Networking != nil ||
		spec.Config.Hostname != "" {
		t.Fatalf("valkey on: tool must join the app network: mode=%q net=%v host=%q",
			spec.HostConfig.NetworkMode, spec.Networking, spec.Config.Hostname)
	}

	h.fake.SetRunning(appID, false)
	if spec := open(); !ownNetwork(spec) {
		t.Fatalf("stopped app: tool must keep its own network: %+v", spec.HostConfig.NetworkMode)
	}
}
