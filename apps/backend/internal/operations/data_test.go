package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// M2: a service whose first boot outlives the readiness deadline is still
// established, so a later ensure refuses a missing volume instead of
// recreating it empty.
func TestSlowFirstBootStillEstablishesService(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{ExitCode: 1} }
	svc, op, err := h.c.CreateService(ctx, domain.EnginePostgres, "16", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "service-not-ready" {
		t.Fatalf("want service-not-ready, got %s %s", got.State, got.ErrorCode)
	}
	row, err := store.GetService(ctx, h.store.DB(), svc.Name)
	if err != nil || !row.Initialized {
		t.Fatalf("service must be established once started: %+v %v", row, err)
	}

	h.fake.Delete(h.c.Names.ServiceContainer(svc.Name))
	if err := h.fake.VolumeRemove(ctx, svc.Volume); err != nil {
		t.Fatal(err)
	}
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{} }
	op, _, err = h.c.Submit(ctx, Submission{Kind: KindServiceEnsure, TargetKind: "service", TargetID: svc.Name})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.ErrorCode != "volume-missing" {
		t.Fatalf("missing volume must be refused, got %s %s", got.State, got.ErrorCode)
	}
}

// M3: a failed ACL LOAD is retried on the next sync even though the ACL file
// on disk no longer changes.
func TestRedisACLReloadRetriedAfterFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.fake.ExecHook = func(string, docker.ExecRequest) docker.ExecResult { return docker.ExecResult{Stdout: []byte("OK")} }
	h.mustSucceed(func() (store.Operation, error) {
		_, op, err := h.c.CreateService(ctx, domain.EngineRedis, "", "")
		return op, err
	}())

	var loads int
	fail := true
	h.fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) > 3 && req.Cmd[2] == "ACL" {
			loads++
			if fail {
				return docker.ExecResult{ExitCode: 1, Stderr: []byte("transient")}
			}
		}
		return docker.ExecResult{Stdout: []byte("OK")}
	}
	if err := h.c.syncRedisACL(ctx); err == nil {
		t.Fatal("first reload should fail")
	}
	fail = false
	if err := h.c.syncRedisACL(ctx); err != nil {
		t.Fatal(err)
	}
	if loads != 2 {
		t.Fatalf("ACL LOAD ran %d times, want 2", loads)
	}
}

// M6: cleanup after a cancelled operation (export resume) must not be cut
// short by the same cancel flag.
func TestUncancellableRunIgnoresCancelRequest(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.cancel() // stop the executor; this test drives the row by hand
	h.c.Shutdown(2 * time.Second)
	op, _, err := store.InsertOperation(ctx, h.store.DB(), store.Operation{ID: platform.NewOperationID(), Kind: KindStackExport, TargetKind: "stack", TargetID: "stack"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.MarkRunning(ctx, h.store.DB(), op.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err := store.RequestCancel(ctx, h.store.DB(), op.ID); err != nil {
		t.Fatal(err)
	}
	r := &Run{c: h.c, Op: op}
	if err := r.Phase(ctx, "archive"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("cancel must be honored at a phase boundary, got %v", err)
	}
	u := r.Uncancellable()
	if err := u.Phase(ctx, "resume"); err != nil || u.Cancelled(ctx) {
		t.Fatalf("resume must ignore the cancel request: %v", err)
	}
}
