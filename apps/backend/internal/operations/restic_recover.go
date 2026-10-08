package operations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// KindResticRecover cleans up after app backup operations that a backend
// restart interrupted: the partial app of a clone, decrypted snapshots left in
// staging, and job containers still running. Recover submits it at startup;
// it is global, so nothing it removes can belong to a running operation.
const KindResticRecover = "restic.recover"

// ResticRecoverRequest lists the interrupted clones whose partial app is
// removed. Staging and job containers are found by scanning.
type ResticRecoverRequest struct {
	Clones []RecoverClone `json:"clones"`
}

// RecoverClone names an interrupted clone operation and the app id it
// allocated at submission.
type RecoverClone struct {
	OpID  string `json:"opId"`
	AppID string `json:"appId"`
}

// stagingLeftovers lists staging and homes entries only app backup
// operations create while they run.
func (c *Controller) stagingLeftovers() []string {
	var out []string
	if entries, err := os.ReadDir(c.Layout.StagingDir()); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "restic-") || strings.HasPrefix(e.Name(), "home-sqlite-") {
				out = append(out, filepath.Join(c.Layout.StagingDir(), e.Name()))
			}
		}
	}
	if entries, err := os.ReadDir(c.Layout.HomesDir()); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".clone-") {
				out = append(out, filepath.Join(c.Layout.HomesDir(), e.Name()))
			}
		}
	}
	return out
}

// submitResticRecovery queues KindResticRecover when interrupted clones or
// staging leftovers exist. It only reads and submits; the handler acts.
func (c *Controller) submitResticRecovery(ctx context.Context, interrupted []store.Operation) error {
	req := ResticRecoverRequest{Clones: []RecoverClone{}}
	for _, o := range interrupted {
		if !isCloneKind(o.Kind) {
			continue
		}
		var cr ResticCloneRequest // also the embedded part of ResticRemoteRequest
		if err := json.Unmarshal(o.Request, &cr); err != nil || cr.AppID == "" {
			c.Log.Warn("interrupted clone has no app id", "op", o.ID, "err", err)
			continue
		}
		req.Clones = append(req.Clones, RecoverClone{OpID: o.ID, AppID: cr.AppID})
	}
	if len(req.Clones) == 0 && len(c.stagingLeftovers()) == 0 {
		return nil
	}
	_, _, err := c.Submit(ctx, Submission{
		Kind: KindResticRecover, TargetKind: "stack", TargetID: "stack", Origin: "recover", Request: req,
	})
	return err
}

func (c *Controller) handleResticRecover(ctx context.Context, r *Run) (any, error) {
	var req ResticRecoverRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	// Job containers first, so nothing still writes into staging.
	if err := r.Phase(ctx, "containers"); err != nil {
		return nil, err
	}
	c.removeOrphanJobs(ctx, r)

	removed := []string{}
	for _, cl := range req.Clones {
		if err := r.Phase(ctx, "rollback "+cl.AppID); err != nil {
			return nil, err
		}
		app, err := store.GetApp(ctx, c.Store.DB(), cl.AppID)
		if errors.Is(err, store.ErrNotFound) {
			continue // interrupted before the app was created
		}
		if err != nil {
			return nil, err
		}
		r.Warn(ctx, "removing %s, left partly created by interrupted operation %s", app.Slug, cl.OpID)
		c.rollbackClone(context.WithoutCancel(ctx), r.Uncancellable(), app, cl.OpID)
		removed = append(removed, app.Slug)
	}

	if err := r.Phase(ctx, "staging"); err != nil {
		return nil, err
	}
	for _, p := range c.stagingLeftovers() {
		if err := os.RemoveAll(p); err != nil {
			r.Warn(ctx, "remove %s: %v", p, err)
		}
	}
	return map[string]any{"removedApps": removed}, nil
}

// removeOrphanJobs removes this stack's backup job containers, running or
// not, whose operation has finished. Containers labeled with something that
// is not an operation id (volume transfers) are left alone.
func (c *Controller) removeOrphanJobs(ctx context.Context, r *Run) {
	list, err := c.Engine.List(ctx, map[string]string{runtime.LabelStackID: c.Stack.ID})
	if err != nil {
		r.Warn(ctx, "list job containers: %v", err)
		return
	}
	for _, t := range list {
		if !c.Names.OwnedBy(t.Labels, runtime.RoleBackup, "") {
			continue
		}
		opID := t.Labels[runtime.LabelOperation]
		if opID == "" || opID == r.Op.ID {
			continue
		}
		op, err := store.GetOperation(ctx, c.Store.DB(), opID)
		if err != nil || !op.State.Terminal() {
			continue
		}
		if err := c.Engine.Remove(ctx, t.ID); err != nil {
			r.Warn(ctx, "remove job container: %v", err)
		}
	}
}

// cloneKinds are the operations that create an app from a snapshot.
var cloneKinds = []string{KindAppCloneFromBackup, KindAppRestoreFromBackup}

func isCloneKind(kind string) bool { return slices.Contains(cloneKinds, kind) }
