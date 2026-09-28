package operations

import (
	"context"
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
		return store.Operation{}, fmt.Errorf("%w: type exactly \"export\"; running apps and data services are stopped briefly for a consistent snapshot", ErrConfirmation)
	}
	if err := validateExportDest(c.Layout.Root, dest); err != nil {
		return store.Operation{}, domain.ValidationErrors{{Field: "destination", Message: err.Error()}}
	}
	op, _, err := c.Submit(ctx, Submission{Kind: KindStackExport, TargetKind: "stack", TargetID: "stack", IdempotencyKey: idem,
		Request: map[string]string{"destination": dest}})
	return op, err
}

func validateExportDest(root, dest string) error {
	if !filepath.IsAbs(dest) || filepath.Clean(dest) != dest {
		return fmt.Errorf("must be a clean absolute path")
	}
	if dest == root || strings.HasPrefix(dest, root+"/") || strings.HasPrefix(root, dest+"/") {
		return fmt.Errorf("must be outside the stack root")
	}
	empty, err := platform.DirIsEmptyOrMissing(dest)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("must be empty or not exist")
	}
	return nil
}

// volumeJob runs tar in a scoped job container using the service's own image.
func (c *Controller) volumeJob(ctx context.Context, image, volume, dir string, readOnlyVolume bool, cmd []string) error {
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
		rctx := context.WithoutCancel(ctx)
		// Resume must run to completion even when the export was cancelled;
		// otherwise every app it stopped stays down.
		r := r.Uncancellable()
		_ = r.Phase(rctx, "resume")
		for _, s := range stoppedServices {
			if _, e := c.ensureService(rctx, r, s, false); e != nil {
				r.Warn(rctx, "service %s did not restart: %v", s.Name, e)
				if err == nil {
					err = Fail("resume-failed", "Start the service manually through reconciliation.", "service %s did not restart: %v", s.Name, e)
				}
			}
		}
		for _, a := range stoppedApps {
			if _, gen, e := c.ensureInstance(rctx, r, a, false); e != nil {
				r.Warn(rctx, "app %s did not restart: %v", a.Slug, e)
			} else if e := c.waitReady(rctx, r, a, gen); e != nil {
				r.Warn(rctx, "app %s not ready after export: %v", a.Slug, e)
			}
		}
	}()
	for _, a := range apps {
		obs, err := c.observe(ctx, a)
		if err != nil {
			return nil, err
		}
		if obs.Exists && obs.Running {
			if err := c.Engine.Stop(ctx, obs.ContainerID, runtime.StopTimeout); err != nil {
				return nil, err
			}
			stoppedApps = append(stoppedApps, a)
		}
	}
	for _, s := range services {
		ins, err := c.Engine.Inspect(ctx, c.Names.ServiceContainer(s.Name))
		if err != nil {
			return nil, err
		}
		if ins != nil && ins.State != nil && ins.State.Running {
			if err := c.Engine.Stop(ctx, ins.ID, 60*time.Second); err != nil {
				return nil, err
			}
			stoppedServices = append(stoppedServices, s)
		}
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
	f, err := os.OpenFile(filepath.Join(dest, "stack.tar.zst"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	err = transfer.ArchiveRoot(c.Layout.Root, f, RootSkip)
	f.Close()
	if err != nil {
		return nil, err
	}
	m := transfer.Manifest{Format: transfer.FormatName, Version: transfer.FormatVersion, SchemaVersion: store.SchemaVersion,
		StackID: c.Stack.ID, StackName: c.Stack.Name, CreatedAt: platform.FormatTime(time.Now()), StateFile: "state.db", RootArchive: "stack.tar.zst"}
	if v, err := c.Engine.Version(ctx); err == nil {
		m.Arch = v.Arch
	}
	for _, s := range services {
		if err := r.Phase(ctx, "archive-volume "+s.Name); err != nil {
			return nil, err
		}
		if vol, err := c.Engine.VolumeInspect(ctx, s.Volume); err != nil || vol == nil {
			return nil, Fail("volume-missing", "Restore the service volume before exporting.", "volume %s is missing", s.Volume)
		}
		file := "volume-" + s.Name + ".tar"
		if err := c.volumeJob(ctx, s.Image, s.Volume, dest, true, []string{"-C", "/v", "-cf", "/x/" + file, "."}); err != nil {
			return nil, err
		}
		m.Services = append(m.Services, transfer.ServiceEntry{Name: s.Name, Engine: string(s.Engine), Version: s.Version, Image: s.Image,
			VolumeFile: file, SourceVolume: s.Volume})
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
