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
	"strconv"
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

func setup(t *testing.T) *env { return setupWithUIDs(t, 40000) }

// setupWithUIDs creates a disposable stack whose app UIDs start at first, so
// two stacks in one test never hand out the same host UID.
func setupWithUIDs(t *testing.T, first int) *env {
	t.Helper()
	if os.Getenv("BENTO_DOCKER_TESTS") != "1" || os.Geteuid() != 0 {
		t.Skip("set BENTO_DOCKER_TESTS=1 and run as root for real Docker integration tests")
	}
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "stack")
	name := "it" + platform.RandomHex(3)
	id, err := stack.Init(ctx, stack.InitOptions{Root: root, Name: name, UIDRange: domain.UIDRange{First: first, Last: first + 999}})
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
	// Runtime builds are recorded per stack and labeled with it.
	if is, err := cli.ImageList(ctx, client.ImageListOptions{Filters: f}); err == nil {
		for _, i := range is.Items {
			cli.ImageRemove(ctx, i.ID, client.ImageRemoveOptions{PruneChildren: true})
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
	// Wait for any queued init operation.
	for {
		ops, _ := store.ListOperations(ctx, e.s.DB(), store.OpFilter{States: []store.OpState{store.OpQueued, store.OpRunning}})
		if len(ops) == 0 {
			break
		}
		time.Sleep(time.Second)
	}
	php := domain.Runtime{Kind: domain.RuntimePHP, PHP: &domain.PHPRuntime{Version: "8.4", DocumentRoot: "public", Routing: "front-controller", Mode: "standard", UploadLimitMB: 16}}
	node := domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
	// "two" opts in to strict file access (open_basedir); "one" keeps the default.
	strictPHP := *php.PHP
	strictPHP.StrictFileAccess = true
	a1, a2, h := e.create("one", php), e.create("two", domain.Runtime{Kind: domain.RuntimePHP, PHP: &strictPHP}), e.create("web", node)
	// Effective web-request settings as FPM serves them: production defaults
	// from the image's zz-bento.ini, no PEAR command script, open_basedir only
	// when strict.
	writeFile(t, filepath.Join(e.layout.AppCode("one"), "public/index.php"), "<?php echo 'one:'.posix_getuid()"+
		".'|basedir='.ini_get('open_basedir').'|display='.(int)ini_get('display_errors')"+
		".'|argv='.(int)ini_get('register_argc_argv').'|assert='.ini_get('zend.assertions')"+
		".'|pearcmd='.(int)file_exists('/usr/local/lib/php/pearcmd.php');", a1.UID)
	writeFile(t, filepath.Join(e.layout.AppCode("two"), "public/index.php"),
		"<?php echo 'two:'.posix_getuid().'|basedir='.ini_get('open_basedir');", a2.UID)
	writeFile(t, filepath.Join(e.layout.AppCode("web"), "s.js"),
		"require('http').createServer((q,s)=>s.end('web:'+process.getuid())).listen(3000,'0.0.0.0')", h.UID)
	for _, a := range []domain.App{a1, a2, h} {
		e.wait(e.c.StartApp(ctx, a.ID, ""))
	}
	for _, tc := range []struct {
		app  domain.App
		want string
	}{
		{a1, fmt.Sprintf("one:%d|basedir=|display=0|argv=0|assert=-1|pearcmd=0", a1.UID)},
		{a2, fmt.Sprintf("two:%d|basedir=%s:/tmp:/usr/local/lib/php:%s", a2.UID, a2.ContainerHome(), a2.Bindings[0].SQLiteContainerDir())},
		{h, "web:" + fmt.Sprint(h.UID)},
	} {
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

// Valkey and minicrond are per-app s6 services: Valkey (off by default) runs
// as the app on loopback and snapshots into the home; either one turned off
// leaves no process behind and no restart loop, and Valkey reloads its
// snapshot when turned back on.
func TestIntegrationAppServices(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	node := domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
	app := e.create("kvapp", node)
	if app.Valkey || !app.Minicrond {
		t.Fatalf("defaults valkey=%v minicrond=%v", app.Valkey, app.Minicrond)
	}
	writeFile(t, filepath.Join(e.layout.AppCode("kvapp"), "s.js"),
		"require('http').createServer((q,s)=>s.end('ok')).listen(3000,'0.0.0.0')", app.UID)
	e.wait(e.c.StartApp(ctx, app.ID, ""))

	exec := func(cmd ...string) string {
		t.Helper()
		obs, err := e.c.Observe(ctx, app)
		if err != nil || !obs.Running {
			t.Fatalf("instance not running: %+v %v", obs, err)
		}
		res, err := e.sdk.Exec(ctx, obs.ContainerID, docker.ExecRequest{Cmd: cmd})
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(res.Stdout) + string(res.Stderr))
	}
	procs := func() string { return exec("sh", "-c", "cat /proc/[0-9]*/comm") }
	update := func(valkey, minicrond bool) {
		t.Helper()
		_, op, err := e.c.UpdateApp(ctx, app.ID, operations.UpdateAppInput{Valkey: &valkey, Minicrond: &minicrond}, "")
		e.wait(op, err)
	}
	if p := procs(); !strings.Contains(p, "minicrond") || strings.Contains(p, "valkey-server") {
		t.Fatalf("default processes:\n%s", p)
	}

	// Without Valkey a tool container has its own address on the apps
	// network, but not the app's routing alias: the alias resolves to the app
	// container only, so the edge never sends visitors to a tool.
	appIns, err := e.sdk.Inspect(ctx, e.c.Names.AppContainer(app.ID))
	if err != nil || appIns == nil {
		t.Fatalf("inspect app: %v", err)
	}
	appIP := appIns.NetworkSettings.Networks[e.c.Names.AppsNetwork()].IPAddress.String()
	plain, err := store.GetApp(ctx, e.s.DB(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := e.c.OpenTool(ctx, plain, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.sdk.Exec(ctx, tool.ContainerID, docker.ExecRequest{Cmd: []string{"/usr/local/bin/bento-exec", "bash", "-c",
		"getent ahostsv4 " + runtime.AppAlias(app.ID) + " | awk '{print $1}' | sort -u; " +
			"node -e \"fetch('http://" + runtime.AppAlias(app.ID) + ":3000').then(r=>r.text()).then(console.log)\""}})
	tool.Close()
	if err != nil || strings.TrimSpace(string(res.Stdout)) != appIP+"\nok" {
		t.Fatalf("alias from tool container: got %q %q %v, want only the app's %s and its reply", res.Stdout, res.Stderr, err, appIP)
	}

	update(true, true)
	if got := exec("valkey-cli", "PING"); got != "PONG" {
		t.Fatalf("valkey PING: %q", got)
	}
	// Capped at a quarter of the app's memory limit; only keys with a TTL
	// are evicted.
	wantMax := strconv.Itoa(runtime.ValkeyMaxMemoryMB(app.Resources.MemoryMB) << 20)
	if got := exec("valkey-cli", "CONFIG", "GET", "maxmemory"); got != "maxmemory\n"+wantMax {
		t.Fatalf("valkey maxmemory: %q, want %s bytes", got, wantMax)
	}
	if got := exec("valkey-cli", "CONFIG", "GET", "maxmemory-policy"); got != "maxmemory-policy\nvolatile-lru" {
		t.Fatalf("valkey maxmemory-policy: %q", got)
	}
	if got := exec("redis-cli", "PING"); got != "PONG" {
		t.Fatalf("redis-cli alias PING: %q", got)
	}
	if got := exec("sh", "-c", "valkey-cli SET greeting hello && valkey-cli SAVE"); got != "OK\nOK" {
		t.Fatalf("SET/SAVE: %q", got)
	}
	// Every process, Valkey included, runs as the app; the data directory is private.
	if got := exec("sh", "-c", "stat -c %u /proc/[0-9]*/ | sort -u; stat -c %u:%a ~/.local/share/valkey"); got !=
		fmt.Sprintf("%d\n%d:700", app.UID, app.UID) {
		t.Fatalf("valkey identity: %q", got)
	}
	if got := exec("bash", "-c", "source /usr/local/lib/bento/env.bash && echo $REDIS_URL"); got != "redis://127.0.0.1:6379" {
		t.Fatalf("REDIS_URL: %q", got)
	}
	// Tool shells, execs and deploy hooks run in a tool container; it shares
	// the app's loopback so REDIS_URL reaches the app's Valkey there too.
	running, err := store.GetApp(ctx, e.s.DB(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	tool, err = e.c.OpenTool(ctx, running, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	res, err = e.sdk.Exec(ctx, tool.ContainerID, docker.ExecRequest{
		Cmd: []string{"/usr/local/bin/bento-exec", "bash", "-c", `valkey-cli -u "$REDIS_URL" GET greeting`},
	})
	tool.Close()
	if err != nil || strings.TrimSpace(string(res.Stdout)) != "hello" {
		t.Fatalf("valkey from tool container: %q %q %v", res.Stdout, res.Stderr, err)
	}
	dump := filepath.Join(e.layout.AppHome("kvapp"), ".local/share/valkey/dump.rdb")
	if info, err := os.Stat(dump); err != nil || info.Size() == 0 {
		t.Fatalf("snapshot in home: %v %v", info, err)
	}

	update(false, false)
	time.Sleep(5 * time.Second) // a restart loop would show up as failures here
	if p := procs(); strings.Contains(p, "minicrond") || strings.Contains(p, "valkey-server") {
		t.Fatalf("services still running while off:\n%s", p)
	}
	if got := exec("sh", "-c", "wc -l < /run/bento/failures"); got != "0" {
		t.Fatalf("off services recorded failures: %s", got)
	}
	if _, err := os.Stat(dump); err != nil {
		t.Fatalf("snapshot must stay in the home while off: %v", err)
	}

	update(true, true)
	if got := exec("valkey-cli", "GET", "greeting"); got != "hello" {
		t.Fatalf("snapshot not reloaded: %q", got)
	}
}

// Operations on different apps run in parallel: two starts submitted together
// overlap in time, and so do the reconcile operations that recreate both
// after their instances are deleted. Both apps end up serving.
func TestIntegrationParallelAppOperations(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	node := domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
	apps := []domain.App{e.create("left", node), e.create("right", node)}
	for _, a := range apps {
		writeFile(t, filepath.Join(e.layout.AppCode(a.Slug), "s.js"),
			"require('http').createServer((q,s)=>s.end('"+a.Slug+"')).listen(3000,'0.0.0.0')", a.UID)
	}
	overlap := func(what string, ops []store.Operation) {
		t.Helper()
		a, b := ops[0], ops[1]
		if !(a.StartedAt < b.FinishedAt && b.StartedAt < a.FinishedAt) {
			t.Fatalf("%s did not overlap: [%s .. %s] vs [%s .. %s]", what, a.StartedAt, a.FinishedAt, b.StartedAt, b.FinishedAt)
		}
	}
	serves := func() {
		t.Helper()
		for _, a := range apps {
			obs, _ := e.c.Observe(ctx, a)
			if !obs.Running {
				t.Fatalf("%s is not running", a.Slug)
			}
			resp, err := http.Get(fmt.Sprintf("http://%s:%d/", obs.IP, a.HTTPPort()))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) != a.Slug {
				t.Fatalf("%s served %q", a.Slug, b)
			}
		}
	}

	var submitted []store.Operation
	for _, a := range apps {
		op, err := e.c.StartApp(ctx, a.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		submitted = append(submitted, op)
	}
	var started []store.Operation
	for _, op := range submitted {
		started = append(started, e.wait(op, nil))
	}
	overlap("start operations", started)
	serves()

	for _, a := range apps {
		obs, _ := e.c.Observe(ctx, a)
		e.sdk.Remove(ctx, obs.ContainerID)
	}
	r := reconcile.New(e.c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	var reconciles []store.Operation
	for _, a := range apps {
		ops, _ := store.ListOperations(ctx, e.s.DB(), store.OpFilter{TargetID: a.ID, States: []store.OpState{store.OpQueued, store.OpRunning, store.OpSucceeded}, Limit: 1})
		if len(ops) != 1 || ops[0].Kind != operations.KindAppReconcile {
			t.Fatalf("%s: reconciler did not submit a reconcile operation: %+v", a.Slug, ops)
		}
		reconciles = append(reconciles, e.wait(ops[0], nil))
	}
	overlap("reconcile operations", reconciles)
	serves()
}
