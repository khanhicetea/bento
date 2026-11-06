package operations

import (
	"context"
	"encoding/json"
	"errors"
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

// edgeContainerSettings is the part of EdgeSettings that shapes the edge
// container. Its JSON matches the settings from before the front proxy, so
// adding that feature does not recreate existing edges.
type edgeContainerSettings struct {
	Enabled   bool   `json:"enabled"`
	Bind      string `json:"bind"`
	HTTPPort  int    `json:"httpPort"`
	HTTPSPort int    `json:"httpsPort"`
	HTTP3     bool   `json:"http3"`
	ACMEEmail string `json:"acmeEmail"`
	ACMEURL   string `json:"acmeUrl"`
}

// frontListeners is the front proxy's container shape: its published ports.
// The trusted list only changes rendered config, so editing it reloads.
type frontListeners struct {
	Bind      string
	HTTPPort  int
	HTTPSPort int
}

// edgeShapeVersion is bumped whenever a running edge must be recreated rather
// than reloaded to pick up a change: 2 added the traffic logs mount, 3 loads
// the ACME module unconditionally (nginx segfaults when a reload adds a
// load_module, so an edge started without it must cold-start with it).
const edgeShapeVersion = 3

func edgeFingerprint(s domain.EdgeSettings, ns NetworkSettings) string {
	var front *frontListeners
	if fp := s.FrontProxy; fp.Enabled {
		front = &frontListeners{fp.Bind, fp.HTTPPort, fp.HTTPSPort}
	}
	b, _ := json.Marshal(struct {
		S     edgeContainerSettings
		IP    string
		I     string
		V     int
		Front *frontListeners `json:",omitempty"`
	}{edgeContainerSettings{s.Enabled, s.Bind, s.HTTPPort, s.HTTPSPort, s.HTTP3, s.ACMEEmail, s.ACMEURL},
		ns.EdgeIP, domain.EdgeImage, edgeShapeVersion, front})
	return platform.SHA256Hex(b)[:20]
}

func (c *Controller) edgeSpec(s domain.EdgeSettings, ns NetworkSettings) (docker.ContainerSpec, error) {
	bind, err := parseEdgeBind(s.Bind)
	if err != nil {
		return docker.ContainerSpec{}, invalidEdgeBind(err)
	}
	// Docker publishes 0.0.0.0 on IPv4 only. "All addresses" also means [::]
	// when the apps network is dual-stack; on an IPv4-only network IPv6
	// visitors would all arrive as the bridge gateway, so they are not
	// accepted there at all.
	binds := []netip.Addr{bind}
	if bind.IsUnspecified() && ns.AppsIPv6 {
		binds = append(binds, netip.IPv6Unspecified())
	}
	publish := func(port int) []network.PortBinding {
		var out []network.PortBinding
		for _, a := range binds {
			out = append(out, network.PortBinding{HostIP: a, HostPort: strconv.Itoa(port)})
		}
		return out
	}
	ports := network.PortMap{
		network.MustParsePort("80/tcp"):  publish(s.HTTPPort),
		network.MustParsePort("443/tcp"): publish(s.HTTPSPort),
	}
	exposed := network.PortSet{network.MustParsePort("80/tcp"): {}, network.MustParsePort("443/tcp"): {}}
	if s.HTTP3 {
		ports[network.MustParsePort("443/udp")] = publish(s.HTTPSPort)
		exposed[network.MustParsePort("443/udp")] = struct{}{}
	}
	if fp := s.FrontProxy; fp.Enabled {
		fbind, err := parseEdgeBind(fp.Bind)
		if err != nil {
			return docker.ContainerSpec{}, invalidEdgeBind(err)
		}
		for container, host := range map[int]int{edge.FrontHTTPPort: fp.HTTPPort, edge.FrontHTTPSPort: fp.HTTPSPort} {
			p := network.MustParsePort(strconv.Itoa(container) + "/tcp")
			ports[p] = []network.PortBinding{{HostIP: fbind, HostPort: strconv.Itoa(host)}}
			exposed[p] = struct{}{}
		}
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
				// Per-host traffic access logs; the backend rotates them.
				{Type: mount.TypeBind, Source: c.Layout.TrafficLogsDir(), Target: edge.LogsMount},
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
	if err := platform.EnsureDir(c.Layout.EdgeACMEDir(), 0o700, platform.Owner{UID: EdgeNginxUID, GID: EdgeNginxUID}); err != nil {
		return err
	}
	return c.prepareTrafficDirs()
}

// edgeContainer returns the running owned edge container id, or "".
func (c *Controller) edgeContainer(ctx context.Context) (string, bool, error) {
	ins, err := c.Engine.Inspect(ctx, c.Names.EdgeContainer())
	if err != nil || ins == nil {
		return "", false, err
	}
	if ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleEdge, "") {
		return "", false, Fail(
			"foreign-container",
			"Resolve the conflicting container manually.",
			"container %s is not this stack's edge",
			c.Names.EdgeContainer(),
		)
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
			return errors.New(strings.TrimSpace(string(res.Stderr)))
		}
		return nil
	}
	s, err := c.EdgeSettings(ctx)
	if err != nil {
		return err
	}
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
		return errors.New(strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// renderEdge renders the complete edge configuration from current desired
// state without touching the filesystem or Docker objects.
// forApply marks a render that will be promoted (see holdRoute).
// acmeSettingKey holds the per-host ACME issuance state (edge.PlanACME).
const acmeSettingKey = "acme"

// acmePlan is one render's ACME decision: the state to persist once the
// render is live, and the hosts whose attempt window just closed.
type acmePlan struct {
	hosts      []edge.ACMEHost
	prev, next map[string]edge.ACMEHostState
	failed     []edge.ACMEFailure
}

func (p acmePlan) changed() bool { return !edge.SameACMEState(p.prev, p.next) }

func (c *Controller) acmeState(ctx context.Context) (map[string]edge.ACMEHostState, error) {
	st := map[string]edge.ACMEHostState{}
	_, err := store.GetSetting(ctx, c.Store.DB(), acmeSettingKey, &st)
	return st, err
}

func (c *Controller) renderEdge(
	ctx context.Context,
	s domain.EdgeSettings,
	ns NetworkSettings,
	warn func(string),
	forApply bool,
) (map[string][]byte, acmePlan, error) {
	var plan acmePlan
	apps, err := store.ListApps(ctx, c.Store.DB())
	if err != nil {
		return nil, plan, err
	}
	hosts, err := store.ListHosts(ctx, c.Store.DB())
	if err != nil {
		return nil, plan, err
	}
	runningApps := map[string]bool{}
	for _, a := range apps {
		if a.Publication == domain.Published {
			obs, err := c.observe(ctx, a)
			// A booting app is not routed until its own operation saw it ready.
			if err == nil && obs.Running && !c.holdRoute(a.ID, forApply) {
				runningApps[a.ID] = true
			}
		}
	}
	var upstream string
	if c.UtilsAppsPort > 0 {
		if gw := ns.AppsGateway(); gw != "" {
			upstream = net.JoinHostPort(gw, strconv.Itoa(c.UtilsAppsPort))
		} else if warn != nil {
			warn("apps network gateway not found on this host; /_bento/webhook/* is not forwarded by the edge")
		}
	}
	in := edge.Input{
		Settings:      s,
		Apps:          apps,
		Hosts:         hosts,
		Running:       runningApps,
		UtilsUpstream: upstream,
		TunnelIP:      ns.TunnelIP,
	}
	plan.hosts = edge.ACMEHosts(in)
	if plan.prev, err = c.acmeState(ctx); err != nil {
		return nil, plan, err
	}
	in.ACMEHold, plan.next, plan.failed = edge.PlanACME(c.Now(), plan.hosts,
		edge.IssuedCertificates(c.Layout.EdgeACMEDir()), plan.prev)
	files, err := edge.Render(in)
	return files, plan, err
}

// persistACME records the ACME state of a render that is now live, and
// reports hosts that just failed or cannot validate at all.
func (c *Controller) persistACME(ctx context.Context, r *Run, s domain.EdgeSettings, plan acmePlan) error {
	for _, f := range plan.failed {
		r.Warn(ctx, "ACME: no certificate for %s within %s (attempt %d); serving the boot certificate and retrying "+
			"at %s. Check that its DNS points here and port 80 is reachable from the internet, or save the host to "+
			"retry now.", f.Host, edge.ACMEAttemptWindow, f.Failures, platform.FormatTime(f.RetryAt))
	}
	if len(plan.hosts) > 0 && s.HTTPPort != 80 && !s.FrontProxy.Enabled {
		if ts, err := c.TunnelSettings(ctx); err == nil && !ts.Enabled {
			r.Warn(ctx, "ACME HTTP-01 validation reaches port 80, but the edge listens on %d with no front proxy or "+
				"tunnel; certificates issue only if something forwards port 80 to it.", s.HTTPPort)
		}
	}
	if !plan.changed() {
		return nil
	}
	return store.PutSetting(ctx, c.Store.DB(), acmeSettingKey, plan.next)
}

// ACMEStatus reports the certificate state of every served ACME host.
func (c *Controller) ACMEStatus(ctx context.Context) (map[string]ACMEHostStatus, error) {
	st, err := c.acmeState(ctx)
	if err != nil {
		return nil, err
	}
	issued := edge.IssuedCertificates(c.Layout.EdgeACMEDir())
	now := c.Now()
	out := map[string]ACMEHostStatus{}
	for name, exp := range issued {
		if exp.After(now) {
			out[name] = ACMEHostStatus{State: ACMEIssued, ExpiresAt: exp}
		}
	}
	for name, h := range st {
		if _, ok := out[name]; ok {
			continue
		}
		hs := ACMEHostStatus{State: ACMERequesting, Failures: h.Failures}
		if h.AttemptFrom.IsZero() && now.Before(h.RetryAt) {
			hs.State, hs.RetryAt = ACMEWaiting, h.RetryAt
		}
		out[name] = hs
	}
	return out, nil
}

// ACME certificate states of a host.
const (
	ACMEIssued     = "issued"
	ACMERequesting = "requesting"
	ACMEWaiting    = "waiting"
)

// ACMEHostStatus is one host's certificate state. A host with no entry has
// not been tried yet (requesting once the edge applies).
type ACMEHostStatus struct {
	State     string
	ExpiresAt time.Time
	RetryAt   time.Time
	Failures  int
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
	// A new image pin (a Bento upgrade) or container shape changes the
	// fingerprint without changing any route: the edge must be recreated.
	ins, err := c.Engine.Inspect(ctx, c.Names.EdgeContainer())
	if err != nil {
		return false, err
	}
	if ins != nil && ins.Config != nil && ins.Config.Labels[runtime.LabelGeneration] != edgeFingerprint(s, ns) {
		return true, nil
	}
	// A warming app renders as unavailable until its operation sees it
	// ready and refreshes the edge itself; that is not drift.
	if c.anyWarming() {
		return false, nil
	}
	files, plan, err := c.renderEdge(ctx, s, ns, nil, false)
	if err != nil {
		return false, err
	}
	// Live on disk but never loaded (a reload that was lost) is drift too,
	// and so is ACME state to record (a window opened or closed).
	gens := c.edgeGenerations()
	return !gens.Same(files) || !gens.Loaded(files) || plan.changed(), nil
}

// markEdgeLoaded records that the running edge serves files. A failed record
// only costs a redundant reload on the next pass, so it is a warning.
func (c *Controller) markEdgeLoaded(ctx context.Context, r *Run, files map[string][]byte) {
	if err := c.edgeGenerations().MarkLoaded(files); err != nil {
		r.Warn(ctx, "record the loaded edge generation: %v", err)
	}
}

// applyEdge renders routes from current desired state and activates them.
// Validation failure leaves the live generation untouched and sends no reload.
// Operations on different apps run in parallel and each ends by applying the
// whole edge, so the render-stage-promote-reload sequence is exclusive.
func (c *Controller) applyEdge(ctx context.Context, r *Run) (err error) {
	c.edgeMu.Lock()
	defer c.edgeMu.Unlock()
	s, err := c.EdgeSettings(ctx)
	if err != nil {
		return err
	}
	id, running, err := c.edgeContainer(ctx)
	if err != nil {
		return err
	}
	if id, running, err = c.settlePreviousEdge(ctx, r, id, running); err != nil {
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
	files, plan, err := c.renderEdge(ctx, s, ns, func(msg string) { r.Warn(ctx, "%s", msg) }, true)
	if err != nil {
		return err
	}
	// The ACME state describes this render; it is recorded only once the
	// render is live, so a failed apply leaves the previous windows intact.
	defer func() {
		if err == nil {
			err = c.persistACME(ctx, r, s, plan)
		}
	}()
	// The pinned image is needed by every path below (validator container,
	// create, recreate), not only when routes changed: an upgrade that bumps
	// only the image pin renders identical routes but recreates the edge, and
	// the Engine API never pulls on create.
	if err := docker.EnsureImage(ctx, c.Engine, domain.EdgeImage, func() error {
		r.Info(ctx, "pulling edge image")
		return nil
	}); err != nil {
		return err
	}
	// Decide on recreation before validating, so a candidate meant for a new
	// image or shape is checked by that image, not by the old running edge.
	spec, err := c.edgeSpec(s, ns)
	if err != nil {
		return err
	}
	gen := spec.Config.Labels[runtime.LabelGeneration]
	recreate := false
	if id != "" {
		ins, err := c.Engine.Inspect(ctx, id)
		if err != nil {
			return err
		}
		recreate = ins == nil || ins.Config == nil || ins.Config.Labels[runtime.LabelGeneration] != gen
	}
	gens := c.edgeGenerations()
	changed := !gens.Same(files)
	if changed {
		cand, err := gens.Stage(files)
		if err != nil {
			return err
		}
		var runningID string
		if running && !recreate {
			runningID = id
		}
		if err := c.validateEdge(ctx, cand, runningID); err != nil {
			gens.Discard(cand)
			return Fail(
				"edge-validation-failed",
				"The live edge configuration is unchanged and no reload was sent. Fix the route or custom drop-in and retry.",
				"edge configuration rejected: %v",
				err,
			)
		}
		if err := gens.Promote(cand); err != nil {
			gens.Discard(cand)
			return err
		}
		r.Info(ctx, "edge generation promoted (%d route files)", len(files)-1)
	}
	// Ensure the edge container matches its planned shape.
	if recreate {
		if !changed {
			// The live generation was validated by the old image; check it
			// with the new one before the old edge is touched.
			if err := c.validateEdge(ctx, "live", ""); err != nil {
				return Fail(
					"edge-validation-failed",
					"The running edge is unchanged. The live configuration is rejected by the new edge image or settings; fix the custom drop-ins and retry.",
					"edge configuration rejected: %v",
					err,
				)
			}
		}
		if err := c.recreateEdge(ctx, r, id, running, spec); err != nil {
			return err
		}
		c.markEdgeLoaded(ctx, r, files)
		return nil
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
		c.markEdgeLoaded(ctx, r, files)
		return nil
	}
	if !running {
		if err := c.Engine.Start(ctx, id); err != nil {
			return err
		}
		c.markEdgeLoaded(ctx, r, files)
		return nil
	}
	// Reload when the generation changed, and also when an earlier reload of
	// the live generation was never delivered.
	if changed || !gens.Loaded(files) {
		if err := c.Engine.Signal(ctx, id, "HUP"); err != nil {
			return Fail(
				"edge-reload-failed",
				"The validated generation is live on disk; the reconciler retries the reload.",
				"reload edge: %v",
				err,
			)
		}
		r.Info(ctx, "edge reloaded")
		c.markEdgeLoaded(ctx, r, files)
	}
	return nil
}

// recreateEdge replaces the edge container with one of the planned shape
// (image, ports, bind, HTTP/3) without losing the old one until the new one
// runs: the old edge is renamed aside, the new one is created under the edge
// name, the old one is stopped to free its ports, and the new one is started.
// Any failure removes the new container and puts the old edge back under its
// name, running if it was. The live generation on disk is shared by both.
func (c *Controller) recreateEdge(ctx context.Context, r *Run, oldID string, wasRunning bool, spec docker.ContainerSpec) error {
	r.Info(ctx, "edge settings or image changed; recreating edge")
	name, prev := c.Names.EdgeContainer(), c.Names.EdgePreviousContainer()
	if err := c.Engine.Rename(ctx, oldID, prev); err != nil {
		return err
	}
	// Past this point every exit either finishes the swap or restores the old
	// edge; neither may be skipped because the operation was cancelled.
	bg := context.WithoutCancel(ctx)
	newID, err := c.Engine.Create(bg, spec)
	if err != nil {
		return c.restorePreviousEdge(bg, r, oldID, wasRunning, "", "edge-create-failed", "create edge: %v", err)
	}
	if err := c.Engine.Stop(bg, oldID, 15*time.Second); err != nil {
		return c.restorePreviousEdge(bg, r, oldID, wasRunning, newID, "edge-recreate-failed", "stop previous edge: %v", err)
	}
	if err := c.Engine.Start(bg, newID); err != nil {
		return c.restorePreviousEdge(bg, r, oldID, wasRunning, newID, "edge-start-failed", "start edge: %v", err)
	}
	if err := c.Engine.Remove(bg, oldID); err != nil {
		// The new edge serves; the next apply removes the leftover.
		r.Warn(ctx, "edge recreated, but the previous edge container %s was not removed: %v", prev, err)
	}
	r.Info(ctx, "edge recreated as %s", name)
	return nil
}

// restorePreviousEdge undoes a failed recreate and returns the failure.
func (c *Controller) restorePreviousEdge(
	ctx context.Context,
	r *Run,
	oldID string,
	wasRunning bool,
	newID, code, format string,
	cause error,
) error {
	failure := fmt.Errorf(format, cause)
	if newID != "" {
		if err := c.Engine.Remove(ctx, newID); err != nil {
			return Fail(code,
				"The new edge could not be removed and the previous edge is kept aside; the next edge apply restores it.",
				"%v; remove new edge: %v", failure, err)
		}
	}
	if err := c.Engine.Rename(ctx, oldID, c.Names.EdgeContainer()); err != nil {
		return Fail(code,
			"The previous edge is kept aside; the next edge apply restores it.",
			"%v; restore previous edge: %v", failure, err)
	}
	if wasRunning {
		if err := c.Engine.Start(ctx, oldID); err != nil {
			return Fail(code,
				"The previous edge was put back but did not start; check the host ports and retry the edge apply.",
				"%v; restart previous edge: %v", failure, err)
		}
	}
	r.Warn(ctx, "edge recreate failed; the previous edge was restored with its old settings")
	return Fail(code,
		"The previous edge was restored and serves with its old settings. Check that the chosen host ports are free and the edge image is available, then retry.",
		"%v", failure)
}

// settlePreviousEdge resolves a previous edge left aside by an interrupted
// recreate. If the edge itself exists the leftover is removed; otherwise the
// leftover is put back under the edge name so the apply continues from it.
// A container with that name that this stack's edge does not own is refused.
func (c *Controller) settlePreviousEdge(ctx context.Context, r *Run, id string, running bool) (string, bool, error) {
	prev := c.Names.EdgePreviousContainer()
	ins, err := c.Engine.Inspect(ctx, prev)
	if err != nil || ins == nil {
		return id, running, err
	}
	if ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleEdge, "") {
		return id, running, Fail(
			"foreign-container",
			"Resolve the conflicting container manually.",
			"container %s is not this stack's edge",
			prev,
		)
	}
	if id != "" {
		r.Info(ctx, "removing previous edge container left by an interrupted recreate")
		if err := c.Engine.Remove(ctx, ins.ID); err != nil {
			return id, running, err
		}
		return id, running, nil
	}
	r.Info(ctx, "restoring previous edge container left by an interrupted recreate")
	if err := c.Engine.Rename(ctx, ins.ID, c.Names.EdgeContainer()); err != nil {
		return id, running, err
	}
	return ins.ID, ins.State != nil && ins.State.Running, nil
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
				runtime.LabelGeneration: tunnelGeneration(s),
			}),
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts: []mount.Mount{{
				Type:     mount.TypeBind,
				Source:   c.Layout.TunnelDir(),
				Target:   "/etc/bento-tunnel",
				ReadOnly: true,
			}},
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

// tunnelGeneration identifies the tunnel container's shape: the token
// generation and the pinned image, so a Bento upgrade that bumps the
// cloudflared pin recreates the tunnel.
func tunnelGeneration(s domain.TunnelSettings) string {
	return "token-" + strconv.FormatInt(s.TokenGeneration, 10) + "-" + platform.SHA256Hex([]byte(domain.TunnelImage))[:12]
}

// TunnelDrift reports whether an enabled tunnel is missing, stopped, or runs
// an outdated token or image.
func (c *Controller) TunnelDrift(ctx context.Context) (bool, error) {
	s, err := c.TunnelSettings(ctx)
	if err != nil || !s.Enabled {
		return false, err
	}
	ins, err := c.Engine.Inspect(ctx, c.Names.TunnelContainer())
	if err != nil {
		return false, err
	}
	if ins == nil || ins.State == nil || !ins.State.Running {
		return true, nil
	}
	return ins.Config == nil || ins.Config.Labels[runtime.LabelGeneration] != tunnelGeneration(s), nil
}

// applyTunnel makes cloudflared match settings; a token rotation or a new
// image pin recreates only the tunnel container.
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
		return Fail(
			"foreign-container",
			"Resolve the conflicting container manually.",
			"container %s is not this stack's tunnel",
			c.Names.TunnelContainer(),
		)
	}
	if !s.Enabled {
		if ins != nil {
			_ = c.Engine.Stop(ctx, ins.ID, 15*time.Second) // graceful stop is best effort; Remove forces
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
			if ins.State == nil || !ins.State.Running {
				return c.Engine.Start(ctx, ins.ID)
			}
			return nil
		}
		r.Info(ctx, "tunnel token or image changed; recreating only the tunnel container")
		_ = c.Engine.Stop(ctx, ins.ID, 15*time.Second) // graceful stop is best effort; Remove forces
		if err := c.Engine.Remove(ctx, ins.ID); err != nil {
			return err
		}
	}
	if err := docker.EnsureImage(ctx, c.Engine, domain.TunnelImage, nil); err != nil {
		return err
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
	return Fail(
		"edge-settings-invalid",
		"The persisted edge bind address is invalid; re-save the edge settings with a valid IPv4 bind address.",
		"edge bind address: %v",
		err,
	)
}
