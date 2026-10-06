package operations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Restoring from another stack's app backup: the operator names an rclone
// remote path and a repository key instead of picking one of this stack's
// apps. The key is written to a pending file at submission (never into the
// operation's request, argv, labels or logs) and deleted when the operation
// ends. It is kept only when the clone adopts the repository
// (backupAfter "same-repo"), in which case it becomes the new app's key.

const (
	KindResticInspectRemote  = "restic.inspect-remote"
	KindAppRestoreFromBackup = "app.restore-from-backup"
)

// CloneBackupSameRepo makes the clone keep backing up to the repository it
// was restored from. Only a remote restore can adopt a repository.
const CloneBackupSameRepo = "same-repo"

const remoteTargetKind = "restic-remote"

// remoteJobID is the app id a remote operation labels its job with and keys
// its restic cache by; it becomes the clone's id.
var remoteJobID = regexp.MustCompile(`^a[0-9a-f]{12}$`)

// ResticRemoteInput is what the API or CLI hands over: the repository, its
// key and the usual inspect/clone options.
type ResticRemoteInput struct {
	Repository   string
	Key          string
	Snapshot     string // "" or "latest": the newest snapshot
	Slug         string
	KeepUsername bool
	BackupAfter  string
}

// ResticRemoteRequest is the persisted intent. Pending names the key file.
type ResticRemoteRequest struct {
	ResticCloneRequest
	Repository string `json:"repository"`
	Pending    string `json:"pending"`
}

func (c *Controller) remoteCacheDir(jobID string) string {
	return filepath.Join(c.Layout.CacheDir(), "restic", jobID)
}

func (c *Controller) validateRemoteInput(in ResticRemoteInput, needSlug bool) (string, domain.ValidationErrors) {
	var errs domain.ValidationErrors
	in.Repository = strings.TrimSpace(in.Repository)
	if _, err := backup.ValidateRemote(in.Repository); err != nil {
		errs.Add("repository", "must be name:path using letters, digits, _ . / -")
	} else if err := c.BackupDeps(nil).CheckRemote(in.Repository); err != nil {
		errs.Add("repository", "%v", err)
	}
	key := strings.TrimSpace(in.Key)
	if key == "" || len(key) > 1024 || strings.ContainsAny(key, "\x00\r\n") {
		errs.Add("key", "paste the repository key")
	}
	if in.Snapshot != "" && in.Snapshot != "latest" && !domain.ResticSnapshotID.MatchString(in.Snapshot) {
		errs.Add("snapshot", "choose a snapshot")
	}
	if in.Slug != "" || needSlug {
		if err := domain.ValidateSlug(in.Slug); err != nil {
			errs.Add("slug", "%s", err)
		}
	}
	return key, errs
}

func (c *Controller) submitRemote(
	ctx context.Context,
	kind string,
	req ResticRemoteRequest,
	key, idem string,
) (store.Operation, error) {
	name, err := c.writePendingKey(req.AppID, key)
	if err != nil {
		return store.Operation{}, err
	}
	req.Pending = name
	op, existed, err := c.Submit(ctx, Submission{
		Kind: kind, TargetKind: remoteTargetKind, TargetID: req.Repository, IdempotencyKey: idem, Request: req,
	})
	if err != nil || existed {
		_ = os.Remove(filepath.Join(c.resticKeyDir(), name))
	}
	return op, err
}

// SubmitResticInspectRemote queues a read-only preview of a snapshot in a
// repository of another stack. With no slug the preview shows placeholders;
// the result also lists the repository's snapshots.
func (c *Controller) SubmitResticInspectRemote(ctx context.Context, in ResticRemoteInput, idem string) (store.Operation, error) {
	key, errs := c.validateRemoteInput(in, false)
	if err := errs.Err(); err != nil {
		return store.Operation{}, err
	}
	req := ResticRemoteRequest{
		ResticCloneRequest: ResticCloneRequest{Snapshot: in.Snapshot, Slug: in.Slug, AppID: platform.NewAppID(),
			KeepUsername: in.KeepUsername},
		Repository: strings.TrimSpace(in.Repository),
	}
	return c.submitRemote(ctx, KindResticInspectRemote, req, key, idem)
}

// SubmitRestoreFromBackup queues a clone of a snapshot of another stack's
// app backup into a new app on this stack. It needs the exact confirmation
// "clone <new-slug>".
func (c *Controller) SubmitRestoreFromBackup(
	ctx context.Context,
	in ResticRemoteInput,
	confirm, idem string,
) (store.Operation, error) {
	key, errs := c.validateRemoteInput(in, true)
	switch in.BackupAfter {
	case "":
		in.BackupAfter = CloneBackupNone
	case CloneBackupNone, CloneBackupNewRepo, CloneBackupSameRepo:
	default:
		errs.Add("backupAfter", "must be %q, %q or %q", CloneBackupNone, CloneBackupNewRepo, CloneBackupSameRepo)
	}
	if err := errs.Err(); err != nil {
		return store.Operation{}, err
	}
	if confirm != "clone "+in.Slug {
		return store.Operation{}, fmt.Errorf(
			"%w: type exactly %q; this creates a new app with its own databases and files", ErrConfirmation, "clone "+in.Slug)
	}
	if _, err := store.GetApp(ctx, c.Store.DB(), in.Slug); err == nil {
		return store.Operation{}, fmt.Errorf("%w: app %q already exists", store.ErrConflict, in.Slug)
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Operation{}, err
	}
	if _, err := os.Lstat(c.Layout.AppHome(in.Slug)); err == nil {
		return store.Operation{}, fmt.Errorf(
			"%w: a retained home for %q exists from an earlier app; prune it first", store.ErrConflict, in.Slug)
	}
	snapshot := in.Snapshot
	if snapshot == "" {
		snapshot = "latest"
	}
	req := ResticRemoteRequest{
		ResticCloneRequest: ResticCloneRequest{Snapshot: snapshot, Slug: in.Slug, AppID: platform.NewAppID(),
			KeepUsername: in.KeepUsername, BackupAfter: in.BackupAfter},
		Repository: strings.TrimSpace(in.Repository),
	}
	return c.submitRemote(ctx, KindAppRestoreFromBackup, req, key, idem)
}

// remoteStart validates a decoded remote request and returns its pending key
// path. The caller removes the file when the operation ends.
func (c *Controller) remoteStart(r *Run) (ResticRemoteRequest, string, error) {
	var req ResticRemoteRequest
	if err := r.Decode(&req); err != nil {
		return req, "", err
	}
	if !remoteJobID.MatchString(req.AppID) {
		return req, "", fmt.Errorf("invalid job id %q", req.AppID)
	}
	pending, err := c.pendingKeyPath(req.Pending)
	if err != nil {
		return req, "", err
	}
	return req, pending, nil
}

// openRemoteRepo starts a restic job on the repository with the pending key
// and verifies that the key opens it.
func (c *Controller) openRemoteRepo(
	ctx context.Context,
	r *Run,
	req ResticRemoteRequest,
	pending string,
	restore bool,
) (cloneRepo, error) {
	if _, err := os.Lstat(pending); err != nil {
		return cloneRepo{}, Fail("restic-key-gone", "Submit the request again; the key was not kept after a restart or retry.",
			"pending key is missing")
	}
	env, err := c.newResticEnv(r.Op.ID, pending)
	if err != nil {
		return cloneRepo{}, err
	}
	mounts := backup.ResticMounts{CtlDir: env.ctl}
	if restore {
		mounts.RestoreDir = env.data
	}
	job, err := c.startResticJob(ctx, r, domain.App{ID: req.AppID}, req.Repository, mounts)
	if err != nil {
		env.close()
		return cloneRepo{}, err
	}
	fail := func(err error) (cloneRepo, error) {
		job.Close(ctx)
		env.close()
		return cloneRepo{}, err
	}
	if err := r.Phase(ctx, "probe-remote"); err != nil {
		return fail(err)
	}
	if err := job.ProbeRemote(ctx, req.Repository, false); err != nil {
		return fail(Fail("remote-unusable",
			"Check the repository path and the remote's credentials; the location must already hold the repository.",
			"%s: %v", req.Repository, err))
	}
	id, err := c.resticRepoID(ctx, job)
	if err != nil {
		return fail(resticFail(err, "open repository"))
	}
	snaps, err := listSnapshots(ctx, job)
	if err != nil {
		return fail(err)
	}
	view := ResticView{Settings: domain.DefaultResticSettings()}
	view.Settings.Repository = req.Repository
	return cloneRepo{env: env, job: job, view: view, repoID: id, keyFile: pending, snapshots: snaps}, nil
}

// resolveSnapshot maps "" / "latest" or a (short) id to the full snapshot id.
func resolveSnapshot(snaps []domain.ResticSnapshot, want string) (string, error) {
	if len(snaps) == 0 {
		return "", Fail("restic-empty", "The repository holds no snapshots; run a backup on the source first.",
			"the repository has no snapshots")
	}
	if want == "" || want == "latest" {
		return snaps[0].ID, nil
	}
	for _, s := range snaps {
		if s.ID == want || s.ShortID == want || strings.HasPrefix(s.ID, want) {
			return s.ID, nil
		}
	}
	return "", Fail("snapshot-not-found", "Choose one of the repository's snapshots.", "no snapshot %q in the repository", want)
}

func (c *Controller) handleResticInspectRemote(ctx context.Context, r *Run) (any, error) {
	req, pending, err := c.remoteStart(r)
	if err != nil {
		return nil, err
	}
	defer os.Remove(pending)
	defer os.RemoveAll(c.remoteCacheDir(req.AppID))
	repo, err := c.openRemoteRepo(ctx, r, req, pending, false)
	if err != nil {
		return nil, err
	}
	defer repo.env.close()
	defer repo.job.Close(ctx)
	snapshot, err := resolveSnapshot(repo.snapshots, req.Snapshot)
	if err != nil {
		return nil, err
	}
	preview, err := c.inspectSnapshot(ctx, r, repo.job, snapshot, req.Slug, req.KeepUsername)
	if err != nil {
		return nil, err
	}
	for _, s := range repo.snapshots {
		preview.Snapshots = append(preview.Snapshots, CloneSnapshot{ID: s.ID, ShortID: s.ShortID, Time: s.Time,
			Tags: nonNilSlice(s.Tags)})
	}
	return preview, nil
}

func (c *Controller) handleAppRestoreFromBackup(ctx context.Context, r *Run) (any, error) {
	req, pending, err := c.remoteStart(r)
	if err != nil {
		return nil, err
	}
	defer os.Remove(pending)
	defer func() {
		// The restic cache stays only when the clone adopted the repository.
		if _, err := os.Lstat(c.resticKeyPath(req.AppID)); err != nil {
			_ = os.RemoveAll(c.remoteCacheDir(req.AppID))
		}
	}()
	return c.runClone(ctx, r, req.ResticCloneRequest, func() (cloneRepo, error) {
		repo, err := c.openRemoteRepo(ctx, r, req, pending, true)
		if err != nil {
			return repo, err
		}
		if repo.snapshot, err = resolveSnapshot(repo.snapshots, req.Snapshot); err != nil {
			repo.job.Close(ctx)
			repo.env.close()
			return cloneRepo{}, err
		}
		return repo, nil
	})
}

// adoptCloneRepository makes the clone keep backing up to the repository it
// was restored from: the pending key becomes the new app's key and the
// repository is recorded as connected. The schedule stays off.
func (c *Controller) adoptCloneRepository(ctx context.Context, repo cloneRepo, app domain.App) string {
	if repo.keyFile == "" || repo.repoID == "" {
		return "App backup could not keep the repository: set it up on the new app's Backup tab."
	}
	if err := platform.EnsureDir(c.resticKeyDir(), 0o700, platform.RootOwner); err != nil {
		return "App backup could not keep the repository: " + err.Error()
	}
	if err := platform.CopyFile(repo.keyFile, c.resticKeyPath(app.ID), 0o400, platform.RootOwner); err != nil {
		return "App backup could not keep the repository key: " + err.Error()
	}
	s := domain.DefaultResticSettings()
	s.Repository = repo.view.Settings.Repository
	if err := store.PutSetting(ctx, c.Store.DB(), resticSettingsKey(app.ID), s); err != nil {
		_ = os.Remove(c.resticKeyPath(app.ID))
		return "App backup could not keep the repository: " + err.Error()
	}
	c.updateResticState(ctx, app.ID, func(st *domain.ResticState) {
		*st = domain.ResticState{RepositoryID: repo.repoID, InitializedAt: time.Now().UTC()}
	})
	if err := c.resticRefresh(ctx, repo.job, app.ID); err != nil {
		return "App backup of the new app now uses repository " + s.Repository + " (refresh it on the Backup tab)."
	}
	return "App backup of the new app now writes to " + s.Repository + ", the repository it was restored from. " +
		"Its schedule is off; enable it on the Backup tab."
}
