package operations

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// toolWrite mimics OpenTool/backup: it rewrites the live config dir without
// reloading or recording applied state.
func (h *harness) toolWrite(app domain.App) {
	h.t.Helper()
	ctx := h.t.Context()
	spec, err := runtime.PlanImage(app.Runtime.ImageKey())
	if err != nil {
		h.t.Fatal(err)
	}
	imageID, _, err := h.fake.ImageID(ctx, spec.Tag())
	if err != nil {
		h.t.Fatal(err)
	}
	passwd, group, err := h.c.Images.IdentityBase(ctx, imageID)
	if err != nil {
		h.t.Fatal(err)
	}
	ns, err := h.c.NetworkPlan(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := runtime.WriteAppConfig(app, runtime.AppContext{Layout: h.layout, TrustedProxies: ns.TrustedProxies(), ImagePasswd: passwd, ImageGroup: group}); err != nil {
		h.t.Fatal(err)
	}
}

// setVacuum moves the app's vacuum slot so its scheduler config changes. The
// slot assigned at creation is random; if it already is Sunday 03:minute,
// the next minute is used so the write is always a change.
func (h *harness) setVacuum(app domain.App, minute int) domain.App {
	h.t.Helper()
	ctx := h.t.Context()
	if v := app.Bindings[0].Vacuum; v != nil && v.DayOfWeek == 0 && v.Hour == 3 && v.Minute == minute {
		minute = (minute + 1) % 60
	}
	v := fmt.Sprintf(`{"dayOfWeek":0,"hour":3,"minute":%d}`, minute)
	if _, err := h.store.DB().ExecContext(ctx, "UPDATE bindings SET vacuum_json = ? WHERE id = ?", v, app.Bindings[0].ID); err != nil {
		h.t.Fatal(err)
	}
	app, _ = store.GetApp(ctx, h.store.DB(), app.ID)
	return app
}

type execLog struct {
	mu   sync.Mutex
	cmds []string
}

func (l *execLog) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.cmds {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func (h *harness) logExec(validateExit int) *execLog {
	l := &execLog{}
	h.fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		cmd := strings.Join(req.Cmd, " ")
		l.mu.Lock()
		l.cmds = append(l.cmds, cmd)
		l.mu.Unlock()
		if strings.Contains(cmd, "minicrond validate") && validateExit != 0 {
			return docker.ExecResult{ExitCode: validateExit, Stderr: []byte("bad")}
		}
		return docker.ExecResult{Stdout: []byte("ready")}
	}
	return l
}

func (h *harness) reconcile(app domain.App) store.Operation {
	op, _, err := h.c.Submit(h.t.Context(), Submission{Kind: KindAppReconcile, TargetKind: "app", TargetID: app.ID, Origin: "reconciler"})
	if err != nil {
		h.t.Fatal(err)
	}
	return h.wait(op)
}

// H4(b): a tool container rewriting config must not absorb a pending scoped
// reload: drift is still reported and reconcile reloads the scheduler.
func TestToolWriteDoesNotAbsorbScopedReload(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))

	if drift, err := h.c.AppConfigDrift(ctx, app); err != nil || drift {
		t.Fatalf("fresh start must not drift: %v %v", drift, err)
	}
	app = h.setVacuum(app, 7)
	h.toolWrite(app)
	drift, err := h.c.AppConfigDrift(ctx, app)
	if err != nil || !drift {
		t.Fatalf("unapplied scheduler change must be drift even when the disk matches: %v %v", drift, err)
	}
	log := h.logExec(0)
	if op := h.reconcile(app); op.State != store.OpSucceeded {
		t.Fatalf("reconcile: %s %s", op.ErrorCode, op.ErrorMessage)
	}
	if !log.has("minicrond reload") {
		t.Fatal("scheduler change written by a tool was never reloaded")
	}
	if drift, err := h.c.AppConfigDrift(ctx, app); err != nil || drift {
		t.Fatalf("after reload there must be no drift: %v %v", drift, err)
	}
}

// H4(a): a rejected validation restores the applied bytes and keeps
// reporting the scope as unapplied.
func TestFailedValidationRestoresAppliedBytes(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	path := filepath.Join(h.layout.AppConfigDir(app.ID), "minicrond.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	app = h.setVacuum(app, 9)
	h.logExec(1)
	if op := h.reconcile(app); op.State == store.OpSucceeded || op.ErrorCode != "validation-failed" {
		t.Fatalf("expected validation failure, got %s %s", op.State, op.ErrorCode)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("rejected scheduler config was not restored to the applied bytes")
	}
	if drift, _ := h.c.AppConfigDrift(ctx, app); !drift {
		t.Fatal("rejected change must still be reported as drift")
	}
	// A later tool write lands the bytes on disk; they must still be reloaded.
	h.toolWrite(app)
	log := h.logExec(0)
	if op := h.reconcile(app); op.State != store.OpSucceeded {
		t.Fatalf("reconcile: %s %s", op.ErrorCode, op.ErrorMessage)
	}
	if !log.has("minicrond reload") {
		t.Fatal("scheduler was not reloaded after the fix")
	}
}

// H6: a stopped container whose network was pruned is recreated instead of
// failing to start forever.
func TestStartRecreatesInstanceWithMissingNetwork(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	old, _ := h.c.Observe(ctx, app)
	h.mustSucceed(h.c.StopApp(ctx, app.ID, ""))
	created := h.fake.CallCount("Create")
	h.fake.FailOn = map[string]error{"Start": errors.New("Error response from daemon: network 3f2a9c not found")}
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	if h.fake.CallCount("Create") != created+1 {
		t.Fatal("instance with a missing network must be recreated")
	}
	obs, _ := h.c.Observe(ctx, app)
	if !obs.Running || obs.ContainerID == old.ContainerID {
		t.Fatalf("expected a new running instance: %+v", obs)
	}
	if _, ok := h.fake.Containers[old.ContainerID]; ok {
		t.Fatal("stale instance was not removed")
	}
}

func TestStartOtherFailureDoesNotRecreate(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	h.mustSucceed(h.c.StopApp(ctx, app.ID, ""))
	created := h.fake.CallCount("Create")
	h.fake.FailOn = map[string]error{"Start": errors.New("port is already allocated")}
	op, err := h.c.StartApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State == store.OpSucceeded {
		t.Fatal("unrelated start failure must fail")
	}
	if h.fake.CallCount("Create") != created {
		t.Fatal("unrelated start failure must not recreate")
	}
}
