package operations

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func newMySQLService(t *testing.T, h *harness) domain.DataService {
	t.Helper()
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{} }
	svc, op, err := h.c.CreateService(t.Context(), domain.EngineMySQL, "8.4", "")
	h.mustSucceed(op, err)
	return svc
}

func (h *harness) serviceCmd(t *testing.T, name string) ([]string, map[string]string, string) {
	t.Helper()
	ins, err := h.c.Engine.Inspect(t.Context(), h.c.Names.ServiceContainer(name))
	if err != nil || ins == nil {
		t.Fatalf("service container %s: %v", name, err)
	}
	return ins.Config.Cmd, ins.Config.Labels, ins.ID
}

// Configuring a service recreates its container on the same volume with the
// tuned flags; restarting keeps the container.
func TestConfigureServiceRecreatesWithSettings(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	cmd, labels, firstID := h.serviceCmd(t, svc.Name)
	if len(cmd) != 0 || labels[runtime.LabelServiceSettings] != "" {
		t.Fatalf("untuned service must keep the image command: %v %v", cmd, labels)
	}

	h.mustSucceed(h.c.ConfigureService(ctx, svc.Name, map[string]string{
		"innodb_buffer_pool_size": "256m", "max_connections": "300", "tmp_table_size": "",
	}, ""))
	cmd, labels, id := h.serviceCmd(t, svc.Name)
	want := []string{"mysqld", "--innodb-buffer-pool-size=256M", "--max-connections=300"}
	if !slices.Equal(cmd, want) || labels[runtime.LabelServiceSettings] == "" || id == firstID {
		t.Fatalf("cmd %v labels %v id %s", cmd, labels, id)
	}
	if v, err := h.c.Engine.VolumeInspect(ctx, svc.Volume); err != nil || v == nil {
		t.Fatalf("volume must survive: %v", err)
	}
	row, _ := store.GetService(ctx, h.store.DB(), svc.Name)
	if len(row.Settings) != 2 || row.Settings["innodb_buffer_pool_size"] != "256M" {
		t.Fatalf("saved settings %v", row.Settings)
	}

	// Same settings again: nothing to replace.
	removes := h.fake.CallCount("Remove ")
	h.mustSucceed(h.c.ConfigureService(ctx, svc.Name, row.Settings, ""))
	if h.fake.CallCount("Remove ") != removes {
		t.Fatal("unchanged settings must not recreate the container")
	}

	stops := h.fake.CallCount("Stop ")
	h.mustSucceed(h.c.RestartService(ctx, svc.Name, ""))
	if _, _, again := h.serviceCmd(t, svc.Name); again != id || h.fake.CallCount("Stop ") != stops+1 ||
		h.fake.CallCount("Remove ") != removes {
		t.Fatal("restart must stop and start the same container")
	}
}

// A failure between removing the old container and starting the new one is
// repaired by a later ensure on the retained volume.
func TestConfigureServiceRecoversAfterCreateFailure(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	h.fake.FailOn = map[string]error{"Create": errors.New("boom")}
	op, err := h.c.ConfigureService(ctx, svc.Name, map[string]string{"max_connections": "200"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed {
		t.Fatalf("want failure, got %s", got.State)
	}
	op, _, err = h.c.Submit(ctx, Submission{Kind: KindServiceEnsure, TargetKind: "service", TargetID: svc.Name})
	h.mustSucceed(op, err)
	if cmd, _, _ := h.serviceCmd(t, svc.Name); !slices.Contains(cmd, "--max-connections=200") {
		t.Fatalf("ensure must apply saved settings: %v", cmd)
	}
}

func TestConfigureServiceValidation(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	for _, in := range []map[string]string{
		{"shared_buffers": "1G"},                              // PostgreSQL key on MySQL
		{"innodb_buffer_pool_size": "1G --skip-grant-tables"}, // not a size
		{"innodb_buffer_pool_size": "1M"},                     // below minimum
		{"max_connections": "-5"},
		{"innodb_buffer_pool_size": "100000G"}, // above host memory
	} {
		if _, err := h.c.ConfigureService(ctx, svc.Name, in, ""); !errors.As(err, new(domain.ValidationErrors)) {
			t.Errorf("%v: want validation error, got %v", in, err)
		}
	}
	if _, err := h.c.ConfigureService(ctx, "mysql99", nil, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown service: %v", err)
	}
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{Stdout: []byte("OK")} }
	h.mustSucceed(func() (store.Operation, error) {
		_, op, err := h.c.CreateService(ctx, domain.EngineRedis, "", "")
		return op, err
	}())
	if _, err := h.c.ConfigureService(ctx, "redis", map[string]string{"maxmemory": "1G"}, ""); err == nil {
		t.Error("redis is not tunable")
	}
}

// Restart refuses a container that is not this stack's service.
func TestRestartServiceRefusesForeignContainer(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	_, _, id := h.serviceCmd(t, svc.Name)
	h.fake.Delete(id)
	if _, err := h.fake.Create(ctx, docker.ContainerSpec{
		Name: h.c.Names.ServiceContainer(svc.Name), Config: &container.Config{Image: "mysql:8.4", Labels: map[string]string{"owner": "someone-else"}},
	}); err != nil {
		t.Fatal(err)
	}
	op, err := h.c.RestartService(ctx, svc.Name, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.ErrorCode != "foreign-container" {
		t.Fatalf("want foreign-container, got %s %s", got.State, got.ErrorCode)
	}
	if h.fake.CallCount("Stop ") != 0 {
		t.Fatal("a foreign container must not be stopped")
	}
}

// A lost administrator secret (e.g. the stack root cleaned by tmpfiles) is
// refused before the container is touched, never silently regenerated.
func TestConfigureServiceRefusesMissingSecret(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	if err := os.RemoveAll(filepath.Join(h.c.Layout.ServicesDir(), svc.Name, "secrets")); err != nil {
		t.Fatal(err)
	}
	op, err := h.c.ConfigureService(ctx, svc.Name, map[string]string{"max_connections": "200"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.ErrorCode != "durable-state-missing" {
		t.Fatalf("want durable-state-missing, got %s %s", got.State, got.ErrorCode)
	}
	if h.fake.CallCount("Remove ") != 0 {
		t.Fatal("the container must not be replaced")
	}
	if _, err := os.Stat(filepath.Join(h.c.Layout.ServicesDir(), svc.Name, "secrets", "root-password")); err == nil {
		t.Fatal("a new secret must not be generated")
	}
}
