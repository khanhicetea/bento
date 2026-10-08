package operations

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func isReadyProbe(req docker.ExecRequest) bool {
	return len(req.Cmd) > 0 && strings.HasSuffix(req.Cmd[0], "bento-ready")
}

var readyResult = docker.ExecResult{Stdout: []byte("ready")}

// gate blocks a fake Exec until released and records how many are blocked.
type gate struct {
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	arrived map[string]bool
}

func newGate(t *testing.T) *gate {
	g := &gate{release: make(chan struct{}), arrived: map[string]bool{}}
	t.Cleanup(g.open) // runs before the harness shuts the executor down
	return g
}

func (g *gate) open() { g.once.Do(func() { close(g.release) }) }

func (g *gate) enter(id string) {
	g.mu.Lock()
	g.arrived[id] = true
	g.mu.Unlock()
	<-g.release
}

func (g *gate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.arrived)
}

func (g *gate) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for g.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d operations reached the gate", g.count(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settle returns once a dispatch pass that began after the call has
// finished, so anything queued before it has been considered for dispatch.
func (h *harness) settle() {
	h.t.Helper()
	target := h.c.passes.Load() + 2 // the pass in flight may predate the call
	deadline := time.Now().Add(5 * time.Second)
	for h.c.passes.Load() < target {
		if time.Now().After(deadline) {
			h.t.Fatal("dispatcher did not run")
		}
		h.c.Wake()
		time.Sleep(time.Millisecond)
	}
}

func (c *Controller) isWarming(appID string) bool { return c.holdRoute(appID, false) }

func state(t *testing.T, h *harness, op store.Operation) store.Operation {
	t.Helper()
	got, err := store.GetOperation(t.Context(), h.store.DB(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestClaimsConflictMatrix(t *testing.T) {
	a := claims{excl: []string{"app:a"}, shared: []string{"service:mysql"}}
	b := claims{excl: []string{"app:b"}, shared: []string{"service:mysql"}}
	svc := claims{excl: []string{"service:mysql"}}
	for _, tc := range []struct {
		name string
		x, y claims
		want bool
	}{
		{"different apps sharing a service", a, b, false},
		{"same app", a, claims{excl: []string{"app:a"}}, true},
		{"service op vs dependent app", svc, a, true},
		{"service op vs unrelated app", svc, claims{excl: []string{"app:c"}}, false},
		{"different services", svc, claims{excl: []string{"service:redis"}}, false},
		{"global vs anything", globalClaims, b, true},
		{"anything vs global", b, globalClaims, true},
		{"global vs global", globalClaims, globalClaims, true},
	} {
		if got := tc.x.conflicts(tc.y); got != tc.want {
			t.Errorf("%s: conflicts=%v, want %v", tc.name, got, tc.want)
		}
		if got := tc.y.conflicts(tc.x); got != tc.want {
			t.Errorf("%s (reversed): conflicts=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestServiceDeps(t *testing.T) {
	app := domain.App{
		Bindings: []domain.Binding{
			{Engine: domain.EngineSQLite},
			{Engine: domain.EngineMySQL, Service: "mysql"},
			{Engine: domain.EngineMySQL, Service: "mysql"},
		},
		Redis: domain.RedisIdentity{Username: "u"},
	}
	got := serviceDeps(app)
	if len(got) != 2 || got[0] != "service:mysql" || got[1] != "service:redis" {
		t.Fatalf("deps %v", got)
	}
	if len(serviceDeps(domain.App{Bindings: []domain.Binding{{Engine: domain.EngineSQLite}}})) != 0 {
		t.Fatal("sqlite bindings have no service dependency")
	}
}

// A kind runs in parallel only when claimsFor says so; a new kind is global
// (runs alone) until someone reviews it.
func TestOnlyReviewedKindsRunInParallel(t *testing.T) {
	h := newHarness(t)
	parallel := map[string]bool{
		KindAppReconcile: true, KindAppStart: true, KindAppRestart: true, KindAppStop: true,
		KindAppUpdate: true, KindAppDeploy: true, KindServiceCreate: true, KindServiceEnsure: true,
		KindImagePrepare: true,
		KindResticInit:   true, KindResticConnect: true, KindResticBackup: true,
		KindResticRefresh: true, KindResticKeyAdd: true, KindResticKeyRemove: true, KindResticCheck: true, KindResticUnlock: true, KindResticInspect: true,
		KindResticInspectRemote: true,
		// Clones download in parallel and escalate (Run.Escalate) before
		// creating the app.
		KindAppCloneFromBackup: true, KindAppRestoreFromBackup: true,
	}
	for kind := range h.c.handlers {
		cl := h.c.claimsFor(t.Context(), store.Operation{Kind: kind, TargetID: "x"})
		if cl.global == parallel[kind] {
			t.Errorf("kind %s: global=%v, want %v", kind, cl.global, !parallel[kind])
		}
	}
	if !h.c.claimsFor(t.Context(), store.Operation{Kind: "future.kind"}).global {
		t.Error("an unknown kind must run alone")
	}
}

func TestUnprovisionedStartOrUpdateRunsAlone(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	for _, kind := range []string{KindAppStart, KindAppUpdate} {
		if h.c.claimsFor(ctx, store.Operation{Kind: kind, TargetID: app.ID}).global {
			t.Fatalf("%s of a provisioned app should not run alone", kind)
		}
	}
	app.Provisioned = false
	if err := store.UpdateApp(ctx, h.store.DB(), app); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{KindAppStart, KindAppUpdate} {
		if !h.c.claimsFor(ctx, store.Operation{Kind: kind, TargetID: app.ID}).global {
			t.Fatalf("%s that provisions (grants, Redis ACL) must run alone", kind)
		}
	}
	if h.c.claimsFor(ctx, store.Operation{Kind: KindAppDeploy, TargetID: app.ID}).global {
		t.Fatal("deploy never provisions and should not run alone")
	}
}

func TestOperationsOnDifferentAppsRunInParallel(t *testing.T) {
	h := newHarness(t)
	a, b := h.createApp("alpha"), h.createApp("beta")
	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if isReadyProbe(req) {
			g.enter(id)
		}
		return readyResult
	}
	ctx := t.Context()
	opA, err := h.c.StartApp(ctx, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	opB, err := h.c.StartApp(ctx, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Both readiness probes are in flight at once: serial execution would
	// leave the second operation queued behind the blocked first.
	g.waitFor(t, 2)
	if h.c.Idle() {
		t.Fatal("executor reports idle while two operations run")
	}
	g.open()
	for _, op := range []store.Operation{opA, opB} {
		if got := h.wait(op); got.State != store.OpSucceeded {
			t.Fatalf("%s: %s %s", got.Kind, got.ErrorCode, got.ErrorMessage)
		}
	}
}

func TestSameAppOperationsStaySerialAcrossAppsOverlap(t *testing.T) {
	h := newHarness(t)
	apps := []domain.App{h.createApp("alpha"), h.createApp("beta")}
	var mu sync.Mutex
	inflight := map[string]int{}
	var total, maxTotal, maxPerApp int
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if !isReadyProbe(req) {
			return readyResult
		}
		mu.Lock()
		inflight[id]++
		total++
		maxTotal = max(maxTotal, total)
		maxPerApp = max(maxPerApp, inflight[id])
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		inflight[id]--
		total--
		mu.Unlock()
		return readyResult
	}
	ctx := t.Context()
	var ops []store.Operation
	for range 3 {
		for _, app := range apps {
			op, err := h.c.StartApp(ctx, app.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			ops = append(ops, op)
		}
	}
	for _, op := range ops {
		h.wait(op)
	}
	mu.Lock()
	defer mu.Unlock()
	if maxPerApp != 1 {
		t.Fatalf("operations on one app overlapped (%d at once)", maxPerApp)
	}
	if maxTotal < 2 {
		t.Fatalf("operations on different apps never overlapped (max %d)", maxTotal)
	}
}

func TestConcurrencyLimitIsHonored(t *testing.T) {
	for _, limit := range []int{1, 2} {
		h := newHarnessWith(t, func(d *Deps) { d.Concurrency = limit })
		var apps []domain.App
		for _, slug := range []string{"one", "two", "three", "four"} {
			apps = append(apps, h.createApp(slug))
		}
		var inflight, peak atomic.Int32
		h.fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
			if isReadyProbe(req) {
				n := inflight.Add(1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				time.Sleep(40 * time.Millisecond)
				inflight.Add(-1)
			}
			return readyResult
		}
		var ops []store.Operation
		for _, app := range apps {
			op, err := h.c.StartApp(t.Context(), app.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			ops = append(ops, op)
		}
		for _, op := range ops {
			h.wait(op)
		}
		if got := int(peak.Load()); got != limit {
			t.Fatalf("concurrency %d: peak %d operations at once", limit, got)
		}
	}
}

// A global operation waits for running work to drain, and operations queued
// behind it never overtake it, even when they could run in parallel with what
// is already running.
func TestGlobalOperationIsABarrier(t *testing.T) {
	h := newHarness(t)
	a, b := h.createApp("alpha"), h.createApp("beta")
	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if isReadyProbe(req) {
			g.enter(id)
		}
		return readyResult
	}
	ctx := t.Context()
	startA, err := h.c.StartApp(ctx, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	g.waitFor(t, 1)
	prune, err := h.c.PruneImage(ctx, strings.Repeat("a", 64), "delete", "")
	if err != nil {
		t.Fatal(err)
	}
	startB, err := h.c.StartApp(ctx, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	h.settle()
	if got := state(t, h, prune); got.State != store.OpQueued {
		t.Fatalf("global operation started while an app operation was running: %s", got.State)
	}
	if got := state(t, h, startB); got.State != store.OpQueued {
		t.Fatalf("operation overtook a waiting global operation: %s", got.State)
	}
	if got := h.c.WaitingOn(prune.ID); got != startA.ID {
		t.Fatalf("global operation waits on %q, want the running start %s", got, startA.ID)
	}
	if got := h.c.WaitingOn(startB.ID); got != prune.ID {
		t.Fatalf("queued start waits on %q, want the global operation %s", got, prune.ID)
	}
	g.open()
	doneA, donePrune, doneB := h.wait(startA), h.wait(prune), h.wait(startB)
	for _, op := range []store.Operation{doneA, donePrune, doneB} {
		if op.State != store.OpSucceeded {
			t.Fatalf("%s: %s %s", op.Kind, op.ErrorCode, op.ErrorMessage)
		}
	}
	if donePrune.StartedAt < doneA.FinishedAt {
		t.Fatalf("global operation started (%s) before the running one finished (%s)", donePrune.StartedAt, doneA.FinishedAt)
	}
	if doneB.StartedAt < donePrune.FinishedAt {
		t.Fatalf("queued operation started (%s) before the global one finished (%s)", doneB.StartedAt, donePrune.FinishedAt)
	}
	for _, op := range []store.Operation{prune, startB} {
		if got := h.c.WaitingOn(op.ID); got != "" {
			t.Fatalf("finished operation still reports waiting on %s", got)
		}
	}
}

// An operation that is only out of capacity reports no blocker.
func TestWaitingOnIsEmptyWhenOnlyOutOfCapacity(t *testing.T) {
	h := newHarnessWith(t, func(d *Deps) { d.Concurrency = 1 })
	a, b := h.createApp("alpha"), h.createApp("beta")
	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if isReadyProbe(req) {
			g.enter(id)
		}
		return readyResult
	}
	ctx := t.Context()
	h.mustSubmit(h.c.StartApp(ctx, a.ID, ""))
	g.waitFor(t, 1)
	second := h.mustSubmit(h.c.StartApp(ctx, b.ID, ""))
	h.settle()
	if got := state(t, h, second); got.State != store.OpQueued {
		t.Fatalf("concurrency limit 1 exceeded: %s", got.State)
	}
	if got := h.c.WaitingOn(second.ID); got != "" {
		t.Fatalf("unrelated operation reported as waiting on %s", got)
	}
	g.open()
	h.wait(second)
}

// An app bound to a data service never boots while an earlier queued
// operation for that service is running, but unrelated apps are not held up.
func TestAppWaitsForItsServiceButNotForOthers(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	svc, op, err := h.c.CreateService(ctx, domain.EnginePostgres, "16", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpSucceeded {
		t.Fatalf("service create: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	// Provisioning asks Postgres who owns the database: empty = it does not exist.
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{} }
	withDB, opDB, err := h.c.CreateApp(ctx, CreateAppInput{
		Slug:    "dbapp",
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}},
		Domains: []string{"dbapp.example.com"}, Bindings: []BindingRequest{{Engine: domain.EnginePostgres, Service: svc.Name}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(opDB); got.State != store.OpSucceeded {
		t.Fatalf("provision: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	plain := h.createApp("plain")

	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) > 0 && req.Cmd[0] == "pg_isready" {
			g.enter(id)
		}
		return readyResult
	}
	ensure, _, err := h.c.Submit(ctx, Submission{Kind: KindServiceEnsure, TargetKind: "service", TargetID: svc.Name})
	if err != nil {
		t.Fatal(err)
	}
	g.waitFor(t, 1)
	startDB, err := h.c.StartApp(ctx, withDB.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	startPlain, err := h.c.StartApp(ctx, plain.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(startPlain); got.State != store.OpSucceeded {
		t.Fatalf("unrelated app: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	if got := state(t, h, startDB); got.State != store.OpQueued {
		t.Fatalf("app started while its service operation was running: %s", got.State)
	}
	g.open()
	doneEnsure, doneDB := h.wait(ensure), h.wait(startDB)
	if doneDB.State != store.OpSucceeded {
		t.Fatalf("bound app: %s %s", doneDB.ErrorCode, doneDB.ErrorMessage)
	}
	if doneDB.StartedAt < doneEnsure.FinishedAt {
		t.Fatalf("bound app started (%s) before its service finished (%s)", doneDB.StartedAt, doneEnsure.FinishedAt)
	}
}

func TestShutdownWaitsForEveryRunningOperation(t *testing.T) {
	h := newHarness(t)
	a, b := h.createApp("alpha"), h.createApp("beta")
	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if isReadyProbe(req) {
			g.enter(id)
		}
		return readyResult
	}
	ctx := t.Context()
	opA, _ := h.c.StartApp(ctx, a.ID, "")
	opB, _ := h.c.StartApp(ctx, b.ID, "")
	g.waitFor(t, 2)

	returned := make(chan struct{})
	go func() {
		h.c.Shutdown(5 * time.Second)
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("Shutdown returned while operations were still running")
	case <-time.After(150 * time.Millisecond):
	}
	g.open()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return after the operations finished")
	}
	for _, op := range []store.Operation{opA, opB} {
		if got := state(t, h, op); !got.State.Terminal() {
			t.Fatalf("operation %s left %s after shutdown", got.Kind, got.State)
		}
	}
	if !h.c.Idle() {
		t.Fatal("executor not idle after shutdown")
	}
}

// slowNetEngine widens the window between checking for a network and
// creating it, and counts creations.
type slowNetEngine struct {
	docker.Engine
	created atomic.Int32
}

func (e *slowNetEngine) InspectNetwork(ctx context.Context, name string) (*docker.NetworkInfo, error) {
	n, err := e.Engine.InspectNetwork(ctx, name)
	time.Sleep(20 * time.Millisecond)
	return n, err
}

func (e *slowNetEngine) EnsureNetwork(ctx context.Context, spec docker.NetworkSpec) (docker.NetworkInfo, error) {
	e.created.Add(1)
	return e.Engine.EnsureNetwork(ctx, spec)
}

func TestParallelEnsureNetworksCreatesEachNetworkOnce(t *testing.T) {
	var eng *slowNetEngine
	h := newHarnessWith(t, func(d *Deps) {
		eng = &slowNetEngine{Engine: d.Engine}
		d.Engine = eng
	})
	var wg sync.WaitGroup
	plans := make([]NetworkSettings, 8)
	for i := range plans {
		wg.Go(func() {
			ns, err := h.c.EnsureNetworks(t.Context())
			if err != nil {
				t.Error(err)
			}
			plans[i] = ns
		})
	}
	wg.Wait()
	if n := eng.created.Load(); n != 2 {
		t.Fatalf("networks created %d times, want 2 (apps + data)", n)
	}
	for _, p := range plans {
		if p != plans[0] {
			t.Fatalf("callers saw different network plans: %+v vs %+v", p, plans[0])
		}
	}
}

func TestApplyEdgeIsExclusive(t *testing.T) {
	h := newHarness(t)
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	if err := store.PutSetting(t.Context(), h.store.DB(), edgeSettingKey, s); err != nil {
		t.Fatal(err)
	}
	h.c.edgeMu.Lock()
	done := make(chan error, 1)
	go func() { done <- h.c.applyEdge(t.Context(), &Run{c: h.c}) }()
	select {
	case err := <-done:
		t.Fatalf("applyEdge ran while another edge apply held the lock (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	h.c.edgeMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("applyEdge did not run after the lock was released")
	}
}

// Another operation's edge apply must not activate the route of an app whose
// own operation has not yet seen it ready.
func TestBootingAppIsNotRoutedByAnotherOperation(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	app := h.createApp("shop")
	cur, _ := store.GetApp(ctx, h.store.DB(), app.ID)
	cur.Publication = domain.Published
	if err := store.UpdateApp(ctx, h.store.DB(), cur); err != nil {
		t.Fatal(err)
	}
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	ns, err := h.c.NetworkPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	routed := func() bool {
		t.Helper()
		files, err := h.c.renderEdge(ctx, s, ns, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range files {
			if strings.Contains(name, "app-shop") {
				return !strings.Contains(string(body), "app currently unavailable")
			}
		}
		t.Fatal("no route file rendered for the published app")
		return false
	}

	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if isReadyProbe(req) {
			g.enter(id)
		}
		return readyResult
	}
	op, err := h.c.StartApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	g.waitFor(t, 1)
	if !h.c.isWarming(app.ID) {
		t.Fatal("app being started is not marked warming")
	}
	if routed() {
		t.Fatal("a route was rendered for an app that is running but not ready")
	}
	g.open()
	if got := h.wait(op); got.State != store.OpSucceeded {
		t.Fatalf("start: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	if h.c.isWarming(app.ID) {
		t.Fatal("warming flag survived the operation")
	}
	if !routed() {
		t.Fatal("a ready, running app must be routed")
	}
}

func TestWarmingIsClearedWhenStartFails(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	h.c.Probe = func(context.Context, string) (int, error) { return 503, nil }
	op, err := h.c.StartApp(t.Context(), app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed {
		t.Fatalf("expected not-ready failure, got %s", got.State)
	}
	if h.c.isWarming(app.ID) {
		t.Fatal("a failed operation left the app marked warming")
	}
}

func (h *harness) mustSubmit(op store.Operation, err error) store.Operation {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
	return op
}

func enableEdge(t *testing.T, h *harness) {
	t.Helper()
	s := domain.DefaultEdgeSettings()
	s.Enabled = true
	if err := store.PutSetting(t.Context(), h.store.DB(), edgeSettingKey, s); err != nil {
		t.Fatal(err)
	}
}

// When another operation applies the edge while a published app restarts,
// the app's route is held as unavailable; the restart must restore it once
// the app is ready, and the reconciler must not see drift meanwhile.
func TestRestartRestoresRouteHeldDuringBoot(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	h.mustSucceed(h.c.PublishApp(ctx, app.ID, ""))
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift before restart: %v %v", drift, err)
	}
	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if isReadyProbe(req) {
			g.enter(id)
		}
		return readyResult
	}
	op := h.mustSubmit(h.c.RestartApp(ctx, app.ID, ""))
	g.waitFor(t, 1)
	// The live route is still active; the booting app renders unavailable.
	// That difference is not drift the reconciler should repair.
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("a warming app must not be reported as edge drift: %v %v", drift, err)
	}
	// Another app's operation applies the edge while this one boots.
	if err := h.c.applyEdge(ctx, &Run{c: h.c}); err != nil {
		t.Fatal(err)
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("a warming app must not be reported as edge drift: %v %v", drift, err)
	}
	g.open()
	h.wait(op)
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("route left unavailable after the restart finished: drift=%v err=%v", drift, err)
	}
	events, _ := store.ListEvents(ctx, h.store.DB(), op.ID, 0)
	if !slices.ContainsFunc(events, func(e store.OpEvent) bool { return strings.Contains(e.Message, "refreshing routes") }) {
		t.Fatal("restart did not refresh the held route")
	}
}

// A restart that no other edge apply overlapped does not touch the edge.
func TestRestartWithoutHeldRouteLeavesEdgeAlone(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	enableEdge(t, h)
	app := h.createApp("shop")
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	h.mustSucceed(h.c.PublishApp(ctx, app.ID, ""))
	signals := h.fake.CallCount("Signal")
	op := h.mustSucceed(h.c.RestartApp(ctx, app.ID, ""))
	events, _ := store.ListEvents(ctx, h.store.DB(), op.ID, 0)
	if slices.ContainsFunc(events, func(e store.OpEvent) bool { return strings.Contains(e.Message, "refreshing routes") }) {
		t.Fatal("restart refreshed routes that no other operation held")
	}
	if h.fake.CallCount("Signal") != signals {
		t.Fatal("restart reloaded the edge")
	}
}

func TestDeploysOfDifferentAppsRunInParallel(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	apps := []domain.App{h.createApp("alpha"), h.createApp("beta")}
	for _, a := range apps {
		if _, err := h.c.SetGitSource(ctx, a.ID, GitSourceInput{RepoURL: "https://github.com/o/r.git", Branch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if slices.Contains(req.Cmd, "bento-deploy") {
			g.enter(id)
			return docker.ExecResult{Stdout: []byte("BENTO_COMMIT=" + testCommit + "\n")}
		}
		return readyResult
	}
	var ops []store.Operation
	for _, a := range apps {
		ops = append(ops, h.mustSubmit(h.c.DeployApp(ctx, a.ID, "")))
	}
	g.waitFor(t, 2) // both fetches in flight at once
	g.open()
	for _, op := range ops {
		if got := h.wait(op); got.State != store.OpSucceeded {
			t.Fatalf("deploy: %s %s", got.ErrorCode, got.ErrorMessage)
		}
	}
}

func TestUpdatesOfDifferentAppsRunInParallel(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	apps := []domain.App{h.createApp("alpha"), h.createApp("beta")}
	for _, a := range apps {
		h.mustSucceed(h.c.StartApp(ctx, a.ID, ""))
		h.fake.Delete(h.c.Names.AppContainer(a.ID)) // the update recreates it and waits for readiness
	}
	g := newGate(t)
	h.fake.ExecHook = func(id string, req docker.ExecRequest) docker.ExecResult {
		if isReadyProbe(req) {
			g.enter(id)
		}
		return readyResult
	}
	var ops []store.Operation
	for _, a := range apps {
		op, _, err := h.c.Submit(ctx, Submission{Kind: KindAppUpdate, TargetKind: "app", TargetID: a.ID})
		ops = append(ops, h.mustSubmit(op, err))
	}
	g.waitFor(t, 2)
	g.open()
	for _, op := range ops {
		if got := h.wait(op); got.State != store.OpSucceeded {
			t.Fatalf("update: %s %s", got.ErrorCode, got.ErrorMessage)
		}
	}
}

// Updates of apps running in parallel each rewrite the Redis ACL; the
// read-write-reload sequences must not interleave.
func TestRedisACLSyncsAreSerialized(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{Stdout: []byte("OK")} }
	h.mustSucceed(func() (store.Operation, error) {
		_, op, err := h.c.CreateService(ctx, domain.EngineRedis, "", "")
		return op, err
	}())
	var inflight, peak atomic.Int32
	h.fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) > 3 && req.Cmd[2] == "ACL" {
			n := inflight.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(20 * time.Millisecond)
			inflight.Add(-1)
		}
		return docker.ExecResult{Stdout: []byte("OK")}
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := h.c.syncRedisACL(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("%d Redis ACL reloads overlapped", peak.Load())
	}
}

// A dispatch pass loads the apps once: the first lookup snapshots them, so an
// app created afterwards is not seen by the same pass (the next pass sees it).
func TestDispatchLoadsAppsOncePerPass(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	a := h.createApp("alpha")
	lookup := h.c.appLookup(ctx)
	if cl := classify(store.Operation{Kind: KindAppStart, TargetID: a.ID}, lookup); cl.global {
		t.Fatal("provisioned start classified as global")
	}
	b := h.createApp("beta")
	if _, err := lookup(b.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lookup queried the store again (err=%v)", err)
	}
	if _, err := h.c.appLookup(ctx)(b.ID); err != nil {
		t.Fatalf("a new pass must see the new app: %v", err)
	}
}
