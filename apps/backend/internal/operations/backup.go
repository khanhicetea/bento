package operations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// BackupDeps wires the backup package (database dumps and restores, restic
// and rclone job containers) to this controller.
func (c *Controller) BackupDeps(progress func(string)) backup.Deps {
	return backup.Deps{
		Engine: c.Engine, Layout: c.Layout, Names: c.Names, Data: c.Data(), Progress: progress,
		ServiceContainer: func(ctx context.Context, name string) (string, domain.DataService, error) {
			svc, err := store.GetService(ctx, c.Store.DB(), name)
			if err != nil {
				return "", domain.DataService{}, err
			}
			id, err := c.serviceContainer(ctx, svc.DataService)
			return id, svc.DataService, err
		},
		ToolSpec: func(ctx context.Context, app domain.App) (docker.ContainerSpec, error) {
			build, err := c.ensureAppBuild(ctx, app, nil)
			imageID := build.ImageID
			if err != nil {
				return docker.ContainerSpec{}, err
			}
			passwd, group, err := c.Images.IdentityBase(ctx, imageID)
			if err != nil {
				return docker.ContainerSpec{}, err
			}
			ns, err := c.NetworkPlan(ctx)
			if err != nil {
				return docker.ContainerSpec{}, err
			}
			m, err := runtime.WriteAppConfig(
				app,
				runtime.AppContext{Layout: c.Layout, TrustedProxies: ns.TrustedProxies(), ImagePasswd: passwd, ImageGroup: group},
			)
			if err != nil {
				return docker.ContainerSpec{}, err
			}
			return runtime.ToolContainerSpec(
				runtime.AppInputs{App: app, Names: c.Names, Layout: c.Layout, ImageID: imageID, Materialized: m},
				"backup",
				time.Hour,
			), nil
		},
	}
}

// findDatabase enforces the app namespace: only this app's recorded
// databases can be restore targets. bindingID, when set, names the binding;
// it is required when the name is in more than one binding (MySQL and
// PostgreSQL bindings both start with <slug>_main), so a restore never
// guesses which engine's database to replace.
func findDatabase(app domain.App, bindingID, database string) (domain.Binding, bool, error) {
	var matches []domain.Binding
	for _, b := range app.Bindings {
		if bindingID != "" && b.ID != bindingID {
			continue
		}
		if (b.Engine == domain.EngineSQLite && b.SQLiteFileID == database) || slices.Contains(b.Databases, database) {
			matches = append(matches, b)
		}
	}
	switch len(matches) {
	case 0:
		if bindingID != "" {
			return domain.Binding{}, false, fmt.Errorf("%w: database %s is not in binding %s of app %s",
				store.ErrNotFound, database, bindingID, app.Slug)
		}
		return domain.Binding{}, false, fmt.Errorf("%w: database %s is not bound to app %s",
			store.ErrNotFound, database, app.Slug)
	case 1:
		return matches[0], matches[0].Engine == domain.EngineSQLite, nil
	}
	return domain.Binding{}, false, domain.ValidationErrors{{Field: "binding",
		Message: fmt.Sprintf("database %s is in more than one binding of %s; choose the binding", database, app.Slug)}}
}

// restoreOptions connects a relational restore to r; quiesce may be nil.
func (c *Controller) restoreOptions(ctx context.Context, r *Run, quiesce func(context.Context) error) backup.RestoreOptions {
	return backup.RestoreOptions{
		Phase:   r.Phase,
		Quiesce: quiesce,
		Warn:    func(msg string) { r.Warn(ctx, "%s", msg) },
	}
}

// restoreFailed reports a failed relational restore, saying whether the
// live database was touched.
func restoreFailed(err error) error {
	if lu := (*backup.LiveUnchangedError)(nil); errors.As(err, &lu) {
		return Fail("restore-failed", "The live database is unchanged; fix the cause or choose another snapshot.", "%v", err)
	}
	return Fail(
		"restore-failed",
		"The live database may be partly replaced; restore again or from another snapshot.",
		"%v",
		err,
	)
}

// RcloneTestRequest is the persisted request of a remote test.
type RcloneTestRequest struct {
	Remote string `json:"remote"`
}

// SubmitRcloneTest queues a read-only listing of remote.
func (c *Controller) SubmitRcloneTest(ctx context.Context, remote, idem string) (store.Operation, error) {
	if _, err := backup.ValidateRemote(remote); err != nil {
		return store.Operation{}, domain.ValidationErrors{{Field: "remote", Message: err.Error()}}
	}
	op, _, err := c.Submit(
		ctx,
		Submission{
			Kind:           KindRcloneTest,
			TargetKind:     "backup",
			TargetID:       remote,
			IdempotencyKey: idem,
			Request:        RcloneTestRequest{Remote: remote},
		},
	)
	return op, err
}

func (c *Controller) handleRcloneTest(ctx context.Context, r *Run) (any, error) {
	var req RcloneTestRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "test-remote"); err != nil {
		return nil, err
	}
	msg, err := c.BackupDeps(nil).TestRemote(ctx, req.Remote)
	if err != nil {
		return nil, Fail("rclone-test-failed", "Fix the remote in the rclone shell, then test again.", "%v", err)
	}
	r.Info(ctx, "%s", msg)
	return map[string]any{"remote": req.Remote, "message": msg}, nil
}

// OpenRcloneShell starts an idle rclone container for an interactive shell
// that can edit the stack's rclone config and nothing else.
func (c *Controller) OpenRcloneShell(ctx context.Context, lifetime time.Duration) (*ToolSession, error) {
	id, err := c.BackupDeps(nil).OpenRcloneShell(ctx, "t"+platform.RandomHex(5), lifetime)
	if err != nil {
		return nil, err
	}
	return &ToolSession{ContainerID: id, c: c}, nil
}
