package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/khanhicetea/bento/apps/backend/internal/assets"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// The database browser is one shared Adminer container on the data network.
// It holds no credentials: the backend's gateway authenticates the operator,
// then injects one binding's connection into each request together with a
// gateway token that the container's front controller requires.
const (
	dbadminSettingKey = "dbadmin"
	// AdminerUID/AdminerGID are the adminer user of the official image.
	AdminerUID = 100
	AdminerGID = 101
	// DBAdminPort is the Adminer listener inside the container.
	DBAdminPort = 8080

	dbadminMount    = "/run/bento-dbadmin"
	dbadminRouter   = "router.php"
	dbadminTokenRel = "gateway-token"
	dbadminVersion  = 1
)

func (c *Controller) DBAdminSettings(ctx context.Context) (domain.DBAdminSettings, error) {
	var s domain.DBAdminSettings
	_, err := store.GetSetting(ctx, c.Store.DB(), dbadminSettingKey, &s)
	return s, err
}

// SetDBAdmin persists the database browser toggle and applies it.
func (c *Controller) SetDBAdmin(ctx context.Context, enabled bool, idem string) (store.Operation, error) {
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindDBAdminApply, TargetKind: "dbadmin", TargetID: "dbadmin", IdempotencyKey: idem,
		Request: map[string]any{"enabled": enabled},
		Mutate: func(ctx context.Context, q store.Q) error {
			return store.PutSetting(ctx, q, dbadminSettingKey, domain.DBAdminSettings{Enabled: enabled})
		},
	})
	return op, err
}

// DBAdminToken reads the gateway token shared with the Adminer container.
func (c *Controller) DBAdminToken() (string, error) {
	b, err := os.ReadFile(filepath.Join(c.Layout.DBAdminDir(), dbadminTokenRel))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// DBAdminEndpoint returns host:port of the running, owned Adminer container
// on the data network (the host reaches it through the bridge).
func (c *Controller) DBAdminEndpoint(ctx context.Context) (string, error) {
	ins, err := c.Engine.Inspect(ctx, c.Names.DBAdminContainer())
	if err != nil {
		return "", err
	}
	if ins == nil || ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleDBAdmin, "") {
		return "", errors.New("the database browser container does not exist")
	}
	if ins.State == nil || !ins.State.Running {
		return "", errors.New("the database browser container is not running")
	}
	if ins.NetworkSettings != nil {
		if ep, ok := ins.NetworkSettings.Networks[c.Names.DataNetwork()]; ok && ep != nil && ep.IPAddress.IsValid() {
			return net.JoinHostPort(ep.IPAddress.String(), strconv.Itoa(DBAdminPort)), nil
		}
	}
	return "", errors.New("the database browser has no data network address")
}

func dbadminRouterSource() ([]byte, error) {
	return fs.ReadFile(assets.FS, "dbadmin/"+dbadminRouter)
}

func dbadminFingerprint(router []byte) string {
	b, _ := json.Marshal(struct {
		R string
		I string
		V int
	}{platform.SHA256Hex(router), domain.AdminerImage, dbadminVersion})
	return platform.SHA256Hex(b)[:20]
}

func (c *Controller) dbadminSpec(generation string) docker.ContainerSpec {
	stop := 5
	return docker.ContainerSpec{
		Name: c.Names.DBAdminContainer(),
		Config: &container.Config{
			Image:      domain.AdminerImage,
			User:       fmt.Sprintf("%d:%d", AdminerUID, AdminerGID),
			WorkingDir: "/var/www/html",
			Entrypoint: []string{"php"},
			Cmd: []string{
				"-d", "session.save_path=/tmp", "-d", "upload_max_filesize=64M", "-d", "post_max_size=64M",
				"-d", "memory_limit=256M", "-d", "expose_php=0", "-d", "display_errors=0",
				"-S", "0.0.0.0:" + strconv.Itoa(DBAdminPort), dbadminMount + "/" + dbadminRouter,
			},
			Env:         []string{"PHP_CLI_SERVER_WORKERS=4"},
			StopTimeout: &stop,
			Labels:      c.Names.Labels(runtime.RoleDBAdmin, map[string]string{runtime.LabelGeneration: generation}),
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			// Only the front controller and gateway token; never app homes,
			// service secrets, SQLite files, or backups.
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: c.Layout.DBAdminDir(), Target: dbadminMount, ReadOnly: true},
			},
			Tmpfs:          map[string]string{"/tmp": "rw,nosuid,nodev,noexec,size=128m"},
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			Resources:      container.Resources{Memory: 512 << 20, MemorySwap: 512 << 20, PidsLimit: new(int64(128))},
			LogConfig:      container.LogConfig{Type: "local", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		// Data network only: it reaches the database services and has no egress.
		Networking: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			c.Names.DataNetwork(): {},
		}},
	}
}

// prepareDBAdminDir writes the front controller and, once, the gateway token.
// Both are readable only by root and the adminer group.
func (c *Controller) prepareDBAdminDir(router []byte) error {
	owner := platform.Owner{UID: 0, GID: AdminerGID}
	if err := platform.EnsureDir(c.Layout.DBAdminDir(), 0o750, owner); err != nil {
		return err
	}
	if err := platform.AtomicWrite(filepath.Join(c.Layout.DBAdminDir(), dbadminRouter), router, 0o440, owner); err != nil {
		return err
	}
	tokenPath := filepath.Join(c.Layout.DBAdminDir(), dbadminTokenRel)
	if tok, err := c.DBAdminToken(); err == nil && len(tok) >= 32 {
		return os.Chown(tokenPath, owner.UID, owner.GID)
	}
	return platform.AtomicWrite(tokenPath, []byte(platform.RandomToken(32)), 0o440, owner)
}

// dbadminContainer inspects the named container and refuses a foreign one.
func (c *Controller) dbadminContainer(ctx context.Context) (*container.InspectResponse, error) {
	ins, err := c.Engine.Inspect(ctx, c.Names.DBAdminContainer())
	if err != nil || ins == nil {
		return nil, err
	}
	if ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleDBAdmin, "") {
		return nil, Fail("foreign-container", "Resolve the conflicting container manually.", "container %s is not this stack's database browser", c.Names.DBAdminContainer())
	}
	return ins, nil
}

// DBAdminDrift reports whether an enabled browser is missing, stopped, or
// runs an outdated generation.
func (c *Controller) DBAdminDrift(ctx context.Context) (bool, error) {
	router, err := dbadminRouterSource()
	if err != nil {
		return false, err
	}
	ins, err := c.dbadminContainer(ctx)
	if err != nil {
		return false, err
	}
	return ins == nil || ins.State == nil || !ins.State.Running || ins.Config.Labels[runtime.LabelGeneration] != dbadminFingerprint(router), nil
}

func (c *Controller) applyDBAdmin(ctx context.Context, r *Run) error {
	s, err := c.DBAdminSettings(ctx)
	if err != nil {
		return err
	}
	ins, err := c.dbadminContainer(ctx)
	if err != nil {
		return err
	}
	if !s.Enabled {
		if ins != nil {
			_ = c.Engine.Stop(ctx, ins.ID, 5*time.Second) // graceful stop is best effort; Remove forces
			return c.Engine.Remove(ctx, ins.ID)
		}
		return nil
	}
	router, err := dbadminRouterSource()
	if err != nil {
		return err
	}
	if err := c.prepareDBAdminDir(router); err != nil {
		return err
	}
	if _, err := c.EnsureNetworks(ctx); err != nil {
		return err
	}
	spec := c.dbadminSpec(dbadminFingerprint(router))
	if ins != nil {
		if ins.Config.Labels[runtime.LabelGeneration] == spec.Config.Labels[runtime.LabelGeneration] {
			if ins.State == nil || !ins.State.Running {
				return c.Engine.Start(ctx, ins.ID)
			}
			return nil
		}
		r.Info(ctx, "database browser changed; recreating its container")
		_ = c.Engine.Stop(ctx, ins.ID, 5*time.Second) // graceful stop is best effort; Remove forces
		if err := c.Engine.Remove(ctx, ins.ID); err != nil {
			return err
		}
	}
	if _, ok, err := c.Engine.ImageID(ctx, domain.AdminerImage); err != nil {
		return err
	} else if !ok {
		if err := r.Phase(ctx, "pull-image"); err != nil {
			return err
		}
		if err := c.Engine.PullImage(ctx, domain.AdminerImage, nil); err != nil {
			return err
		}
	}
	id, err := c.Engine.Create(ctx, spec)
	if err != nil {
		return err
	}
	return c.Engine.Start(ctx, id)
}

func (c *Controller) handleDBAdminApply(ctx context.Context, r *Run) (any, error) {
	if err := r.Phase(ctx, "apply-dbadmin"); err != nil {
		return nil, err
	}
	return map[string]any{"applied": true}, c.applyDBAdmin(ctx, r)
}
