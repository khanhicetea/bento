package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/transfer"
)

type ImportOptions struct {
	Root string
	From string
	Name string // optional new stack name (clone)
	// NewUIDRange moves future allocations to a range disjoint from the
	// imported ledger (required for same-host clones to avoid reusing the
	// source stack's future UIDs). Existing identities are never renumbered.
	NewUIDRange *domain.UIDRange
}

// Import stages an export into an empty root. The imported stack starts with
// every app stopped and unpublished, no tunnel, no edge, no backup schedule,
// and no replayable pending operations. Failure removes only resources this
// import created.
func Import(ctx context.Context, engine docker.Engine, log *slog.Logger, opts ImportOptions) (err error) {
	layout, err := platform.NewLayout(opts.Root)
	if err != nil {
		return err
	}
	from, m, name, err := readExport(ctx, engine, opts)
	if err != nil {
		return err
	}
	empty, err := platform.DirIsEmptyOrMissing(layout.Root)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("refusing to import: %s is not empty", layout.Root)
	}
	var createdRoot bool
	if _, statErr := os.Lstat(layout.Root); errors.Is(statErr, fs.ErrNotExist) {
		createdRoot = true
	}
	var createdVolumes []string
	// Registered first so it runs last, after the store and lock are released.
	defer func() {
		if err != nil {
			cleanupFailedImport(context.WithoutCancel(ctx), engine, log, layout.Root, createdRoot, createdVolumes)
		}
	}()
	if err = extractRoot(layout, from, m); err != nil {
		return err
	}
	lock, err := platform.TryLock(layout.ControllerLock())
	if err != nil {
		return err
	}
	defer lock.Release()
	if err = platform.CopyFile(
		filepath.Join(from, m.StateFile),
		layout.Database(),
		0o600,
		platform.RootOwner,
	); err != nil {
		return err
	}
	s, err := store.Open(layout.Database())
	if err != nil {
		return err
	}
	defer s.Close()
	newID := platform.NewStackID()
	names := runtime.Names{StackID: newID, StackName: name}
	if err = s.Tx(ctx, func(q store.Q) error {
		return rewriteImportedState(ctx, q, m, names, opts.NewUIDRange)
	}); err != nil {
		return err
	}
	if err = adoptHomes(ctx, s.DB(), layout, newID); err != nil {
		return err
	}
	// On error createdVolumes still lists what was created, for cleanup.
	createdVolumes, err = restoreServiceVolumes(ctx, engine, log, names, from, m.Services)
	return err
}

// readExport resolves the export directory, validates its manifest against
// this Bento and Docker host, and returns the stack name to import as.
func readExport(
	ctx context.Context,
	engine docker.Engine,
	opts ImportOptions,
) (from string, m transfer.Manifest, name string, err error) {
	if from, err = filepath.Abs(opts.From); err != nil {
		return "", m, "", err
	}
	if m, err = transfer.ReadManifest(from); err != nil {
		return "", m, "", fmt.Errorf("read manifest: %w", err)
	}
	if err := m.Validate(store.SchemaVersion); err != nil {
		return "", m, "", err
	}
	if v, verr := engine.Version(ctx); verr == nil && m.Arch != "" && v.Arch != m.Arch {
		return "", m, "", fmt.Errorf("export was taken on %s; this Docker host is %s", m.Arch, v.Arch)
	}
	name = m.StackName
	if opts.Name != "" {
		name = opts.Name
	}
	if err := runtime.ValidateStackName(name); err != nil {
		return "", m, "", err
	}
	return from, m, name, nil
}

// cleanupFailedImport removes what a failed import created: its volumes and
// the root (or, for a root that already existed, the root's contents).
// Cleanup is best effort; the import error is what the caller reports, and
// leftovers are logged so the operator can remove them.
func cleanupFailedImport(
	ctx context.Context,
	engine docker.Engine,
	log *slog.Logger,
	root string,
	createdRoot bool,
	createdVolumes []string,
) {
	for _, v := range createdVolumes {
		log.Warn("import failed; removing volume created by this import", "volume", v)
		if rerr := removeVolume(ctx, engine, v); rerr != nil {
			log.Warn("remove imported volume", "volume", v, "err", rerr)
		}
	}
	if createdRoot {
		if rerr := os.RemoveAll(root); rerr != nil {
			log.Warn("remove partially imported stack root", "root", root, "err", rerr)
		}
		return
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if rerr := os.RemoveAll(filepath.Join(root, e.Name())); rerr != nil {
			log.Warn("remove partially imported entry", "path", filepath.Join(root, e.Name()), "err", rerr)
		}
	}
}

// extractRoot creates the root, extracts the root archive into it, and
// ensures the skeleton directories.
func extractRoot(layout platform.Layout, from string, m transfer.Manifest) error {
	if err := platform.EnsureDir(layout.Root, 0o711, platform.RootOwner); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(from, m.RootArchive))
	if err != nil {
		return err
	}
	err = transfer.ExtractRoot(f, layout.Root)
	_ = f.Close() // read-only; the extract result is what matters
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	for dir, mode := range layout.SkeletonDirs() {
		if err := platform.EnsureDir(dir, os.FileMode(mode), platform.RootOwner); err != nil {
			return err
		}
	}
	return nil
}

// rewriteImportedState gives the imported state its new identity and makes
// it inert: nothing archived as running, pending, or enabled is honored.
func rewriteImportedState(
	ctx context.Context,
	q store.Q,
	m transfer.Manifest,
	names runtime.Names,
	uidRange *domain.UIDRange,
) error {
	for _, kv := range [][2]string{
		{"stack_id", names.StackID},
		{"stack_name", names.StackName},
		{"imported_from", m.StackID},
	} {
		if err := store.SetMeta(ctx, q, kv[0], kv[1]); err != nil {
			return err
		}
	}
	stmts := []string{
		// Archived running intent is never honored automatically.
		"UPDATE apps SET desired_runtime='stopped', publication='unpublished'",
		// Pending/interrupted work in the archive is never replayed.
		"UPDATE operations SET state='cancelled', error_code='imported', error_message='not replayed after import' WHERE state IN ('queued','running')",
		"DELETE FROM sessions",
		// New networks are planned for this host.
		"DELETE FROM settings WHERE key IN ('network', 'backup_schedule_state')",
	}
	for _, st := range stmts {
		if _, err := q.ExecContext(ctx, st); err != nil {
			return err
		}
	}
	if err := disableSetting(ctx, q, "edge"); err != nil {
		return err
	}
	if err := store.PutSetting(ctx, q, "tunnel", map[string]any{"enabled": false, "tokenGeneration": 0}); err != nil {
		return err
	}
	if err := disableSetting(ctx, q, "backup_schedule"); err != nil {
		return err
	}
	for _, svc := range m.Services {
		if _, err := q.ExecContext(
			ctx,
			"UPDATE data_services SET volume=? WHERE name=?",
			names.ServiceVolume(svc.Name),
			svc.Name,
		); err != nil {
			return err
		}
	}
	if uidRange != nil {
		return moveUIDRange(ctx, q, *uidRange)
	}
	return nil
}

// disableSetting sets enabled=false in a JSON setting, if it exists, keeping
// every other field.
func disableSetting(ctx context.Context, q store.Q, key string) error {
	var v map[string]any
	if _, err := store.GetSetting(ctx, q, key, &v); err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	v["enabled"] = false
	return store.PutSetting(ctx, q, key, v)
}

// moveUIDRange moves future allocations to r, which must not overlap any UID
// in the imported ledger. The ledger is preserved; allocation resumes at the
// new range.
func moveUIDRange(ctx context.Context, q store.Q, r domain.UIDRange) error {
	if r.First < 1000 || r.Last <= r.First {
		return fmt.Errorf("invalid uid range %d-%d", r.First, r.Last)
	}
	ledger, err := store.ListLedger(ctx, q)
	if err != nil {
		return err
	}
	for _, e := range ledger {
		if e.UID >= r.First && e.UID <= r.Last {
			return fmt.Errorf("new uid range %d-%d overlaps allocated uid %d from the imported ledger", r.First, r.Last, e.UID)
		}
	}
	if err := store.PutSetting(ctx, q, "uid_range", r); err != nil {
		return err
	}
	return store.SetMeta(ctx, q, "uid_highwater", fmt.Sprint(r.First-1))
}

// adoptHomes is the explicit, validated adoption of the imported homes by
// the new stack id: each sidecar must still name its app and UID.
func adoptHomes(ctx context.Context, q store.Q, layout platform.Layout, newID string) error {
	apps, err := store.ListApps(ctx, q)
	if err != nil {
		return err
	}
	for _, a := range apps {
		path := layout.HomeSidecar(a.Slug)
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		var sc operations.HomeSidecar
		if json.Unmarshal(raw, &sc) != nil || sc.AppID != a.ID || sc.UID != a.UID {
			return fmt.Errorf("home of %s does not match its recorded identity; refusing to adopt it", a.Slug)
		}
		sc.StackID = newID
		out, _ := json.MarshalIndent(sc, "", "  ")
		if err := platform.AtomicWrite(path, append(out, '\n'), 0o444, platform.RootOwner); err != nil {
			return err
		}
	}
	return nil
}

// restoreServiceVolumes creates each service's volume under the new stack's
// name and restores its archive. It returns the volumes it created, also on
// error, so a failed import removes exactly those.
func restoreServiceVolumes(
	ctx context.Context,
	engine docker.Engine,
	log *slog.Logger,
	names runtime.Names,
	from string,
	services []transfer.ServiceEntry,
) (created []string, err error) {
	for _, svc := range services {
		vol := names.ServiceVolume(svc.Name)
		if existing, verr := engine.VolumeInspect(ctx, vol); verr != nil {
			return created, verr
		} else if existing != nil {
			return created, fmt.Errorf("volume %s already exists; choose a different stack name", vol)
		}
		if _, err := engine.VolumeCreate(
			ctx,
			vol,
			names.Labels(runtime.RoleVolume, map[string]string{runtime.LabelService: svc.Name}),
		); err != nil {
			return created, err
		}
		created = append(created, vol)
		if err := restoreVolume(ctx, engine, names, svc.Image, vol, from, svc.VolumeFile); err != nil {
			return created, err
		}
		log.Info("restored volume", "service", svc.Name, "volume", vol)
	}
	return created, nil
}
