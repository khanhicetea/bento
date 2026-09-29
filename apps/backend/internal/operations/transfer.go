package operations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/transfer"
)

// RootSkip lists stack-root entries excluded from the root archive: the live
// database (captured separately with VACUUM INTO), runtime coordination,
// caches, staging, and local backup artifacts.
var RootSkip = map[string]bool{
	"bento.db": true, "bento.db-wal": true, "bento.db-shm": true, "run": true, "locks": true,
	"cache": true, "staging": true, "backups": true,
}

// SubmitExport requires confirm "export" because it briefly stops running
// apps and data services to take consistent snapshots.
func (c *Controller) SubmitExport(ctx context.Context, dest, confirm, idem string) (store.Operation, error) {
	if confirm != "export" {
		return store.Operation{}, fmt.Errorf(
			"%w: type exactly \"export\"; running apps and data services are stopped briefly for a consistent snapshot",
			ErrConfirmation,
		)
	}
	if err := validateExportDest(c.Layout.Root, dest); err != nil {
		return store.Operation{}, domain.ValidationErrors{{Field: "destination", Message: err.Error()}}
	}
	op, _, err := c.Submit(
		ctx,
		Submission{Kind: KindStackExport, TargetKind: "stack", TargetID: "stack", IdempotencyKey: idem,
			Request: map[string]string{"destination": dest}},
	)
	return op, err
}

func validateExportDest(root, dest string) error {
	if !filepath.IsAbs(dest) || filepath.Clean(dest) != dest {
		return errors.New("must be a clean absolute path")
	}
	if dest == root || strings.HasPrefix(dest, root+"/") || strings.HasPrefix(root, dest+"/") {
		return errors.New("must be outside the stack root")
	}
	empty, err := platform.DirIsEmptyOrMissing(dest)
	if err != nil {
		return err
	}
	if !empty {
		return errors.New("must be empty or not exist")
	}
	return nil
}

// volumeJob runs tar in a scoped job container using the service's own image.
func (c *Controller) volumeJob(
	ctx context.Context,
	image, volume, dir string,
	readOnlyVolume bool,
	cmd []string,
) error {
	opID := "vol-" + platform.RandomHex(5)
	spec := docker.ContainerSpec{
		Name: c.Names.BackupContainer(opID),
		Config: &container.Config{Image: image, Entrypoint: []string{"tar"}, Cmd: cmd, User: "0:0",
			Labels: c.Names.Labels(runtime.RoleBackup, map[string]string{runtime.LabelOperation: opID})},
		HostConfig: &container.HostConfig{
			NetworkMode: "none",
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: volume, Target: "/v", ReadOnly: readOnlyVolume},
				{Type: mount.TypeBind, Source: dir, Target: "/x", ReadOnly: !readOnlyVolume},
			},
		},
	}
	id, err := c.Engine.Create(ctx, spec)
	if err != nil {
		return err
	}
	defer c.Engine.Remove(context.WithoutCancel(ctx), id)
	if err := c.Engine.Start(ctx, id); err != nil {
		return err
	}
	code, err := c.Engine.Wait(ctx, id)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("volume archive job for %s exited %d: %s", volume, code, c.logTail(ctx, id, 10))
	}
	return nil
}

// privatize makes every export file owner-only.
func privatize(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Chmod(filepath.Join(dir, e.Name()), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) handleStackExport(ctx context.Context, r *Run) (res any, err error) {
	var req struct {
		Destination string `json:"destination"`
	}
	if err := r.Decode(&req); err != nil {
		return nil, err
	}
	dest := req.Destination
	if err := validateExportDest(c.Layout.Root, dest); err != nil {
		return nil, Fail("destination", "Choose an empty directory outside the stack root.", "%v", err)
	}
	if err := platform.EnsureDir(dest, 0o700, platform.RootOwner); err != nil {
		return nil, err
	}
	db := c.Store.DB()
	apps, err := store.ListApps(ctx, db)
	if err != nil {
		return nil, err
	}
	services, err := store.ListServices(ctx, db)
	if err != nil {
		return nil, err
	}
	// Quiesce writers: stop running apps (scheduler/SQLite/minicrond writers)
	// and volume-backed services; restore exactly the prior running set.
	if err := r.Phase(ctx, "quiesce"); err != nil {
		return nil, err
	}
	var stoppedApps []domain.App
	var stoppedServices []store.ServiceRow
	defer func() {
		// A service that fails to restart fails an otherwise successful export.
		if rerr := c.resumeAfterExport(ctx, r, stoppedApps, stoppedServices); err == nil {
			err = rerr
		}
	}()
	// On error the lists hold what was stopped so far, so resume restarts it.
	stoppedApps, stoppedServices, err = c.quiesceForExport(ctx, apps, services)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "snapshot-state"); err != nil {
		return nil, err
	}
	if err := c.Store.SnapshotTo(ctx, filepath.Join(dest, "state.db")); err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "archive-root"); err != nil {
		return nil, err
	}
	if err := c.archiveRootTo(filepath.Join(dest, "stack.tar.zst")); err != nil {
		return nil, err
	}
	m := c.exportManifest(ctx)
	if m.Services, err = c.archiveVolumes(ctx, r, dest, services); err != nil {
		return nil, err
	}
	if err := transfer.WriteManifest(dest, m); err != nil {
		return nil, err
	}
	if err := privatize(dest); err != nil {
		return nil, err
	}
	r.Info(ctx, "export written to %s (sensitive: contains credentials and data)", dest)
	return map[string]any{"destination": dest, "services": len(m.Services), "apps": len(apps)}, nil
}

// quiesceForExport stops every running app and data service. It returns what
// it stopped, including on error, so the caller can restart exactly that set.
func (c *Controller) quiesceForExport(
	ctx context.Context,
	apps []domain.App,
	services []store.ServiceRow,
) (stoppedApps []domain.App, stoppedServices []store.ServiceRow, err error) {
	for _, a := range apps {
		obs, err := c.observe(ctx, a)
		if err != nil {
			return stoppedApps, stoppedServices, err
		}
		if obs.Exists && obs.Running {
			if err := c.Engine.Stop(ctx, obs.ContainerID, runtime.StopTimeout); err != nil {
				return stoppedApps, stoppedServices, err
			}
			stoppedApps = append(stoppedApps, a)
		}
	}
	for _, s := range services {
		ins, err := c.Engine.Inspect(ctx, c.Names.ServiceContainer(s.Name))
		if err != nil {
			return stoppedApps, stoppedServices, err
		}
		if ins != nil && ins.State != nil && ins.State.Running {
			if err := c.Engine.Stop(ctx, ins.ID, 60*time.Second); err != nil {
				return stoppedApps, stoppedServices, err
			}
			stoppedServices = append(stoppedServices, s)
		}
	}
	return stoppedApps, stoppedServices, nil
}

// resumeAfterExport restarts what quiesceForExport stopped. It must run to
// completion even when the export was cancelled; otherwise every app it
// stopped stays down. It returns a resume-failed error for the first service
// that did not restart; an app that does not restart is only a warning.
func (c *Controller) resumeAfterExport(
	ctx context.Context,
	r *Run,
	apps []domain.App,
	services []store.ServiceRow,
) error {
	rctx := context.WithoutCancel(ctx)
	r = r.Uncancellable()
	_ = r.Phase(rctx, "resume")
	var failed error
	for _, s := range services {
		if _, e := c.ensureService(rctx, r, s, false); e != nil {
			r.Warn(rctx, "service %s did not restart: %v", s.Name, e)
			if failed == nil {
				failed = Fail(
					"resume-failed",
					"Start the service manually through reconciliation.",
					"service %s did not restart: %v",
					s.Name,
					e,
				)
			}
		}
	}
	for _, a := range apps {
		if _, gen, e := c.ensureInstance(rctx, r, a, false); e != nil {
			r.Warn(rctx, "app %s did not restart: %v", a.Slug, e)
		} else if e := c.waitReady(rctx, r, a, gen); e != nil {
			r.Warn(rctx, "app %s not ready after export: %v", a.Slug, e)
		}
	}
	return failed
}

// archiveRootTo writes the stack root, minus RootSkip, to a new file at path.
func (c *Controller) archiveRootTo(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = transfer.ArchiveRoot(c.Layout.Root, f, RootSkip)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("close root archive: %w", cerr)
	}
	return err
}

// exportManifest describes this stack; the caller adds the service entries.
func (c *Controller) exportManifest(ctx context.Context) transfer.Manifest {
	m := transfer.Manifest{
		Format:        transfer.FormatName,
		Version:       transfer.FormatVersion,
		SchemaVersion: store.SchemaVersion,
		StackID:       c.Stack.ID,
		StackName:     c.Stack.Name,
		CreatedAt:     platform.FormatTime(time.Now()),
		StateFile:     "state.db",
		RootArchive:   "stack.tar.zst",
	}
	if v, err := c.Engine.Version(ctx); err == nil {
		m.Arch = v.Arch
	}
	return m
}

// archiveVolumes tars each service volume into dest with a job container of
// the service's own image, and returns the manifest entries.
func (c *Controller) archiveVolumes(
	ctx context.Context,
	r *Run,
	dest string,
	services []store.ServiceRow,
) ([]transfer.ServiceEntry, error) {
	var entries []transfer.ServiceEntry
	for _, s := range services {
		if err := r.Phase(ctx, "archive-volume "+s.Name); err != nil {
			return nil, err
		}
		if vol, err := c.Engine.VolumeInspect(ctx, s.Volume); err != nil || vol == nil {
			return nil, Fail("volume-missing", "Restore the service volume before exporting.", "volume %s is missing", s.Volume)
		}
		file := "volume-" + s.Name + ".tar"
		if err := c.volumeJob(
			ctx,
			s.Image,
			s.Volume,
			dest,
			true,
			[]string{"-C", "/v", "-cf", "/x/" + file, "."},
		); err != nil {
			return nil, err
		}
		entries = append(
			entries,
			transfer.ServiceEntry{Name: s.Name, Engine: string(s.Engine), Version: s.Version, Image: s.Image,
				VolumeFile: file, SourceVolume: s.Volume},
		)
	}
	return entries, nil
}
