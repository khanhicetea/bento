package reconcile

import (
	"errors"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/testutil"
)

// staleImage makes the running app's build predate the image inputs, as
// after an upgrade that changed them: no usable build of its runtime exists.
func staleImage(t *testing.T, h *testutil.Harness, app domain.App) (oldImage, containerID string) {
	t.Helper()
	obs, err := h.C.Observe(t.Context(), app)
	if err != nil || !obs.Running {
		t.Fatalf("app not running: %v", err)
	}
	h.StaleBuilds(t, app.Runtime.ImageKey())
	return obs.Image, obs.ContainerID
}

func countOps(t *testing.T, h *testutil.Harness, kind, target string) (n int, last store.Operation) {
	t.Helper()
	ops, err := store.ListOperations(t.Context(), h.Store.DB(), store.OpFilter{TargetID: target})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if o.Kind == kind {
			if n == 0 {
				last = o
			}
			n++
		}
	}
	return n, last
}

func TestMissingImageIsPreparedBeforeRollout(t *testing.T) {
	h, r, app := setup(t)
	oldImage, before := staleImage(t, h, app)
	key := app.Runtime.ImageKey().String()
	reconciles, _ := countOps(t, h, operations.KindAppReconcile, app.ID)

	pass(t, h, r)
	if n, op := countOps(t, h, operations.KindImagePrepare, key); n != 1 || op.State != store.OpSucceeded {
		t.Fatalf("want one successful image.prepare, got %d (%s %s)", n, op.State, op.ErrorMessage)
	}
	if n, _ := countOps(t, h, operations.KindAppReconcile, app.ID); n != reconciles {
		t.Fatal("the app must not be reconciled before its image is built")
	}
	if obs, _ := h.C.Observe(t.Context(), app); obs.ContainerID != before || !obs.Running {
		t.Fatal("the running instance must keep serving while its image builds")
	}
	if _, ok, _ := h.C.PlannedGeneration(t.Context(), app); !ok {
		t.Fatal("planned build not recorded")
	}

	pass(t, h, r)
	obs, _ := h.C.Observe(t.Context(), app)
	if obs.ContainerID == before || !obs.Running {
		t.Fatal("the app should be replaced once its image is ready")
	}
	if obs.Image == oldImage {
		t.Fatal("replacement still runs the old image")
	}
	if n, _ := countOps(t, h, operations.KindImagePrepare, key); n != 1 {
		t.Fatalf("image prepared %d times, want 1", n)
	}
	if st, ok := r.Statuses()[imageTargetPrefix+key]; ok {
		t.Fatalf("a built image leaves no reconcile target behind: %+v", st)
	}
}

func TestFailedImagePrepareKeepsOldInstance(t *testing.T) {
	h, r, app := setup(t)
	_, before := staleImage(t, h, app)
	key := app.Runtime.ImageKey().String()
	reconciles, _ := countOps(t, h, operations.KindAppReconcile, app.ID)
	r.BaseBackoff = time.Hour // the settling pass must not retry
	h.Fake.FailOn = map[string]error{"BuildImage": errors.New("injected")}

	pass(t, h, r)
	pass(t, h, r) // settles the failure
	if _, op := countOps(t, h, operations.KindImagePrepare, key); op.State != store.OpFailed {
		t.Fatalf("prepare state %s", op.State)
	}
	if n, _ := countOps(t, h, operations.KindAppReconcile, app.ID); n != reconciles {
		t.Fatal("a failed build must not roll out the app")
	}
	if obs, _ := h.C.Observe(t.Context(), app); obs.ContainerID != before || !obs.Running {
		t.Fatal("a failed build must leave the running instance alone")
	}
	st := r.Statuses()[imageTargetPrefix+key]
	if st.Failures == 0 || st.LastError == "" {
		t.Fatalf("the failed build should be observable: %+v", st)
	}
}
