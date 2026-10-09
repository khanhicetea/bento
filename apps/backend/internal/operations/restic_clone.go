package operations

import (
	"bytes"
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
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Restoring an app backup always creates a new app: restic.inspect previews a
// snapshot read-only, app.clone-from-backup builds the clone on this stack.
// The source app, its home, its databases and its repository are never
// written to (apart from restic's own lock files). The clone is left stopped
// and unpublished.

const (
	KindResticInspect      = "restic.inspect"
	KindAppCloneFromBackup = "app.clone-from-backup"
)

// BackupAfter values of a clone request.
const (
	CloneBackupNone    = "none"
	CloneBackupNewRepo = "new-repo"
)

// ResticInspectRequest names the snapshot to preview. Slug and KeepUsername
// are optional: with a slug the preview shows the real target names.
type ResticInspectRequest struct {
	Snapshot     string `json:"snapshot"`
	Slug         string `json:"slug,omitempty"`
	KeepUsername bool   `json:"keepUsername,omitempty"`
}

// ResticCloneRequest is the persisted intent of a clone. AppID is allocated at
// submission so the partial app of an interrupted attempt can be recognized
// and removed by restic.recover after a restart.
type ResticCloneRequest struct {
	Snapshot     string `json:"snapshot"`
	Slug         string `json:"slug"`
	AppID        string `json:"appId,omitempty"`
	KeepUsername bool   `json:"keepUsername"`
	BackupAfter  string `json:"backupAfter,omitempty"`
}

// CloneDatabase is one relational database of the clone.
type CloneDatabase struct {
	Engine         domain.Engine `json:"engine"`
	Service        string        `json:"service"`
	Version        string        `json:"version"`
	Source         string        `json:"source"`
	Target         string        `json:"target"`
	SourceUsername string        `json:"sourceUsername,omitempty"`
	Username       string        `json:"username"`
	UsernameKept   bool          `json:"usernameKept"`
	UsernameNote   string        `json:"usernameNote,omitempty"`
	PasswordKept   bool          `json:"passwordKept"`
}

// CloneSQLite is one SQLite binding file of the clone.
type CloneSQLite struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// CloneDomain is a domain of the source that is never moved automatically.
type CloneDomain struct {
	Name  string `json:"name"`
	InUse bool   `json:"inUse"`
}

// ResticClonePreview is the result of restic.inspect.
type ResticClonePreview struct {
	Snapshot       string           `json:"snapshot"`
	SnapshotTime   time.Time        `json:"snapshotTime"`
	FormatVersion  int              `json:"formatVersion"`
	SizeBytes      int64            `json:"sizeBytes"`
	SourceSlug     string           `json:"sourceSlug"`
	SourceAppID    string           `json:"sourceAppId"`
	StackID        string           `json:"stackId"`
	Slug           string           `json:"slug"`
	Secrets        bool             `json:"secrets"`
	RuntimeKind    string           `json:"runtimeKind"`
	RuntimeVersion string           `json:"runtimeVersion"`
	Resources      domain.Resources `json:"resources"`
	EnvKeys        []string         `json:"envKeys"`
	EmptyEnv       []string         `json:"emptyEnv"`
	HomePath       string           `json:"homePath"`
	Databases      []CloneDatabase  `json:"databases"`
	SQLite         []CloneSQLite    `json:"sqlite"`
	Minicron       bool             `json:"minicron"`
	Domains        []CloneDomain    `json:"domains"`
	Git            bool             `json:"git"`
	Notes          []string         `json:"notes"`
	Blockers       []string         `json:"blockers"`
	// Snapshots lists the repository's snapshots (newest first). Only the
	// preview of another stack's repository fills it.
	Snapshots []CloneSnapshot `json:"snapshots"`
}

// CloneSnapshot is one snapshot of a repository being restored from.
type CloneSnapshot struct {
	ID      string    `json:"id"`
	ShortID string    `json:"shortId"`
	Time    time.Time `json:"time"`
	Tags    []string  `json:"tags"`
}

// ResticCloneResult is the result of app.clone-from-backup: what the operator
// checks before starting the clone. It never contains secret values.
type ResticCloneResult struct {
	AppID        string          `json:"appId"`
	Slug         string          `json:"slug"`
	SourceSlug   string          `json:"sourceSlug"`
	SourceAppID  string          `json:"sourceAppId"`
	Snapshot     string          `json:"snapshot"`
	SnapshotTime time.Time       `json:"snapshotTime"`
	Stopped      bool            `json:"stopped"`
	HomePath     string          `json:"homePath"`
	Databases    []CloneDatabase `json:"databases"`
	SQLite       []CloneSQLite   `json:"sqlite"`
	EmptyEnv     []string        `json:"emptyEnv"`
	Minicron     bool            `json:"minicron"`
	// DeployKey is the new public deploy key to add to the git host.
	DeployKey string        `json:"deployKey,omitempty"`
	Domains   []CloneDomain `json:"domains"`
	Checklist []string      `json:"checklist"`
}

// ---- submission ----

// SubmitResticInspect queues a read-only preview of a snapshot.
func (c *Controller) SubmitResticInspect(
	ctx context.Context,
	appID string,
	req ResticInspectRequest,
	idem string,
) (store.Operation, error) {
	if _, _, err := c.requireResticRepo(ctx, appID); err != nil {
		return store.Operation{}, err
	}
	var errs domain.ValidationErrors
	if !domain.ResticSnapshotID.MatchString(req.Snapshot) {
		errs.Add("snapshot", "choose a snapshot")
	}
	if req.Slug != "" {
		if err := domain.ValidateSlug(req.Slug); err != nil {
			errs.Add("slug", "%s", err)
		}
	}
	if err := errs.Err(); err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindResticInspect, TargetKind: "app", TargetID: appID, IdempotencyKey: idem, Request: req,
	})
	return op, err
}

// SubmitResticClone queues a clone of a snapshot into a new app on this
// stack. It needs the exact confirmation "clone <new-slug>".
func (c *Controller) SubmitResticClone(
	ctx context.Context,
	appID string,
	req ResticCloneRequest,
	confirm, idem string,
) (store.Operation, error) {
	if _, _, err := c.requireResticRepo(ctx, appID); err != nil {
		return store.Operation{}, err
	}
	var errs domain.ValidationErrors
	if !domain.ResticSnapshotID.MatchString(req.Snapshot) {
		errs.Add("snapshot", "choose a snapshot")
	}
	if err := domain.ValidateSlug(req.Slug); err != nil {
		errs.Add("slug", "%s", err)
	}
	switch req.BackupAfter {
	case "":
		req.BackupAfter = CloneBackupNone
	case CloneBackupNone, CloneBackupNewRepo:
	default:
		errs.Add("backupAfter", "must be %q or %q", CloneBackupNone, CloneBackupNewRepo)
	}
	if err := errs.Err(); err != nil {
		return store.Operation{}, err
	}
	if confirm != "clone "+req.Slug {
		return store.Operation{}, fmt.Errorf(
			"%w: type exactly %q; this creates a new app with its own databases and files", ErrConfirmation, "clone "+req.Slug)
	}
	if _, err := store.GetApp(ctx, c.Store.DB(), req.Slug); err == nil {
		return store.Operation{}, fmt.Errorf("%w: app %q already exists", store.ErrConflict, req.Slug)
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Operation{}, err
	}
	if _, err := os.Lstat(c.Layout.AppHome(req.Slug)); err == nil {
		return store.Operation{}, fmt.Errorf(
			"%w: a retained home for %q exists from an earlier app; prune it first", store.ErrConflict, req.Slug)
	}
	req.AppID = platform.NewAppID()
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindAppCloneFromBackup, TargetKind: "app", TargetID: appID, IdempotencyKey: idem, Request: req,
	})
	return op, err
}

// ---- snapshot metadata ----

// parseAppSpec decodes app.json strictly and checks what a clone relies on.
func parseAppSpec(raw []byte) (AppSpec, error) {
	var s AppSpec
	if err := decodeStrict(raw, &s); err != nil {
		return s, fmt.Errorf("app.json: %w", err)
	}
	if s.FormatVersion < 1 || s.FormatVersion > ResticFormatVersion {
		return s, fmt.Errorf("unsupported app.json format %d (supported 1-%d)", s.FormatVersion, ResticFormatVersion)
	}
	if s.Slug == "" {
		return s, errors.New("app.json has no slug")
	}
	return s, nil
}

// parseResticSecrets decodes secrets.json strictly.
func parseResticSecrets(raw []byte) (ResticSecrets, error) {
	var s ResticSecrets
	if err := decodeStrict(raw, &s); err != nil {
		return s, fmt.Errorf("secrets.json: %w", err)
	}
	for _, b := range s.Bindings {
		if b.Index < 0 || !secretPassword.MatchString(b.Password) {
			return s, errors.New("secrets.json holds an unusable database password")
		}
	}
	return s, nil
}

var secretPassword = regexp.MustCompile(`^[A-Za-z0-9]{8,256}$`)

func decodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// readSnapshotMeta reads manifest.json and app.json of a snapshot without
// restoring it.
func readSnapshotMeta(ctx context.Context, job *backup.ResticJob, snapshot string) (ResticManifest, AppSpec, error) {
	dump := func(name string) ([]byte, error) {
		return job.Output(ctx, []string{"dump", "--no-lock", snapshot, backup.ResticBentoMount + "/" + name}, 4<<20)
	}
	var man ResticManifest
	var spec AppSpec
	raw, err := dump("manifest.json")
	if err != nil {
		return man, spec, resticFail(err, "read manifest")
	}
	if man, err = parseManifest(raw); err != nil {
		return man, spec, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "%v", err)
	}
	if raw, err = dump("app.json"); err != nil {
		return man, spec, resticFail(err, "read app.json")
	}
	if spec, err = parseAppSpec(raw); err != nil {
		return man, spec, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "%v", err)
	}
	return man, spec, nil
}

// snapshotSize returns the restored size of a snapshot, or 0 when unknown.
func snapshotSize(ctx context.Context, job *backup.ResticJob, snapshot string) int64 {
	raw, err := job.Output(ctx, []string{"stats", "--no-lock", "--mode", "restore-size", "--json", snapshot}, 64<<10)
	if err != nil {
		return 0
	}
	var st struct {
		TotalSize int64 `json:"total_size"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &st) != nil {
		return 0
	}
	return st.TotalSize
}

func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// ---- clone plan ----

type cloneInput struct {
	Man          ResticManifest
	Spec         AppSpec
	Secrets      *ResticSecrets // nil: not read (preview) or not in the snapshot
	Slug         string         // empty: preview without a target slug
	AppID        string
	KeepUsername bool
}

// bindingMap links one source binding to its clone (index into App.Bindings,
// -1 when the binding cannot be cloned) and maps database names.
type bindingMap struct {
	src    AppSpecBinding
	newIdx int
	dbs    map[string]string
}

type clonePlan struct {
	App       domain.App
	maps      []bindingMap
	Databases []CloneDatabase
	SQLite    []CloneSQLite
	EnvKeys   []string
	EmptyEnv  []string
	Domains   []CloneDomain
	Git       *domain.GitSource
	Minicron  bool
	Notes     []string
	Blockers  []string
}

const previewSlug = "<new-slug>"

// pickCloneService finds the service a source binding is cloned onto: the
// service of the same name, engine and version, else any of the same engine
// and version. Services already taken by another binding of the clone are
// skipped: an app has at most one binding per service (one user, one
// password). taken reports whether a matching service exists but is taken.
func pickCloneService(svcs []store.ServiceRow, sb AppSpecBinding, used map[string]bool) (svc *store.ServiceRow, taken bool) {
	for i := range svcs {
		if svcs[i].Name == sb.Service && svcs[i].Engine == sb.Engine && svcs[i].Version == sb.Version && !used[svcs[i].Name] {
			return &svcs[i], false
		}
	}
	for i := range svcs {
		if svcs[i].Engine == sb.Engine && svcs[i].Version == sb.Version {
			if !used[svcs[i].Name] {
				return &svcs[i], false
			}
			taken = true
		}
	}
	return nil, taken
}

// canKeepUsername applies the §4d rule: the source database user is reused
// only when no user of that name exists on the service, no binding of any app
// on this stack uses it, and no retained (not yet pruned) app lists it.
func (c *Controller) canKeepUsername(ctx context.Context, svc store.ServiceRow, user string) (bool, string) {
	apps, err := store.ListApps(ctx, c.Store.DB())
	if err != nil {
		return false, "could not check existing bindings"
	}
	for _, a := range apps {
		for _, b := range a.Bindings {
			if b.Username == user {
				return false, fmt.Sprintf("user %s is still used by app %s", user, a.Slug)
			}
		}
	}
	retired, err := store.ListRetired(ctx, c.Store.DB())
	if err != nil {
		return false, "could not check retained apps"
	}
	for _, ret := range retired {
		if ret.PrunedAt != "" {
			continue
		}
		for _, rel := range ret.Artifacts.Relational {
			if rel.Username == user {
				return false, fmt.Sprintf("user %s belongs to retained app %s", user, ret.Slug)
			}
		}
	}
	id, err := c.serviceContainer(ctx, svc.DataService)
	if err != nil {
		return false, fmt.Sprintf("could not check %s for the user", svc.Name)
	}
	exists, err := c.Data().UserExists(ctx, svc.DataService, id, user)
	if err != nil {
		return false, fmt.Sprintf("could not check %s for the user", svc.Name)
	}
	if exists {
		return false, fmt.Sprintf("user %s already exists on %s", user, svc.Name)
	}
	return true, ""
}

// planClone turns a snapshot's description into the new app: names, env,
// bindings and the list of things the operator must check. It validates like
// operator input and reports every blocking problem instead of stopping at
// the first.
func (c *Controller) planClone(ctx context.Context, in cloneInput) (*clonePlan, error) {
	p := &clonePlan{Notes: []string{}, Blockers: []string{}, EnvKeys: []string{}, EmptyEnv: []string{}}
	block := func(format string, args ...any) { p.Blockers = append(p.Blockers, fmt.Sprintf(format, args...)) }
	preview := in.Slug == ""
	slug := in.Slug
	if preview {
		slug = previewSlug
	} else if err := domain.ValidateSlug(slug); err != nil {
		block("slug %q: %s", slug, err)
	} else {
		if _, err := store.GetApp(ctx, c.Store.DB(), slug); err == nil {
			block("an app named %s already exists", slug)
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if _, err := os.Lstat(c.Layout.AppHome(slug)); err == nil {
			block("a retained home for %s exists from an earlier app; prune it first", slug)
		}
	}

	home := in.Man.HomePath
	if home == "" {
		home = "/home/" + in.Man.Slug
	}
	if err := domain.ValidateHomePath(home); err != nil {
		block("home path %q %s", home, err)
	}
	stored := home
	if home == "/home/"+slug {
		stored = ""
	}

	rt := in.Spec.Runtime
	env := make([]domain.EnvVar, 0, len(rt.Env))
	secretEnv := map[string]string{}
	if in.Secrets != nil {
		for _, e := range in.Secrets.Env {
			secretEnv[e.Key] = e.Value
		}
	}
	for _, e := range rt.Env {
		p.EnvKeys = append(p.EnvKeys, e.Key)
		// app.json holds every secret value as RedactedEnvValue; the backup
		// decided what was secret, so the clone does not guess again.
		if val, ok := secretEnv[e.Key]; ok || e.Value == domain.RedactedEnvValue {
			if ok {
				e.Value = val
			} else if in.Secrets == nil && in.Man.Secrets {
				// Preview of a snapshot that keeps its secrets.
				e.Value = domain.RedactedEnvValue
			} else {
				e.Value = ""
				p.EmptyEnv = append(p.EmptyEnv, e.Key)
			}
		}
		env = append(env, e)
	}
	rt.Env = env
	res := in.Spec.Resources
	accessLog := in.Spec.AccessLog
	if in.Spec.Route != nil {
		accessLog = in.Spec.Route.AccessLog
	}
	ingress := in.Spec.Ingress
	if ingress == "" {
		ingress = domain.IngressManaged
	}
	var errs domain.ValidationErrors
	domain.ValidateRuntime(&rt, &errs)
	domain.ValidateResources(&res, rt.Kind, &errs)
	domain.ValidateEnv(rt.Env, &errs)
	if err := domain.ValidateIngress(ingress); err != nil {
		errs.Add("ingress", "%s", err)
	}
	if err := errs.Err(); err != nil {
		block("the snapshot's app settings are invalid: %v", err)
	}

	now := time.Now().UTC()
	app := domain.App{
		ID: in.AppID, Slug: slug, Runtime: rt, Resources: res, DesiredRuntime: domain.DesiredStopped,
		Ingress: ingress, Publication: domain.Unpublished, AccessLog: accessLog, HomePath: stored,
		ConfigGeneration: 1, CredentialsGeneration: 1, CreatedAt: now, UpdatedAt: now,
		Redis: domain.RedisIdentity{Mode: "acl", Prefix: slug + ":", Username: "app-" + in.AppID,
			Password: platform.RandomPassword(32)},
	}
	p.App = app

	svcs, err := store.ListServices(ctx, c.Store.DB())
	if err != nil {
		return nil, err
	}
	secretPW := map[int]string{}
	if in.Secrets != nil {
		for _, b := range in.Secrets.Bindings {
			secretPW[b.Index] = b.Password
		}
	}
	usedSvc := map[string]bool{}
	for i, sb := range in.Spec.Bindings {
		bm := bindingMap{src: sb, newIdx: -1, dbs: map[string]string{}}
		b := domain.Binding{ID: "b" + platform.RandomHex(6), AppID: in.AppID, Engine: sb.Engine, CreatedAt: now}
		switch sb.Engine {
		case domain.EngineSQLite:
			b.SQLiteFileID = slug + "_" + platform.RandomHex(5)
			b.Vacuum = newVacuumSlot()
			p.SQLite = append(p.SQLite, CloneSQLite{
				Source: domain.Binding{SQLiteFileID: sb.SQLiteID}.SQLiteContainerDir() + "/" + in.Spec.Slug + ".db",
				Target: b.SQLiteContainerDir() + "/" + slug + ".db",
			})
		case domain.EngineMySQL, domain.EnginePostgres:
			svc, taken := pickCloneService(svcs, sb, usedSvc)
			if svc == nil {
				if taken {
					block("the snapshot's app has several %s %s bindings; add another %s service of version %s first",
						sb.Engine, sb.Version, sb.Engine, sb.Version)
				} else {
					block("no %s service of version %s on this stack (add it first)", sb.Engine, sb.Version)
				}
				p.maps = append(p.maps, bm)
				continue
			}
			usedSvc[svc.Name] = true
			b.Service = svc.Name
			srcUser := ""
			if in.Man.FormatVersion >= 2 {
				for _, d := range in.Man.Dumps {
					if d.Binding == i && d.Username != "" {
						srcUser = d.Username
					}
				}
			}
			b.Username = "u" + in.AppID
			note := ""
			if in.KeepUsername {
				if srcUser == "" {
					note = "the snapshot does not record the source database user"
				} else if ok, why := c.canKeepUsername(ctx, *svc, srcUser); ok {
					b.Username = srcUser
				} else {
					note = why
				}
			}
			usernameKept := srcUser != "" && b.Username == srcUser
			if !usernameKept && note == "" {
				note = "a new database user is used"
			}
			b.Password = platform.RandomPassword(32)
			passwordKept := false
			if pw, ok := secretPW[i]; ok {
				b.Password, passwordKept = pw, true
			}
			for _, db := range sb.Databases {
				target := dbName(slug, dumpSuffix(in.Spec.Slug, db))
				if !preview {
					if err := domain.ValidateDatabaseName(target); err != nil {
						block("database name %s %s", target, err)
					}
				}
				if slices.Contains(b.Databases, target) {
					block("databases of %s collide on the name %s", sb.Service, target)
					continue
				}
				b.Databases = append(b.Databases, target)
				bm.dbs[db] = target
				p.Databases = append(p.Databases, CloneDatabase{
					Engine: sb.Engine, Service: svc.Name, Version: svc.Version, Source: db, Target: target,
					SourceUsername: srcUser, Username: b.Username, UsernameKept: usernameKept, UsernameNote: note,
					PasswordKept: passwordKept,
				})
			}
		default:
			block("binding %d has an unsupported engine %q", i, sb.Engine)
			p.maps = append(p.maps, bm)
			continue
		}
		bm.newIdx = len(p.App.Bindings)
		p.App.Bindings = append(p.App.Bindings, b)
		p.maps = append(p.maps, bm)
	}

	used := map[string]bool{}
	if hosts, err := store.ListHosts(ctx, c.Store.DB()); err == nil {
		for _, h := range hosts {
			used[h.Name] = true
		}
	}
	p.Domains = []CloneDomain{}
	for _, d := range in.Spec.Domains {
		p.Domains = append(p.Domains, CloneDomain{Name: d, InUse: used[d]})
	}

	minicronRel := domain.MinicronDataDir + "/" + domain.MinicronDBFile
	p.Minicron = in.Man.Minicron != nil || slices.Contains(in.Man.HomeSQLite, minicronRel)

	if in.Spec.GitRepoURL != "" {
		var gerrs domain.ValidationErrors
		domain.ValidateGitSource(in.Spec.GitRepoURL, in.Spec.GitBranch, &gerrs)
		if err := gerrs.Err(); err != nil {
			p.Notes = append(p.Notes, "the git source was not copied: "+err.Error())
		} else {
			p.Git = &domain.GitSource{RepoURL: in.Spec.GitRepoURL, Branch: in.Spec.GitBranch,
				DeployedCommit: in.Spec.GitCommit}
		}
	}
	return p, nil
}

// resolveDump finds the clone binding and the target database of one dump.
// Format 1 manifests carry no binding index, so dumps are matched by name.
func (p *clonePlan) resolveDump(man ResticManifest, d ResticDump) (domain.Binding, string, error) {
	pick := -1
	if man.FormatVersion >= 2 && d.Binding >= 0 && d.Binding < len(p.maps) && p.maps[d.Binding].src.Engine == d.Engine {
		pick = d.Binding
	} else {
		for i, bm := range p.maps {
			if bm.src.Engine != d.Engine {
				continue
			}
			if d.Engine == domain.EngineSQLite && (bm.src.SQLiteID == d.Database || bm.src.SQLiteID == d.FileID) {
				pick = i
			} else if _, ok := bm.dbs[d.Database]; ok && d.Engine != domain.EngineSQLite {
				pick = i
			}
		}
	}
	if pick < 0 || p.maps[pick].newIdx < 0 {
		return domain.Binding{}, "", fmt.Errorf("no binding of the clone matches the dump of %s", d.Database)
	}
	b := p.App.Bindings[p.maps[pick].newIdx]
	if d.Engine == domain.EngineSQLite {
		return b, b.SQLiteFileID, nil
	}
	target, ok := p.maps[pick].dbs[d.Database]
	if !ok {
		return domain.Binding{}, "", fmt.Errorf("database %s is not part of the snapshot's app", d.Database)
	}
	return b, target, nil
}

// checkCloneTargets asks the data services whether any target database (or a
// kept user) already exists. A clone only ever creates new, empty databases.
func (c *Controller) checkCloneTargets(ctx context.Context, p *clonePlan) ([]string, error) {
	var out []string
	for _, b := range p.App.Bindings {
		if b.Engine == domain.EngineSQLite {
			continue
		}
		svc, err := store.GetService(ctx, c.Store.DB(), b.Service)
		if err != nil {
			return nil, err
		}
		id, err := c.serviceContainer(ctx, svc.DataService)
		if err != nil {
			return nil, err
		}
		for _, db := range b.Databases {
			exists, err := c.Data().DatabaseExists(ctx, svc.DataService, id, db)
			if err != nil {
				return nil, err
			}
			if exists {
				out = append(out, fmt.Sprintf("database %s already exists on %s", db, svc.Name))
			}
		}
		exists, err := c.Data().UserExists(ctx, svc.DataService, id, b.Username)
		if err != nil {
			return nil, err
		}
		if exists {
			out = append(out, fmt.Sprintf("database user %s already exists on %s", b.Username, svc.Name))
		}
	}
	return out, nil
}

// checkDumpSpace checks that the volume of each data service the clone
// imports into has room for its dumps (twice their size: compressed dumps and
// indexes grow on import). The staging check before the download does not
// cover Docker's data root.
func (c *Controller) checkDumpSpace(ctx context.Context, r *Run, man ResticManifest, p *clonePlan, bentoDir string) error {
	need := map[string]int64{}
	for _, d := range man.Dumps {
		if d.Engine == domain.EngineSQLite {
			continue
		}
		b, _, err := p.resolveDump(man, d)
		if err != nil {
			continue // reported when the dump is restored
		}
		path, err := platform.ContainedPath(bentoDir, filepath.FromSlash(d.File))
		if err != nil {
			continue
		}
		if info, err := os.Lstat(path); err == nil {
			need[b.Service] += info.Size()
		}
	}
	for name, size := range need {
		svc, err := store.GetService(ctx, c.Store.DB(), name)
		if err != nil {
			return err
		}
		vol, err := c.Engine.VolumeInspect(ctx, svc.Volume)
		if err != nil || vol == nil || vol.Mountpoint == "" {
			r.Warn(ctx, "free space on %s's volume is unknown; the database import is not checked", name)
			continue
		}
		free, err := freeBytes(vol.Mountpoint)
		if err != nil {
			r.Warn(ctx, "free space on %s's volume is unknown: %v", name, err)
			continue
		}
		if free < uint64(2*size) {
			return Fail("disk-full", "Free disk space where Docker keeps volumes, then retry.",
				"importing into %s needs about %s but only %s is free", name, humanBytes(2*size), humanBytes(int64(free)))
		}
	}
	return nil
}

func runtimeLabel(rt domain.Runtime) (string, string) {
	switch {
	case rt.PHP != nil:
		return string(rt.Kind), rt.PHP.Version
	case rt.HTTP != nil:
		return string(rt.Kind), rt.HTTP.Toolchain + " " + rt.HTTP.Version
	}
	return string(rt.Kind), ""
}

// ---- inspect ----

func (c *Controller) handleResticInspect(ctx context.Context, r *Run) (any, error) {
	var req ResticInspectRequest
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
	return c.inspectSnapshot(ctx, r, job, req.Snapshot, req.Slug, req.KeepUsername)
}

// inspectSnapshot reads a snapshot's manifest and app.json and plans the
// clone without changing anything.
func (c *Controller) inspectSnapshot(
	ctx context.Context,
	r *Run,
	job *backup.ResticJob,
	snapshot, reqSlug string,
	keepUsername bool,
) (ResticClonePreview, error) {
	none := ResticClonePreview{}
	if err := r.Phase(ctx, "read-snapshot"); err != nil {
		return none, err
	}
	man, spec, err := readSnapshotMeta(ctx, job, snapshot)
	if err != nil {
		return none, err
	}
	size := snapshotSize(ctx, job, snapshot)
	if job.LockLeaked() {
		r.Warn(ctx, "%s", lockLeakWarning)
	}
	if err := r.Phase(ctx, "plan"); err != nil {
		return none, err
	}
	plan, err := c.planClone(ctx, cloneInput{Man: man, Spec: spec, Slug: reqSlug, AppID: "apreview",
		KeepUsername: keepUsername})
	if err != nil {
		return none, err
	}
	if reqSlug != "" {
		// Services that are down cannot be asked; the clone checks again.
		if more, err := c.checkCloneTargets(ctx, plan); err == nil {
			plan.Blockers = append(plan.Blockers, more...)
		}
	}
	kind, version := runtimeLabel(plan.App.Runtime)
	slug := reqSlug
	if slug == "" {
		slug = previewSlug
	}
	return ResticClonePreview{
		Snapshot: snapshot, Snapshots: []CloneSnapshot{}, SnapshotTime: man.CreatedAt, FormatVersion: man.FormatVersion, SizeBytes: size,
		SourceSlug: man.Slug, SourceAppID: man.AppID, StackID: man.StackID, Slug: slug, Secrets: man.Secrets,
		RuntimeKind: kind, RuntimeVersion: version, Resources: plan.App.Resources,
		EnvKeys: plan.EnvKeys, EmptyEnv: plan.EmptyEnv, HomePath: plan.App.ContainerHome(),
		Databases: nonNilSlice(plan.Databases), SQLite: nonNilSlice(plan.SQLite), Minicron: plan.Minicron,
		Domains: nonNilSlice(plan.Domains), Git: plan.Git != nil, Notes: plan.Notes, Blockers: plan.Blockers,
	}, nil
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ---- clone ----

func redactOpError(err error, rep *strings.Replacer) error {
	if err == nil || errors.Is(err, ErrCancelled) || errors.Is(err, context.Canceled) {
		return err
	}
	var oe *OpError
	if errors.As(err, &oe) {
		cp := *oe
		cp.Message, cp.Guidance = rep.Replace(cp.Message), rep.Replace(cp.Guidance)
		return &cp
	}
	if msg := rep.Replace(err.Error()); msg != err.Error() {
		return errors.New(msg)
	}
	return err
}

// addGuidance appends a sentence to an operation error's guidance.
// Cancellation is left as is.
func addGuidance(err error, more string) error {
	if errors.Is(err, ErrCancelled) || errors.Is(err, context.Canceled) {
		return err
	}
	var oe *OpError
	if errors.As(err, &oe) {
		cp := *oe
		cp.Guidance = strings.TrimSpace(cp.Guidance + " " + more)
		return &cp
	}
	return Fail("clone-failed", more, "%v", err)
}

func cloneSecretRedactor(s *ResticSecrets) *strings.Replacer {
	var pairs []string
	if s != nil {
		for _, e := range s.Env {
			if e.Value != "" {
				pairs = append(pairs, e.Value, domain.RedactedEnvValue)
			}
		}
		for _, b := range s.Bindings {
			pairs = append(pairs, b.Password, domain.RedactedEnvValue)
		}
	}
	return strings.NewReplacer(pairs...)
}

// cloneRepo is the open restic job a clone reads its snapshot from.
type cloneRepo struct {
	env  *resticEnv
	job  *backup.ResticJob
	view ResticView // settings of the repository being restored from
	// Set only for a repository of another stack:
	repoID    string
	keyFile   string // the pending key, adopted by backupAfter "same-repo"
	snapshot  string // resolved snapshot id, overrides the request's
	snapshots []domain.ResticSnapshot
}

func (c *Controller) handleAppCloneFromBackup(ctx context.Context, r *Run) (any, error) {
	var req ResticCloneRequest
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	return c.runClone(ctx, r, req, func() (cloneRepo, error) {
		src, v, err := c.loadResticApp(ctx, r)
		if err != nil {
			return cloneRepo{}, err
		}
		env, job, err := c.resticJobFor(ctx, r, src, v, func(env *resticEnv) backup.ResticMounts {
			return backup.ResticMounts{CtlDir: env.ctl, RestoreDir: env.data}
		})
		if err != nil {
			return cloneRepo{}, err
		}
		return cloneRepo{env: env, job: job, view: v}, nil
	})
}

// runClone builds the clone of a snapshot; open provides the repository job.
func (c *Controller) runClone(
	ctx context.Context,
	r *Run,
	req ResticCloneRequest,
	open func() (cloneRepo, error),
) (result any, err error) {
	if _, perr := store.GetApp(ctx, c.Store.DB(), req.Slug); perr == nil {
		return nil, Fail("slug-taken", "Choose another slug.", "app %s already exists", req.Slug)
	}
	if _, perr := os.Lstat(c.Layout.AppHome(req.Slug)); perr == nil {
		return nil, Fail("home-retained", "Prune the retained app data first, or choose another slug.",
			"a retained home for %s exists", req.Slug)
	}

	repo, err := open()
	if err != nil {
		return nil, err
	}
	env, job, v := repo.env, repo.job, repo.view
	defer env.close()
	defer job.Close(ctx)
	if repo.snapshot != "" {
		req.Snapshot = repo.snapshot
	}

	if err := r.Phase(ctx, "check-disk"); err != nil {
		return nil, err
	}
	if size := snapshotSize(ctx, job, req.Snapshot); size == 0 {
		r.Warn(ctx, "%s", "the snapshot size is unknown, so the free-disk check was skipped")
	} else {
		if free, ferr := freeBytes(env.root); ferr == nil && free < uint64(size)+uint64(size)/10 {
			return nil, Fail("disk-full", "Free disk space on the stack's staging directory, then retry.",
				"the snapshot needs %s (+10%%) but only %s is free", humanBytes(size), humanBytes(int64(free)))
		}
	}
	if err := r.Phase(ctx, "download"); err != nil {
		return nil, err
	}
	if err := job.Run(ctx, []string{"restore", req.Snapshot, "--target", backup.ResticRestoreMount}, nil); err != nil {
		return nil, resticFail(err, "restore")
	}
	if job.LockLeaked() {
		r.Warn(ctx, "%s", lockLeakWarning)
	}
	restored := filepath.Join(env.data, "backup")
	bentoDir := filepath.Join(restored, "bento")
	// restic recreates symlinks as stored. Every file Bento reads from the
	// bento/ tree must be a plain file reached through plain directories, or a
	// crafted snapshot could make the clone read host files as root.
	if err := rejectLinkedTree(bentoDir); err != nil {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "%v", err)
	}

	man, err := readManifest(bentoDir)
	if err != nil {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "%v", err)
	}
	rawSpec, err := os.ReadFile(filepath.Join(bentoDir, "app.json"))
	if err != nil {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "app.json: %v", err)
	}
	spec, err := parseAppSpec(rawSpec)
	if err != nil {
		return nil, Fail("snapshot-invalid", "Choose a snapshot taken by Bento's app backup.", "%v", err)
	}
	var secrets *ResticSecrets
	if man.Secrets {
		raw, err := os.ReadFile(filepath.Join(bentoDir, "secrets.json"))
		if err != nil {
			return nil, Fail("snapshot-invalid", "The snapshot claims to hold secrets but secrets.json is unreadable.",
				"secrets.json: %v", err)
		}
		s, err := parseResticSecrets(raw)
		if err != nil {
			return nil, Fail("snapshot-invalid", "Choose another snapshot.", "%v", err)
		}
		secrets = &s
	}
	// Secret values are redacted from every error from here on.
	redact := cloneSecretRedactor(secrets)
	defer func() { err = redactOpError(err, redact) }()

	// The download ran beside other operations. Planning, creating the app
	// and provisioning grants on shared data services need the stack alone.
	if err := r.Phase(ctx, "wait-exclusive"); err != nil {
		return nil, err
	}
	if err := r.Escalate(ctx); err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "plan"); err != nil {
		return nil, err
	}
	plan, err := c.planClone(ctx, cloneInput{Man: man, Spec: spec, Secrets: secrets, Slug: req.Slug, AppID: req.AppID,
		KeepUsername: req.KeepUsername})
	if err != nil {
		return nil, err
	}
	if len(plan.Blockers) == 0 {
		more, err := c.checkCloneTargets(ctx, plan)
		if err != nil {
			return nil, err
		}
		plan.Blockers = append(plan.Blockers, more...)
	}
	if len(plan.Blockers) > 0 {
		return nil, Fail("clone-blocked", "Fix the listed problems (see the preview), then clone again.", "%s",
			strings.Join(plan.Blockers, "; "))
	}
	if err := c.checkDumpSpace(ctx, r, man, plan, bentoDir); err != nil {
		return nil, err
	}

	// Step 1-2: the new app and its bindings.
	if err := r.Phase(ctx, "create-app"); err != nil {
		return nil, err
	}
	app := plan.App
	var key domain.GitSource
	if plan.Git != nil {
		key = *plan.Git
		var kerr error
		key.PrivateKey, key.PublicKey, key.Fingerprint, kerr = NewDeployKey("bento-" + c.Stack.Name + "-" + app.Slug)
		if kerr != nil {
			return nil, kerr
		}
		key.KeyCreatedAt = time.Now().UTC()
	}
	rng := domain.DefaultUIDRange()
	if _, err := store.GetSetting(ctx, c.Store.DB(), "uid_range", &rng); err != nil {
		return nil, fmt.Errorf("read uid range: %w", err)
	}
	err = c.Store.Tx(ctx, func(q store.Q) error {
		if _, err := store.GetApp(ctx, q, app.Slug); err == nil {
			return fmt.Errorf("%w: app %q already exists", store.ErrConflict, app.Slug)
		}
		uid, err := store.AllocateUID(ctx, q, rng, c.HostIDs, app.ID, app.Slug)
		if err != nil {
			return err
		}
		app.UID, app.GID = uid, uid
		if err := store.InsertApp(ctx, q, app); err != nil {
			return err
		}
		for _, b := range app.Bindings {
			if err := store.InsertBinding(ctx, q, b); err != nil {
				return err
			}
			for _, name := range b.Databases {
				if err := store.AddBindingDatabase(ctx, q, b.ID, name); err != nil {
					return err
				}
			}
		}
		if plan.Git != nil {
			return store.PutGitSource(ctx, q, app.ID, key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// From here on a failure removes what this operation created.
	defer func() {
		if err != nil {
			r.Warn(ctx, "clone failed; removing the new app %s", app.Slug)
			if c.rollbackClone(context.WithoutCancel(ctx), r.Uncancellable(), app, r.Op.ID) {
				err = addGuidance(err, "The partly created app "+app.Slug+" was removed.")
			} else {
				err = addGuidance(err, "Removing the partly created app "+app.Slug+" was incomplete; see the warnings.")
			}
		}
	}()

	// Step 3: provisioning (home, SQLite dirs, users and grants, Redis ACL).
	if app, err = store.GetApp(ctx, c.Store.DB(), app.ID); err != nil {
		return nil, err
	}
	if err = c.provision(ctx, r, app); err != nil {
		return nil, err
	}
	if app, err = store.GetApp(ctx, c.Store.DB(), app.ID); err != nil {
		return nil, err
	}

	// Step 5: databases.
	deps := c.BackupDeps(nil)
	for _, d := range man.Dumps {
		b, target, rerr := plan.resolveDump(man, d)
		if rerr != nil {
			return nil, Fail("database-mismatch", "The snapshot is inconsistent; choose another snapshot.", "%v", rerr)
		}
		if err = r.Phase(ctx, "restore "+target); err != nil {
			return nil, err
		}
		dump, perr := platform.ContainedPath(bentoDir, filepath.FromSlash(d.File))
		if perr == nil {
			perr = platform.NoSymlinkBetween(bentoDir, dump)
		}
		if perr != nil {
			return nil, perr
		}
		if d.Engine == domain.EngineSQLite {
			err = deps.RestoreSQLite(app, b, dump)
		} else {
			err = deps.RestoreRelational(ctx, app, b, target, dump)
		}
		if err != nil {
			return nil, Fail("restore-failed", "Try again.", "%s: %v", target, err)
		}
		r.Info(ctx, "restored %s into %s", d.Database, target)
	}

	// Step 6: home.
	if err = r.Phase(ctx, "restore-files"); err != nil {
		return nil, err
	}
	if err = c.installRestoredHome(app, filepath.Join(restored, "home"), man.Paths, r.Op.ID); err != nil {
		return nil, Fail("restore-files-failed", "Try again.", "%v", err)
	}
	if err = c.ensureHome(app); err != nil {
		return nil, err
	}

	// Step 7: scheduler database and other SQLite files of the home.
	if err = r.Phase(ctx, "restore-scheduler"); err != nil {
		return nil, err
	}
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	c.dropMinicronLogs(app)
	for _, rel := range man.HomeSQLite {
		if strings.HasPrefix(filepath.Base(rel), "minicron-logs.db") {
			continue
		}
		if err = c.restoreHomeSQLite(app, bentoDir, rel, owner); err != nil {
			return nil, Fail("restore-files-failed", "Try again.", "%s: %v", rel, err)
		}
	}

	// Step 9: the image was prepared by provisioning; the clone stays stopped.
	res := c.cloneResult(req, man, plan, app, key.PublicKey)
	switch req.BackupAfter {
	case CloneBackupNewRepo:
		if note := c.configureCloneBackup(ctx, v, man.Slug, app); note != "" {
			res.Checklist = append(res.Checklist, note)
		}
	case CloneBackupSameRepo:
		res.Checklist = append(res.Checklist, c.adoptCloneRepository(ctx, repo, app))
	}
	r.Info(ctx, "Cloned from %s @ %s (snapshot %s); %s is stopped", man.Slug,
		man.CreatedAt.UTC().Format(time.RFC3339), short(req.Snapshot), app.Slug)
	return res, nil
}

// configureCloneBackup saves app backup settings (no schedule, no secrets)
// for the clone, pointing at a new repository path derived from the source's.
// The operator initializes it on the Backup tab.
func (c *Controller) configureCloneBackup(
	ctx context.Context,
	srcView ResticView,
	srcSlug string,
	app domain.App,
) string {
	repo := srcView.Settings.Repository
	if p, ok := strings.CutSuffix(repo, "/"+srcSlug); ok && p != "" {
		repo = p + "/" + app.Slug
	} else {
		repo += "-" + app.Slug
	}
	if _, err := backup.ValidateRemote(repo); err != nil {
		return "Set up app backup for the new app: the derived repository path " + repo + " is not valid."
	}
	s := domain.DefaultResticSettings()
	s.Repository = repo
	s.Retention = srcView.Settings.Retention
	s.Paths = srcView.Settings.Paths
	s.Excludes = srcView.Settings.Excludes
	s.DefaultExcludes = srcView.Settings.DefaultExcludes
	s.SQLitePaths = srcView.Settings.SQLitePaths
	if err := store.PutSetting(ctx, c.Store.DB(), resticSettingsKey(app.ID), s); err != nil {
		return "Set up app backup for the new app: " + err.Error()
	}
	return "App backup settings for the new app point at " + repo + ". Initialize that repository on its Backup tab."
}

func (c *Controller) cloneResult(
	req ResticCloneRequest,
	man ResticManifest,
	p *clonePlan,
	app domain.App,
	deployKey string,
) ResticCloneResult {
	res := ResticCloneResult{
		AppID: app.ID, Slug: app.Slug, SourceSlug: man.Slug, SourceAppID: man.AppID, Snapshot: req.Snapshot,
		SnapshotTime: man.CreatedAt, Stopped: true, HomePath: app.ContainerHome(), Databases: nonNilSlice(p.Databases),
		SQLite: nonNilSlice(p.SQLite), EmptyEnv: p.EmptyEnv, Minicron: p.Minicron, DeployKey: deployKey,
		Domains: nonNilSlice(p.Domains),
	}
	add := func(format string, args ...any) { res.Checklist = append(res.Checklist, fmt.Sprintf(format, args...)) }
	add("%s is stopped and not published. Check it, then press Start.", app.Slug)
	add("The home inside the container is still %s, so paths under it keep working.", res.HomePath)
	seenUser := map[string]bool{}
	for _, d := range p.Databases {
		add("Database %s is now %s on %s: change hard-coded database names.", d.Source, d.Target, d.Service)
		if seenUser[d.Service+d.Username] {
			continue
		}
		seenUser[d.Service+d.Username] = true
		if !d.UsernameKept {
			add("%s uses database user %s (the source's was %s): change a hard-coded DB user.", d.Service, d.Username,
				orDash(d.SourceUsername))
		}
		if !d.PasswordKept {
			add("%s uses a new database password: change a hard-coded DB password (DB_* and BENTO_DB_* env are already new).", d.Service)
		}
	}
	for _, s := range p.SQLite {
		add("SQLite database %s is now %s: change hard-coded paths.", s.Source, s.Target)
	}
	if len(p.EmptyEnv) > 0 {
		add("Set these environment values (not in the backup): %s.", strings.Join(p.EmptyEnv, ", "))
	}
	if p.Minicron {
		add("Scheduler jobs and workers were restored; their run history starts empty.")
	}
	if deployKey != "" {
		add("Add this deploy key to the git repository and update any webhook: %s", strings.TrimSpace(deployKey))
	}
	if len(p.Domains) > 0 {
		names := make([]string, 0, len(p.Domains))
		for _, d := range p.Domains {
			if d.InUse {
				d.Name += " (in use here)"
			}
			names = append(names, d.Name)
		}
		add("After checking the clone, point these Ingress hosts at it: %s.", strings.Join(names, ", "))
	}
	add("Redis keys are not backed up; the clone uses the prefix %s.", app.Redis.Prefix)
	res.Checklist = append(res.Checklist, p.Notes...)
	return res
}

func orDash(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ---- files ----

// rejectSpecialFiles refuses a restored tree that holds device nodes, pipes
// or sockets. Symlinks are allowed (and never followed).
func rejectSpecialFiles(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&(os.ModeDevice|os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeIrregular) != 0 {
			rel, _ := filepath.Rel(root, path)
			return fmt.Errorf("the snapshot holds a special file (%s)", rel)
		}
		return nil
	})
}

// rejectLinkedTree refuses a restored Bento metadata tree (and the
// directories above it in the staging area) unless it holds only directories
// and regular files.
func rejectLinkedTree(root string) error {
	for _, dir := range []string{filepath.Dir(root), root} {
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("snapshot holds no Bento metadata: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("the snapshot's %s is not a directory", filepath.Base(dir))
		}
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, path)
			return fmt.Errorf("the snapshot's Bento metadata holds a link or special file (%s)", rel)
		}
		return nil
	})
}

// installRestoredHome puts the restored home tree in place of the new app's
// freshly provisioned home. Restored files are re-owned to the new UID
// (symlinks re-owned, never followed); the home keeps the new identity
// sidecar. When the snapshot did not cover the whole home only the listed
// paths are moved in.
func (c *Controller) installRestoredHome(app domain.App, restoredHome string, paths []string, opID string) error {
	if err := c.verifyHomeIdentity(app); err != nil {
		return err
	}
	info, err := os.Lstat(restoredHome)
	if err != nil || !info.IsDir() {
		return errors.New("snapshot holds no home directory")
	}
	if err := rejectSpecialFiles(restoredHome); err != nil {
		return err
	}
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	if _, err := platform.ChownTree(restoredHome, owner, false, 0); err != nil {
		return fmt.Errorf("re-own restored files: %w", err)
	}
	home := c.Layout.AppHome(app.Slug)
	// Older snapshots may list nested paths; each tree is moved once.
	paths = domain.TopmostRels(paths)
	if slices.Contains(paths, ".") {
		homeOwner, mode, err := platform.StatOwner(home)
		if err != nil {
			return err
		}
		sidecar, err := os.ReadFile(c.Layout.HomeSidecar(app.Slug))
		if err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(restoredHome, domain.HomeSidecarName))
		if err := platform.AtomicWrite(filepath.Join(restoredHome, domain.HomeSidecarName), sidecar, 0o444,
			platform.RootOwner); err != nil {
			return err
		}
		if err := os.Chmod(restoredHome, mode.Perm()); err != nil {
			return err
		}
		if err := os.Lchown(restoredHome, homeOwner.UID, homeOwner.GID); err != nil {
			return err
		}
		aside := filepath.Join(c.Layout.HomesDir(), ".clone-"+opID)
		_ = os.RemoveAll(aside)
		if err := renameNoCross(home, aside); err != nil {
			return err
		}
		if err := renameNoCross(restoredHome, home); err != nil {
			_ = os.Rename(aside, home)
			return err
		}
		return os.RemoveAll(aside)
	}
	for _, p := range paths {
		src, err := platform.ContainedPath(restoredHome, filepath.FromSlash(p))
		if err != nil {
			return err
		}
		// rename follows symlinked parents: a restored "x -> /etc" with the path
		// "x/passwd" would move a host file. The last component may be a link.
		if err := platform.NoSymlinkBetween(restoredHome, filepath.Dir(src)); err != nil {
			return fmt.Errorf("restored path %s: %w", p, err)
		}
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		dst, err := platform.ContainedPath(home, filepath.FromSlash(p))
		if err != nil {
			return err
		}
		if err := platform.NoSymlinkBetween(home, dst); err != nil {
			return err
		}
		if _, err := os.Lstat(dst); err == nil {
			// Only an empty directory made by provisioning may be replaced.
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("%s already exists in the new home: %w", p, err)
			}
		}
		if err := c.ensureHomeParents(home, filepath.Dir(dst), owner); err != nil {
			return err
		}
		if err := renameNoCross(src, dst); err != nil {
			return err
		}
	}
	return nil
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
		return fmt.Errorf("staging and homes are on different filesystems; a clone needs them on one: %w", err)
	}
	return err
}

// dropMinicronLogs removes minicrond's log database, journals and socket
// from a restored home; minicrond creates a fresh log database itself.
func (c *Controller) dropMinicronLogs(app domain.App) {
	home := c.Layout.AppHome(app.Slug)
	dir := filepath.Join(home, domain.MinicronDataDir)
	if platform.NoSymlinkBetween(home, dir) != nil {
		return
	}
	for _, name := range []string{"minicron-logs.db", "minicron-logs.db-wal", "minicron-logs.db-shm", "minicron.sock",
		"minicron.db-wal", "minicron.db-shm", "minicron.db-journal"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// restoreHomeSQLite installs a .backup copy at its home path (0600, app
// owner), discarding any journals so they are not replayed against it.
func (c *Controller) restoreHomeSQLite(app domain.App, bentoDir, rel string, owner platform.Owner) error {
	srcDir := filepath.Join(bentoDir, "home-sqlite")
	src, err := platform.ContainedPath(srcDir, filepath.FromSlash(rel))
	if err != nil {
		return err
	}
	if err := platform.NoSymlinkBetween(bentoDir, src); err != nil {
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

// ---- rollback ----

// rollbackClone removes everything a failed or interrupted clone created: the
// databases and users (created by this operation, which first verified that
// none existed), the SQLite directories, the home, generated config and the
// app row. Its UID is marked burned and never reused. Every destructive step
// first checks that the resource belongs to this app. Failures are logged and
// the remaining steps still run.
// opID is the clone operation, whose aside home may be left behind.
func (c *Controller) rollbackClone(ctx context.Context, r *Run, app domain.App, opID string) (clean bool) {
	clean = true
	warn := func(format string, args ...any) {
		clean = false
		r.Warn(ctx, format, args...)
	}
	if cur, err := store.GetApp(ctx, c.Store.DB(), app.ID); err == nil {
		app = cur
	}
	if cs, err := c.Engine.List(ctx, map[string]string{runtime.LabelAppID: app.ID, runtime.LabelStackID: c.Stack.ID}); err == nil {
		for _, t := range cs {
			switch {
			case c.Names.OwnedBy(t.Labels, runtime.RoleTool, app.ID), c.Names.OwnedBy(t.Labels, runtime.RoleRuntime, app.ID):
			case c.Names.OwnedBy(t.Labels, runtime.RoleBackup, app.ID) && t.State != "running":
			default:
				continue
			}
			if err := c.Engine.Remove(ctx, t.ID); err != nil {
				warn("rollback: remove container: %v", err)
			}
		}
	}
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite {
			dir := c.Layout.SQLiteFileDir(b.SQLiteFileID)
			if o, mode, err := platform.StatOwner(dir); err == nil && mode.IsDir() && o.UID == app.UID &&
				platform.NoSymlinkBetween(c.Layout.SQLiteDir(), dir) == nil {
				if err := os.RemoveAll(dir); err != nil {
					warn("rollback: remove %s: %v", dir, err)
				}
			}
			continue
		}
		svc, err := store.GetService(ctx, c.Store.DB(), b.Service)
		if err == nil {
			var id string
			if id, err = c.serviceContainer(ctx, svc.DataService); err == nil {
				err = c.Data().DropBinding(ctx, svc.DataService, id, b.Username, b.Databases)
			}
		}
		if err != nil {
			warn("rollback: could not drop %s databases %v and user %s: %v", b.Service, b.Databases, b.Username, err)
		}
	}
	home := c.Layout.AppHome(app.Slug)
	if raw, err := os.ReadFile(c.Layout.HomeSidecar(app.Slug)); err == nil && platform.NoSymlinkBetween(c.Layout.HomesDir(), home) == nil {
		var sc HomeSidecar
		if json.Unmarshal(raw, &sc) == nil && sc.AppID == app.ID && sc.StackID == c.Stack.ID {
			if err := os.RemoveAll(home); err != nil {
				warn("rollback: remove home: %v", err)
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(c.Layout.HomesDir(), ".clone-"+opID))
	err := c.Store.Tx(ctx, func(q store.Q) error {
		if err := store.DeleteGitSource(ctx, q, app.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := store.DeleteApp(ctx, q, app.ID); err != nil {
			return err
		}
		return store.SetLedgerState(ctx, q, app.UID, "burned")
	})
	if err != nil {
		warn("rollback: remove app record: %v", err)
	}
	if err := os.RemoveAll(c.Layout.AppDir(app.ID)); err != nil {
		warn("rollback: remove generated config: %v", err)
	}
	if err := c.syncRedisACL(ctx); err != nil {
		warn("rollback: redis ACL not refreshed: %v", err)
	}
	return clean
}
