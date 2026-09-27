// Package integration holds real Docker Engine tests. They run only with
// BENTO_DOCKER_TESTS=1, as root, against disposable stack roots, and remove
// only resources labeled with their own random stack id.
package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/reconcile"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/stack"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

type env struct {
	t      *testing.T
	c      *operations.Controller
	s      *store.Store
	layout platform.Layout
	sdk    *docker.SDK
}

func setup(t *testing.T) *env {
	t.Helper()
	if os.Getenv("BENTO_DOCKER_TESTS") != "1" || os.Geteuid() != 0 {
		t.Skip("set BENTO_DOCKER_TESTS=1 and run as root for real Docker integration tests")
	}
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "stack")
	name := "it" + platform.RandomHex(3)
	id, err := stack.Init(ctx, stack.InitOptions{Root: root, Name: name, UIDRange: domain.UIDRange{First: 40000, Last: 40999}})
	if err != nil {
		t.Fatal(err)
	}
	layout := platform.Layout{Root: root}
	s, err := store.Open(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := docker.NewSDK()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := operations.NewController(operations.Deps{Store: s, Engine: sdk, Layout: layout, HostIDs: platform.FileHostIDs{}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	c.Start(cctx)
	t.Cleanup(func() {
		cancel()
		c.Shutdown(30 * time.Second)
		s.Close()
		cleanupStack(id.ID)
	})
	return &env{t: t, c: c, s: s, layout: layout, sdk: sdk}
}

// cleanupStack removes containers, networks, and volumes labeled with this
// test's stack id only.
func cleanupStack(stackID string) {
	ctx := context.Background()
	cli, err := client.New(client.WithHostFromEnv(), client.WithAPIVersionNegotiation())
	if err != nil {
		return
	}
	defer cli.Close()
	f := make(client.Filters).Add("label", runtime.LabelStackID+"="+stackID)
	if cs, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f}); err == nil {
		for _, c := range cs.Items {
			cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true})
		}
	}
	if ns, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: f}); err == nil {
		for _, n := range ns.Items {
			cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{})
		}
	}
	if vs, err := cli.VolumeList(ctx, client.VolumeListOptions{Filters: f}); err == nil {
		for _, v := range vs.Items {
			cli.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{})
		}
	}
}

func (e *env) wait(op store.Operation, err error) store.Operation {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := store.GetOperation(context.Background(), e.s.DB(), op.ID)
		if err != nil {
			e.t.Fatal(err)
		}
		if got.State.Terminal() {
			if got.State != store.OpSucceeded {
				e.t.Fatalf("%s %s: %s: %s", got.Kind, got.State, got.ErrorCode, got.ErrorMessage)
			}
			return got
		}
		time.Sleep(500 * time.Millisecond)
	}
	e.t.Fatalf("operation %s timed out", op.ID)
	return op
}

func (e *env) create(slug string, rt domain.Runtime) domain.App {
	app, op, err := e.c.CreateApp(context.Background(), operations.CreateAppInput{Slug: slug, Runtime: rt,
		Bindings: []operations.BindingRequest{{Engine: domain.EngineSQLite}}}, "")
	e.wait(op, err)
	app, _ = store.GetApp(context.Background(), e.s.DB(), app.ID)
	return app
}

func writeFile(t *testing.T, path, body string, uid int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o750)
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	for p := path; strings.Count(p, "/") > 0; p = filepath.Dir(p) {
		if strings.HasSuffix(filepath.Dir(p), "/homes") {
			break
		}
		os.Lchown(p, uid, uid)
	}
}

// Acceptance scenarios 1-4 (subset): two same-image PHP apps and one HTTP
// app with independent identities; hardened containers; private HTTP;
// stop intent; recreation of a deleted instance; singleton lock.
func TestIntegrationAppLifecycle(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Redis is initialized by the queued init operation.
	for {
		ops, _ := store.ListOperations(ctx, e.s.DB(), store.OpFilter{States: []store.OpState{store.OpQueued, store.OpRunning}})
		if len(ops) == 0 {
			break
		}
		time.Sleep(time.Second)
	}
	php := domain.Runtime{Kind: domain.RuntimePHP, PHP: &domain.PHPRuntime{Version: "8.4", DocumentRoot: "public", Routing: "front-controller", Pool: "tiny", UploadLimitMB: 16}}
	node := domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
	a1, a2, h := e.create("one", php), e.create("two", php), e.create("web", node)
	writeFile(t, filepath.Join(e.layout.AppHome("one"), "public/index.php"), "<?php echo 'one:'.posix_getuid();", a1.UID)
	writeFile(t, filepath.Join(e.layout.AppHome("two"), "public/index.php"), "<?php echo 'two:'.posix_getuid();", a2.UID)
	writeFile(t, filepath.Join(e.layout.AppHome("web"), "s.js"),
		"require('http').createServer((q,s)=>s.end('web:'+process.getuid())).listen(3000,'0.0.0.0')", h.UID)
	for _, a := range []domain.App{a1, a2, h} {
		e.wait(e.c.StartApp(ctx, a.ID, ""))
	}
	for _, tc := range []struct {
		app  domain.App
		want string
	}{{a1, "one:" + fmt.Sprint(a1.UID)}, {a2, "two:" + fmt.Sprint(a2.UID)}, {h, "web:" + fmt.Sprint(h.UID)}} {
		obs, _ := e.c.Observe(ctx, tc.app)
		resp, err := http.Get(fmt.Sprintf("http://%s:%d/", obs.IP, tc.app.HTTPPort()))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != tc.want {
			t.Fatalf("%s served %q, want %q", tc.app.Slug, b, tc.want)
		}
		ins, _ := e.sdk.Inspect(ctx, obs.ContainerID)
		if !ins.HostConfig.ReadonlyRootfs || ins.HostConfig.CapDrop[0] != "ALL" || len(ins.HostConfig.PortBindings) != 0 || ins.Config.User != fmt.Sprintf("%d:%d", tc.app.UID, tc.app.GID) {
			t.Fatalf("%s container is not hardened", tc.app.Slug)
		}
	}
	// Every process in the PHP instance runs as the app identity.
	obs, _ := e.c.Observe(ctx, a1)
	res, err := e.sdk.Exec(ctx, obs.ContainerID, docker.ExecRequest{Cmd: []string{"sh", "-c", "stat -c %u /proc/[0-9]*/ | sort -u"}})
	if err != nil || strings.TrimSpace(string(res.Stdout)) != fmt.Sprint(a1.UID) {
		t.Fatalf("process identities: %q %v", res.Stdout, err)
	}
	// Singleton: a second persistent instance on the same home refuses to start.
	ins, _ := e.sdk.Inspect(ctx, obs.ContainerID)
	dupSpec := docker.ContainerSpec{Name: "dup-" + platform.RandomHex(4), Config: ins.Config, HostConfig: ins.HostConfig}
	dupSpec.Config.Labels = map[string]string{runtime.LabelStackID: e.c.Stack.ID, "io.bento.test": "dup"}
	dupSpec.HostConfig.RestartPolicy.Name = "no"
	dupSpec.HostConfig.NetworkMode = "none"
	dupID, err := e.sdk.Create(ctx, dupSpec)
	if err != nil {
		t.Fatal(err)
	}
	e.sdk.Start(ctx, dupID)
	if code, _ := e.sdk.Wait(ctx, dupID); code != 75 {
		t.Fatalf("duplicate instance exited %d, want 75 (instance lock held)", code)
	}
	// Stop intent persists and disables the restart policy.
	e.wait(e.c.StopApp(ctx, a2.ID, ""))
	obs2, _ := e.c.Observe(ctx, a2)
	if obs2.Running || obs2.RestartMode != "no" {
		t.Fatalf("stopped app: %+v", obs2)
	}
	// A manually deleted running instance is recreated from intact data.
	e.sdk.Remove(ctx, obs.ContainerID)
	r := reconcile.New(e.c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		o, _ := e.c.Observe(ctx, a1)
		if o.Running && o.ContainerID != obs.ContainerID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deleted instance was not recreated")
		}
		time.Sleep(2 * time.Second)
	}
	// Reconciliation leaves the stopped app stopped.
	r.Pass(ctx)
	time.Sleep(2 * time.Second)
	if o, _ := e.c.Observe(ctx, a2); o.Running {
		t.Fatal("stopped app was resurrected")
	}
}
