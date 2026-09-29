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
	from, err := filepath.Abs(opts.From)
	if err != nil {
		return err
	}
	m, err := transfer.ReadManifest(from)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if err := m.Validate(store.SchemaVersion); err != nil {
		return err
	}
	if v, verr := engine.Version(ctx); verr == nil && m.Arch != "" && v.Arch != m.Arch {
		return fmt.Errorf("export was taken on %s; this Docker host is %s", m.Arch, v.Arch)
	}
	name := m.StackName
	if opts.Name != "" {
		name = opts.Name
	}
	if err := runtime.ValidateStackName(name); err != nil {
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
	defer func() {
		if err == nil {
			return
		}
		cctx := context.WithoutCancel(ctx)
		// Cleanup is best effort; the import error is what the caller reports,
		// and leftovers are logged so the operator can remove them.
		for _, v := range createdVolumes {
			log.Warn("import failed; removing volume created by this import", "volume", v)
			if rerr := removeVolume(cctx, engine, v); rerr != nil {
				log.Warn("remove imported volume", "volume", v, "err", rerr)
			}
		}
		if createdRoot {
			if rerr := os.RemoveAll(layout.Root); rerr != nil {
				log.Warn("remove partially imported stack root", "root", layout.Root, "err", rerr)
			}
		} else {
			entries, _ := os.ReadDir(layout.Root)
			for _, e := range entries {
				if rerr := os.RemoveAll(filepath.Join(layout.Root, e.Name())); rerr != nil {
					log.Warn("remove partially imported entry", "path", filepath.Join(layout.Root, e.Name()), "err", rerr)
				}
			}
		}
	}()
	if err = platform.EnsureDir(layout.Root, 0o711, platform.RootOwner); err != nil {
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
		if err = platform.EnsureDir(dir, os.FileMode(mode), platform.RootOwner); err != nil {
			return err
		}
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
	err = s.Tx(ctx, func(q store.Q) error {
		if err := store.SetMeta(ctx, q, "stack_id", newID); err != nil {
			return err
		}
		if err := store.SetMeta(ctx, q, "stack_name", name); err != nil {
			return err
		}
		if err := store.SetMeta(ctx, q, "imported_from", m.StackID); err != nil {
			return err
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
		var edge map[string]any
		if _, err := store.GetSetting(ctx, q, "edge", &edge); err != nil {
			return err
		}
		if edge != nil {
			edge["enabled"] = false
			if err := store.PutSetting(ctx, q, "edge", edge); err != nil {
				return err
			}
		}
		if err := store.PutSetting(ctx, q, "tunnel", map[string]any{"enabled": false, "tokenGeneration": 0}); err != nil {
			return err
		}
		var sched map[string]any
		if _, err := store.GetSetting(ctx, q, "backup_schedule", &sched); err != nil {
			return err
		}
		if sched != nil {
			sched["enabled"] = false
			if err := store.PutSetting(ctx, q, "backup_schedule", sched); err != nil {
				return err
			}
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
		if r := opts.NewUIDRange; r != nil {
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
			// The ledger is preserved; allocation resumes at the new range.
			if err := store.SetMeta(ctx, q, "uid_highwater", fmt.Sprint(r.First-1)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Explicit, validated adoption of the imported homes by the new stack id.
	apps, err := store.ListApps(ctx, s.DB())
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
		if err = platform.AtomicWrite(path, append(out, '\n'), 0o444, platform.RootOwner); err != nil {
			return err
		}
	}
	for _, svc := range m.Services {
		vol := names.ServiceVolume(svc.Name)
		if existing, verr := engine.VolumeInspect(ctx, vol); verr != nil {
			return verr
		} else if existing != nil {
			return fmt.Errorf("volume %s already exists; choose a different stack name", vol)
		}
		if _, err = engine.VolumeCreate(
			ctx,
			vol,
			names.Labels(runtime.RoleVolume, map[string]string{runtime.LabelService: svc.Name}),
		); err != nil {
			return err
		}
		createdVolumes = append(createdVolumes, vol)
		if err = restoreVolume(ctx, engine, names, svc.Image, vol, from, svc.VolumeFile); err != nil {
			return err
		}
		log.Info("restored volume", "service", svc.Name, "volume", vol)
	}
	return nil
}
