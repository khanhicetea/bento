package runtime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// StopTimeout bounds graceful shutdown of an app instance.
const StopTimeout = 20 * time.Second

// AppInputs are resolved inputs for planning one app's containers.
type AppInputs struct {
	App          domain.App
	Names        Names
	Layout       platform.Layout
	ImageID      string
	Materialized Materialized
}

func appMounts(in AppInputs) []mount.Mount {
	app := in.App
	ms := []mount.Mount{
		{Type: mount.TypeBind, Source: in.Layout.AppHome(app.Slug), Target: app.ContainerHome()},
		{Type: mount.TypeBind, Source: in.Layout.AppConfigDir(app.ID), Target: "/etc/bento", ReadOnly: true},
		{Type: mount.TypeBind, Source: in.Layout.AppIdentityDir(app.ID) + "/passwd", Target: "/etc/passwd", ReadOnly: true},
		{Type: mount.TypeBind, Source: in.Layout.AppIdentityDir(app.ID) + "/group", Target: "/etc/group", ReadOnly: true},
	}
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite {
			ms = append(ms, mount.Mount{Type: mount.TypeBind, Source: in.Layout.SQLiteFileDir(b.SQLiteFileID), Target: b.SQLiteContainerDir()})
		}
	}
	for i := range ms {
		ms[i].BindOptions = &mount.BindOptions{Propagation: mount.PropagationRPrivate, CreateMountpoint: false}
	}
	return ms
}

func tmpfs(app domain.App) map[string]string {
	owner := fmt.Sprintf("uid=%d,gid=%d", app.UID, app.GID)
	return map[string]string{
		"/run": "rw,exec,nosuid,nodev,size=64m,mode=0755," + owner,
		"/tmp": "rw,nosuid,nodev,size=512m,mode=1777," + owner,
	}
}

func appResources(r domain.Resources) container.Resources {
	pids := int64(r.PIDs)
	return container.Resources{
		Memory:     int64(r.MemoryMB) << 20,
		MemorySwap: int64(r.MemoryMB) << 20,
		NanoCPUs:   int64(r.CPUMillis) * 1_000_000,
		PidsLimit:  &pids,
	}
}

func appEnv(app domain.App) []string {
	return []string{"HOME=" + app.ContainerHome(), "USER=" + app.Slug, "LANG=C.UTF-8"}
}

func appNetworks(in AppInputs) *network.NetworkingConfig {
	eps := map[string]*network.EndpointSettings{
		in.Names.AppsNetwork(): {Aliases: []string{AppAlias(in.App.ID)}},
	}
	if needsDataNetwork(in.App) {
		eps[in.Names.DataNetwork()] = &network.EndpointSettings{}
	}
	return &network.NetworkingConfig{EndpointsConfig: eps}
}

// needsDataNetwork: every app joins the data network because Redis ACL
// identities are provisioned for all apps.
func needsDataNetwork(domain.App) bool { return true }

func logConfig() container.LogConfig {
	return container.LogConfig{Type: "local", Config: map[string]string{"max-size": "10m", "max-file": "3"}}
}

// fingerprintInput is every non-secret, boot-static input of the persistent
// instance. Changing any of it requires recreation.
type fingerprintInput struct {
	Image                 string             `json:"image"`
	User                  string             `json:"user"`
	Env                   []string           `json:"env"`
	Mounts                []string           `json:"mounts"`
	Tmpfs                 map[string]string  `json:"tmpfs"`
	Resources             domain.Resources   `json:"resources"`
	Networks              []string           `json:"networks"`
	RuntimeEnvHash        string             `json:"runtimeEnv"`
	ArgvHash              string             `json:"argv"`
	IdentityHash          string             `json:"identity"`
	CredentialsGeneration int64              `json:"credentialsGeneration"`
	Kind                  domain.RuntimeKind `json:"kind"`
	SpecVersion           int                `json:"specVersion"`
}

// specVersion changes when the planner itself changes container shape.
const specVersion = 1

// Fingerprint returns the non-secret configuration fingerprint recorded in
// the io.bento.generation label.
func Fingerprint(in AppInputs) string {
	var mounts []string
	for _, m := range appMounts(in) {
		mounts = append(mounts, fmt.Sprintf("%s:%s:%t", m.Source, m.Target, m.ReadOnly))
	}
	sort.Strings(mounts)
	nets := []string{in.Names.AppsNetwork()}
	if needsDataNetwork(in.App) {
		nets = append(nets, in.Names.DataNetwork())
	}
	fi := fingerprintInput{
		Image: in.ImageID, User: userSpec(in.App), Env: appEnv(in.App), Mounts: mounts, Tmpfs: tmpfs(in.App),
		Resources: in.App.Resources, Networks: nets, RuntimeEnvHash: in.Materialized.RuntimeEnvHash,
		ArgvHash: in.Materialized.ArgvHash, IdentityHash: in.Materialized.IdentityHash,
		CredentialsGeneration: in.App.CredentialsGeneration, Kind: in.App.Runtime.Kind, SpecVersion: specVersion,
	}
	b, _ := json.Marshal(fi)
	return platform.SHA256Hex(b)[:20]
}

func userSpec(app domain.App) string { return strconv.Itoa(app.UID) + ":" + strconv.Itoa(app.GID) }

// AppContainerSpec plans the single persistent instance for an app.
func AppContainerSpec(in AppInputs, running bool) (docker.ContainerSpec, string) {
	app := in.App
	gen := Fingerprint(in)
	restart := container.RestartPolicyMode(container.RestartPolicyDisabled)
	if running {
		restart = container.RestartPolicyUnlessStopped
	}
	stop := int(StopTimeout.Seconds())
	cfg := &container.Config{
		Image:       in.ImageID,
		User:        userSpec(app),
		Hostname:    app.Slug,
		Env:         appEnv(app),
		WorkingDir:  app.ContainerHome(),
		StopTimeout: &stop,
		Labels: in.Names.Labels(RoleRuntime, map[string]string{
			LabelAppID: app.ID, LabelGeneration: gen, LabelImageKey: app.Runtime.ImageKey().String(),
		}),
		Healthcheck: &container.HealthConfig{
			Test:        []string{"CMD", "/usr/local/bin/bento-ready"},
			Interval:    20 * time.Second,
			Timeout:     8 * time.Second,
			StartPeriod: 90 * time.Second,
			Retries:     3,
		},
	}
	host := &container.HostConfig{
		RestartPolicy:  container.RestartPolicy{Name: restart},
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		Tmpfs:          tmpfs(app),
		Mounts:         appMounts(in),
		Resources:      appResources(app.Resources),
		LogConfig:      logConfig(),
		Init:           boolPtr(false),
		IpcMode:        "private",
		ShmSize:        64 << 20,
	}
	return docker.ContainerSpec{
		Name: in.Names.AppContainer(app.ID), Config: cfg, HostConfig: host, Networking: appNetworks(in),
	}, gen
}

// ToolContainerSpec plans an ephemeral tooling container: same image and
// identity, dedicated exec entrypoint (never /init), no daemons, no restart,
// and no backup archives. It idles until the requested exec finishes.
func ToolContainerSpec(in AppInputs, opID string, maxLifetime time.Duration) docker.ContainerSpec {
	app := in.App
	cfg := &container.Config{
		Image:      in.ImageID,
		User:       userSpec(app),
		Hostname:   app.Slug + "-tool",
		Env:        appEnv(app),
		WorkingDir: app.ContainerHome(),
		Entrypoint: []string{"/usr/local/bin/bento-exec"},
		Cmd:        []string{"sleep", strconv.Itoa(int(maxLifetime.Seconds()))},
		Labels:     in.Names.Labels(RoleTool, map[string]string{LabelAppID: app.ID, LabelOperation: opID}),
	}
	host := &container.HostConfig{
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyDisabled},
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		Tmpfs:          tmpfs(app),
		Mounts:         appMounts(in),
		Resources:      appResources(app.Resources),
		LogConfig:      logConfig(),
		Init:           boolPtr(true),
	}
	return docker.ContainerSpec{
		Name: in.Names.ToolContainer(app.ID, opID), Config: cfg, HostConfig: host, Networking: appNetworks(in),
	}
}

func boolPtr(b bool) *bool { return &b }
