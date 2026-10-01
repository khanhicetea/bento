package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// App-scoped restic backups. Each app has its own repository on an rclone
// remote. A snapshot holds the app home (minus excludes, with live SQLite
// files replaced by .backup copies) and a Bento directory with app.json,
// manifest.json and plain database dumps, so it restores into the same app or
// into an app of another stack.

const (
	KindResticInit      = "restic.init"
	KindResticConnect   = "restic.connect"
	KindResticBackup    = "restic.backup"
	KindResticRestore   = "restic.restore"
	KindResticRefresh   = "restic.refresh"
	KindResticKeyAdd    = "restic.key-add"
	KindResticKeyRemove = "restic.key-remove"
	KindResticCheck     = "restic.check"
	KindResticUnlock    = "restic.unlock"
)

// lockLeakWarning explains a lock restic could not delete.
const lockLeakWarning = "restic could not delete its repository lock: the remote refused a delete. Grant the rclone remote's credentials delete permission on the repository path (restic also needs it for retention), then use Remove stale locks."

// ResticFormatVersion versions the Bento directory inside snapshots.
const ResticFormatVersion = 1

// resticPruneEvery bounds how often a backup also prunes the repository.
const resticPruneEvery = 7 * 24 * time.Hour

func resticSettingsKey(appID string) string { return "restic:" + appID }
func resticStateKey(appID string) string    { return "restic-state:" + appID }
func resticScheduleID(appID string) string  { return "restic-" + appID }
func resticClaim(appID string) string       { return "restic:" + appID }

// resticPool bounds concurrent restic backups/restores: each reads a whole
// home and uploads.
const resticPool = "restic"

// ResticView is an app's restic configuration and what Bento knows about its
// repository.
type ResticView struct {
	Configured bool
	Settings   domain.ResticSettings
	State      domain.ResticState
}

func (c *Controller) resticKeyDir() string { return filepath.Join(c.Layout.SecretsDir(), "restic") }
func (c *Controller) resticKeyPath(appID string) string {
	return filepath.Join(c.resticKeyDir(), appID+".key")
}

// ResticSettings returns an app's restic configuration (defaults when unset).
func (c *Controller) ResticSettings(ctx context.Context, appID string) (ResticView, error) {
	if _, err := store.GetApp(ctx, c.Store.DB(), appID); err != nil {
		return ResticView{}, err
	}
	return c.resticView(ctx, store.Q(c.Store.DB()), appID)
}

func (c *Controller) resticView(ctx context.Context, q store.Q, appID string) (ResticView, error) {
	v := ResticView{Settings: domain.DefaultResticSettings()}
	ok, err := store.GetSetting(ctx, q, resticSettingsKey(appID), &v.Settings)
	if err != nil {
		return v, err
	}
	v.Configured = ok
	if _, err := store.GetSetting(ctx, q, resticStateKey(appID), &v.State); err != nil {
		return v, err
	}
	if v.State.Snapshots == nil {
		v.State.Snapshots = []domain.ResticSnapshot{}
	}
	if v.State.Keys == nil {
		v.State.Keys = []domain.ResticKey{}
	}
	return v, nil
}

// AppResticView is one app's backup view for the stack-wide overview.
type AppResticView struct {
	App  domain.App
	View ResticView
}

// ResticOverview lists every app with restic settings, by slug.
func (c *Controller) ResticOverview(ctx context.Context) ([]AppResticView, error) {
	apps, err := store.ListApps(ctx, c.Store.DB())
	if err != nil {
		return nil, err
	}
	out := []AppResticView{}
	for _, app := range apps {
		v, err := c.resticView(ctx, c.Store.DB(), app.ID)
		if err != nil {
			return nil, err
		}
		if v.Configured {
			out = append(out, AppResticView{App: app, View: v})
		}
	}
	slices.SortFunc(out, func(a, b AppResticView) int { return strings.Compare(a.App.Slug, b.App.Slug) })
	return out, nil
}

// SaveResticSettings validates and stores an app's restic settings and its
// backup schedule. Changing the repository of an initialized app forgets the
// repository state: it must be initialized or connected again.
func (c *Controller) SaveResticSettings(ctx context.Context, appID string, s domain.ResticSettings) (ResticView, error) {
	if _, err := store.GetApp(ctx, c.Store.DB(), appID); err != nil {
		return ResticView{}, err
	}
	s.Paths = normalizeRels(s.Paths, true)
	s.SQLitePaths = normalizeRels(s.SQLitePaths, false)
	if s.Excludes == nil {
		s.Excludes = []string{}
	}
	s.Schedule.Cron = strings.TrimSpace(s.Schedule.Cron)
	errs := domain.ValidateResticSettings(s)
	if s.Repository != "" {
		if _, err := backup.ValidateRemote(s.Repository); err != nil {
			errs.Add("repository", "must be name:path using letters, digits, _ . / -")
		} else if err := c.BackupDeps(nil).CheckRemote(s.Repository); err != nil {
			errs.Add("repository", "%v", err)
		}
	}
	if s.Schedule.Cron != "" {
		if _, err := scheduleParser.Parse(s.Schedule.Cron); err != nil {
			errs.Add("schedule", "invalid cron expression: %v", err)
		}
	}
	if err := errs.Err(); err != nil {
		return ResticView{}, err
	}
	err := c.Store.Tx(ctx, func(q store.Q) error {
		prev, err := c.resticView(ctx, q, appID)
		if err != nil {
			return err
		}
		if prev.Configured && prev.Settings.Repository != s.Repository && prev.State.RepositoryID != "" {
			if err := store.PutSetting(ctx, q, resticStateKey(appID), domain.ResticState{}); err != nil {
				return err
			}
		}
		if err := store.PutSetting(ctx, q, resticSettingsKey(appID), s); err != nil {
			return err
		}
		return c.putResticSchedule(ctx, q, appID, s.Schedule)
	})
	if err != nil {
		return ResticView{}, err
	}
	return c.ResticSettings(ctx, appID)
}

func normalizeRels(in []string, allowRoot bool) []string {
	out := []string{}
	for _, p := range in {
		if c, bad := domain.CleanHomeRel(p, allowRoot); bad == "" {
			p = c
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

type resticScheduleSpec struct {
	AppID string `json:"appId"`
}

func (c *Controller) putResticSchedule(ctx context.Context, q store.Q, appID string, s domain.ResticSchedule) error {
	id := resticScheduleID(appID)
	if s.Cron == "" {
		err := store.DeleteSchedule(ctx, q, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	spec, _ := json.Marshal(resticScheduleSpec{AppID: appID})
	return store.PutSchedule(ctx, q, store.Schedule{
		ID: id, Kind: "app-backup", Name: "App backup " + appID, Cron: s.Cron, Enabled: s.Enabled, Spec: spec,
	}, platform.FormatTime(time.Now()))
}

func submitScheduledResticBackup(ctx context.Context, c *Controller, s store.Schedule) (store.Operation, error) {
	var spec resticScheduleSpec
	if err := s.DecodeSpec(&spec); err != nil {
		return store.Operation{}, err
	}
	return c.SubmitResticBackup(ctx, spec.AppID, "schedule", "")
}

func (c *Controller) updateResticState(ctx context.Context, appID string, fn func(*domain.ResticState)) {
	err := c.Store.Tx(ctx, func(q store.Q) error {
		v, err := c.resticView(ctx, q, appID)
		if err != nil {
			return err
		}
		fn(&v.State)
		return store.PutSetting(ctx, q, resticStateKey(appID), v.State)
	})
	if err != nil {
		c.Log.Warn("save restic state", "app", appID, "err", err)
	}
}

// ---- requests ----

type resticKeyRequest struct {
	// Pending names a key file in the restic key directory written at
	// submission; it never holds the key itself.
	Pending string `json:"pending,omitempty"`
	Label   string `json:"label,omitempty"`
	KeyID   string `json:"keyId,omitempty"`
}

type ResticBackupRequest struct {
	Trigger string `json:"trigger"`
}

type ResticRestoreRequest struct {
	Snapshot  string `json:"snapshot"`
	Files     bool   `json:"files"`
	Databases bool   `json:"databases"`
}

var pendingKeyName = regexp.MustCompile(`^a[0-9a-f]+\.pending-[0-9a-f]{12}\.key$`)
var keyLabel = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)

// writePendingKey stores a key for an operation to consume and returns its
// file name.
func (c *Controller) writePendingKey(appID, key string) (string, error) {
	if err := platform.EnsureDir(c.resticKeyDir(), 0o700, platform.RootOwner); err != nil {
		return "", err
	}
	name := appID + ".pending-" + platform.RandomHex(6) + ".key"
	return name, platform.AtomicWrite(filepath.Join(c.resticKeyDir(), name), []byte(key), 0o400, platform.RootOwner)
}

func (c *Controller) pendingKeyPath(name string) (string, error) {
	if !pendingKeyName.MatchString(name) {
		return "", fmt.Errorf("invalid pending key %q", name)
	}
	return filepath.Join(c.resticKeyDir(), name), nil
}

func newResticKey() string { return platform.RandomToken(32) }

func (c *Controller) requireResticRepo(ctx context.Context, appID string) (domain.App, ResticView, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), appID)
	if err != nil {
		return app, ResticView{}, err
	}
	v, err := c.resticView(ctx, c.Store.DB(), appID)
	if err != nil {
		return app, v, err
	}
	if !v.Configured || v.Settings.Repository == "" {
		return app, v, fmt.Errorf("%w: save the app's restic settings first", store.ErrConflict)
	}
	if v.State.RepositoryID == "" {
		return app, v, fmt.Errorf("%w: initialize or connect the restic repository first", store.ErrConflict)
	}
	return app, v, nil
}

// SubmitResticInit generates the repository key and queues `restic init`.
// The key is returned once, here; Bento never reveals it again.
func (c *Controller) SubmitResticInit(ctx context.Context, appID, idem string) (store.Operation, string, error) {
	v, err := c.ResticSettings(ctx, appID)
	if err != nil {
		return store.Operation{}, "", err
	}
	if !v.Configured || v.Settings.Repository == "" {
		return store.Operation{}, "", fmt.Errorf("%w: save the app's restic settings first", store.ErrConflict)
	}
	if v.State.RepositoryID != "" {
		return store.Operation{}, "", fmt.Errorf(
			"%w: the repository is already initialized; add an access key instead", store.ErrConflict)
	}
	return c.submitWithPendingKey(ctx, KindResticInit, appID, newResticKey(), "", idem)
}

// SubmitResticConnect queues verification of an existing repository with an
// operator-supplied key, for example to restore into a new app.
func (c *Controller) SubmitResticConnect(ctx context.Context, appID, key, idem string) (store.Operation, error) {
	v, err := c.ResticSettings(ctx, appID)
	if err != nil {
		return store.Operation{}, err
	}
	if !v.Configured || v.Settings.Repository == "" {
		return store.Operation{}, fmt.Errorf("%w: save the app's restic settings first", store.ErrConflict)
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 1024 || strings.ContainsAny(key, "\x00\r\n") {
		return store.Operation{}, domain.ValidationErrors{{Field: "key", Message: "paste the repository key"}}
	}
	op, _, err := c.submitWithPendingKey(ctx, KindResticConnect, appID, key, "", idem)
	return op, err
}

// SubmitResticKeyAdd adds a new key slot to the repository and returns the
// new key once. It needs the exact confirmation "export <slug>".
func (c *Controller) SubmitResticKeyAdd(
	ctx context.Context,
	appID, label, confirm, idem string,
) (store.Operation, string, error) {
	app, _, err := c.requireResticRepo(ctx, appID)
	if err != nil {
		return store.Operation{}, "", err
	}
	if confirm != "export "+app.Slug {
		return store.Operation{}, "", fmt.Errorf(
			"%w: type exactly %q; the key decrypts every backup of this app", ErrConfirmation, "export "+app.Slug)
	}
	if label == "" {
		label = "access"
	}
	if !keyLabel.MatchString(label) {
		return store.Operation{}, "", domain.ValidationErrors{
			{Field: "label", Message: "use 1-40 letters, digits, _ . -"}}
	}
	return c.submitWithPendingKey(ctx, KindResticKeyAdd, appID, newResticKey(), label, idem)
}

func (c *Controller) submitWithPendingKey(
	ctx context.Context,
	kind, appID, key, label, idem string,
) (store.Operation, string, error) {
	name, err := c.writePendingKey(appID, key)
	if err != nil {
		return store.Operation{}, "", err
	}
	op, existed, err := c.Submit(ctx, Submission{
		Kind: kind, TargetKind: "app", TargetID: appID, IdempotencyKey: idem,
		Request: resticKeyRequest{Pending: name, Label: label},
	})
	if err != nil || existed {
		_ = os.Remove(filepath.Join(c.resticKeyDir(), name))
		// A replayed request never shows a key again.
		return op, "", err
	}
	return op, key, nil
}

// SubmitResticKeyRemove removes a key slot other than the one Bento uses.
func (c *Controller) SubmitResticKeyRemove(ctx context.Context, appID, keyID, idem string) (store.Operation, error) {
	_, v, err := c.requireResticRepo(ctx, appID)
	if err != nil {
		return store.Operation{}, err
	}
	if !domain.ResticKeyID.MatchString(keyID) {
		return store.Operation{}, fmt.Errorf("%w: no key with that id", store.ErrNotFound)
	}
	for _, k := range v.State.Keys {
		if k.Current && strings.HasPrefix(k.ID, keyID) {
			return store.Operation{}, fmt.Errorf("%w: Bento uses this key; it cannot be removed", store.ErrConflict)
		}
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindResticKeyRemove, TargetKind: "app", TargetID: appID, IdempotencyKey: idem,
		Request: resticKeyRequest{KeyID: keyID},
	})
	return op, err
}

// SubmitResticBackup queues a snapshot of the app while it keeps running.
func (c *Controller) SubmitResticBackup(ctx context.Context, appID, trigger, idem string) (store.Operation, error) {
	if _, _, err := c.requireResticRepo(ctx, appID); err != nil {
		return store.Operation{}, err
	}
	if trigger == "" {
		trigger = "manual"
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindResticBackup, TargetKind: "app", TargetID: appID, IdempotencyKey: idem,
		Request: ResticBackupRequest{Trigger: trigger}, Origin: trigger,
	})
	return op, err
}

// SubmitResticSimple queues refresh or check.
func (c *Controller) SubmitResticSimple(ctx context.Context, kind, appID, idem string) (store.Operation, error) {
	if kind != KindResticRefresh && kind != KindResticCheck && kind != KindResticUnlock {
		return store.Operation{}, fmt.Errorf("unknown restic operation %q", kind)
	}
	if _, _, err := c.requireResticRepo(ctx, appID); err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{Kind: kind, TargetKind: "app", TargetID: appID, IdempotencyKey: idem})
	return op, err
}

// SubmitResticRestore restores a snapshot into the app. It needs the exact
// confirmation "restore <slug>" and a stopped app.
func (c *Controller) SubmitResticRestore(
	ctx context.Context,
	appID string,
	req ResticRestoreRequest,
	confirm, idem string,
) (store.Operation, error) {
	app, _, err := c.requireResticRepo(ctx, appID)
	if err != nil {
		return store.Operation{}, err
	}
	if confirm != "restore "+app.Slug {
		return store.Operation{}, fmt.Errorf(
			"%w: type exactly %q; restore replaces the app's files and databases", ErrConfirmation, "restore "+app.Slug)
	}
	if !domain.ResticSnapshotID.MatchString(req.Snapshot) {
		return store.Operation{}, domain.ValidationErrors{{Field: "snapshot", Message: "choose a snapshot"}}
	}
	if !req.Files && !req.Databases {
		return store.Operation{}, domain.ValidationErrors{{Field: "files", Message: "restore files, databases, or both"}}
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindResticRestore, TargetKind: "app", TargetID: appID, IdempotencyKey: idem, Request: req,
	})
	return op, err
}

// ---- job plumbing ----

// resticEnv is one operation's private staging: ctl (key, excludes; mounted
// read-only) and data (snapshot source or restore target).
type resticEnv struct {
	root string
	ctl  string
	data string
}

func (c *Controller) newResticEnv(opID string, keyFile string) (*resticEnv, error) {
	if err := platform.EnsureDir(c.Layout.StagingDir(), 0o700, platform.RootOwner); err != nil {
		return nil, err
	}
	root := filepath.Join(c.Layout.StagingDir(), "restic-"+opID)
	_ = os.RemoveAll(root) // a retried operation starts clean
	e := &resticEnv{root: root, ctl: filepath.Join(root, "ctl"), data: filepath.Join(root, "data")}
	for _, d := range []string{root, e.ctl, e.data} {
		if err := platform.EnsureDir(d, 0o700, platform.RootOwner); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
	}
	if err := platform.CopyFile(keyFile, filepath.Join(e.ctl, backup.ResticKeyFile), 0o400, platform.RootOwner); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("read restic key: %w", err)
	}
	return e, nil
}

func (e *resticEnv) close() { _ = os.RemoveAll(e.root) }

func (c *Controller) startResticJob(
	ctx context.Context,
	r *Run,
	app domain.App,
	repository string,
	m backup.ResticMounts,
) (*backup.ResticJob, error) {
	if err := r.Phase(ctx, "backup-image"); err != nil {
		return nil, err
	}
	image, _, err := c.Images.EnsureRestic(ctx, func(s string) { r.Info(ctx, "%s", s) })
	if err != nil {
		return nil, Fail("image", "Check Docker connectivity and network access to github.com.", "backup image: %v", err)
	}
	job, err := c.BackupDeps(nil).StartResticJob(ctx, image, app.ID, r.Op.ID, repository, m)
	if err != nil {
		return nil, Fail("restic-start", "Check the rclone remote in the rclone shell.", "%v", err)
	}
	return job, nil
}

// resticFail maps restic exit codes to operator guidance.
func resticFail(err error, what string) error {
	if errors.Is(err, backup.ErrResticTimeout) {
		return Fail("restic-timeout", "Check that the repository's bucket exists and the rclone remote's credentials can read and write it (rclone shell: rclone lsf <remote:path>).",
			"%s: %v", what, err)
	}
	var re *backup.ResticError
	if !errors.As(err, &re) {
		return err
	}
	switch re.Exit {
	case backup.ResticExitRepoMissing:
		return Fail("restic-repo-missing", "Initialize the repository, or fix the repository path.", "%s: %s", what, re.Stderr)
	case backup.ResticExitLocked:
		return Fail("restic-locked", "Another restic process holds the repository lock; retry later. A crashed run's stale lock expires after 30 minutes.",
			"%s: %s", what, re.Stderr)
	case backup.ResticExitBadPassword:
		return Fail("restic-key-rejected", "Use a key that belongs to this repository.", "%s: wrong key", what)
	}
	return Fail("restic-failed", "See the operation log; the repository is unchanged unless stated.", "%s: %v", what, re)
}

func (c *Controller) resticRefresh(ctx context.Context, job *backup.ResticJob, appID string) error {
	raw, err := job.RunJSON(ctx, []string{"snapshots", "--no-lock"})
	if err != nil {
		return resticFail(err, "list snapshots")
	}
	var snaps []struct {
		ID       string    `json:"id"`
		ShortID  string    `json:"short_id"`
		Time     time.Time `json:"time"`
		Tags     []string  `json:"tags"`
		Hostname string    `json:"hostname"`
	}
	if err := json.Unmarshal(raw, &snaps); err != nil {
		return fmt.Errorf("parse snapshots: %w", err)
	}
	raw, err = job.RunJSON(ctx, []string{"key", "list", "--no-lock"})
	if err != nil {
		return resticFail(err, "list keys")
	}
	var rawKeys []struct {
		ID       string `json:"id"`
		Current  bool   `json:"current"`
		UserName string `json:"userName"`
		HostName string `json:"hostName"`
		// restic prints local time as "2006-01-02 15:04:05".
		Created string `json:"created"`
	}
	if err := json.Unmarshal(raw, &rawKeys); err != nil {
		return fmt.Errorf("parse keys: %w", err)
	}
	keys := make([]domain.ResticKey, 0, len(rawKeys))
	for _, k := range rawKeys {
		created, _ := time.Parse(time.DateTime, k.Created)
		keys = append(keys, domain.ResticKey{ID: k.ID, Current: k.Current, UserName: k.UserName, HostName: k.HostName,
			Created: created})
	}
	out := make([]domain.ResticSnapshot, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, domain.ResticSnapshot{ID: s.ID, ShortID: s.ShortID, Time: s.Time, Tags: s.Tags, Hostname: s.Hostname})
	}
	slices.SortFunc(out, func(a, b domain.ResticSnapshot) int { return b.Time.Compare(a.Time) })
	if len(out) > 500 {
		out = out[:500]
	}
	c.updateResticState(ctx, appID, func(s *domain.ResticState) {
		s.Snapshots, s.Keys, s.RefreshedAt = out, keys, time.Now().UTC()
	})
	return nil
}

func (c *Controller) resticRepoID(ctx context.Context, job *backup.ResticJob) (string, error) {
	raw, err := job.RunJSON(ctx, []string{"cat", "config", "--no-lock"})
	if err != nil {
		return "", err
	}
	var cfg struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.ID == "" {
		return "", fmt.Errorf("unreadable repository config")
	}
	return cfg.ID, nil
}

// ---- handlers ----

func (c *Controller) handleResticInit(ctx context.Context, r *Run) (any, error) {
	return c.adoptKey(ctx, r, true)
}

func (c *Controller) handleResticConnect(ctx context.Context, r *Run) (any, error) {
	return c.adoptKey(ctx, r, false)
}

// adoptKey initializes (create) or verifies a repository with a pending key
// and, on success, makes it the app's key.
func (c *Controller) adoptKey(ctx context.Context, r *Run, create bool) (any, error) {
	var req resticKeyRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	pending, err := c.pendingKeyPath(req.Pending)
	if err != nil {
		return nil, err
	}
	defer os.Remove(pending)
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	v, err := c.resticView(ctx, c.Store.DB(), app.ID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(pending); err != nil {
		return nil, Fail("restic-key-gone", "Submit the request again; the key was not kept after a restart or retry.",
			"pending key is missing")
	}
	env, err := c.newResticEnv(r.Op.ID, pending)
	if err != nil {
		return nil, err
	}
	defer env.close()
	job, err := c.startResticJob(ctx, r, app, v.Settings.Repository, backup.ResticMounts{CtlDir: env.ctl})
	if err != nil {
		return nil, err
	}
	defer job.Close(ctx)
	if err := r.Phase(ctx, "probe-remote"); err != nil {
		return nil, err
	}
	if err := job.ProbeRemote(ctx, v.Settings.Repository, create); err != nil {
		guidance := "Create the bucket, or fix the path or the remote's credentials in the rclone shell."
		if !create {
			guidance = "Check the repository path and the remote's credentials; the location must already hold the repository."
		}
		return nil, Fail("remote-unusable", guidance, "%s: %v", v.Settings.Repository, err)
	}
	if create {
		if err := r.Phase(ctx, "init"); err != nil {
			return nil, err
		}
		if err := job.RunQuick(ctx, []string{"init", "--repository-version", "2"}, nil); err != nil {
			var re *backup.ResticError
			if errors.As(err, &re) && strings.Contains(re.Stderr, "already") {
				return nil, Fail("restic-repo-exists", "The path already holds a repository: connect with its key, or choose another path.",
					"repository %s already exists", v.Settings.Repository)
			}
			return nil, resticFail(err, "init")
		}
	}
	if err := r.Phase(ctx, "verify"); err != nil {
		return nil, err
	}
	id, err := c.resticRepoID(ctx, job)
	if err != nil {
		return nil, resticFail(err, "open repository")
	}
	if err := platform.CopyFile(pending, c.resticKeyPath(app.ID), 0o400, platform.RootOwner); err != nil {
		return nil, err
	}
	c.updateResticState(ctx, app.ID, func(s *domain.ResticState) {
		*s = domain.ResticState{RepositoryID: id, InitializedAt: time.Now().UTC()}
	})
	if err := c.resticRefresh(ctx, job, app.ID); err != nil {
		r.Warn(ctx, "repository ready but listing failed: %v", err)
	}
	r.Info(ctx, "repository %s ready (id %s)", v.Settings.Repository, id[:8])
	return map[string]any{"repositoryId": id}, nil
}

// resticJobFor opens a job with the app's stored key.
func (c *Controller) resticJobFor(
	ctx context.Context,
	r *Run,
	app domain.App,
	v ResticView,
	m func(env *resticEnv) backup.ResticMounts,
) (*resticEnv, *backup.ResticJob, error) {
	if v.State.RepositoryID == "" {
		return nil, nil, Fail("restic-not-ready", "Initialize or connect the repository first.", "repository not initialized")
	}
	env, err := c.newResticEnv(r.Op.ID, c.resticKeyPath(app.ID))
	if err != nil {
		return nil, nil, Fail("restic-key-missing", "Connect the repository again with one of its keys.", "%v", err)
	}
	mounts := backup.ResticMounts{CtlDir: env.ctl}
	if m != nil {
		mounts = m(env)
	}
	job, err := c.startResticJob(ctx, r, app, v.Settings.Repository, mounts)
	if err != nil {
		env.close()
		return nil, nil, err
	}
	id, err := c.resticRepoID(ctx, job)
	if err == nil && id != v.State.RepositoryID {
		err = Fail("restic-repo-changed", "The remote path now holds a different repository; connect it again deliberately.",
			"repository id %s does not match %s", id, v.State.RepositoryID)
	} else if err != nil {
		err = resticFail(err, "open repository")
	}
	if err != nil {
		job.Close(ctx)
		env.close()
		return nil, nil, err
	}
	return env, job, nil
}

func (c *Controller) loadResticApp(ctx context.Context, r *Run) (domain.App, ResticView, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return app, ResticView{}, err
	}
	v, err := c.resticView(ctx, c.Store.DB(), app.ID)
	return app, v, err
}

func (c *Controller) handleResticRefresh(ctx context.Context, r *Run) (any, error) {
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	env, job, err := c.resticJobFor(ctx, r, app, v, nil)
	if err != nil {
		return nil, err
	}
	defer env.close()
	defer job.Close(ctx)
	return nil, c.resticRefresh(ctx, job, app.ID)
}

// handleResticUnlock removes stale locks only (restic unlock): locks older
// than 30 minutes or whose process is gone. Live operations keep theirs.
func (c *Controller) handleResticUnlock(ctx context.Context, r *Run) (any, error) {
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	env, job, err := c.resticJobFor(ctx, r, app, v, nil)
	if err != nil {
		return nil, err
	}
	defer env.close()
	defer job.Close(ctx)
	var out strings.Builder
	if err := job.RunQuick(ctx, []string{"unlock"}, &out); err != nil {
		return nil, resticFail(err, "unlock")
	}
	if job.LockLeaked() {
		return nil, Fail("restic-unlock-denied", "Grant the remote's credentials delete permission on the repository path.",
			"the remote refused to delete the stale locks")
	}
	r.Info(ctx, "%s", strings.TrimSpace(out.String()))
	return nil, nil
}

func (c *Controller) handleResticCheck(ctx context.Context, r *Run) (any, error) {
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	env, job, err := c.resticJobFor(ctx, r, app, v, nil)
	if err != nil {
		return nil, err
	}
	defer env.close()
	defer job.Close(ctx)
	if err := r.Phase(ctx, "check"); err != nil {
		return nil, err
	}
	started := time.Now()
	err = job.Run(ctx, []string{"check", "--read-data-subset", "5%"}, nil)
	res := domain.ResticRunResult{At: time.Now().UTC(), OK: err == nil, Seconds: time.Since(started).Seconds()}
	if err != nil {
		res.Error = err.Error()
	}
	c.updateResticState(ctx, app.ID, func(s *domain.ResticState) { s.LastCheck = &res })
	if err != nil {
		return nil, resticFail(err, "check")
	}
	return map[string]any{"ok": true}, nil
}

func (c *Controller) handleResticKeyAdd(ctx context.Context, r *Run) (any, error) {
	var req resticKeyRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	pending, err := c.pendingKeyPath(req.Pending)
	if err != nil {
		return nil, err
	}
	defer os.Remove(pending)
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	env, job, err := c.resticJobFor(ctx, r, app, v, nil)
	if err != nil {
		return nil, err
	}
	defer env.close()
	defer job.Close(ctx)
	if err := platform.CopyFile(pending, filepath.Join(env.ctl, backup.ResticNewKeyFile), 0o400, platform.RootOwner); err != nil {
		return nil, Fail("restic-key-gone", "Submit the request again; the new key was not kept.", "%v", err)
	}
	if err := r.Phase(ctx, "add-key"); err != nil {
		return nil, err
	}
	if err := job.RunQuick(ctx, []string{"key", "add", "--new-password-file", "/run/bento-ctl/" + backup.ResticNewKeyFile,
		"--host", "bento", "--user", req.Label}, nil); err != nil {
		return nil, resticFail(err, "add key")
	}
	r.Info(ctx, "added key %q", req.Label)
	return nil, c.resticRefresh(ctx, job, app.ID)
}

func (c *Controller) handleResticKeyRemove(ctx context.Context, r *Run) (any, error) {
	var req resticKeyRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	env, job, err := c.resticJobFor(ctx, r, app, v, nil)
	if err != nil {
		return nil, err
	}
	defer env.close()
	defer job.Close(ctx)
	// restic refuses to remove the key it was opened with, which is
	// always the key Bento uses.
	if err := job.RunQuick(ctx, []string{"key", "remove", req.KeyID}, nil); err != nil {
		return nil, resticFail(err, "remove key")
	}
	r.Info(ctx, "removed key %s", req.KeyID)
	return nil, c.resticRefresh(ctx, job, app.ID)
}

// ---- backup ----

// AppSpec is the portable description stored as app.json in snapshots. IDs,
// UIDs, generations and credentials are never included; env values whose
// keys look secret are redacted.
type AppSpec struct {
	FormatVersion int                `json:"formatVersion"`
	Slug          string             `json:"slug"`
	Runtime       domain.Runtime     `json:"runtime"`
	Resources     domain.Resources   `json:"resources"`
	Ingress       domain.IngressMode `json:"ingress"`
	Route         domain.Route       `json:"route"`
	Domains       []string           `json:"domains"`
	PrimaryDomain string             `json:"primaryDomain,omitempty"`
	Bindings      []AppSpecBinding   `json:"bindings"`
	GitRepoURL    string             `json:"gitRepoUrl,omitempty"`
	GitBranch     string             `json:"gitBranch,omitempty"`
	GitCommit     string             `json:"gitCommit,omitempty"`
}

type AppSpecBinding struct {
	Engine    domain.Engine `json:"engine"`
	Service   string        `json:"service,omitempty"`
	Version   string        `json:"version,omitempty"`
	Databases []string      `json:"databases,omitempty"`
	SQLiteID  string        `json:"sqliteFileId,omitempty"`
}

// ResticManifest describes a snapshot's Bento directory.
type ResticManifest struct {
	FormatVersion int          `json:"formatVersion"`
	StackID       string       `json:"stackId"`
	AppID         string       `json:"appId"`
	Slug          string       `json:"slug"`
	CreatedAt     time.Time    `json:"createdAt"`
	Paths         []string     `json:"paths"`
	Dumps         []ResticDump `json:"dumps"`
	// HomeSQLite are home-relative SQLite files stored as .backup copies
	// under home-sqlite/.
	HomeSQLite []string `json:"homeSqlite"`
}

type ResticDump struct {
	File     string        `json:"file"`
	Engine   domain.Engine `json:"engine"`
	Service  string        `json:"service,omitempty"`
	Version  string        `json:"version,omitempty"`
	Database string        `json:"database"`
}

func redactedEnv(env []domain.EnvVar) []domain.EnvVar {
	out := make([]domain.EnvVar, 0, len(env))
	for _, e := range env {
		if domain.SensitiveEnvKey(e.Key) {
			e.Value = domain.RedactedEnvValue
		}
		out = append(out, e)
	}
	return out
}

func (c *Controller) appSpec(ctx context.Context, app domain.App, versions map[string]string) AppSpec {
	rt := app.Runtime
	rt.Env = redactedEnv(rt.Env)
	s := AppSpec{
		FormatVersion: ResticFormatVersion, Slug: app.Slug, Runtime: rt, Resources: app.Resources,
		Ingress: app.Ingress, Route: app.Route, Domains: []string{}, Bindings: []AppSpecBinding{},
	}
	for _, d := range app.Domains {
		s.Domains = append(s.Domains, d.Name)
		if d.Primary {
			s.PrimaryDomain = d.Name
		}
	}
	for _, b := range app.Bindings {
		s.Bindings = append(s.Bindings, AppSpecBinding{Engine: b.Engine, Service: b.Service, Version: versions[b.Service],
			Databases: b.Databases, SQLiteID: b.SQLiteFileID})
	}
	if g, ok, err := store.GetGitSource(ctx, c.Store.DB(), app.ID); err == nil && ok {
		s.GitRepoURL, s.GitBranch, s.GitCommit = g.RepoURL, g.Branch, g.DeployedCommit
	}
	return s
}

func (c *Controller) serviceVersions(ctx context.Context, app domain.App) map[string]string {
	out := map[string]string{}
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite {
			continue
		}
		if svc, err := store.GetService(ctx, c.Store.DB(), b.Service); err == nil {
			out[b.Service] = svc.Version
		}
	}
	return out
}

// homeSQLiteFiles lists SQLite files to snapshot with .backup: minicrond's
// data files plus the operator's SQLitePaths that exist.
func (c *Controller) homeSQLiteFiles(app domain.App, s domain.ResticSettings) []string {
	home := c.Layout.AppHome(app.Slug)
	var out []string
	dir := filepath.Join(home, domain.MinicronDataDir)
	if platform.NoSymlinkBetween(home, dir) == nil {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			rel := domain.MinicronDataDir + "/" + e.Name()
			if e.Type().IsRegular() && backup.IsSQLiteFile(filepath.Join(dir, e.Name())) {
				out = append(out, rel)
			}
		}
	}
	for _, p := range s.SQLitePaths {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func (c *Controller) handleResticBackup(ctx context.Context, r *Run) (any, error) {
	var req ResticBackupRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	res, err := c.resticBackup(ctx, r, app, v, req.Trigger)
	res.OpID, res.Trigger = r.Op.ID, req.Trigger
	res.At, res.OK, res.Seconds = time.Now().UTC(), err == nil, time.Since(started).Seconds()
	if err != nil {
		res.Error = err.Error()
	}
	c.updateResticState(context.WithoutCancel(ctx), app.ID, func(s *domain.ResticState) {
		s.LastBackup = &res
		s.History = append([]domain.ResticRunResult{res}, s.History...)
		if len(s.History) > domain.ResticHistoryLimit {
			s.History = s.History[:domain.ResticHistoryLimit]
		}
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Controller) resticBackup(
	ctx context.Context,
	r *Run,
	app domain.App,
	v ResticView,
	trigger string,
) (domain.ResticRunResult, error) {
	var res domain.ResticRunResult
	if err := c.verifyHomeIdentity(app); err != nil {
		return res, err
	}
	env, job, err := c.resticJobFor(ctx, r, app, v, func(env *resticEnv) backup.ResticMounts {
		return backup.ResticMounts{CtlDir: env.ctl, Home: c.Layout.AppHome(app.Slug), Bento: env.data}
	})
	if err != nil {
		return res, err
	}
	defer env.close()
	defer job.Close(ctx)

	// Databases first: files captured afterwards include every upload the
	// dumped rows can reference.
	versions := c.serviceVersions(ctx, app)
	man := ResticManifest{FormatVersion: ResticFormatVersion, StackID: c.Stack.ID, AppID: app.ID, Slug: app.Slug,
		CreatedAt: time.Now().UTC(), Paths: v.Settings.Paths, Dumps: []ResticDump{}, HomeSQLite: []string{}}
	deps := c.BackupDeps(func(s string) { r.Info(ctx, "%s", s) })
	for _, sub := range []string{"db", "sqlite", "home-sqlite"} {
		if err := platform.EnsureDir(filepath.Join(env.data, sub), 0o700, platform.RootOwner); err != nil {
			return res, err
		}
	}
	for _, b := range app.Bindings {
		dbs := b.Databases
		if b.Engine == domain.EngineSQLite {
			dbs = []string{b.SQLiteFileID}
		}
		for _, db := range dbs {
			if err := r.Phase(ctx, "dump "+db); err != nil {
				return res, err
			}
			d := ResticDump{Engine: b.Engine, Service: b.Service, Version: versions[b.Service], Database: db}
			if b.Engine == domain.EngineSQLite {
				dbFile := filepath.Join(c.Layout.SQLiteFileDir(b.SQLiteFileID), app.Slug+".db")
				if _, err := os.Lstat(dbFile); errors.Is(err, fs.ErrNotExist) {
					r.Info(ctx, "skipped SQLite %s: database file does not exist yet", db)
					continue
				}
				d.Service, d.File = "", "sqlite/"+db+".db"
			} else {
				d.File = fmt.Sprintf("db/%s-%s-%s.sql", b.Engine, b.Service, db)
			}
			if err := deps.DumpPlain(ctx, backup.Target{App: app, Binding: b, Database: db},
				filepath.Join(env.data, filepath.FromSlash(d.File))); err != nil {
				return res, Fail("dump-failed", "Check the data service; no snapshot was taken.", "dump %s: %v", db, err)
			}
			man.Dumps = append(man.Dumps, d)
		}
	}
	sqliteFiles := c.homeSQLiteFiles(app, v.Settings)
	if len(sqliteFiles) > 0 {
		if err := r.Phase(ctx, "snapshot-home-sqlite"); err != nil {
			return res, err
		}
		skipped, err := deps.SnapshotHomeSQLite(ctx, app, sqliteFiles, filepath.Join(env.data, "home-sqlite"))
		if err != nil {
			return res, Fail("sqlite-snapshot-failed", "Check the listed SQLite paths.", "%v", err)
		}
		for _, f := range sqliteFiles {
			if slices.Contains(skipped, f) {
				r.Info(ctx, "skipped SQLite %s: file does not exist", f)
				continue
			}
			man.HomeSQLite = append(man.HomeSQLite, f)
		}
	}
	spec := c.appSpec(ctx, app, versions)
	for name, val := range map[string]any{"app.json": spec, "manifest.json": man} {
		raw, _ := json.MarshalIndent(val, "", "  ")
		if err := platform.AtomicWrite(filepath.Join(env.data, name), append(raw, '\n'), 0o600, platform.RootOwner); err != nil {
			return res, err
		}
	}
	excludes := domain.ResticExcludeLines(v.Settings, backup.ResticHomeMount, sqliteFiles)
	if err := platform.AtomicWrite(filepath.Join(env.ctl, backup.ResticExcludeFile),
		[]byte(strings.Join(excludes, "\n")+"\n"), 0o400, platform.RootOwner); err != nil {
		return res, err
	}

	if err := r.Phase(ctx, "snapshot"); err != nil {
		return res, err
	}
	args := []string{"backup", "--host", "bento", "--tag", "app=" + app.ID, "--tag", "slug=" + app.Slug,
		"--tag", "trigger=" + trigger, "--exclude-file", "/run/bento-ctl/" + backup.ResticExcludeFile,
		"--exclude-caches", "--exclude-if-present", ".nobackup", "--json"}
	for _, p := range v.Settings.Paths {
		if p == "." {
			args = append(args, backup.ResticHomeMount)
		} else {
			args = append(args, backup.ResticHomeMount+"/"+p)
		}
	}
	args = append(args, backup.ResticBentoMount)
	var summary struct {
		SnapshotID string  `json:"snapshot_id"`
		FilesNew   int64   `json:"files_new"`
		FilesTotal int64   `json:"total_files_processed"`
		DataAdded  int64   `json:"data_added"`
		Duration   float64 `json:"total_duration"`
	}
	var lastProgress time.Time
	lw := &backup.LineWriter{Fn: func(line []byte) {
		var m struct {
			Type    string  `json:"message_type"`
			Percent float64 `json:"percent_done"`
			Item    string  `json:"item"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &m) != nil {
			return
		}
		switch m.Type {
		case "summary":
			_ = json.Unmarshal(line, &summary)
		case "error":
			r.Warn(ctx, "%s: %s", strings.TrimPrefix(m.Item, backup.ResticHomeMount), m.Error.Message)
		case "status":
			if time.Since(lastProgress) > 30*time.Second {
				lastProgress = time.Now()
				r.Info(ctx, "snapshot %.0f%% done", m.Percent*100)
			}
		}
	}}
	if err := job.Run(ctx, args, lw); err != nil {
		// Exit 3: snapshot created but some files were unreadable.
		var re *backup.ResticError
		if !errors.As(err, &re) || re.Exit != 3 || summary.SnapshotID == "" {
			return res, resticFail(err, "backup")
		}
		r.Warn(ctx, "snapshot %s is missing files that could not be read", summary.SnapshotID)
	}
	res.SnapshotID, res.FilesNew, res.FilesTotal, res.BytesAdded =
		summary.SnapshotID, summary.FilesNew, summary.FilesTotal, summary.DataAdded
	r.Info(ctx, "snapshot %s: %d files (%d new), %s added", short(summary.SnapshotID), summary.FilesTotal,
		summary.FilesNew, humanBytes(summary.DataAdded))

	if err := r.Phase(ctx, "retention"); err != nil {
		return res, err
	}
	ret := v.Settings.Retention
	fargs := []string{"forget", "--tag", "app=" + app.ID, "--group-by", "host"}
	for flag, n := range map[string]int{"--keep-hourly": ret.Hourly, "--keep-daily": ret.Daily,
		"--keep-weekly": ret.Weekly, "--keep-monthly": ret.Monthly} {
		if n > 0 {
			fargs = append(fargs, flag, fmt.Sprint(n))
		}
	}
	if v.State.LastPruneAt.IsZero() || time.Since(v.State.LastPruneAt) > resticPruneEvery {
		fargs = append(fargs, "--prune", "--max-unused", "5%")
	}
	if err := job.Run(ctx, fargs, nil); err != nil {
		r.Warn(ctx, "snapshot saved; retention failed: %v", resticFail(err, "forget"))
	} else if slices.Contains(fargs, "--prune") {
		c.updateResticState(ctx, app.ID, func(s *domain.ResticState) { s.LastPruneAt = time.Now().UTC() })
	}
	if err := c.resticRefresh(ctx, job, app.ID); err != nil {
		r.Warn(ctx, "snapshot saved; listing failed: %v", err)
	}
	if job.LockLeaked() {
		r.Warn(ctx, "%s", lockLeakWarning)
	}
	return res, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ---- restore ----

// mapDump picks the target database for a dump: the same name bound to the
// app, else the app's only database of that engine when the snapshot has
// only one too. Relational versions must match exactly (for example MySQL
// 8.4 or PostgreSQL 18).
func mapDump(app domain.App, man ResticManifest, d ResticDump, versions map[string]string) (domain.Binding, string, error) {
	var candidates []struct {
		b  domain.Binding
		db string
	}
	for _, b := range app.Bindings {
		if b.Engine != d.Engine {
			continue
		}
		dbs := b.Databases
		if b.Engine == domain.EngineSQLite {
			dbs = []string{b.SQLiteFileID}
		}
		for _, db := range dbs {
			candidates = append(candidates, struct {
				b  domain.Binding
				db string
			}{b, db})
		}
	}
	pick := -1
	for i, cand := range candidates {
		if cand.db == d.Database {
			pick = i
		}
	}
	sameEngine := 0
	for _, x := range man.Dumps {
		if x.Engine == d.Engine {
			sameEngine++
		}
	}
	if pick < 0 && len(candidates) == 1 && sameEngine == 1 {
		pick = 0
	}
	if pick < 0 {
		return domain.Binding{}, "", fmt.Errorf("no %s database of %s matches %s; bind one with that name", d.Engine, app.Slug, d.Database)
	}
	cand := candidates[pick]
	if d.Engine != domain.EngineSQLite && d.Version != "" && versions[cand.b.Service] != d.Version {
		return domain.Binding{}, "", fmt.Errorf("%s was dumped from %s %s but %s runs %s %s; bind a service of the same version",
			d.Database, d.Engine, d.Version, cand.b.Service, d.Engine, versions[cand.b.Service])
	}
	return cand.b, cand.db, nil
}

func (c *Controller) handleResticRestore(ctx context.Context, r *Run) (any, error) {
	var req ResticRestoreRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	app, v, err := c.loadResticApp(ctx, r)
	if err != nil {
		return nil, err
	}
	obs, err := c.observe(ctx, app)
	if err != nil {
		return nil, err
	}
	if obs.Running {
		return nil, Fail("app-running", "Stop the app before restoring, then start it again afterwards.", "app %s is running", app.Slug)
	}
	if req.Files {
		if err := c.verifyHomeIdentity(app); err != nil {
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

	if err := r.Phase(ctx, "download"); err != nil {
		return nil, err
	}
	rargs := []string{"restore", req.Snapshot, "--target", backup.ResticRestoreMount}
	if !req.Files {
		rargs = append(rargs, "--include", backup.ResticBentoMount)
	}
	if err := job.Run(ctx, rargs, nil); err != nil {
		return nil, resticFail(err, "restore")
	}
	job.Close(ctx)
	restored := filepath.Join(env.data, "backup")
	bentoDir := filepath.Join(restored, "bento")
	man, err := readManifest(bentoDir)
	if err != nil {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "%v", err)
	}
	if man.Slug != app.Slug || man.AppID != app.ID {
		r.Warn(ctx, "snapshot was taken from app %s (%s, stack %s); restoring into %s", man.Slug, man.AppID, man.StackID, app.Slug)
	}
	versions := c.serviceVersions(ctx, app)
	type plan struct {
		d  ResticDump
		b  domain.Binding
		db string
	}
	var plans []plan
	if req.Databases {
		for _, d := range man.Dumps {
			b, db, err := mapDump(app, man, d, versions)
			if err != nil {
				return nil, Fail("database-mismatch", "Bind matching databases to the app, or restore files only.", "%v", err)
			}
			plans = append(plans, plan{d, b, db})
		}
	}
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	if req.Files {
		if err := r.Phase(ctx, "restore-files"); err != nil {
			return nil, err
		}
		kept, err := c.swapHomePaths(app, filepath.Join(restored, "home"), man.Paths)
		if err != nil {
			return nil, Fail("restore-files-failed", "The previous files are kept in "+kept+"; move them back if needed.", "%v", err)
		}
		r.Info(ctx, "previous files kept in %s (delete it once the restore is verified)", kept)
		for _, rel := range man.HomeSQLite {
			if err := c.restoreHomeSQLite(app, bentoDir, rel, owner); err != nil {
				return nil, Fail("restore-files-failed", "Restore again; the file may be partially restored.", "%s: %v", rel, err)
			}
		}
	}
	deps := c.BackupDeps(nil)
	for _, p := range plans {
		if err := r.Phase(ctx, "restore "+p.db); err != nil {
			return nil, err
		}
		src, err := platform.ContainedPath(bentoDir, filepath.FromSlash(p.d.File))
		if err != nil {
			return nil, err
		}
		if p.d.Engine == domain.EngineSQLite {
			err = deps.RestoreSQLite(app, p.b, src)
		} else {
			err = deps.RestoreRelational(ctx, app, p.b, p.db, src)
		}
		if err != nil {
			return nil, Fail("restore-failed", "The database may be partially restored; restore again.", "%s: %v", p.db, err)
		}
		r.Info(ctx, "restored %s into %s", p.d.Database, p.db)
	}
	return map[string]any{"snapshot": req.Snapshot, "files": req.Files, "databases": len(plans)}, nil
}

func readManifest(dir string) (ResticManifest, error) {
	var m ResticManifest
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, fmt.Errorf("snapshot has no Bento manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if m.FormatVersion != ResticFormatVersion {
		return m, fmt.Errorf("unsupported snapshot format %d (supported %d)", m.FormatVersion, ResticFormatVersion)
	}
	for _, p := range m.Paths {
		if _, bad := domain.CleanHomeRel(p, true); bad != "" {
			return m, fmt.Errorf("manifest path %q %s", p, bad)
		}
	}
	for _, p := range m.HomeSQLite {
		if _, bad := domain.CleanHomeRel(p, false); bad != "" {
			return m, fmt.Errorf("manifest path %q %s", p, bad)
		}
	}
	for _, d := range m.Dumps {
		if d.File == "" || strings.Contains(d.File, "..") || strings.HasPrefix(d.File, "/") {
			return m, fmt.Errorf("manifest dump %q is unsafe", d.File)
		}
	}
	return m, nil
}

// swapHomePaths moves restored paths into the live home. The replaced paths
// are moved, never deleted, into homes/.pre-restore-<slug>-<stamp>, which is
// returned. Restored files are re-owned to the app (restic restores the
// source UID, which differs on another stack); symlinks are re-owned, never
// followed. The home's identity record stays the live one.
func (c *Controller) swapHomePaths(app domain.App, restoredHome string, paths []string) (string, error) {
	home := c.Layout.AppHome(app.Slug)
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	kept := filepath.Join(c.Layout.HomesDir(), ".pre-restore-"+app.Slug+"-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := platform.EnsureDir(kept, 0o700, platform.RootOwner); err != nil {
		return kept, err
	}
	info, err := os.Lstat(restoredHome)
	if err != nil || !info.IsDir() {
		return kept, errors.New("snapshot holds no home directory")
	}
	if _, err := platform.ChownTree(restoredHome, owner, false, 0); err != nil {
		return kept, fmt.Errorf("re-own restored files: %w", err)
	}
	if slices.Contains(paths, ".") {
		homeOwner, mode, err := platform.StatOwner(home)
		if err != nil {
			return kept, err
		}
		sidecar, err := os.ReadFile(c.Layout.HomeSidecar(app.Slug))
		if err != nil {
			return kept, err
		}
		_ = os.Remove(filepath.Join(restoredHome, domain.HomeSidecarName))
		if err := platform.AtomicWrite(filepath.Join(restoredHome, domain.HomeSidecarName), sidecar, 0o444, platform.RootOwner); err != nil {
			return kept, err
		}
		if err := os.Chmod(restoredHome, mode.Perm()); err != nil {
			return kept, err
		}
		if err := os.Lchown(restoredHome, homeOwner.UID, homeOwner.GID); err != nil {
			return kept, err
		}
		if err := renameNoCross(home, filepath.Join(kept, "home")); err != nil {
			return kept, err
		}
		if err := renameNoCross(restoredHome, home); err != nil {
			// Put the original back; the restore did not happen.
			_ = os.Rename(filepath.Join(kept, "home"), home)
			return kept, err
		}
		return kept, nil
	}
	for _, p := range paths {
		src := filepath.Join(restoredHome, filepath.FromSlash(p))
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		dst, err := platform.ContainedPath(home, filepath.FromSlash(p))
		if err != nil {
			return kept, err
		}
		if err := platform.NoSymlinkBetween(home, dst); err != nil {
			return kept, err
		}
		if _, err := os.Lstat(dst); err == nil {
			aside := filepath.Join(kept, "home", filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(aside), 0o700); err != nil {
				return kept, err
			}
			if err := renameNoCross(dst, aside); err != nil {
				return kept, err
			}
		}
		if err := c.ensureHomeParents(home, filepath.Dir(dst), owner); err != nil {
			return kept, err
		}
		if err := renameNoCross(src, dst); err != nil {
			return kept, err
		}
	}
	return kept, nil
}

// ensureHomeParents creates missing directories between home and dir as the
// app owner.
func (c *Controller) ensureHomeParents(home, dir string, owner platform.Owner) error {
	rel, err := filepath.Rel(home, dir)
	if err != nil || rel == "." {
		return err
	}
	cur := home
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(cur, 0o750); err != nil {
				return err
			}
			if err := os.Lchown(cur, owner.UID, owner.GID); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is not a directory", cur)
		}
	}
	return nil
}

func renameNoCross(from, to string) error {
	err := os.Rename(from, to)
	if errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("staging and homes are on different filesystems; restore needs them on one: %w", err)
	}
	return err
}

// restoreHomeSQLite installs a .backup copy at its home path, discarding the
// live file's journals so they are not replayed against it.
func (c *Controller) restoreHomeSQLite(app domain.App, bentoDir, rel string, owner platform.Owner) error {
	src, err := platform.ContainedPath(filepath.Join(bentoDir, "home-sqlite"), filepath.FromSlash(rel))
	if err != nil {
		return err
	}
	if !backup.IsSQLiteFile(src) {
		return errors.New("snapshot copy is not a SQLite database")
	}
	home := c.Layout.AppHome(app.Slug)
	dst, err := platform.ContainedPath(home, filepath.FromSlash(rel))
	if err != nil {
		return err
	}
	if err := platform.NoSymlinkBetween(home, dst); err != nil {
		return err
	}
	if err := c.ensureHomeParents(home, filepath.Dir(dst), owner); err != nil {
		return err
	}
	for _, sfx := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(dst + sfx); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	tmp := filepath.Join(filepath.Dir(dst), ".bento-restore-"+platform.RandomHex(6))
	if err := platform.CopyFile(src, tmp, 0o600, owner); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
