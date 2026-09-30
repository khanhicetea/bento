package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// BackupRequest is the persisted request of a backup operation.
type BackupRequest struct {
	Scope       string `json:"scope"`
	AppID       string `json:"appId,omitempty"`
	BindingID   string `json:"bindingId,omitempty"`
	Compression string `json:"compression"`
	Upload      bool   `json:"upload"`
	Trigger     string `json:"trigger"`
	// ScheduleID names the schedule whose retention and remote apply; empty
	// means the default backup schedule.
	ScheduleID string `json:"scheduleId,omitempty"`
}

type RestoreRequest struct {
	Artifact string `json:"artifact"`
	AppID    string `json:"appId"`
	Database string `json:"database"`
}

// ScheduleState is the scheduler bookkeeping reported for a schedule.
type ScheduleState struct {
	LastSlot  string
	LastRun   string
	LastState string
	LastOpID  string
	Missed    int
}

// backupSpec is the spec_json of a "backup" schedule.
type backupSpec struct {
	Compression  string `json:"compression"`
	Retain       int    `json:"retain"`
	RcloneRemote string `json:"rcloneRemote"`
}

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
			imageID, _, err := c.Images.Ensure(ctx, app.Runtime.ImageKey(), nil)
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

// BackupSchedule returns the default backup schedule, or the built-in
// default when none is stored.
func (c *Controller) BackupSchedule(ctx context.Context) (domain.BackupSchedule, ScheduleState, error) {
	return c.backupSchedule(ctx, store.BackupScheduleID)
}

func (c *Controller) backupSchedule(ctx context.Context, id string) (domain.BackupSchedule, ScheduleState, error) {
	if id == "" {
		id = store.BackupScheduleID
	}
	s := domain.DefaultBackupSchedule()
	row, err := store.GetSchedule(ctx, c.Store.DB(), id)
	if errors.Is(err, store.ErrNotFound) {
		return s, ScheduleState{}, nil
	}
	if err != nil {
		return s, ScheduleState{}, err
	}
	if row.Kind != "backup" {
		return s, ScheduleState{}, fmt.Errorf("schedule %s is a %s schedule, not backup", id, row.Kind)
	}
	spec := backupSpec{Compression: s.Compression, Retain: s.Retain}
	if err := row.DecodeSpec(&spec); err != nil {
		return s, ScheduleState{}, err
	}
	s = domain.BackupSchedule{
		Enabled: row.Enabled, Cron: row.Cron,
		Compression: spec.Compression, Retain: spec.Retain, RcloneRemote: spec.RcloneRemote,
	}
	st := ScheduleState{
		LastSlot: row.LastSlot, LastRun: row.LastRunAt, LastState: row.LastState, LastOpID: row.LastOpID, Missed: row.Missed,
	}
	return s, st, nil
}

func (c *Controller) SetBackupSchedule(ctx context.Context, s domain.BackupSchedule) error {
	var errs domain.ValidationErrors
	if _, err := cron.ParseStandard(s.Cron); err != nil {
		errs.Add("cron", "invalid 5-field cron expression")
	}
	if s.Compression == "" {
		s.Compression = "zstd"
	}
	if s.Compression != "zstd" && s.Compression != "gzip" && s.Compression != "none" {
		errs.Add("compression", "must be zstd, gzip, or none")
	}
	if s.Retain < 1 || s.Retain > 365 {
		errs.Add("retain", "must be 1-365")
	}
	if s.RcloneRemote != "" {
		if _, err := backup.ValidateRemote(s.RcloneRemote); err != nil {
			errs.Add("rcloneRemote", "must be name:path using letters, digits, _ . / -")
		}
	}
	if err := errs.Err(); err != nil {
		return err
	}
	spec, err := json.Marshal(backupSpec{Compression: s.Compression, Retain: s.Retain, RcloneRemote: s.RcloneRemote})
	if err != nil {
		return err
	}
	// Changing the schedule resets its slot bookkeeping to now so that
	// enabling it never triggers a burst of catch-up runs.
	now := platform.FormatTime(time.Now())
	return store.PutSchedule(ctx, c.Store.DB(), store.Schedule{
		ID: store.BackupScheduleID, Kind: "backup", Name: "All backups", Cron: s.Cron, Enabled: s.Enabled,
		Spec: spec, LastSlot: now,
	}, now)
}

// submitScheduledBackup queues the batch for a due "backup" schedule.
func submitScheduledBackup(ctx context.Context, c *Controller, s store.Schedule) (store.Operation, error) {
	sc, _, err := c.backupSchedule(ctx, s.ID)
	if err != nil {
		return store.Operation{}, err
	}
	return c.SubmitBackup(ctx, BackupRequest{
		Scope: "all", Compression: sc.Compression, Trigger: "schedule", Upload: sc.RcloneRemote != "", ScheduleID: s.ID,
	}, "")
}

// SubmitBackup validates scope and queues a backup batch.
func (c *Controller) SubmitBackup(ctx context.Context, req BackupRequest, idem string) (store.Operation, error) {
	switch req.Scope {
	case "all":
	case "app", "binding":
		if _, err := store.GetApp(ctx, c.Store.DB(), req.AppID); err != nil {
			return store.Operation{}, err
		}
	default:
		return store.Operation{}, domain.ValidationErrors{{Field: "scope", Message: "must be all, app, or binding"}}
	}
	if req.Compression == "" {
		req.Compression = "zstd"
	}
	if req.Compression != "zstd" && req.Compression != "gzip" && req.Compression != "none" {
		return store.Operation{}, domain.ValidationErrors{{Field: "compression", Message: "must be zstd, gzip, or none"}}
	}
	if req.Trigger == "" {
		req.Trigger = "manual"
	}
	if req.Upload {
		sched, _, err := c.backupSchedule(ctx, req.ScheduleID)
		if err != nil {
			return store.Operation{}, err
		}
		if sched.RcloneRemote == "" {
			return store.Operation{}, domain.ValidationErrors{{
				Field:   "upload",
				Message: "set an rclone remote in the backup schedule first",
			}}
		}
	}
	op, _, err := c.Submit(
		ctx,
		Submission{
			Kind:           KindBackupRun,
			TargetKind:     "backup",
			TargetID:       "backup",
			IdempotencyKey: idem,
			Request:        req,
			Origin:         req.Trigger,
		},
	)
	return op, err
}

func (c *Controller) backupTargets(ctx context.Context, req BackupRequest) ([]backup.Target, error) {
	apps, err := store.ListApps(ctx, c.Store.DB())
	if err != nil {
		return nil, err
	}
	var out []backup.Target
	for _, app := range apps {
		if req.Scope != "all" && app.ID != req.AppID {
			continue
		}
		for _, b := range app.Bindings {
			if req.Scope == "binding" && b.ID != req.BindingID {
				continue
			}
			if b.Engine == domain.EngineSQLite {
				out = append(out, backup.Target{App: app, Binding: b, Database: b.SQLiteFileID})
				continue
			}
			for _, db := range b.Databases {
				out = append(out, backup.Target{App: app, Binding: b, Database: db})
			}
		}
	}
	return out, nil
}

func (c *Controller) handleBackupRun(ctx context.Context, r *Run) (any, error) {
	var req BackupRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	lock, err := platform.TryLock(c.Layout.BackupLock())
	if err != nil && !errors.Is(err, platform.ErrLocked) {
		return nil, fmt.Errorf("acquire backup lock: %w", err)
	}
	if err != nil {
		return nil, Fail(
			"backup-running",
			"Wait for the running backup batch to finish.",
			"another backup batch holds the lock",
		)
	}
	defer lock.Release()
	runID := r.Op.ID
	if err := store.InsertBackupRun(
		ctx,
		c.Store.DB(),
		store.BackupRun{ID: runID, Trigger: req.Trigger, State: "running", StartedAt: platform.FormatTime(time.Now())},
	); err != nil {
		return nil, err
	}
	finish := func(state, upload, msg string, arts []backup.Artifact) {
		var paths []string
		for _, a := range arts {
			paths = append(paths, a.Path)
		}
		if err := store.FinishBackupRun(
			context.WithoutCancel(ctx),
			c.Store.DB(),
			store.BackupRun{ID: runID, State: state, Artifacts: paths, UploadState: upload, Error: msg},
		); err != nil {
			c.Log.Warn("record backup run", "run", runID, "err", err)
		}
	}
	targets, err := c.backupTargets(ctx, req)
	if err != nil {
		finish("failed", "", err.Error(), nil)
		return nil, err
	}
	deps := c.BackupDeps(func(s string) { r.Info(ctx, "%s", s) })
	var arts []backup.Artifact
	var failed []string
	okKeys := map[string]bool{}
	var attempted int
	for _, t := range targets {
		if err := r.Phase(ctx, fmt.Sprintf("dump %s/%s", t.App.Slug, t.Database)); err != nil {
			finish("cancelled", "", err.Error(), arts)
			return map[string]any{"artifacts": arts}, err
		}
		if t.Binding.Engine == domain.EngineSQLite {
			dbFile := filepath.Join(c.Layout.SQLiteFileDir(t.Binding.SQLiteFileID), t.App.Slug+".db")
			if _, err := os.Lstat(dbFile); errors.Is(err, os.ErrNotExist) {
				r.Info(ctx, "skipped %s/%s: SQLite database file does not exist yet", t.App.Slug, t.Database)
				continue
			}
		}
		attempted++
		a, err := deps.Dump(ctx, t, req.Compression)
		if err != nil {
			// Continue with the remaining targets; this series keeps its
			// older artifacts because retention skips it below.
			r.Warn(ctx, "dump %s/%s failed: %v", t.App.Slug, t.Database, err)
			failed = append(failed, fmt.Sprintf("%s/%s: %v", t.App.Slug, t.Database, err))
			continue
		}
		r.Info(ctx, "published %s (%d bytes)", a.Path, a.SizeBytes)
		arts = append(arts, a)
		okKeys[backup.RetentionKey(a)] = true
	}
	if attempted > 0 && len(failed) == attempted {
		msg := strings.Join(failed, "; ")
		finish("failed", "", msg, arts)
		return map[string]any{"artifacts": arts}, Fail(
			"backup-failed",
			"No target could be dumped; retention was not applied. Fix the failing targets and rerun.",
			"%s",
			msg,
		)
	}
	sched, _, schedErr := c.backupSchedule(ctx, req.ScheduleID)
	if schedErr != nil {
		// Never prune with the default retention in place of the operator's.
		r.Warn(ctx, "backup settings unreadable; retention skipped: %v", schedErr)
	}
	if schedErr == nil && (req.Trigger == "schedule" || req.Scope == "all") && len(okKeys) > 0 {
		if err := r.Phase(ctx, "retention"); err != nil {
			return nil, err
		}
		// Only series dumped successfully in this batch are pruned.
		removed, err := backup.RetainKeys(c.Layout.BackupsDir(), sched.Retain, okKeys)
		if err != nil {
			r.Warn(ctx, "retention: %v", err)
		} else if len(removed) > 0 {
			r.Info(ctx, "retention removed %d old artifact(s)", len(removed))
		}
	}
	state, msg := "succeeded", ""
	var partialErr error
	if len(failed) > 0 {
		state, msg = "partial", strings.Join(failed, "; ")
		partialErr = Fail(
			"backup-partial",
			"Successful artifacts were kept and retention was applied only to their series. Fix the failing targets and rerun.",
			"%d of %d target(s) failed: %s",
			len(failed),
			attempted,
			msg,
		)
	}
	var upload string
	if req.Upload || (req.Trigger == "schedule" && sched.RcloneRemote != "") {
		if err := r.Phase(ctx, "upload"); err != nil {
			return nil, err
		}
		if err := deps.Upload(ctx, sched.RcloneRemote, arts); err != nil {
			upload = "failed"
			finish(state, upload, strings.TrimPrefix(msg+"; upload failed: "+err.Error(), "; "), arts)
			if partialErr != nil {
				return map[string]any{"artifacts": arts}, Fail(
					"backup-partial",
					"Local artifacts were kept. Fix the failing targets and the rclone configuration, then rerun.",
					"%d of %d target(s) failed: %s; upload failed: %v",
					len(failed),
					attempted,
					msg,
					err,
				)
			}
			return map[string]any{"artifacts": arts}, Fail(
				"upload-failed",
				"Local artifacts were kept. Check the rclone configuration and remote.",
				"%v",
				err,
			)
		}
		upload = "succeeded"
	}
	finish(state, upload, msg, arts)
	if partialErr != nil {
		return map[string]any{"artifacts": arts, "upload": upload, "failed": failed}, partialErr
	}
	return map[string]any{"artifacts": arts, "upload": upload}, nil
}

// SubmitRestore requires the exact confirmation "replace <database>".
func (c *Controller) SubmitRestore(
	ctx context.Context,
	req RestoreRequest,
	confirm, idem string,
) (store.Operation, error) {
	if confirm != "replace "+req.Database {
		return store.Operation{}, fmt.Errorf(
			"%w: type exactly %q; restore replaces the database contents",
			ErrConfirmation,
			"replace "+req.Database,
		)
	}
	if _, err := backup.ResolveArtifact(c.Layout.BackupsDir(), req.Artifact); err != nil {
		return store.Operation{}, fmt.Errorf("%w: %v", store.ErrNotFound, err)
	}
	app, err := store.GetApp(ctx, c.Store.DB(), req.AppID)
	if err != nil {
		return store.Operation{}, err
	}
	if _, _, err := findDatabase(app, req.Database); err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(
		ctx,
		Submission{Kind: KindBackupRestore, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Request: req},
	)
	return op, err
}

// findDatabase enforces the app namespace: only this app's recorded
// databases can be restore targets.
func findDatabase(app domain.App, database string) (domain.Binding, bool, error) {
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite && b.SQLiteFileID == database {
			return b, true, nil
		}
		if slices.Contains(b.Databases, database) {
			return b, false, nil
		}
	}
	return domain.Binding{}, false, fmt.Errorf(
		"%w: database %s is not bound to app %s",
		store.ErrNotFound,
		database,
		app.Slug,
	)
}

func (c *Controller) handleBackupRestore(ctx context.Context, r *Run) (any, error) {
	var req RestoreRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	app, err := c.loadApp(ctx, req.AppID)
	if err != nil {
		return nil, err
	}
	b, sqlite, err := findDatabase(app, req.Database)
	if err != nil {
		return nil, err
	}
	path, err := backup.ResolveArtifact(c.Layout.BackupsDir(), req.Artifact)
	if err != nil {
		return nil, err
	}
	engineOK := strings.Contains(slashBase(path), string(b.Engine)+"-")
	if !engineOK {
		return nil, Fail(
			"engine-mismatch",
			"Choose an artifact of the binding's engine.",
			"artifact %s is not a %s backup",
			req.Artifact,
			b.Engine,
		)
	}
	if warn := crossAppWarning(c.Layout.BackupsDir(), path, app.Slug); warn != "" {
		r.Warn(ctx, "%s", warn)
	}
	if err := r.Phase(ctx, "restore"); err != nil {
		return nil, err
	}
	deps := c.BackupDeps(nil)
	if sqlite {
		obs, err := c.observe(ctx, app)
		if err != nil {
			return nil, err
		}
		if obs.Running {
			return nil, Fail("app-running", "Stop the app before restoring a SQLite database.", "app %s is running", app.Slug)
		}
		err = deps.RestoreSQLite(app, b, path)
	} else {
		err = deps.RestoreRelational(ctx, app, b, req.Database, path)
	}
	if err != nil {
		return nil, Fail(
			"restore-failed",
			"The destination may be partially restored; restore again or from another artifact.",
			"%v",
			err,
		)
	}
	return map[string]any{"restored": req.Database, "from": req.Artifact}, nil
}

// crossAppWarning reports when an artifact was produced for a different app
// than the restore target. Artifacts live under <backups>/<source-slug>/, so
// the directory names the source app. Cross-app restores are allowed (for
// example, cloning data into a staging app) but are surfaced as a warning.
func crossAppWarning(backupsDir, artifactPath, targetSlug string) string {
	rel, err := filepath.Rel(backupsDir, artifactPath)
	if err != nil {
		return ""
	}
	source, _, ok := strings.Cut(filepath.ToSlash(rel), "/")
	if !ok || source == targetSlug {
		return ""
	}
	return fmt.Sprintf("artifact %s was taken from app %s, not %s; restoring cross-app data", rel, source, targetSlug)
}

// slashBase returns the text after the last "/" (p itself when there is none).
// Unlike filepath.Base it does not trim trailing slashes or map "" to ".".
func slashBase(p string) string {
	if _, base, ok := strings.CutLast(p, "/"); ok {
		return base
	}
	return p
}

// NextBackup is the first slot of the backup schedule after now, or zero
// when it is off or invalid.
func NextBackup(s domain.BackupSchedule, now time.Time) time.Time {
	return NextRun(store.Schedule{Enabled: s.Enabled, Cron: s.Cron}, now)
}

// SubmitBackupDelete removes one published artifact. It requires the literal
// confirmation "delete"; the path is re-resolved when the operation runs.
func (c *Controller) SubmitBackupDelete(ctx context.Context, artifact, confirm, idem string) (store.Operation, error) {
	if confirm != "delete" {
		return store.Operation{}, fmt.Errorf("%w: type exactly \"delete\" to remove this backup", ErrConfirmation)
	}
	if _, err := backup.ResolveArtifact(c.Layout.BackupsDir(), artifact); err != nil {
		return store.Operation{}, fmt.Errorf("%w: %v", store.ErrNotFound, err)
	}
	op, _, err := c.Submit(
		ctx,
		Submission{Kind: KindBackupDelete, TargetKind: "backup", TargetID: artifact, IdempotencyKey: idem},
	)
	return op, err
}

func (c *Controller) handleBackupDelete(ctx context.Context, r *Run) (any, error) {
	path, err := backup.ResolveArtifact(c.Layout.BackupsDir(), r.Op.TargetID)
	if err != nil {
		return map[string]any{"alreadyRemoved": true}, nil
	}
	if err := r.Phase(ctx, "remove-artifact"); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	r.Info(ctx, "removed backup %s", r.Op.TargetID)
	return map[string]any{"artifact": r.Op.TargetID}, nil
}

// RcloneTestRequest is the persisted request of a remote test.
type RcloneTestRequest struct {
	Remote string `json:"remote"`
}

// SubmitRcloneTest queues a read-only listing of remote, or of the schedule's
// remote when none is given.
func (c *Controller) SubmitRcloneTest(ctx context.Context, remote, idem string) (store.Operation, error) {
	if remote == "" {
		sched, _, err := c.BackupSchedule(ctx)
		if err != nil {
			return store.Operation{}, err
		}
		remote = sched.RcloneRemote
	}
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
