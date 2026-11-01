package operations

import (
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"

	"github.com/khanhicetea/bento/apps/backend/internal/dataservices"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func demux(w io.Writer, r io.Reader) (int64, error) { return stdcopy.StdCopy(w, w, r) }

func (c *Controller) Data() *dataservices.Manager {
	return &dataservices.Manager{Engine: c.Engine, Layout: c.Layout, Names: c.Names}
}

// serviceContainer returns the running, ready container of an established
// service, or a diagnosable error. It never creates anything.
func (c *Controller) serviceContainer(ctx context.Context, s domain.DataService) (string, error) {
	ins, err := c.Engine.Inspect(ctx, c.Names.ServiceContainer(s.Name))
	if err != nil {
		return "", err
	}
	if ins == nil || ins.State == nil || !ins.State.Running {
		return "", Fail(
			"service-unavailable",
			"Wait for reconciliation to start the service, or inspect its status.",
			"data service %s is not running",
			s.Name,
		)
	}
	if ins.Config == nil || !c.Names.OwnedBy(
		ins.Config.Labels,
		runtime.RoleDatabase,
		"",
	) || ins.Config.Labels[runtime.LabelService] != s.Name {
		return "", Fail(
			"foreign-container",
			"Resolve the conflicting container manually.",
			"container %s is not Bento's %s service",
			c.Names.ServiceContainer(s.Name),
			s.Name,
		)
	}
	if err := c.Data().Ready(ctx, s, ins.ID); err != nil {
		return "", Fail("service-not-ready", "Retry after the service finishes starting.", "%v", err)
	}
	return ins.ID, nil
}

func (c *Controller) provisionRelational(ctx context.Context, b domain.Binding) error {
	svc, err := store.GetService(ctx, c.Store.DB(), b.Service)
	if err != nil {
		return Fail("service-missing", "Add the data service first.", "binding references unknown service %s", b.Service)
	}
	id, err := c.serviceContainer(ctx, svc.DataService)
	if err != nil {
		return err
	}
	if err := c.Data().ProvisionBinding(ctx, svc.DataService, id, b); err != nil {
		if _, ok := errors.AsType[*dataservices.AdoptionRefused](err); ok {
			return Fail(
				"database-retained",
				"Choose a different database name, or prune the retained app that owns it.",
				"%v",
				err,
			)
		}
		return Fail("grant-failed", "Inspect the data service; grants are idempotent and can be retried.", "%v", err)
	}
	return nil
}

// ensureService makes a data service container exist and run. When
// allowInit is false (reconciliation of an established service) a missing
// volume blocks recovery instead of being replaced with an empty one.
func (c *Controller) ensureService(ctx context.Context, r *Run, svc store.ServiceRow, allowInit bool) (string, error) {
	if _, err := c.EnsureNetworks(ctx); err != nil {
		return "", err
	}
	if err := c.Data().EnsureSecrets(svc.DataService, svc.Initialized); err != nil {
		if errors.Is(err, dataservices.ErrSecretMissing) {
			return "", Fail(
				"durable-state-missing",
				"Restore services/"+svc.Name+"/secrets from a backup of the stack root. Bento never replaces an established service's administrator secret.",
				"%v",
				err,
			)
		}
		return "", err
	}
	vol, err := c.Engine.VolumeInspect(ctx, svc.Volume)
	if err != nil {
		return "", err
	}
	if vol == nil {
		if svc.Initialized || !allowInit {
			return "", Fail(
				"volume-missing",
				"Restore the volume from a backup or raw archive. Bento never replaces an established service's data with an empty volume.",
				"volume %s for established service %s is missing",
				svc.Volume,
				svc.Name,
			)
		}
		if _, err := c.Engine.VolumeCreate(
			ctx,
			svc.Volume,
			c.Names.Labels(runtime.RoleVolume, map[string]string{runtime.LabelService: svc.Name}),
		); err != nil {
			return "", err
		}
		r.Info(ctx, "created volume %s", svc.Volume)
	} else if !c.Names.OwnedBy(vol.Labels, runtime.RoleVolume, "") || vol.Labels[runtime.LabelService] != svc.Name {
		return "", Fail(
			"foreign-volume",
			"Rename the conflicting volume or choose another service name; Bento never adopts unknown volumes.",
			"volume %s exists but is not owned by this stack's %s service",
			svc.Volume,
			svc.Name,
		)
	}
	if _, ok, err := c.Engine.ImageID(ctx, svc.Image); err != nil {
		return "", err
	} else if !ok {
		r.Info(ctx, "pulling %s", svc.Image)
		if err := c.Engine.PullImage(ctx, svc.Image, nil); err != nil {
			return "", Fail("pull-failed", "Check registry connectivity.", "pull %s: %v", svc.Image, err)
		}
	}
	spec := c.Data().ContainerSpec(svc.DataService)
	ins, err := c.Engine.Inspect(ctx, spec.Name)
	if err != nil {
		return "", err
	}
	var id string
	if ins != nil {
		if ins.Config == nil || !c.Names.OwnedBy(
			ins.Config.Labels,
			runtime.RoleDatabase,
			"",
		) || ins.Config.Labels[runtime.LabelService] != svc.Name {
			return "", Fail(
				"foreign-container",
				"Resolve the conflicting container manually.",
				"container %s is not owned by this stack",
				spec.Name,
			)
		}
		var mounted bool
		for _, m := range ins.Mounts {
			if m.Name == svc.Volume {
				mounted = true
			}
		}
		if !mounted {
			return "", Fail(
				"unexpected-mounts",
				"Inspect the service container.",
				"service container %s does not mount volume %s",
				spec.Name,
				svc.Volume,
			)
		}
		id = ins.ID
		if ins.Config.Labels[runtime.LabelServiceSettings] != spec.Config.Labels[runtime.LabelServiceSettings] {
			// Tuned settings changed: replace the container on the same
			// volume. Ownership and the volume mount were verified above.
			if err := r.Phase(ctx, "apply-settings"); err != nil {
				return "", err
			}
			if ins.State != nil && ins.State.Running {
				r.Info(ctx, "stopping %s to apply settings", svc.Name)
				if err := c.Engine.Stop(ctx, id, serviceStopTimeout); err != nil {
					return "", err
				}
			}
			if err := c.Engine.Remove(ctx, id); err != nil {
				return "", err
			}
			if id, err = c.Engine.Create(ctx, spec); err != nil {
				return "", err
			}
			if err := c.Engine.Start(ctx, id); err != nil {
				return "", err
			}
			r.Info(ctx, "recreated %s with %s", svc.Name, describeSettings(svc.DataService))
		} else if ins.State == nil || !ins.State.Running {
			if err := c.Engine.Start(ctx, id); err != nil {
				return "", err
			}
		}
	} else {
		if id, err = c.Engine.Create(ctx, spec); err != nil {
			return "", err
		}
		if err := c.Engine.Start(ctx, id); err != nil {
			return "", err
		}
	}
	// The container has now run against the volume, so the volume holds this
	// service's data even if first-boot initialization turns out slow or fails
	// readiness below. Mark it established now: from here on a missing volume
	// is refused instead of recreated empty, and the reconciler keeps it running.
	if !svc.Initialized {
		if err := store.MarkServiceInitialized(ctx, c.Store.DB(), svc.Name); err != nil {
			return "", err
		}
	}
	deadline := time.Now().Add(c.ServiceReadyTimeout)
	for {
		err := c.Data().Ready(ctx, svc.DataService, id)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return "", Fail("service-not-ready", "Inspect the service logs.", "%v", err)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(c.PollInterval):
		}
	}
	return id, nil
}

func (c *Controller) handleServiceCreate(ctx context.Context, r *Run) (any, error) {
	svc, err := store.GetService(ctx, c.Store.DB(), r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "initialize"); err != nil {
		return nil, err
	}
	id, err := c.ensureService(ctx, r, svc, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{"containerId": id}, nil
}

func (c *Controller) handleServiceEnsure(ctx context.Context, r *Run) (any, error) {
	svc, err := store.GetService(ctx, c.Store.DB(), r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "ensure"); err != nil {
		return nil, err
	}
	id, err := c.ensureService(ctx, r, svc, !svc.Initialized)
	if err != nil {
		return nil, err
	}
	return map[string]any{"containerId": id}, nil
}

// serviceStopTimeout matches the containers' StopTimeout so the server can
// flush and shut down cleanly.
const serviceStopTimeout = 60 * time.Second

func describeSettings(s domain.DataService) string {
	if len(s.Settings) == 0 {
		return "default settings"
	}
	parts := make([]string, 0, len(s.Settings))
	for _, k := range slices.Sorted(maps.Keys(s.Settings)) {
		parts = append(parts, k+"="+s.Settings[k])
	}
	return strings.Join(parts, " ")
}

// handleServiceConfigure applies saved settings: the container is recreated
// on its volume when they differ from the running ones.
func (c *Controller) handleServiceConfigure(ctx context.Context, r *Run) (any, error) {
	svc, err := c.loadEstablishedService(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "ensure"); err != nil {
		return nil, err
	}
	id, err := c.ensureService(ctx, r, svc, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"containerId": id}, nil
}

// handleServiceRestart stops the service cleanly and starts it again (with
// the saved settings), then waits for readiness.
func (c *Controller) handleServiceRestart(ctx context.Context, r *Run) (any, error) {
	svc, err := c.loadEstablishedService(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	ins, err := c.Engine.Inspect(ctx, c.Names.ServiceContainer(svc.Name))
	if err != nil {
		return nil, err
	}
	if ins != nil {
		if ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleDatabase, "") ||
			ins.Config.Labels[runtime.LabelService] != svc.Name {
			return nil, Fail("foreign-container", "Resolve the conflicting container manually.",
				"container %s is not owned by this stack", c.Names.ServiceContainer(svc.Name))
		}
		if ins.State != nil && ins.State.Running {
			if err := r.Phase(ctx, "stop"); err != nil {
				return nil, err
			}
			if err := c.Engine.Stop(ctx, ins.ID, serviceStopTimeout); err != nil {
				return nil, err
			}
		}
	}
	if err := r.Phase(ctx, "start"); err != nil {
		return nil, err
	}
	id, err := c.ensureService(ctx, r, svc, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"containerId": id}, nil
}

func (c *Controller) loadEstablishedService(ctx context.Context, name string) (store.ServiceRow, error) {
	svc, err := store.GetService(ctx, c.Store.DB(), name)
	if errors.Is(err, store.ErrNotFound) {
		return svc, Fail("service-missing", "The service no longer exists.", "service %s not found", name)
	}
	if err != nil {
		return svc, err
	}
	if !svc.Initialized {
		return svc, Fail("service-uninitialized", "Wait for the service to finish initializing.",
			"service %s is not initialized yet", name)
	}
	return svc, nil
}

// handleBindingAdd provisions newly added bindings/databases, then refreshes
// credentials; a running app is recreated so its environment matches.
func (c *Controller) handleBindingAdd(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "grants"); err != nil {
		return nil, err
	}
	if err := c.ensureSQLiteDirs(app); err != nil {
		return nil, err
	}
	for _, b := range app.Bindings {
		if b.Engine != domain.EngineSQLite {
			if err := c.provisionRelational(ctx, b); err != nil {
				return nil, err
			}
		}
	}
	return c.handleUpdate(ctx, r)
}
