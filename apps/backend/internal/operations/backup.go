package operations

import (
	"context"
	"errors"
	"fmt"
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

const (
	scheduleSettingKey = "backup_schedule"
	scheduleStateKey   = "backup_schedule_state"
)

// BackupRequest is the persisted request of a backup operation.
type BackupRequest struct {
	Scope       string `json:"scope"`
	AppID       string `json:"appId,omitempty"`
	BindingID   string `json:"bindingId,omitempty"`
	Compression string `json:"compression"`
	Upload      bool   `json:"upload"`
	Trigger     string `json:"trigger"`
}

type RestoreRequest struct {
	Artifact string `json:"artifact"`
	AppID    string `json:"appId"`
	Database string `json:"database"`
}

// ScheduleState records scheduler bookkeeping (never replayed).
type ScheduleState struct {
	LastSlot  string `json:"lastSlot"`
	LastRun   string `json:"lastRun"`
	LastState string `json:"lastState"`
	Missed    int    `json:"missed"`
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
			m, err := runtime.WriteAppConfig(app, runtime.AppContext{Layout: c.Layout, TrustedProxies: ns.TrustedProxies(), ImagePasswd: passwd, ImageGroup: group})
			if err != nil {
				return docker.ContainerSpec{}, err
			}
			return runtime.ToolContainerSpec(runtime.AppInputs{App: app, Names: c.Names, Layout: c.Layout, ImageID: imageID, Materialized: m}, "backup", time.Hour), nil
		},
	}
}

func (c *Controller) BackupSchedule(ctx context.Context) (domain.BackupSchedule, ScheduleState, error) {
	s := domain.DefaultBackupSchedule()
	var st ScheduleState
	if _, err := store.GetSetting(ctx, c.Store.DB(), scheduleSettingKey, &s); err != nil {
		return s, st, err
	}
	_, err := store.GetSetting(ctx, c.Store.DB(), scheduleStateKey, &st)
	return s, st, err
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
	if s.RcloneRemote != "" && strings.ContainsAny(s.RcloneRemote, " \n\t'\"") {
		errs.Add("rcloneRemote", "invalid remote")
	}
	if err := errs.Err(); err != nil {
		return err
	}
	return c.Store.Tx(ctx, func(q store.Q) error {
		// Changing the schedule resets its slot bookkeeping to now so that
		// enabling it never triggers a burst of catch-up runs.
		if err := store.PutSetting(ctx, q, scheduleStateKey, ScheduleState{LastSlot: platform.FormatTime(time.Now())}); err != nil {
			return err
		}
		return store.PutSetting(ctx, q, scheduleSettingKey, s)
	})
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
	op, _, err := c.Submit(ctx, Submission{Kind: KindBackupRun, TargetKind: "backup", TargetID: "backup", IdempotencyKey: idem, Request: req, Origin: req.Trigger})
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
	if err != nil {
		return nil, Fail("backup-running", "Wait for the running backup batch to finish.", "another backup batch holds the lock")
	}
	defer lock.Release()
	runID := r.Op.ID
	if err := store.InsertBackupRun(ctx, c.Store.DB(), store.BackupRun{ID: runID, Trigger: req.Trigger, State: "running", StartedAt: platform.FormatTime(time.Now())}); err != nil {
		return nil, err
	}
	finish := func(state, upload, msg string, arts []backup.Artifact) {
		var paths []string
		for _, a := range arts {
			paths = append(paths, a.Path)
		}
		_ = store.FinishBackupRun(context.WithoutCancel(ctx), c.Store.DB(), store.BackupRun{ID: runID, State: state, Artifacts: paths, UploadState: upload, Error: msg})
	}
	targets, err := c.backupTargets(ctx, req)
	if err != nil {
		finish("failed", "", err.Error(), nil)
		return nil, err
	}
	deps := c.BackupDeps(func(s string) { r.Info(ctx, "%s", s) })
	var arts []backup.Artifact
	for _, t := range targets {
		if err := r.Phase(ctx, fmt.Sprintf("dump %s/%s", t.App.Slug, t.Database)); err != nil {
			finish("cancelled", "", err.Error(), arts)
			return map[string]any{"artifacts": arts}, err
		}
		a, err := deps.Dump(ctx, t, req.Compression)
		if err != nil {
			// Earlier completed artifacts remain; retention is skipped.
			finish("failed", "", err.Error(), arts)
			return map[string]any{"artifacts": arts}, Fail("backup-failed", "Earlier artifacts in this batch were kept; retention was not applied. Fix the failing target and rerun.",
				"%s/%s: %v", t.App.Slug, t.Database, err)
		}
		r.Info(ctx, "published %s (%d bytes)", a.Path, a.SizeBytes)
		arts = append(arts, a)
	}
	sched, _, _ := c.BackupSchedule(ctx)
	if req.Trigger == "schedule" || req.Scope == "all" {
		if err := r.Phase(ctx, "retention"); err != nil {
			return nil, err
		}
		removed, err := backup.Retain(c.Layout.BackupsDir(), sched.Retain)
		if err != nil {
			r.Warn(ctx, "retention: %v", err)
		} else if len(removed) > 0 {
			r.Info(ctx, "retention removed %d old artifact(s)", len(removed))
		}
	}
	upload := ""
	if req.Upload || (req.Trigger == "schedule" && sched.RcloneRemote != "") {
		if err := r.Phase(ctx, "upload"); err != nil {
			return nil, err
		}
		if err := deps.Upload(ctx, sched.RcloneRemote, arts); err != nil {
			upload = "failed"
			finish("succeeded", upload, "upload failed: "+err.Error(), arts)
			return map[string]any{"artifacts": arts}, Fail("upload-failed", "Local artifacts were kept. Check the rclone configuration and remote.", "%v", err)
		}
		upload = "succeeded"
	}
	finish("succeeded", upload, "", arts)
	return map[string]any{"artifacts": arts, "upload": upload}, nil
}

// SubmitRestore requires the exact confirmation "replace <database>".
func (c *Controller) SubmitRestore(ctx context.Context, req RestoreRequest, confirm, idem string) (store.Operation, error) {
	if confirm != "replace "+req.Database {
		return store.Operation{}, fmt.Errorf("%w: type exactly %q; restore replaces the database contents", ErrConfirmation, "replace "+req.Database)
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
	op, _, err := c.Submit(ctx, Submission{Kind: KindBackupRestore, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Request: req})
	return op, err
}

// findDatabase enforces the app namespace: only this app's recorded
// databases can be restore targets.
func findDatabase(app domain.App, database string) (domain.Binding, bool, error) {
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite && b.SQLiteFileID == database {
			return b, true, nil
		}
		for _, d := range b.Databases {
			if d == database {
				return b, false, nil
			}
		}
	}
	return domain.Binding{}, false, fmt.Errorf("%w: database %s is not bound to app %s", store.ErrNotFound, database, app.Slug)
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
	engineOK := strings.Contains(filepath_Base(path), string(b.Engine)+"-")
	if !engineOK {
		return nil, Fail("engine-mismatch", "Choose an artifact of the binding's engine.", "artifact %s is not a %s backup", req.Artifact, b.Engine)
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
		return nil, Fail("restore-failed", "The destination may be partially restored; restore again or from another artifact.", "%v", err)
	}
	return map[string]any{"restored": req.Database, "from": req.Artifact}, nil
}

func filepath_Base(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// RunSchedule evaluates the backup schedule once per minute. Missed slots
// (for example while the backend was down) are counted, not replayed.
func (c *Controller) RunSchedule(ctx context.Context) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s, st, err := c.BackupSchedule(ctx)
		if err != nil || !s.Enabled {
			continue
		}
		sched, err := parser.Parse(s.Cron)
		if err != nil {
			continue
		}
		now := time.Now()
		last := platform.ParseTime(st.LastSlot)
		if last.IsZero() {
			last = now
		}
		due := sched.Next(last)
		if due.After(now) {
			if st.LastSlot == "" {
				st.LastSlot = platform.FormatTime(now)
				_ = store.PutSetting(ctx, c.Store.DB(), scheduleStateKey, st)
			}
			continue
		}
		// Count slots missed beyond the most recent one; run only once.
		missed := 0
		for next := sched.Next(due); !next.After(now) && missed < 10000; next = sched.Next(next) {
			missed++
			due = next
		}
		st.Missed += missed
		st.LastSlot = platform.FormatTime(due)
		if now.Sub(due) > 10*time.Minute {
			// The backend was down across this slot: record, do not replay.
			st.Missed++
			st.LastState = "missed"
			_ = store.PutSetting(ctx, c.Store.DB(), scheduleStateKey, st)
			c.Log.Warn("scheduled backup slot missed while backend was unavailable", "slot", due)
			continue
		}
		st.LastRun = platform.FormatTime(now)
		op, err := c.SubmitBackup(ctx, BackupRequest{Scope: "all", Compression: s.Compression, Trigger: "schedule", Upload: s.RcloneRemote != ""}, "")
		if err != nil {
			st.LastState = "submit-failed"
		} else {
			st.LastState = "submitted " + op.ID
		}
		if err := store.PutSetting(ctx, c.Store.DB(), scheduleStateKey, st); err != nil && !errors.Is(err, context.Canceled) {
			c.Log.Warn("schedule state", "err", err)
		}
	}
}
