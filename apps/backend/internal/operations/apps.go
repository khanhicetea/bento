package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Operation kinds.
const (
	KindAppProvision  = "app.provision"
	KindAppStart      = "app.start"
	KindAppStop       = "app.stop"
	KindAppRestart    = "app.restart"
	KindAppUpdate     = "app.update"
	KindAppPublish    = "app.publish"
	KindAppUnpublish  = "app.unpublish"
	KindAppRemove     = "app.remove"
	KindAppPrune      = "app.prune"
	KindAppReconcile  = "app.reconcile"
	KindBindingAdd    = "binding.add"
	KindDatabaseAdd   = "database.add"
	KindServiceCreate = "service.create"
	KindServiceEnsure = "service.reconcile"
	KindEdgeApply     = "edge.apply"
	KindTunnelApply   = "tunnel.apply"
	KindDBAdminApply  = "dbadmin.apply"
	KindBackupRun     = "backup.run"
	KindBackupRestore = "backup.restore"
	KindStackExport   = "stack.export"
	KindPermissions   = "app.permissions"
)

func (c *Controller) registerHandlers() {
	c.handlers = map[string]handler{
		KindAppProvision:  c.handleProvision,
		KindAppStart:      c.handleStart,
		KindAppStop:       c.handleStop,
		KindAppRestart:    c.handleRestart,
		KindAppUpdate:     c.handleUpdate,
		KindAppPublish:    c.handlePublish,
		KindAppUnpublish:  c.handleUnpublish,
		KindAppRemove:     c.handleRemove,
		KindAppPrune:      c.handlePrune,
		KindAppReconcile:  c.handleReconcile,
		KindBindingAdd:    c.handleBindingAdd,
		KindDatabaseAdd:   c.handleBindingAdd,
		KindServiceCreate: c.handleServiceCreate,
		KindServiceEnsure: c.handleServiceEnsure,
		KindEdgeApply:     c.handleEdgeApply,
		KindTunnelApply:   c.handleTunnelApply,
		KindDBAdminApply:  c.handleDBAdminApply,
		KindBackupRun:     c.handleBackupRun,
		KindBackupRestore: c.handleBackupRestore,
		KindStackExport:   c.handleStackExport,
		KindPermissions:   c.handlePermissions,
		KindAppDeploy:     c.handleDeploy,
	}
}

func (c *Controller) loadApp(ctx context.Context, id string) (domain.App, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if errors.Is(err, store.ErrNotFound) {
		return app, Fail("app-not-found", "The app was removed after this operation was accepted.", "app %s no longer exists", id)
	}
	return app, err
}

// HomeSidecar records identity for consistency checks. It is not a credential.
type HomeSidecar struct {
	StackID   string `json:"stackId"`
	AppID     string `json:"appId"`
	Slug      string `json:"slug"`
	UID       int    `json:"uid"`
	GID       int    `json:"gid"`
	CreatedAt string `json:"createdAt"`
}

// ensureHome creates a new home for this incarnation, or verifies an existing
// home belongs to it. A retained home of another incarnation is never adopted
// or recursively re-owned.
func (c *Controller) ensureHome(app domain.App) error {
	home := c.Layout.AppHome(app.Slug)
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	info, err := os.Lstat(home)
	switch {
	case os.IsNotExist(err):
		if err := platform.EnsureDir(home, 0o750, owner); err != nil {
			return err
		}
		sc := HomeSidecar{StackID: c.Stack.ID, AppID: app.ID, Slug: app.Slug, UID: app.UID, GID: app.GID, CreatedAt: platform.FormatTime(time.Now())}
		raw, _ := json.MarshalIndent(sc, "", "  ")
		if err := platform.AtomicWrite(c.Layout.HomeSidecar(app.Slug), append(raw, '\n'), 0o444, platform.RootOwner); err != nil {
			return err
		}
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
		return Fail("home-unsafe", "Replace the path with a real directory or restore it from backup.", "app home %s is not a real directory", home)
	default:
		if err := c.verifyHomeIdentity(app); err != nil {
			return err
		}
	}
	for _, sub := range []string{"app", "tmp", "tmp/sessions", ".local", ".local/share", ".local/state", "logs"} {
		p := filepath.Join(home, sub)
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			if err := platform.EnsureDir(p, 0o750, owner); err != nil {
				return err
			}
		}
	}
	return c.verifyCodeDir(app)
}

// verifyHome checks that required durable state exists and matches identity.
func (c *Controller) verifyHome(app domain.App) error {
	if err := c.verifyHomeIdentity(app); err != nil {
		return err
	}
	if err := c.verifyCodeDir(app); err != nil {
		return err
	}
	for _, b := range app.Bindings {
		if b.Engine != domain.EngineSQLite {
			continue
		}
		dir := c.Layout.SQLiteFileDir(b.SQLiteFileID)
		o, mode, err := platform.StatOwner(dir)
		if err != nil {
			return Fail("durable-state-missing", "Restore the SQLite directory from backup.", "sqlite binding directory %s is missing", dir)
		}
		if !mode.IsDir() || o.UID != app.UID {
			return Fail("durable-state-invalid", "Repair permissions or restore the directory.", "sqlite binding directory %s has unexpected type or owner", dir)
		}
	}
	return nil
}

func (c *Controller) verifyCodeDir(app domain.App) error {
	code := c.Layout.AppCode(app.Slug)
	owner, mode, err := platform.StatOwner(code)
	if os.IsNotExist(err) {
		return Fail("durable-state-missing", "Restore the app code directory from backup; Bento will not create an empty replacement for an established app.",
			"app code directory %s is missing", code)
	}
	if err != nil {
		return err
	}
	if !mode.IsDir() || owner.UID != app.UID {
		return Fail("home-unsafe", "Replace the app code path with a real directory owned by the app UID.",
			"app code directory %s has unexpected type or owner", code)
	}
	return nil
}

func (c *Controller) verifyHomeIdentity(app domain.App) error {
	home := c.Layout.AppHome(app.Slug)
	info, err := os.Lstat(home)
	if os.IsNotExist(err) {
		return Fail("durable-state-missing", "Restore the app home from backup; Bento will not create an empty replacement for an established app.",
			"app home %s is missing", home)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Fail("home-unsafe", "Replace the path with a real directory.", "app home %s is not a real directory", home)
	}
	raw, err := os.ReadFile(c.Layout.HomeSidecar(app.Slug))
	if err != nil {
		return Fail("home-retained", "This home was not created for this app incarnation. Prune the retained app data or restore explicitly; Bento never adopts or re-owns it automatically.",
			"app home %s exists without a Bento identity record", home)
	}
	var sc HomeSidecar
	if err := json.Unmarshal(raw, &sc); err != nil || sc.AppID != app.ID || sc.StackID != c.Stack.ID || sc.UID != app.UID {
		return Fail("home-retained", "The home belongs to a different app incarnation or stack. Prune it or restore explicitly.",
			"app home %s belongs to app %s (uid %d), not %s (uid %d)", home, sc.AppID, sc.UID, app.ID, app.UID)
	}
	owner, _, err := platform.StatOwner(home)
	if err != nil {
		return err
	}
	if owner.UID != app.UID {
		return Fail("home-owner", "Run a permission check/repair for the app.", "app home is owned by uid %d, expected %d", owner.UID, app.UID)
	}
	return nil
}

func (c *Controller) ensureSQLiteDirs(app domain.App) error {
	for _, b := range app.Bindings {
		if b.Engine != domain.EngineSQLite {
			continue
		}
		dir := c.Layout.SQLiteFileDir(b.SQLiteFileID)
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			if err := platform.EnsureDir(dir, 0o700, platform.Owner{UID: app.UID, GID: app.GID}); err != nil {
				return err
			}
			continue
		}
		o, mode, err := platform.StatOwner(dir)
		if err != nil {
			return err
		}
		if !mode.IsDir() || o.UID != app.UID {
			return Fail("sqlite-retained", "The SQLite directory exists for another identity; refusing to adopt it.", "sqlite directory %s is not owned by this app", dir)
		}
	}
	return nil
}

// materialize resolves the runtime image and writes generated config.
func (c *Controller) materialize(ctx context.Context, r *Run, app domain.App) (runtime.AppInputs, runtime.Changes, error) {
	imageID, _, err := c.Images.Ensure(ctx, app.Runtime.ImageKey(), func(s string) { r.Info(ctx, "%s", s) })
	if err != nil {
		return runtime.AppInputs{}, runtime.Changes{}, Fail("image", "Check Docker connectivity and build output, then retry.", "managed image: %v", err)
	}
	passwd, group, err := c.Images.IdentityBase(ctx, imageID)
	if err != nil {
		return runtime.AppInputs{}, runtime.Changes{}, err
	}
	ns, err := c.NetworkPlan(ctx)
	if err != nil {
		return runtime.AppInputs{}, runtime.Changes{}, err
	}
	m, ch, err := runtime.WriteAppConfigChanges(app, runtime.AppContext{
		Layout: c.Layout, TrustedProxies: ns.TrustedProxies(), ImagePasswd: passwd, ImageGroup: group,
	})
	if err != nil {
		return runtime.AppInputs{}, ch, err
	}
	return runtime.AppInputs{App: app, Names: c.Names, Layout: c.Layout, ImageID: imageID, Materialized: m}, ch, nil
}

// provision performs idempotent provisioning; it never starts the app.
func (c *Controller) provision(ctx context.Context, r *Run, app domain.App) error {
	if err := r.Phase(ctx, "home"); err != nil {
		return err
	}
	if err := c.ensureHome(app); err != nil {
		return err
	}
	if err := c.ensureSQLiteDirs(app); err != nil {
		return err
	}
	if err := r.Phase(ctx, "data-bindings"); err != nil {
		return err
	}
	if _, err := c.EnsureNetworks(ctx); err != nil {
		return err
	}
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite {
			continue
		}
		if err := c.provisionRelational(ctx, b); err != nil {
			return err
		}
	}
	if err := c.syncRedisACL(ctx); err != nil {
		r.Warn(ctx, "redis ACL not applied yet: %v", err)
	}
	if err := r.Phase(ctx, "materialize"); err != nil {
		return err
	}
	if _, _, err := c.materialize(ctx, r, app); err != nil {
		return err
	}
	return c.Store.Tx(ctx, func(q store.Q) error {
		cur, err := store.GetApp(ctx, q, app.ID)
		if err != nil {
			return err
		}
		cur.Provisioned = true
		if err := store.UpdateApp(ctx, q, cur); err != nil {
			return err
		}
		return store.SetLedgerState(ctx, q, app.UID, "active")
	})
}

func (c *Controller) handleProvision(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := c.provision(ctx, r, app); err != nil {
		return nil, err
	}
	return map[string]any{"appId": app.ID, "provisioned": true}, nil
}

// Observation is the observed Docker state of an app's persistent instance.
type Observation struct {
	Exists      bool
	ContainerID string
	Running     bool
	Status      string
	Health      string
	Generation  string
	RestartMode string
	Owned       bool
	Duplicates  []string
	IP          string
	StartedAt   string
	ExitCode    int
	Image       string
}

func (c *Controller) observe(ctx context.Context, app domain.App) (Observation, error) {
	var o Observation
	ins, err := c.Engine.Inspect(ctx, c.Names.AppContainer(app.ID))
	if err != nil {
		return o, err
	}
	if ins != nil {
		o.Exists = true
		o.ContainerID = ins.ID
		if ins.Config != nil {
			o.Owned = c.Names.OwnedBy(ins.Config.Labels, runtime.RoleRuntime, app.ID)
			o.Generation = ins.Config.Labels[runtime.LabelGeneration]
			o.Image = ins.Config.Image
		}
		if ins.State != nil {
			o.Running = ins.State.Running
			o.Status = string(ins.State.Status)
			o.ExitCode = ins.State.ExitCode
			o.StartedAt = ins.State.StartedAt
			if ins.State.Health != nil {
				o.Health = string(ins.State.Health.Status)
			}
		}
		if ins.HostConfig != nil {
			o.RestartMode = string(ins.HostConfig.RestartPolicy.Name)
		}
		if ins.NetworkSettings != nil {
			if ep, ok := ins.NetworkSettings.Networks[c.Names.AppsNetwork()]; ok && ep != nil && ep.IPAddress.IsValid() {
				o.IP = ep.IPAddress.String()
			}
		}
	}
	// Scoped to this stack: an imported clone legitimately shares app ids.
	all, err := c.Engine.List(ctx, map[string]string{runtime.LabelStackID: c.Stack.ID, runtime.LabelAppID: app.ID, runtime.LabelRole: string(runtime.RoleRuntime)})
	if err != nil {
		return o, err
	}
	for _, s := range all {
		if s.ID != o.ContainerID {
			o.Duplicates = append(o.Duplicates, s.ID)
		}
	}
	return o, nil
}

// verifyOwnedInstance checks labels and actual mounts before any
// destructive action on an existing container.
func (c *Controller) verifyOwnedInstance(ctx context.Context, app domain.App, id string) error {
	ins, err := c.Engine.Inspect(ctx, id)
	if err != nil || ins == nil {
		return err
	}
	if ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleRuntime, app.ID) {
		return Fail("foreign-container", "Rename or remove the conflicting container manually; Bento never adopts or deletes unknown resources.",
			"container %s is not Bento's instance for app %s", c.Names.AppContainer(app.ID), app.Slug)
	}
	if ins.HostConfig != nil {
		wantHome := c.Layout.AppHome(app.Slug)
		found := false
		for _, m := range ins.HostConfig.Mounts {
			if m.Type == mount.TypeBind && m.Target == app.ContainerHome() && m.Source == wantHome {
				found = true
			}
		}
		if !found {
			return Fail("unexpected-mounts", "Inspect the container; its mounts do not match this app.", "container for %s does not mount the expected home", app.Slug)
		}
	}
	return nil
}

// ensureInstance makes the persistent instance match the planned generation
// and be running. The previous instance is stopped before its replacement
// starts (no overlap of schedulers or workers).
func (c *Controller) ensureInstance(ctx context.Context, r *Run, app domain.App, forceRestart bool) (string, string, error) {
	if err := r.Phase(ctx, "verify-durable-state"); err != nil {
		return "", "", err
	}
	if err := c.verifyHome(app); err != nil {
		return "", "", err
	}
	if _, err := c.EnsureNetworks(ctx); err != nil {
		return "", "", err
	}
	if err := r.Phase(ctx, "prepare"); err != nil {
		return "", "", err
	}
	in, _, err := c.materialize(ctx, r, app)
	if err != nil {
		return "", "", err
	}
	spec, gen := runtime.AppContainerSpec(in, true)
	obs, err := c.observe(ctx, app)
	if err != nil {
		return "", "", err
	}
	if len(obs.Duplicates) > 0 {
		return "", "", Fail("duplicate-instance", "Stop and remove the extra containers manually after verifying them.",
			"found %d additional runtime container(s) labeled for app %s: %s", len(obs.Duplicates), app.Slug, strings.Join(obs.Duplicates, ", "))
	}
	if obs.Exists {
		if err := c.verifyOwnedInstance(ctx, app, obs.ContainerID); err != nil {
			return "", "", err
		}
		if obs.Generation == gen {
			if err := r.Phase(ctx, "start"); err != nil {
				return "", "", err
			}
			if obs.RestartMode != string(container.RestartPolicyUnlessStopped) {
				if err := c.Engine.SetRestartPolicy(ctx, obs.ContainerID, container.RestartPolicyUnlessStopped); err != nil {
					return "", "", err
				}
			}
			if obs.Running && forceRestart {
				r.Info(ctx, "restarting instance (scheduler and workers are interrupted)")
				if err := c.Engine.Stop(ctx, obs.ContainerID, runtime.StopTimeout); err != nil {
					return "", "", err
				}
				obs.Running = false
			}
			if !obs.Running {
				if err := c.Engine.Start(ctx, obs.ContainerID); err != nil {
					return "", "", Fail("start-failed", "Inspect the app logs.", "start: %v", err)
				}
			}
			return obs.ContainerID, gen, nil
		}
		if err := r.Phase(ctx, "replace"); err != nil {
			return "", "", err
		}
		r.Info(ctx, "configuration generation changed (%s -> %s); replacing instance", short(obs.Generation), short(gen))
		if obs.Running {
			if err := c.Engine.Stop(ctx, obs.ContainerID, runtime.StopTimeout); err != nil {
				return "", "", Fail("stop-failed", "Retry; the previous instance is still in place.", "stop previous instance: %v", err)
			}
		}
		if err := c.Engine.Remove(ctx, obs.ContainerID); err != nil {
			return "", "", Fail("remove-failed", "Retry; the previous instance is stopped.", "remove previous instance: %v", err)
		}
	}
	if err := r.Phase(ctx, "create"); err != nil {
		return "", "", err
	}
	id, err := c.Engine.Create(ctx, spec)
	if err != nil {
		return "", "", Fail("create-failed", "Check for a conflicting container name and Docker errors.", "create: %v", err)
	}
	r.Info(ctx, "created instance %s generation %s", short(id), short(gen))
	if err := r.Phase(ctx, "start"); err != nil {
		return id, gen, err
	}
	if err := c.Engine.Start(ctx, id); err != nil {
		return id, gen, Fail("start-failed", "Inspect the app logs.", "start: %v", err)
	}
	return id, gen, nil
}

func short(s string) string {
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ReadyCheck is one readiness evaluation.
type ReadyCheck struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`
}

// checkReady verifies the running instance, intended generation, in-container
// component readiness (scheduler, FPM, local HTTP), and the real HTTP
// endpoint reached over the app network from the backend.
func (c *Controller) checkReady(ctx context.Context, app domain.App, gen string) ReadyCheck {
	obs, err := c.observe(ctx, app)
	if err != nil {
		return ReadyCheck{Reason: "inspect: " + err.Error()}
	}
	if !obs.Exists || !obs.Running {
		return ReadyCheck{Reason: "instance is not running"}
	}
	if gen != "" && obs.Generation != gen {
		return ReadyCheck{Reason: "instance generation does not match intended configuration"}
	}
	ectx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := c.Engine.Exec(ectx, obs.ContainerID, docker.ExecRequest{
		User: strconv.Itoa(app.UID) + ":" + strconv.Itoa(app.GID), Cmd: []string{"/usr/local/bin/bento-ready"}, OutputLimit: 4096,
	})
	if err != nil {
		return ReadyCheck{Reason: "readiness exec: " + err.Error()}
	}
	if res.ExitCode != 0 {
		return ReadyCheck{Reason: strings.TrimSpace(string(res.Stdout) + string(res.Stderr))}
	}
	if obs.IP == "" {
		return ReadyCheck{Reason: "instance has no address on the app network"}
	}
	url := fmt.Sprintf("http://%s:%d%s", obs.IP, app.HTTPPort(), app.ReadyPath())
	code, err := c.Probe(ctx, url)
	if err != nil {
		return ReadyCheck{Reason: "direct HTTP probe: " + err.Error()}
	}
	if code >= 500 {
		return ReadyCheck{Reason: fmt.Sprintf("direct HTTP probe returned %d", code)}
	}
	return ReadyCheck{Ready: true}
}

// waitReady polls readiness within the configured bound. An exited container
// fails fast with a bounded log tail instead of waiting out the timeout.
func (c *Controller) waitReady(ctx context.Context, r *Run, app domain.App, gen string) error {
	if err := r.Phase(ctx, "readiness"); err != nil {
		return err
	}
	deadline := time.Now().Add(c.ReadyTimeout)
	last := ""
	for {
		chk := c.checkReady(ctx, app, gen)
		if chk.Ready {
			r.Info(ctx, "app is ready")
			return nil
		}
		if chk.Reason != last {
			r.Info(ctx, "waiting: %s", chk.Reason)
			last = chk.Reason
		}
		if chk.Reason == "instance is not running" {
			obs, _ := c.observe(ctx, app)
			if obs.Exists && !obs.Running && obs.Status == "exited" {
				tail := c.logTail(ctx, obs.ContainerID, 30)
				return Fail("instance-exited", "Fix the application error shown in the log tail, then start again.",
					"instance exited with code %d during startup:\n%s", obs.ExitCode, tail)
			}
		}
		if r.Cancelled(ctx) {
			return ErrCancelled
		}
		if time.Now().After(deadline) {
			return Fail("not-ready", "The instance keeps running; inspect logs and readiness configuration. Publication was not changed.",
				"app did not become ready within %s: %s", c.ReadyTimeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.PollInterval):
		}
	}
}

func (c *Controller) logTail(ctx context.Context, id string, lines int) string {
	rc, tty, err := c.Engine.Logs(ctx, id, strconv.Itoa(lines), false, "")
	if err != nil {
		return "(logs unavailable)"
	}
	defer rc.Close()
	out, err := ReadLogs(rc, tty, 16<<10)
	if err != nil {
		return "(logs unavailable)"
	}
	return string(out)
}

// ReadLogs demultiplexes a bounded amount of Docker log output.
func ReadLogs(rc io.Reader, tty bool, limit int64) ([]byte, error) {
	lr := io.LimitReader(rc, limit)
	if tty {
		return io.ReadAll(lr)
	}
	var buf strings.Builder
	if _, err := demux(&buf, lr); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return []byte(buf.String()), err
	}
	return []byte(buf.String()), nil
}

func (c *Controller) handleStart(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if app.DesiredRuntime != domain.DesiredRunning {
		r.Info(ctx, "superseded: desired runtime is now %s", app.DesiredRuntime)
		return map[string]any{"superseded": true}, nil
	}
	if !app.Provisioned {
		if err := c.provision(ctx, r, app); err != nil {
			return nil, err
		}
		if app, err = c.loadApp(ctx, app.ID); err != nil {
			return nil, err
		}
	}
	id, gen, err := c.ensureInstance(ctx, r, app, false)
	if err != nil {
		return nil, err
	}
	if err := c.waitReady(ctx, r, app, gen); err != nil {
		return nil, err
	}
	if app.Ingress == domain.IngressManaged && app.Publication == domain.Published {
		if err := r.Phase(ctx, "activate-route"); err != nil {
			return nil, err
		}
		if err := c.applyEdge(ctx, r); err != nil {
			return nil, err
		}
	}
	return map[string]any{"containerId": id, "generation": gen}, nil
}

// stopInstance stops the persistent instance and disables its restart policy
// so neither Docker nor a host reboot resurrects it.
func (c *Controller) stopInstance(ctx context.Context, r *Run, app domain.App) error {
	obs, err := c.observe(ctx, app)
	if err != nil {
		return err
	}
	if !obs.Exists {
		r.Info(ctx, "no instance exists")
		return nil
	}
	if err := c.verifyOwnedInstance(ctx, app, obs.ContainerID); err != nil {
		return err
	}
	if err := c.Engine.SetRestartPolicy(ctx, obs.ContainerID, container.RestartPolicyDisabled); err != nil {
		return err
	}
	if obs.Running {
		if err := c.Engine.Stop(ctx, obs.ContainerID, runtime.StopTimeout); err != nil {
			return Fail("stop-failed", "Stop intent is persisted; retry stop.", "stop: %v", err)
		}
	}
	r.Info(ctx, "instance stopped (scheduler and workers stopped with it)")
	return nil
}

func (c *Controller) handleStop(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "remove-route"); err != nil {
		return nil, err
	}
	if err := c.applyEdge(ctx, r); err != nil {
		return nil, Fail("route-removal-failed", "Stop intent is persisted and the app was not stopped; fix the edge error and retry stop.", "%v", err)
	}
	if err := r.Phase(ctx, "stop"); err != nil {
		return nil, err
	}
	if err := c.stopInstance(ctx, r, app); err != nil {
		return nil, err
	}
	return map[string]any{"stopped": true}, nil
}

func (c *Controller) handleRestart(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if app.DesiredRuntime != domain.DesiredRunning {
		return nil, Fail("not-running", "Start the app instead.", "app %s is not desired running", app.Slug)
	}
	id, gen, err := c.ensureInstance(ctx, r, app, true)
	if err != nil {
		return nil, err
	}
	if err := c.waitReady(ctx, r, app, gen); err != nil {
		return nil, err
	}
	return map[string]any{"containerId": id, "generation": gen}, nil
}

// handleUpdate applies a persisted configuration change with the narrowest
// effect: recreation only for boot-static changes of a running app, otherwise
// validated scoped reloads. Stopped apps stay stopped.
func (c *Controller) handleUpdate(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if !app.Provisioned {
		return nil, c.provision(ctx, r, app)
	}
	if err := r.Phase(ctx, "provision-refresh"); err != nil {
		return nil, err
	}
	if err := c.ensureSQLiteDirs(app); err != nil {
		return nil, err
	}
	if err := c.syncRedisACL(ctx); err != nil {
		r.Warn(ctx, "redis ACL not applied: %v", err)
	}
	result := map[string]any{}
	if app.DesiredRuntime == domain.DesiredRunning {
		obs, err := c.observe(ctx, app)
		if err != nil {
			return nil, err
		}
		in, ch, err := c.materialize(ctx, r, app)
		if err != nil {
			return nil, err
		}
		_, gen := runtime.AppContainerSpec(in, true)
		if !obs.Exists || obs.Generation != gen {
			id, gen, err := c.ensureInstance(ctx, r, app, false)
			if err != nil {
				return nil, err
			}
			if err := c.waitReady(ctx, r, app, gen); err != nil {
				return nil, err
			}
			result["recreated"] = true
			result["containerId"] = id
		} else if obs.Running {
			if err := c.scopedReloads(ctx, r, app, obs.ContainerID, ch); err != nil {
				return nil, err
			}
			result["reloaded"] = map[string]bool{"frontend": ch.Frontend, "pool": ch.Pool, "scheduler": ch.Scheduler}
		}
	} else {
		if _, _, err := c.materialize(ctx, r, app); err != nil {
			return nil, err
		}
		result["stopped"] = true
	}
	if err := r.Phase(ctx, "routes"); err != nil {
		return nil, err
	}
	if err := c.applyEdge(ctx, r); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Controller) appExec(ctx context.Context, app domain.App, id string, argv ...string) (docker.ExecResult, error) {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return c.Engine.Exec(cctx, id, docker.ExecRequest{
		User: strconv.Itoa(app.UID) + ":" + strconv.Itoa(app.GID), Cmd: append([]string{"/usr/local/bin/bento-exec"}, argv...), OutputLimit: 16 << 10,
		Env: []string{"BENTO_EXEC_WORKDIR=" + app.ContainerCode()},
	})
}

// scopedReloads validates candidates with the running process's own
// validator. Validation failure restores the previous bytes and sends no
// reload.
func (c *Controller) scopedReloads(ctx context.Context, r *Run, app domain.App, id string, ch runtime.Changes) error {
	steps := []struct {
		changed  bool
		files    []string
		name     string
		validate []string
		reload   []string
	}{
		{ch.Frontend, runtime.FrontendFiles, "local nginx", []string{"nginx", "-t", "-q", "-e", "stderr", "-c", "/etc/bento/nginx.conf"},
			[]string{"nginx", "-e", "stderr", "-c", "/etc/bento/nginx.conf", "-s", "reload"}},
		{ch.Pool, runtime.PoolFiles, "php-fpm", []string{"php-fpm", "-t", "--fpm-config", "/etc/bento/php-fpm.conf"},
			[]string{"/package/admin/s6/command/s6-svc", "-r", "/run/service/php-fpm"}},
		{ch.Scheduler, runtime.SchedulerFiles, "scheduler", []string{"minicrond", "validate", "/etc/bento/minicrond.toml"},
			[]string{"minicrond", "reload"}},
	}
	for _, s := range steps {
		if !s.changed || app.Runtime.Kind != domain.RuntimePHP && s.name != "scheduler" {
			continue
		}
		if err := r.Phase(ctx, "reload "+s.name); err != nil {
			return err
		}
		res, err := c.appExec(ctx, app, id, s.validate...)
		if err != nil || res.ExitCode != 0 {
			_ = ch.Restore(s.files...)
			detail := ""
			if err == nil {
				detail = strings.TrimSpace(string(res.Stderr) + string(res.Stdout))
			} else {
				detail = err.Error()
			}
			return Fail("validation-failed", "Previous configuration restored; no reload was sent. Correct the configuration and retry.",
				"%s rejected the new configuration: %s", s.name, detail)
		}
		res, err = c.appExec(ctx, app, id, s.reload...)
		if err != nil || res.ExitCode != 0 {
			return Fail("reload-failed", "The validated configuration is in place; retry the update or restart the app.",
				"%s reload failed: %v %s", s.name, err, strings.TrimSpace(string(res.Stderr)))
		}
		r.Info(ctx, "%s reloaded", s.name)
	}
	return nil
}

func (c *Controller) handlePublish(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if app.Ingress != domain.IngressManaged {
		return nil, Fail("not-managed", "Publication controls apply only to Bento-managed edge routes.", "app %s uses %s ingress", app.Slug, app.Ingress)
	}
	edge, err := c.EdgeSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !edge.Enabled {
		return nil, Fail("edge-disabled", "Enable the managed edge first.", "the managed edge is not enabled")
	}
	if app.DesiredRuntime != domain.DesiredRunning {
		return nil, Fail("not-running", "Start the app first; publish never starts an app implicitly.", "app %s is stopped", app.Slug)
	}
	if len(app.Domains) == 0 {
		return nil, Fail("no-domain", "Add a primary domain first.", "app %s has no domains", app.Slug)
	}
	if err := r.Phase(ctx, "verify-readiness"); err != nil {
		return nil, err
	}
	in, _, err := c.materialize(ctx, r, app)
	if err != nil {
		return nil, err
	}
	_, gen := runtime.AppContainerSpec(in, true)
	if chk := c.checkReady(ctx, app, gen); !chk.Ready {
		return nil, Fail("not-ready", "Publication unchanged. Wait for the app to become ready (or restart it) and publish again.", "app is not ready: %s", chk.Reason)
	}
	if err := c.Store.Tx(ctx, func(q store.Q) error {
		cur, err := store.GetApp(ctx, q, app.ID)
		if err != nil {
			return err
		}
		if cur.DesiredRuntime != domain.DesiredRunning {
			return Fail("not-running", "", "app was stopped while publishing")
		}
		cur.Publication = domain.Published
		return store.UpdateApp(ctx, q, cur)
	}); err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "activate-route"); err != nil {
		return nil, err
	}
	if err := c.applyEdge(ctx, r); err != nil {
		return nil, err
	}
	return map[string]any{"published": true}, nil
}

func (c *Controller) handleUnpublish(ctx context.Context, r *Run) (any, error) {
	if err := r.Phase(ctx, "remove-route"); err != nil {
		return nil, err
	}
	if err := c.applyEdge(ctx, r); err != nil {
		return nil, err
	}
	return map[string]any{"published": false}, nil
}

func (c *Controller) handleRemove(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "remove-route"); err != nil {
		return nil, err
	}
	if err := c.applyEdge(ctx, r); err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "remove-containers"); err != nil {
		return nil, err
	}
	obs, err := c.observe(ctx, app)
	if err != nil {
		return nil, err
	}
	if obs.Exists {
		if err := c.verifyOwnedInstance(ctx, app, obs.ContainerID); err != nil {
			return nil, err
		}
		if err := c.stopInstance(ctx, r, app); err != nil {
			return nil, err
		}
		if err := c.Engine.Remove(ctx, obs.ContainerID); err != nil {
			return nil, err
		}
	}
	tools, err := c.Engine.List(ctx, map[string]string{runtime.LabelAppID: app.ID, runtime.LabelStackID: c.Stack.ID})
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		// Tool and runtime containers are removed outright; backup job
		// containers only once exited (an orphan left by a crash).
		switch {
		case c.Names.OwnedBy(t.Labels, runtime.RoleTool, app.ID), c.Names.OwnedBy(t.Labels, runtime.RoleRuntime, app.ID):
		case c.Names.OwnedBy(t.Labels, runtime.RoleBackup, app.ID) && t.State != "running":
		default:
			continue
		}
		if err := c.Engine.Remove(ctx, t.ID); err != nil {
			return nil, err
		}
	}
	left, err := c.Engine.List(ctx, map[string]string{runtime.LabelStackID: c.Stack.ID, runtime.LabelAppID: app.ID})
	if err != nil {
		return nil, err
	}
	if len(left) > 0 {
		return nil, Fail("containers-remain", "Remove the remaining containers labeled for this app, then retry.", "%d container(s) for the app still exist", len(left))
	}
	if err := r.Phase(ctx, "retire-identity"); err != nil {
		return nil, err
	}
	arts := store.RetainedArtifacts{Home: c.Layout.AppHome(app.Slug), RedisUser: app.Redis.Username}
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite {
			arts.SQLiteFileIDs = append(arts.SQLiteFileIDs, b.SQLiteFileID)
		} else {
			arts.Relational = append(arts.Relational, store.RetainedRelational{Engine: b.Engine, Service: b.Service, Username: b.Username, Databases: b.Databases})
		}
	}
	if err := c.Store.Tx(ctx, func(q store.Q) error {
		if err := store.InsertRetired(ctx, q, store.RetiredApp{AppID: app.ID, Slug: app.Slug, UID: app.UID, Artifacts: arts}); err != nil {
			return err
		}
		state := "retired"
		if !app.Provisioned {
			state = "burned"
		}
		if err := store.SetLedgerState(ctx, q, app.UID, state); err != nil {
			return err
		}
		return store.DeleteApp(ctx, q, app.ID)
	}); err != nil {
		return nil, err
	}
	// Generated configuration (including credentials) is Bento-owned and is
	// removed; durable data (home, SQLite, databases) is retained.
	if err := os.RemoveAll(c.Layout.AppDir(app.ID)); err != nil {
		r.Warn(ctx, "could not remove generated config: %v", err)
	}
	if err := c.syncRedisACL(ctx); err != nil {
		r.Warn(ctx, "redis ACL not refreshed: %v", err)
	}
	return map[string]any{"removed": true, "retained": arts}, nil
}

func (c *Controller) handlePrune(ctx context.Context, r *Run) (any, error) {
	ret, err := store.GetRetired(ctx, c.Store.DB(), r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if ret.PrunedAt != "" {
		return map[string]any{"alreadyPruned": true}, nil
	}
	if _, err := store.GetApp(ctx, c.Store.DB(), ret.Slug); err == nil {
		return nil, Fail("slug-active", "Remove the active app with this slug first.", "an active app now uses slug %s", ret.Slug)
	}
	if err := r.Phase(ctx, "drop-databases"); err != nil {
		return nil, err
	}
	for _, rel := range ret.Artifacts.Relational {
		svc, err := store.GetService(ctx, c.Store.DB(), rel.Service)
		if err != nil {
			return nil, err
		}
		id, err := c.serviceContainer(ctx, svc.DataService)
		if err != nil {
			return nil, err
		}
		if err := c.Data().DropBinding(ctx, svc.DataService, id, rel.Username, rel.Databases); err != nil {
			return nil, err
		}
		r.Info(ctx, "dropped %s databases %v and user %s", rel.Service, rel.Databases, rel.Username)
	}
	if err := r.Phase(ctx, "remove-files"); err != nil {
		return nil, err
	}
	for _, id := range ret.Artifacts.SQLiteFileIDs {
		dir := c.Layout.SQLiteFileDir(id)
		if err := platform.NoSymlinkBetween(c.Layout.SQLiteDir(), dir); err != nil {
			return nil, err
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
	}
	home := c.Layout.AppHome(ret.Slug)
	if err := platform.NoSymlinkBetween(c.Layout.HomesDir(), home); err != nil {
		return nil, err
	}
	// Only remove the home if it still belongs to the retired incarnation.
	if raw, err := os.ReadFile(c.Layout.HomeSidecar(ret.Slug)); err == nil {
		var sc HomeSidecar
		if json.Unmarshal(raw, &sc) == nil && sc.AppID == ret.AppID {
			if err := os.RemoveAll(home); err != nil {
				return nil, err
			}
		} else {
			r.Warn(ctx, "home %s belongs to another incarnation; left in place", home)
		}
	}
	if err := store.MarkPruned(ctx, c.Store.DB(), ret.AppID); err != nil {
		return nil, err
	}
	return map[string]any{"pruned": true, "uidReclaimed": false}, nil
}

// handleReconcile converges one app toward durable intent. It is submitted
// by the reconciler only.
func (c *Controller) handleReconcile(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if !app.Provisioned {
		return map[string]any{"skipped": "not provisioned"}, nil
	}
	if app.DesiredRuntime == domain.DesiredStopped {
		return nil, c.stopInstance(ctx, r, app)
	}
	// A running instance whose shape is current only needs its generated
	// config refreshed: apply template changes through validated scoped
	// reloads instead of recreating.
	obs, err := c.observe(ctx, app)
	if err != nil {
		return nil, err
	}
	if obs.Exists && obs.Running && len(obs.Duplicates) == 0 {
		in, ch, err := c.materialize(ctx, r, app)
		if err != nil {
			return nil, err
		}
		if _, gen := runtime.AppContainerSpec(in, true); obs.Generation == gen {
			if err := c.scopedReloads(ctx, r, app, obs.ContainerID, ch); err != nil {
				return nil, err
			}
			return map[string]any{"reloaded": map[string]bool{"frontend": ch.Frontend, "pool": ch.Pool, "scheduler": ch.Scheduler}}, nil
		}
	}
	id, gen, err := c.ensureInstance(ctx, r, app, false)
	if err != nil {
		return nil, err
	}
	if err := c.waitReady(ctx, r, app, gen); err != nil {
		return nil, err
	}
	if app.Publication == domain.Published {
		if err := c.applyEdge(ctx, r); err != nil {
			return nil, err
		}
	}
	return map[string]any{"containerId": id, "generation": gen}, nil
}

func (c *Controller) handlePermissions(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	var req struct {
		Mode string `json:"mode"`
	}
	_ = r.Decode(&req)
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	home := c.Layout.AppHome(app.Slug)
	if err := c.verifyHome(app); err != nil {
		var oe *OpError
		if !(errors.As(err, &oe) && oe.Code == "home-owner") {
			return nil, err
		}
	}
	var issues []platform.PermissionIssue
	switch req.Mode {
	case "check", "dry-run":
		issues, err = platform.ChownTree(home, owner, true, 500000, c.Layout.HomeSidecar(app.Slug))
	case "shallow":
		if err := os.Lchown(home, app.UID, app.GID); err != nil {
			return nil, err
		}
	case "recursive":
		// The root-owned identity sidecar is never re-owned.
		issues, err = platform.ChownTree(home, owner, false, 2000000, c.Layout.HomeSidecar(app.Slug))
	default:
		return nil, Fail("invalid-mode", "", "mode must be check, dry-run, shallow, or recursive")
	}
	if err != nil {
		return nil, err
	}
	if len(issues) > 200 {
		issues = issues[:200]
	}
	return map[string]any{"mode": req.Mode, "issues": issues}, nil
}

// PlannedGeneration computes the intended fingerprint without writing files
// or building images. ok is false when the managed image does not exist yet.
func (c *Controller) PlannedGeneration(ctx context.Context, app domain.App) (string, bool, error) {
	spec, err := runtime.PlanImage(app.Runtime.ImageKey())
	if err != nil {
		return "", false, err
	}
	imageID, ok, err := c.Engine.ImageID(ctx, spec.Tag())
	if err != nil || !ok {
		return "", false, err
	}
	passwd, group, err := c.Images.IdentityBase(ctx, imageID)
	if err != nil {
		return "", false, err
	}
	ns, err := c.NetworkPlan(ctx)
	if err != nil {
		return "", false, err
	}
	_, _, m, err := runtime.RenderAppConfig(app, runtime.AppContext{Layout: c.Layout, TrustedProxies: ns.TrustedProxies(), ImagePasswd: passwd, ImageGroup: group})
	if err != nil {
		return "", false, err
	}
	return runtime.Fingerprint(runtime.AppInputs{App: app, Names: c.Names, Layout: c.Layout, ImageID: imageID, Materialized: m}), true, nil
}

// AppConfigDrift reports whether generated config on disk differs from what
// the embedded templates render now. It never writes.
func (c *Controller) AppConfigDrift(ctx context.Context, app domain.App) (bool, error) {
	spec, err := runtime.PlanImage(app.Runtime.ImageKey())
	if err != nil {
		return false, err
	}
	imageID, ok, err := c.Engine.ImageID(ctx, spec.Tag())
	if err != nil || !ok {
		return !ok, err
	}
	passwd, group, err := c.Images.IdentityBase(ctx, imageID)
	if err != nil {
		return false, err
	}
	ns, err := c.NetworkPlan(ctx)
	if err != nil {
		return false, err
	}
	return runtime.AppConfigDrift(app, runtime.AppContext{Layout: c.Layout, TrustedProxies: ns.TrustedProxies(), ImagePasswd: passwd, ImageGroup: group})
}

// Observe exposes observed state to status readers.
func (c *Controller) Observe(ctx context.Context, app domain.App) (Observation, error) {
	return c.observe(ctx, app)
}

// CheckReady exposes a single readiness evaluation.
func (c *Controller) CheckReady(ctx context.Context, app domain.App) ReadyCheck {
	gen, _, _ := c.PlannedGeneration(ctx, app)
	return c.checkReady(ctx, app, gen)
}
