package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const (
	edgeSettingKey   = "edge"
	tunnelSettingKey = "tunnel"
	// EdgeNginxUID is the nginx user in the official image (ACME state owner).
	EdgeNginxUID = 101
	// TunnelUID is the nonroot user of the cloudflared image.
	TunnelUID = 65532
)

func (c *Controller) EdgeSettings(ctx context.Context) (domain.EdgeSettings, error) {
	s := domain.DefaultEdgeSettings()
	_, err := store.GetSetting(ctx, c.Store.DB(), edgeSettingKey, &s)
	return s, err
}

func (c *Controller) TunnelSettings(ctx context.Context) (domain.TunnelSettings, error) {
	var s domain.TunnelSettings
	_, err := store.GetSetting(ctx, c.Store.DB(), tunnelSettingKey, &s)
	return s, err
}

func (c *Controller) edgeGenerations() edge.Generations {
	return edge.Generations{Dir: c.Layout.EdgeConfDir()}
}

func edgeFingerprint(s domain.EdgeSettings, ns NetworkSettings) string {
	b, _ := json.Marshal(struct {
		S  domain.EdgeSettings
		IP string
		I  string
		V  int
	}{s, ns.EdgeIP, domain.EdgeImage, 1})
	return platform.SHA256Hex(b)[:20]
}

func (c *Controller) edgeSpec(s domain.EdgeSettings, ns NetworkSettings) (docker.ContainerSpec, error) {
	bind, err := parseEdgeBind(s.Bind)
	if err != nil {
		return docker.ContainerSpec{}, invalidEdgeBind(err)
	}
	ports := network.PortMap{
		network.MustParsePort("80/tcp"):  {{HostIP: bind, HostPort: strconv.Itoa(s.HTTPPort)}},
		network.MustParsePort("443/tcp"): {{HostIP: bind, HostPort: strconv.Itoa(s.HTTPSPort)}},
	}
	exposed := network.PortSet{network.MustParsePort("80/tcp"): {}, network.MustParsePort("443/tcp"): {}}
	if s.HTTP3 {
		ports[network.MustParsePort("443/udp")] = []network.PortBinding{{HostIP: bind, HostPort: strconv.Itoa(s.HTTPSPort)}}
		exposed[network.MustParsePort("443/udp")] = struct{}{}
	}
	stop := 15
	return docker.ContainerSpec{
		Name: c.Names.EdgeContainer(),
		Config: &container.Config{
			Image: domain.EdgeImage, Entrypoint: []string{"nginx"},
			Cmd:          []string{"-c", edge.LiveConf, "-g", "daemon off;"},
			ExposedPorts: exposed, StopTimeout: &stop,
			Labels: c.Names.Labels(runtime.RoleEdge, map[string]string{runtime.LabelGeneration: edgeFingerprint(s, ns)}),
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			PortBindings:  ports,
			// Edge never mounts app homes, sockets, credentials, SQLite, or backups.
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: c.Layout.EdgeConfDir(), Target: edge.ConfMount, ReadOnly: true},
				{Type: mount.TypeBind, Source: c.Layout.EdgeCertsDir(), Target: edge.CertsMount, ReadOnly: true},
				{Type: mount.TypeBind, Source: c.Layout.EdgeCustomDir(), Target: edge.CustomMount, ReadOnly: true},
				{Type: mount.TypeBind, Source: c.Layout.EdgeACMEDir(), Target: edge.ACMEMount},
			},
			Tmpfs: map[string]string{
				"/var/cache/nginx": "rw,nosuid,nodev,size=256m",
				"/run":             "rw,nosuid,nodev,size=16m",
				"/tmp":             "rw,nosuid,nodev,size=64m",
			},
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			CapAdd:         []string{"NET_BIND_SERVICE", "SETUID", "SETGID", "CHOWN", "DAC_OVERRIDE"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			LogConfig:      container.LogConfig{Type: "local", Config: map[string]string{"max-size": "20m", "max-file": "5"}},
		},
		Networking: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			c.Names.AppsNetwork(): {IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(ns.EdgeIP)}},
		}},
	}, nil
}

func (c *Controller) prepareEdgeDirs() error {
	for _, d := range []string{c.Layout.EdgeConfDir(), c.Layout.EdgeCustomDir()} {
		if err := platform.EnsureDir(d, 0o755, platform.RootOwner); err != nil {
			return err
		}
	}
	for _, sub := range []string{"main.d", "http.d", "sites.d", "routes"} {
		if err := platform.EnsureDir(c.Layout.EdgeCustomDir()+"/"+sub, 0o755, platform.RootOwner); err != nil {
			return err
		}
	}
	if err := platform.EnsureDir(c.Layout.EdgeCertsDir(), 0o700, platform.RootOwner); err != nil {
		return err
	}
	if err := edge.EnsureBootCert(c.Layout.EdgeCertsDir()); err != nil {
		return err
	}
	return platform.EnsureDir(c.Layout.EdgeACMEDir(), 0o700, platform.Owner{UID: EdgeNginxUID, GID: EdgeNginxUID})
}

// edgeContainer returns the running owned edge container id, or "".
func (c *Controller) edgeContainer(ctx context.Context) (string, bool, error) {
	ins, err := c.Engine.Inspect(ctx, c.Names.EdgeContainer())
	if err != nil || ins == nil {
		return "", false, err
	}
	if ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleEdge, "") {
		return "", false, Fail("foreign-container", "Resolve the conflicting container manually.", "container %s is not this stack's edge", c.Names.EdgeContainer())
	}
	return ins.ID, ins.State != nil && ins.State.Running, nil
}

// validateEdge runs nginx -t against a candidate: inside the running edge
// when available, otherwise in a scoped, networked validator container.
func (c *Controller) validateEdge(ctx context.Context, candidate string, running string) error {
	conf := edge.ConfMount + "/" + candidate + "/nginx.conf"
	cmd := []string{"nginx", "-t", "-q", "-c", conf}
	if running != "" {
		res, err := c.Engine.Exec(ctx, running, docker.ExecRequest{Cmd: cmd, OutputLimit: 16 << 10})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s", strings.TrimSpace(string(res.Stderr)))
		}
		return nil
	}
	s, _ := c.EdgeSettings(ctx)
	ns, err := c.NetworkPlan(ctx)
	if err != nil {
		return err
	}
	spec, err := c.edgeSpec(s, ns)
	if err != nil {
		return err
	}
	opID := "validate-" + platform.RandomHex(4)
	spec.Name = c.Names.BackupContainer(opID)
	spec.Config.Entrypoint = []string{"sleep"}
	spec.Config.Cmd = []string{"120"}
	spec.Config.Labels = c.Names.Labels(runtime.RoleTool, map[string]string{runtime.LabelOperation: opID})
	spec.HostConfig.PortBindings = nil
	spec.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	spec.Networking.EndpointsConfig[c.Names.AppsNetwork()].IPAMConfig = nil
	id, err := c.Engine.Create(ctx, spec)
	if err != nil {
		return err
	}
	defer c.Engine.Remove(context.WithoutCancel(ctx), id)
	if err := c.Engine.Start(ctx, id); err != nil {
		return err
	}
	res, err := c.Engine.Exec(ctx, id, docker.ExecRequest{Cmd: cmd, OutputLimit: 16 << 10})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// renderEdge renders the complete edge configuration from current desired
// state without touching the filesystem or Docker objects.
func (c *Controller) renderEdge(ctx context.Context, s domain.EdgeSettings, ns NetworkSettings, warn func(string)) (map[string][]byte, error) {
	apps, err := store.ListApps(ctx, c.Store.DB())
	if err != nil {
		return nil, err
	}
	proxies, err := store.ListProxies(ctx, c.Store.DB())
	if err != nil {
		return nil, err
	}
	runningApps := map[string]bool{}
	for _, a := range apps {
		if a.Publication == domain.Published {
			obs, err := c.observe(ctx, a)
			if err == nil && obs.Running {
				runningApps[a.ID] = true
			}
		}
	}
	upstream := ""
	if c.UtilsAppsPort > 0 {
		if gw := ns.AppsGateway(); gw != "" {
			upstream = net.JoinHostPort(gw, strconv.Itoa(c.UtilsAppsPort))
		} else if warn != nil {
			warn("apps network gateway not found on this host; /_bento/webhook/* is not forwarded by the edge")
		}
	}
	return edge.Render(edge.Input{Settings: s, Apps: apps, Proxies: proxies, Running: runningApps, UtilsUpstream: upstream})
}

// EdgeConfigDrift reports whether the live edge generation differs from what
// current desired state and the embedded templates render.
func (c *Controller) EdgeConfigDrift(ctx context.Context) (bool, error) {
	s, err := c.EdgeSettings(ctx)
	if err != nil || !s.Enabled {
		return false, err
	}
	ns, err := c.NetworkPlan(ctx)
	if err != nil {
		return false, err
	}
	files, err := c.renderEdge(ctx, s, ns, nil)
	if err != nil {
		return false, err
	}
	return !c.edgeGenerations().Same(files), nil
}

// applyEdge renders routes from current desired state and activates them.
// Validation failure leaves the live generation untouched and sends no reload.
func (c *Controller) applyEdge(ctx context.Context, r *Run) error {
	s, err := c.EdgeSettings(ctx)
	if err != nil {
		return err
	}
	id, running, err := c.edgeContainer(ctx)
	if err != nil {
		return err
	}
	if !s.Enabled {
		if id != "" {
			r.Info(ctx, "edge disabled; removing edge container")
			if err := c.Engine.Stop(ctx, id, 15*time.Second); err != nil {
				return err
			}
			return c.Engine.Remove(ctx, id)
		}
		return nil
	}
	if _, err := parseEdgeBind(s.Bind); err != nil {
		return invalidEdgeBind(err)
	}
	if err := c.prepareEdgeDirs(); err != nil {
		return err
	}
	ns, err := c.EnsureNetworks(ctx)
	if err != nil {
		return err
	}
	files, err := c.renderEdge(ctx, s, ns, func(msg string) { r.Warn(ctx, "%s", msg) })
	if err != nil {
		return err
	}
	gens := c.edgeGenerations()
	changed := !gens.Same(files)
	if changed {
		if _, ok, err := c.Engine.ImageID(ctx, domain.EdgeImage); err != nil {
			return err
		} else if !ok {
			r.Info(ctx, "pulling edge image")
			if err := c.Engine.PullImage(ctx, domain.EdgeImage, nil); err != nil {
				return err
			}
		}
		cand, err := gens.Stage(files)
		if err != nil {
			return err
		}
		runningID := ""
		if running {
			runningID = id
		}
		if err := c.validateEdge(ctx, cand, runningID); err != nil {
			gens.Discard(cand)
			return Fail("edge-validation-failed", "The live edge configuration is unchanged and no reload was sent. Fix the route or custom drop-in and retry.",
				"edge configuration rejected: %v", err)
		}
		if err := gens.Promote(cand); err != nil {
			gens.Discard(cand)
			return err
		}
		r.Info(ctx, "edge generation promoted (%d route files)", len(files)-1)
	}
	// Ensure the edge container matches its planned shape.
	spec, err := c.edgeSpec(s, ns)
	if err != nil {
		return err
	}
	gen := spec.Config.Labels[runtime.LabelGeneration]
	if id != "" {
		ins, err := c.Engine.Inspect(ctx, id)
		if err != nil {
			return err
		}
		if ins.Config.Labels[runtime.LabelGeneration] != gen {
			r.Info(ctx, "edge settings changed; recreating edge")
			if err := c.Engine.Stop(ctx, id, 15*time.Second); err != nil {
				return err
			}
			if err := c.Engine.Remove(ctx, id); err != nil {
				return err
			}
			id, running = "", false
		}
	}
	if id == "" {
		if id, err = c.Engine.Create(ctx, spec); err != nil {
			return Fail("edge-create-failed", "Check that the chosen host ports are free.", "create edge: %v", err)
		}
		if err := c.Engine.Start(ctx, id); err != nil {
			_ = c.Engine.Remove(ctx, id)
			return Fail("edge-start-failed", "Check that the chosen host ports are free.", "start edge: %v", err)
		}
		r.Info(ctx, "edge started")
		return nil
	}
	if !running {
		return c.Engine.Start(ctx, id)
	}
	if changed {
		if err := c.Engine.Signal(ctx, id, "HUP"); err != nil {
			return Fail("edge-reload-failed", "The validated generation is live on disk; retry to reload.", "reload edge: %v", err)
		}
		r.Info(ctx, "edge reloaded")
	}
	return nil
}

func (c *Controller) handleEdgeApply(ctx context.Context, r *Run) (any, error) {
	if err := r.Phase(ctx, "apply-routes"); err != nil {
		return nil, err
	}
	return map[string]any{"applied": true}, c.applyEdge(ctx, r)
}

// ---- cloudflared ----

func (c *Controller) tunnelSpec(s domain.TunnelSettings, ns NetworkSettings) docker.ContainerSpec {
	stop := 15
	return docker.ContainerSpec{
		Name: c.Names.TunnelContainer(),
		Config: &container.Config{
			Image:       domain.TunnelImage,
			Cmd:         []string{"tunnel", "--no-autoupdate", "run", "--token-file", "/etc/bento-tunnel/token"},
			StopTimeout: &stop,
			Labels: c.Names.Labels(runtime.RoleTunnel, map[string]string{
				runtime.LabelGeneration: "token-" + strconv.FormatInt(s.TokenGeneration, 10),
			}),
		},
		HostConfig: &container.HostConfig{
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts:         []mount.Mount{{Type: mount.TypeBind, Source: c.Layout.TunnelDir(), Target: "/etc/bento-tunnel", ReadOnly: true}},
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			LogConfig:      container.LogConfig{Type: "local", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		Networking: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			c.Names.AppsNetwork(): {IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(ns.TunnelIP)}},
		}},
	}
}

// applyTunnel makes cloudflared match settings; token rotation recreates only
// the tunnel container.
func (c *Controller) applyTunnel(ctx context.Context, r *Run) error {
	s, err := c.TunnelSettings(ctx)
	if err != nil {
		return err
	}
	ins, err := c.Engine.Inspect(ctx, c.Names.TunnelContainer())
	if err != nil {
		return err
	}
	if ins != nil && (ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleTunnel, "")) {
		return Fail("foreign-container", "Resolve the conflicting container manually.", "container %s is not this stack's tunnel", c.Names.TunnelContainer())
	}
	if !s.Enabled {
		if ins != nil {
			_ = c.Engine.Stop(ctx, ins.ID, 15*time.Second)
			return c.Engine.Remove(ctx, ins.ID)
		}
		return nil
	}
	ns, err := c.EnsureNetworks(ctx)
	if err != nil {
		return err
	}
	spec := c.tunnelSpec(s, ns)
	if ins != nil {
		if ins.Config.Labels[runtime.LabelGeneration] == spec.Config.Labels[runtime.LabelGeneration] {
			if !ins.State.Running {
				return c.Engine.Start(ctx, ins.ID)
			}
			return nil
		}
		r.Info(ctx, "tunnel token changed; recreating only the tunnel container")
		_ = c.Engine.Stop(ctx, ins.ID, 15*time.Second)
		if err := c.Engine.Remove(ctx, ins.ID); err != nil {
			return err
		}
	}
	if _, ok, err := c.Engine.ImageID(ctx, domain.TunnelImage); err != nil {
		return err
	} else if !ok {
		if err := c.Engine.PullImage(ctx, domain.TunnelImage, nil); err != nil {
			return err
		}
	}
	id, err := c.Engine.Create(ctx, spec)
	if err != nil {
		return err
	}
	return c.Engine.Start(ctx, id)
}

func (c *Controller) handleTunnelApply(ctx context.Context, r *Run) (any, error) {
	if err := r.Phase(ctx, "apply-tunnel"); err != nil {
		return nil, err
	}
	return map[string]any{"applied": true}, c.applyTunnel(ctx, r)
}

func invalidEdgeBind(err error) error {
	return Fail("edge-settings-invalid", "The persisted edge bind address is invalid; re-save the edge settings with a valid IPv4 bind address.",
		"edge bind address: %v", err)
}
