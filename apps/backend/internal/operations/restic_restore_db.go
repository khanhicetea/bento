package operations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// KindResticRestoreDB replaces one database of an app with its dump in one
// of the app's own snapshots. It is unlisted in claims.go, so it runs alone:
// the relational restore stages and grants on shared data services.
const KindResticRestoreDB = "restic.restore-db"

// ResticRestoreDBRequest is the persisted intent of a database restore.
type ResticRestoreDBRequest struct {
	Snapshot string `json:"snapshot"`
	Database string `json:"database"`
	// Binding is the binding id; required when the name is in two bindings.
	Binding string `json:"binding,omitempty"`
}

// SubmitResticRestoreDB requires the exact confirmation "replace <database>".
func (c *Controller) SubmitResticRestoreDB(
	ctx context.Context,
	appID string,
	req ResticRestoreDBRequest,
	confirm, idem string,
) (store.Operation, error) {
	if confirm != "replace "+req.Database {
		return store.Operation{}, fmt.Errorf(
			"%w: type exactly %q; restore replaces the database contents", ErrConfirmation, "replace "+req.Database)
	}
	if req.Snapshot == "" {
		req.Snapshot = SnapshotLatest
	}
	if !validSnapshotRef(req.Snapshot) {
		return store.Operation{}, domain.ValidationErrors{{Field: "snapshot", Message: "choose a snapshot"}}
	}
	app, _, err := c.requireResticRepo(ctx, appID)
	if err != nil {
		return store.Operation{}, err
	}
	if _, _, err := findDatabase(app, req.Binding, req.Database); err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindResticRestoreDB, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Request: req,
	})
	return op, err
}

// dumpFileName is the shape of a dump path inside a snapshot's Bento
// directory. It is also used as a restic --include pattern, so glob
// characters must never reach it.
var dumpFileName = regexp.MustCompile(`^(db|sqlite)/[A-Za-z0-9_.-]+$`)

// snapshotDump finds the dump of database in a manifest. Only a dump of the
// binding's engine qualifies, and for MySQL and PostgreSQL only one taken
// from the binding's service: every relational binding's first database is
// <slug>_main, so an app bound to two services of one engine has two dumps
// with the same name.
func snapshotDump(man ResticManifest, b domain.Binding, database string) (ResticDump, bool) {
	for _, d := range man.Dumps {
		if d.Engine != b.Engine {
			continue
		}
		if d.Engine != domain.EngineSQLite && d.Service != b.Service {
			continue
		}
		if d.Database == database || (d.Engine == domain.EngineSQLite && d.FileID == database) {
			return d, true
		}
	}
	return ResticDump{}, false
}

func (c *Controller) handleResticRestoreDB(ctx context.Context, r *Run) (any, error) {
	var req ResticRestoreDBRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	b, sqlite, err := findDatabase(app, req.Binding, req.Database)
	if err != nil {
		return nil, err
	}
	if sqlite {
		// Checked before the download, and again right before the swap.
		if err := c.requireStopped(ctx, app); err != nil {
			return nil, err
		}
	}
	env, job, err := c.resticJobFor(ctx, r, app, v, func(env *resticEnv) backup.ResticMounts {
		return backup.ResticMounts{CtlDir: env.ctl, RestoreDir: env.data}
	})
	if err != nil {
		return nil, err
	}
	defer env.close()
	defer job.Close(ctx)
	snapshot, err := resolveAppSnapshot(ctx, r, job, app.ID, req.Snapshot)
	if err != nil {
		return nil, err
	}

	if err := r.Phase(ctx, "read-manifest"); err != nil {
		return nil, err
	}
	man, _, err := readSnapshotMeta(ctx, job, snapshot)
	if err != nil {
		return nil, err
	}
	if man.AppID != app.ID {
		return nil, Fail("snapshot-invalid", "Choose a snapshot of this app.",
			"snapshot %s belongs to app %s, not %s", snapshot, orDash(man.Slug), app.Slug)
	}
	d, ok := snapshotDump(man, b, req.Database)
	if !ok {
		return nil, Fail("database-missing", "Choose a snapshot taken while the database was bound to the app.",
			"snapshot %s holds no %s dump of %s", snapshot, b.Engine, req.Database)
	}
	if !dumpFileName.MatchString(d.File) || strings.Contains(d.File, "..") {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.",
			"unexpected dump path %q", d.File)
	}

	if err := r.Phase(ctx, "download"); err != nil {
		return nil, err
	}
	if err := job.Run(ctx, []string{"restore", snapshot, "--target", backup.ResticRestoreMount,
		"--include", backup.ResticBentoMount + "/" + d.File}, nil); err != nil {
		return nil, resticFail(err, "restore")
	}
	if job.LockLeaked() {
		r.Warn(ctx, "%s", lockLeakWarning)
	}
	bentoDir := filepath.Join(env.data, "backup", "bento")
	if err := rejectLinkedTree(bentoDir); err != nil {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "%v", err)
	}
	dump := filepath.Join(bentoDir, filepath.FromSlash(d.File))
	if info, err := os.Lstat(dump); err != nil || !info.Mode().IsRegular() {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.",
			"%s is missing from the restored snapshot", d.File)
	}

	if err := r.Phase(ctx, "restore"); err != nil {
		return nil, err
	}
	deps := c.BackupDeps(nil)
	if sqlite {
		if err := c.requireStopped(ctx, app); err != nil {
			return nil, err
		}
		if err := deps.RestoreSQLite(app, b, dump); err != nil {
			return nil, Fail("restore-failed",
				"The destination may be partially restored; restore again or from another snapshot.", "%v", err)
		}
		r.Info(ctx, "restored %s from snapshot %s", req.Database, shortID(snapshot))
		return map[string]any{"restored": req.Database, "snapshot": snapshot}, nil
	}
	// The app keeps running through a swap; it is stopped only when MySQL
	// must rebuild the live database in place, and started again after.
	var stopped bool
	defer func() {
		if stopped {
			_ = c.resumeStopped(ctx, r, []domain.App{app}, nil)
		}
	}()
	method, err := deps.RestoreRelational(ctx, app, b, req.Database, dump, c.restoreOptions(ctx, r, func(ctx context.Context) error {
		if err := r.Phase(ctx, "stop-app"); err != nil {
			return err
		}
		obs, err := c.observe(ctx, app)
		if err != nil || !obs.Exists || !obs.Running {
			return err
		}
		if err := c.Engine.Stop(ctx, obs.ContainerID, runtime.StopTimeout); err != nil {
			return err
		}
		stopped = true
		r.Info(ctx, "stopped %s: the database has views, routines or events and is restored in place", app.Slug)
		return nil
	}))
	if err != nil {
		return nil, restoreFailed(err)
	}
	r.Info(ctx, "restored %s from snapshot %s (%s)", req.Database, shortID(snapshot), method)
	return map[string]any{"restored": req.Database, "snapshot": snapshot, "method": method}, nil
}

// requireStopped refuses a SQLite restore while the app runs: a writer could
// hold the file.
func (c *Controller) requireStopped(ctx context.Context, app domain.App) error {
	obs, err := c.observe(ctx, app)
	if err != nil {
		return err
	}
	if obs.Running {
		return Fail("app-running", "Stop the app before restoring a SQLite database.", "app %s is running", app.Slug)
	}
	return nil
}

// shortID is the 8-character form restic prints for a snapshot id.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
