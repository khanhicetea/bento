package operations

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// ToolSession is a scoped ephemeral tooling container for one app.
type ToolSession struct {
	ContainerID string
	App         domain.App
	c           *Controller
}

// Close removes the tooling container.
func (t *ToolSession) Close() {
	if t == nil || t.ContainerID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = t.c.Engine.Remove(ctx, t.ContainerID)
}

// OpenTool starts a tooling container: same image and identity, home and
// bound data only, no daemons, no persistent-instance lock, no backups.
func (c *Controller) OpenTool(ctx context.Context, app domain.App, lifetime time.Duration) (*ToolSession, error) {
	if !app.Provisioned {
		return nil, Fail("not-provisioned", "Wait for provisioning to finish.", "app %s is not provisioned", app.Slug)
	}
	if err := c.verifyHome(app); err != nil {
		return nil, err
	}
	if _, err := c.EnsureNetworks(ctx); err != nil {
		return nil, err
	}
	imageID, _, err := c.Images.Ensure(ctx, app.Runtime.ImageKey(), nil)
	if err != nil {
		return nil, err
	}
	passwd, group, err := c.Images.IdentityBase(ctx, imageID)
	if err != nil {
		return nil, err
	}
	ns, err := c.NetworkPlan(ctx)
	if err != nil {
		return nil, err
	}
	m, err := runtime.WriteAppConfig(app, runtime.AppContext{Layout: c.Layout, TrustedProxies: ns.TrustedProxies(), ImagePasswd: passwd, ImageGroup: group})
	if err != nil {
		return nil, err
	}
	in := runtime.AppInputs{App: app, Names: c.Names, Layout: c.Layout, ImageID: imageID, Materialized: m}
	spec := runtime.ToolContainerSpec(in, "t"+platform.RandomHex(5), lifetime)
	id, err := c.Engine.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	if err := c.Engine.Start(ctx, id); err != nil {
		_ = c.Engine.Remove(context.WithoutCancel(ctx), id)
		return nil, err
	}
	return &ToolSession{ContainerID: id, App: app, c: c}, nil
}

// RunningInstance returns the running, owned persistent container of an app.
func (c *Controller) RunningInstance(ctx context.Context, app domain.App) (string, error) {
	obs, err := c.observe(ctx, app)
	if err != nil {
		return "", err
	}
	if !obs.Exists || !obs.Running || !obs.Owned {
		return "", Fail("not-running", "Start the app first.", "app %s has no running instance", app.Slug)
	}
	return obs.ContainerID, nil
}

// ExecRequestFor builds an exec as the app identity through the tooling
// entrypoint, with a working directory contained in the code directory.
func ExecRequestFor(app domain.App, argv []string, workdir string) (docker.ExecRequest, error) {
	wd := app.ContainerCode()
	if workdir != "" {
		rel, err := domain.CleanRelative(workdir)
		if err != nil {
			return docker.ExecRequest{}, fmt.Errorf("workdir: %w", err)
		}
		if rel != "" {
			wd = wd + "/" + rel
		}
	}
	return docker.ExecRequest{
		User: strconv.Itoa(app.UID) + ":" + strconv.Itoa(app.GID),
		Cmd:  append([]string{"/usr/local/bin/bento-exec"}, argv...),
		Env:  []string{"BENTO_EXEC_WORKDIR=" + wd},
	}, nil
}
