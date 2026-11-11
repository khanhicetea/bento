package operations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/traffic"
)

const trafficHost = "api.example.com"

// trafficHarness has a running edge serving trafficHost (an upstream host)
// with its access log on.
func trafficHarness(t *testing.T, accessLog bool) *harness {
	t.Helper()
	return trafficHarnessWith(t, accessLog, nil)
}

func trafficHarnessWith(t *testing.T, accessLog bool, mutate func(*Deps)) *harness {
	t.Helper()
	h := newHarnessWith(t, mutate)
	enableEdge(t, h)
	_, op, err := h.c.CreateHost(t.Context(), HostInput{Name: trafficHost, Target: domain.HostTargetUpstream,
		Upstreams: []string{"http://10.0.0.9:8080"}, Enabled: true,
		Route: domain.Route{TLS: domain.TLSSelfSigned, AccessLog: accessLog}}, "")
	h.mustSucceed(op, err)
	old := trafficReopenWait
	trafficReopenWait = 0
	t.Cleanup(func() { trafficReopenWait = old })
	return h
}

// goAccessJobs makes GoAccess jobs write the fixture report (or exit with
// code) and records their specs.
func goAccessJobs(t *testing.T, h *harness, code *int64) *[]docker.ContainerSpec {
	t.Helper()
	fixture, err := os.ReadFile("../traffic/testdata/goaccess-1.12.json")
	if err != nil {
		t.Fatal(err)
	}
	var specs []docker.ContainerSpec
	h.fake.WaitHook = func(spec docker.ContainerSpec) (int64, error) {
		if spec.Config == nil || spec.Config.Image != domain.GoAccessImage {
			return 0, nil
		}
		specs = append(specs, spec)
		if *code != 0 {
			return *code, nil
		}
		dir := spec.HostConfig.Mounts[0].Source
		return 0, os.WriteFile(filepath.Join(dir, traffic.NextReportFile), fixture, 0o600)
	}
	return &specs
}

func writeTrafficLog(t *testing.T, h *harness, host, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.c.Layout.TrafficLogsDir(), edge.TrafficLogFile(host)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pendingLogs(t *testing.T, h *harness, host string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(h.c.Layout.TrafficHostDir(host), traffic.PendingDir))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestTrafficLogRenderedOnlyForAccessLogHosts(t *testing.T) {
	h := trafficHarness(t, true)
	live := filepath.Join(h.c.Layout.EdgeConfDir(), "live")
	site, _ := os.ReadFile(filepath.Join(live, "sites", edge.RouteName(trafficHost)+".conf"))
	if !strings.Contains(string(site), "access_log /var/log/bento-edge/api.example.com.log bento_traffic if=$bento_traffic;") {
		t.Fatalf("site lacks the traffic log:\n%s", site)
	}
	main, _ := os.ReadFile(filepath.Join(live, "nginx.conf"))
	if !strings.Contains(string(main), "log_format bento_traffic") || !strings.Contains(string(main), "map $status $bento_traffic {") {
		t.Fatalf("nginx.conf lacks the traffic format:\n%s", main)
	}
	ins, _ := h.fake.Inspect(t.Context(), h.c.Names.EdgeContainer())
	spec := h.fake.Containers[ins.ID].Spec
	if !slices.ContainsFunc(spec.HostConfig.Mounts, func(m mount.Mount) bool {
		return m.Source == h.c.Layout.TrafficLogsDir() && m.Target == edge.LogsMount && !m.ReadOnly
	}) {
		t.Fatalf("edge does not mount the traffic logs: %+v", spec.HostConfig.Mounts)
	}

	// Without any access log the generated config has no traffic parts.
	off := trafficHarness(t, false)
	live = filepath.Join(off.c.Layout.EdgeConfDir(), "live")
	main, _ = os.ReadFile(filepath.Join(live, "nginx.conf"))
	site, _ = os.ReadFile(filepath.Join(live, "sites", edge.RouteName(trafficHost)+".conf"))
	if strings.Contains(string(main), "bento_traffic") || strings.Contains(string(site), "bento_traffic") {
		t.Fatal("traffic config rendered for a host without access log")
	}
}

func TestTrafficCycleRotatesReopensAndReports(t *testing.T) {
	h := trafficHarness(t, true)
	ctx := t.Context()
	var code int64
	jobs := goAccessJobs(t, h, &code)
	writeTrafficLog(t, h, trafficHost, "203.0.113.1 [05/Nov/2026:10:00:00 +0700] \"GET / HTTP/2.0\" 200 5 \"-\" \"x\" 0.001\n")
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if edgeControls(h, "reopen") == 0 {
		t.Fatalf("the edge was not asked to reopen its logs: %v", h.fake.Calls)
	}
	if _, err := os.Stat(filepath.Join(h.c.Layout.TrafficLogsDir(), edge.TrafficLogFile(trafficHost))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the live log was not rotated: %v", err)
	}
	if p := pendingLogs(t, h, trafficHost); len(p) != 0 {
		t.Fatalf("pending logs left after a successful run: %v", p)
	}
	if len(*jobs) != 1 {
		t.Fatalf("goaccess jobs = %d", len(*jobs))
	}
	job := (*jobs)[0]
	hc := job.HostConfig
	if hc.NetworkMode != "none" || !hc.ReadonlyRootfs || !slices.Equal(hc.CapDrop, []string{"ALL"}) || len(hc.CapAdd) != 0 ||
		len(hc.Mounts) != 1 || hc.Mounts[0].Source != h.c.Layout.TrafficHostDir(trafficHost) || hc.Memory == 0 {
		t.Fatalf("goaccess job is not confined: %+v", hc)
	}
	if !slices.ContainsFunc(job.Config.Cmd, func(a string) bool { return strings.HasPrefix(a, "/traffic/pending/") }) {
		t.Fatalf("goaccess job reads no pending log: %v", job.Config.Cmd)
	}
	if n := len(h.fake.Containers); n != 1 {
		t.Fatalf("the goaccess job container must be removed; %d containers left", n)
	}
	r, _, ok, err := h.c.TrafficReport(trafficHost)
	if err != nil || !ok || r.Requests != 100 {
		t.Fatalf("report = %+v %v %v", r.Requests, ok, err)
	}
	if st := h.c.TrafficStatus(trafficHost); st.LastRun.IsZero() || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}

	// A run with no new logs and a fresh report does nothing.
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*jobs) != 1 {
		t.Fatalf("goaccess ran without new logs: %d jobs", len(*jobs))
	}
}

func TestTrafficFailedRunKeepsLogsForTheNextRun(t *testing.T) {
	h := trafficHarness(t, true)
	ctx := t.Context()
	code := int64(1)
	jobs := goAccessJobs(t, h, &code)
	writeTrafficLog(t, h, trafficHost, "line\n")
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if st := h.c.TrafficStatus(trafficHost); !strings.Contains(st.Error, "goaccess exited 1") {
		t.Fatalf("status = %+v", st)
	}
	if p := pendingLogs(t, h, trafficHost); len(p) != 1 {
		t.Fatalf("a failed run must keep its log: %v", p)
	}
	if _, _, ok, _ := h.c.TrafficReport(trafficHost); ok {
		t.Fatal("no report may appear from a failed run")
	}
	code = 0
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if p := pendingLogs(t, h, trafficHost); len(p) != 0 || len(*jobs) != 2 {
		t.Fatalf("the retry must read the kept log: pending %v, jobs %d", p, len(*jobs))
	}
	if st := h.c.TrafficStatus(trafficHost); st.Error != "" {
		t.Fatalf("status after retry = %+v", st)
	}
}

func TestTrafficUndeliveredReopenDefersReading(t *testing.T) {
	h := trafficHarness(t, true)
	ctx := t.Context()
	var code int64
	jobs := goAccessJobs(t, h, &code)
	writeTrafficLog(t, h, trafficHost, "line\n")
	h.fake.FailOn = map[string]error{edgeControlCall(h, "reopen"): errors.New("injected reopen failure")}
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	// The edge may still write to the renamed file: it must not be read.
	if len(*jobs) != 0 {
		t.Fatal("goaccess read a log the edge may still write to")
	}
	if p := pendingLogs(t, h, trafficHost); len(p) != 1 {
		t.Fatalf("pending = %v", p)
	}
	if st := h.c.TrafficStatus(trafficHost); !strings.Contains(st.Error, "logs not rotated") {
		t.Fatalf("status = %+v", st)
	}
	// Nothing new was logged, but the next run still reopens before reading.
	before := edgeControls(h, "reopen")
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if edgeControls(h, "reopen") != before+1 {
		t.Fatal("leftover rotated logs were read without reopening the edge's logs first")
	}
	if len(*jobs) != 1 || len(pendingLogs(t, h, trafficHost)) != 0 {
		t.Fatalf("the next run must reopen and read: jobs %d", len(*jobs))
	}
}

// stallingEngine blocks Exec until its context ends while stall is set, like
// a Docker daemon that never answers.
type stallingEngine struct {
	docker.Engine
	stall atomic.Bool
}

func (e *stallingEngine) Exec(ctx context.Context, id string, req docker.ExecRequest) (docker.ExecResult, error) {
	if e.stall.Load() {
		<-ctx.Done()
		return docker.ExecResult{}, ctx.Err()
	}
	return e.Engine.Exec(ctx, id, req)
}

// A stalled reopen must not hold edgeMu forever: the cycle gives up and the
// next one retries.
func TestTrafficReopenTimesOutAndReleasesTheEdge(t *testing.T) {
	var eng *stallingEngine
	h := trafficHarnessWith(t, true, func(d *Deps) {
		eng = &stallingEngine{Engine: d.Engine}
		d.Engine = eng
	})
	old := trafficReopenTimeout
	trafficReopenTimeout = 50 * time.Millisecond
	t.Cleanup(func() { trafficReopenTimeout = old })
	var code int64
	jobs := goAccessJobs(t, h, &code)
	writeTrafficLog(t, h, trafficHost, "line\n")
	eng.stall.Store(true)

	done := make(chan error, 1)
	go func() { done <- h.c.TrafficCycle(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the traffic cycle is stuck on a stalled reopen")
	}
	if len(*jobs) != 0 {
		t.Fatal("goaccess read a log the edge may still write to")
	}
	if p := pendingLogs(t, h, trafficHost); len(p) != 1 {
		t.Fatalf("pending = %v", p)
	}
	if st := h.c.TrafficStatus(trafficHost); !strings.Contains(st.Error, "logs not rotated") {
		t.Fatalf("status = %+v", st)
	}
	if !h.c.edgeMu.TryLock() {
		t.Fatal("edgeMu is still held after the reopen timed out")
	}
	h.c.edgeMu.Unlock()

	// The next cycle retries and delivers.
	eng.stall.Store(false)
	if err := h.c.TrafficCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(*jobs) != 1 || len(pendingLogs(t, h, trafficHost)) != 0 {
		t.Fatalf("the next run must reopen and read: jobs %d", len(*jobs))
	}
}

func TestTrafficWithoutRunningEdgeReadsWithoutSignal(t *testing.T) {
	h := trafficHarness(t, true)
	ctx := t.Context()
	var code int64
	jobs := goAccessJobs(t, h, &code)
	h.fake.SetRunning(h.c.Names.EdgeContainer(), false)
	writeTrafficLog(t, h, trafficHost, "line\n")
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if edgeControls(h, "reopen") != 0 {
		t.Fatal("a stopped edge was signalled")
	}
	if len(*jobs) != 1 {
		t.Fatalf("jobs = %d", len(*jobs))
	}
}

func TestTrafficRemovedHostLosesItsReportAndStaleLogsDrop(t *testing.T) {
	h := trafficHarness(t, true)
	ctx := t.Context()
	var code int64
	goAccessJobs(t, h, &code)
	// A log that could not be read for longer than MaxPendingAge is dropped.
	stale := filepath.Join(h.c.Layout.TrafficHostDir(trafficHost), traffic.PendingDir)
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	old := traffic.PendingName(time.Now().Add(-traffic.MaxPendingAge - time.Hour))
	if err := os.WriteFile(filepath.Join(stale, old), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTrafficLog(t, h, trafficHost, "line\n")
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if p := pendingLogs(t, h, trafficHost); len(p) != 0 {
		t.Fatalf("pending = %v", p)
	}
	h.mustSucceed(h.c.DeleteHost(ctx, trafficHost, "delete "+trafficHost, ""))
	// The edge may have written a last line before the reload.
	writeTrafficLog(t, h, trafficHost, "line\n")
	if err := h.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.c.Layout.TrafficHostDir(trafficHost)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a removed host keeps its traffic data: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.c.Layout.TrafficLogsDir(), edge.TrafficLogFile(trafficHost))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a removed host keeps its live log: %v", err)
	}
}

func TestTrafficIgnoresForeignFiles(t *testing.T) {
	h := trafficHarness(t, true)
	var code int64
	jobs := goAccessJobs(t, h, &code)
	for _, name := range []string{"../escape.log", "notes.txt", "UPPER.example.com.log"} {
		_ = os.WriteFile(filepath.Join(h.c.Layout.TrafficLogsDir(), filepath.Base(name)), []byte("x\n"), 0o644)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(h.c.Layout.TrafficLogsDir(), "link.example.com.log")); err != nil {
		t.Fatal(err)
	}
	if err := h.c.TrafficCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(*jobs) != 0 {
		t.Fatalf("foreign files were read: %d jobs", len(*jobs))
	}
	if _, err := os.Lstat(filepath.Join(h.c.Layout.TrafficLogsDir(), "link.example.com.log")); err != nil {
		t.Fatalf("a symlink was moved: %v", err)
	}
}
