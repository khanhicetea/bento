package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

// Fake is an in-memory Engine for unit tests. It models container existence,
// state, labels, and restart policy; it is not runtime evidence.
type Fake struct {
	mu         sync.Mutex
	seq        int
	Containers map[string]*FakeContainer
	Networks   map[string]NetworkInfo
	Volumes    map[string]VolumeInfo
	Images     map[string]string
	// ImageLabels holds labels by image ID (set by BuildImage).
	ImageLabels map[string]map[string]string
	// ExecHook answers Exec calls; default exits 0.
	ExecHook func(id string, req ExecRequest) ExecResult
	// FailOn makes the named method return an error once (fault injection).
	FailOn map[string]error
	Calls  []string
}

type FakeContainer struct {
	ID      string
	Name    string
	Spec    ContainerSpec
	Running bool
	Health  string
	IP      netip.Addr
	// Created is reported by List; State overrides the exited state List
	// reports for a stopped container (e.g. "created").
	Created time.Time
	State   string
}

func NewFake() *Fake {
	return &Fake{Containers: map[string]*FakeContainer{}, Networks: map[string]NetworkInfo{}, Volumes: map[string]VolumeInfo{}, Images: map[string]string{}, ImageLabels: map[string]map[string]string{}}
}

func (f *Fake) record(call string) error {
	f.Calls = append(f.Calls, call)
	name := strings.SplitN(call, " ", 2)[0]
	if err, ok := f.FailOn[name]; ok {
		delete(f.FailOn, name)
		return err
	}
	return nil
}

func (f *Fake) find(nameOrID string) *FakeContainer {
	for _, c := range f.Containers {
		if c.ID == nameOrID || c.Name == nameOrID {
			return c
		}
	}
	return nil
}

func (f *Fake) Version(context.Context) (VersionInfo, error) {
	return VersionInfo{ServerVersion: "fake", APIVersion: "1.51", Arch: "amd64"}, nil
}

func (f *Fake) ImageID(_ context.Context, ref string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.Images[ref]
	return id, ok, nil
}

func (f *Fake) PullImage(_ context.Context, ref string, _ func(string)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("PullImage " + ref); err != nil {
		return err
	}
	f.Images[ref] = "sha256:" + fmt.Sprintf("%x", len(ref)*7919)
	return nil
}

func (f *Fake) BuildImage(_ context.Context, tag string, r io.Reader, _, labels map[string]string, _ func(string)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("BuildImage " + tag); err != nil {
		return "", err
	}
	_, _ = io.Copy(io.Discard, r)
	id := "sha256:built-" + tag
	f.Images[tag] = id
	f.ImageLabels[id] = labels
	return id, nil
}

func (f *Fake) ReadImageFile(_ context.Context, _, path string, _ map[string]string) ([]byte, error) {
	if path == "/etc/passwd" {
		return []byte("root:x:0:0:root:/root:/bin/bash\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\n"), nil
	}
	return []byte("root:x:0:\nnogroup:x:65534:\n"), nil
}

func (f *Fake) EnsureNetwork(_ context.Context, spec NetworkSpec) (NetworkInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.Networks[spec.Name]; ok {
		return n, nil
	}
	n := NetworkInfo{ID: "net-" + spec.Name, Name: spec.Name, Internal: spec.Internal, Labels: spec.Labels, Subnets: []netip.Prefix{spec.Subnet}}
	f.Networks[spec.Name] = n
	return n, nil
}

func (f *Fake) UsedSubnets(context.Context) ([]netip.Prefix, error) { return nil, nil }

func (f *Fake) InspectNetwork(_ context.Context, name string) (*NetworkInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.Networks[name]; ok {
		return &n, nil
	}
	return nil, nil
}

func (f *Fake) Inspect(_ context.Context, nameOrID string) (*container.InspectResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(nameOrID)
	if c == nil {
		return nil, nil
	}
	status := container.StateExited
	if c.Running {
		status = container.StateRunning
	}
	res := &container.InspectResponse{
		ID: c.ID, Name: "/" + c.Name, Config: c.Spec.Config, HostConfig: c.Spec.HostConfig,
		State:           &container.State{Status: status, Running: c.Running},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{}},
	}
	if c.Spec.HostConfig != nil {
		for _, m := range c.Spec.HostConfig.Mounts {
			mp := container.MountPoint{Type: m.Type, Destination: m.Target, Source: m.Source}
			if m.Type == mount.TypeVolume {
				mp.Name = m.Source
			}
			res.Mounts = append(res.Mounts, mp)
		}
	}
	if c.Health != "" {
		res.State.Health = &container.Health{Status: container.HealthStatus(c.Health)}
	}
	if c.Spec.Networking != nil {
		for name := range c.Spec.Networking.EndpointsConfig {
			res.NetworkSettings.Networks[name] = &network.EndpointSettings{IPAddress: c.IP}
		}
	}
	return res, nil
}

func (f *Fake) List(_ context.Context, labels map[string]string) ([]container.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []container.Summary
outer:
	for _, c := range f.Containers {
		for k, v := range labels {
			got, ok := c.Spec.Config.Labels[k]
			if !ok || (v != "" && got != v) {
				continue outer
			}
		}
		state := container.StateExited
		if c.Running {
			state = container.StateRunning
		}
		if c.State != "" && !c.Running {
			state = container.ContainerState(c.State)
		}
		sum := container.Summary{ID: c.ID, Names: []string{"/" + c.Name}, Image: c.Spec.Config.Image, ImageID: f.Images[c.Spec.Config.Image],
			Labels: c.Spec.Config.Labels, State: state, Created: c.Created.Unix(), NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{}}}
		if c.Spec.HostConfig != nil {
			for _, m := range c.Spec.HostConfig.Mounts {
				sum.Mounts = append(sum.Mounts, container.MountPoint{Type: m.Type, Name: m.Source, Source: m.Source, Destination: m.Target})
			}
		}
		if c.Spec.Networking != nil {
			for name := range c.Spec.Networking.EndpointsConfig {
				sum.NetworkSettings.Networks[name] = &network.EndpointSettings{}
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

func (f *Fake) Create(_ context.Context, spec ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Create " + spec.Name); err != nil {
		return "", err
	}
	if f.find(spec.Name) != nil {
		return "", fmt.Errorf("Conflict. The container name %q is already in use", spec.Name)
	}
	f.seq++
	id := fmt.Sprintf("c%04d", f.seq)
	f.Containers[id] = &FakeContainer{ID: id, Name: spec.Name, Spec: spec, Created: time.Now(), IP: netip.AddrFrom4([4]byte{10, 211, 0, byte(100 + f.seq)})}
	return id, nil
}

func (f *Fake) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Start " + id); err != nil {
		return err
	}
	c := f.find(id)
	if c == nil {
		return fmt.Errorf("no such container %s", id)
	}
	c.Running = true
	c.Health = "healthy"
	return nil
}

func (f *Fake) Stop(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Stop " + id); err != nil {
		return err
	}
	if c := f.find(id); c != nil {
		c.Running = false
		c.Health = ""
	}
	return nil
}

func (f *Fake) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Remove " + id); err != nil {
		return err
	}
	if c := f.find(id); c != nil {
		delete(f.Containers, c.ID)
	}
	return nil
}

func (f *Fake) Signal(_ context.Context, id, sig string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record("Signal " + id + " " + sig)
}

func (f *Fake) SetRestartPolicy(_ context.Context, id string, p container.RestartPolicyMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(id); c != nil {
		c.Spec.HostConfig.RestartPolicy.Name = p
	}
	return f.record("SetRestartPolicy " + id + " " + string(p))
}

func (f *Fake) Wait(context.Context, string) (int64, error) { return 0, nil }

func (f *Fake) Exec(_ context.Context, id string, req ExecRequest) (ExecResult, error) {
	f.mu.Lock()
	hook := f.ExecHook
	err := f.record("Exec " + id + " " + strings.Join(req.Cmd, " "))
	f.mu.Unlock()
	if err != nil {
		return ExecResult{}, err
	}
	if req.Stdin != nil {
		// Drain like a real exec; hooks see the bytes that were sent.
		b, _ := io.ReadAll(req.Stdin)
		req.Stdin = bytes.NewReader(b)
	}
	if hook != nil {
		return hook(id, req), nil
	}
	return ExecResult{}, nil
}

func (f *Fake) ExecAttach(context.Context, string, ExecRequest, uint, uint) (*ExecSession, error) {
	return nil, fmt.Errorf("fake engine does not support attach")
}
func (f *Fake) ExecResize(context.Context, string, uint, uint) error { return nil }
func (f *Fake) ExecExitCode(context.Context, string) (int, bool, error) {
	return 0, true, nil
}

func (f *Fake) Logs(context.Context, string, string, bool, string) (io.ReadCloser, bool, error) {
	return io.NopCloser(strings.NewReader("")), true, nil
}

func (f *Fake) Events(ctx context.Context, _ map[string]string) (<-chan events.Message, <-chan error) {
	msgs := make(chan events.Message)
	errs := make(chan error, 1)
	go func() {
		<-ctx.Done()
		errs <- ctx.Err()
	}()
	return msgs, errs
}

func (f *Fake) VolumeCreate(_ context.Context, name string, labels map[string]string) (VolumeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := VolumeInfo{Name: name, Labels: labels, Mountpoint: "/var/lib/docker/volumes/" + name}
	f.Volumes[name] = v
	return v, nil
}

func (f *Fake) VolumeInspect(_ context.Context, name string) (*VolumeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.Volumes[name]; ok {
		return &v, nil
	}
	return nil, nil
}

func (f *Fake) VolumeRemove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Volumes, name)
	return f.record("VolumeRemove " + name)
}

func (f *Fake) ListImages(context.Context) ([]ImageSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := map[string]int{}
	var out []ImageSummary
	for _, ref := range sortedKeys(f.Images) {
		id := f.Images[ref]
		if i, ok := idx[id]; ok {
			out[i].Tags = append(out[i].Tags, ref)
			continue
		}
		idx[id] = len(out)
		out = append(out, ImageSummary{ID: id, Tags: []string{ref}, Labels: f.ImageLabels[id]})
	}
	return out, nil
}

func (f *Fake) RemoveImage(_ context.Context, id string, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("RemoveImage " + id); err != nil {
		return err
	}
	for _, c := range f.Containers {
		if f.Images[c.Spec.Config.Image] == id {
			return fmt.Errorf("conflict: image %s is being used by container %s", id, c.ID)
		}
	}
	for ref, got := range f.Images {
		if got == id {
			delete(f.Images, ref)
		}
	}
	delete(f.ImageLabels, id)
	return nil
}

func (f *Fake) ListVolumes(context.Context) ([]VolumeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]VolumeInfo, 0, len(f.Volumes))
	for _, k := range sortedKeys(f.Volumes) {
		out = append(out, f.Volumes[k])
	}
	return out, nil
}

func (f *Fake) ListNetworks(context.Context) ([]NetworkSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]NetworkSummary, 0, len(f.Networks))
	for _, k := range sortedKeys(f.Networks) {
		n := f.Networks[k]
		out = append(out, NetworkSummary{ID: n.ID, Name: n.Name, Driver: "bridge", Internal: n.Internal, Labels: n.Labels, Subnets: n.Subnets})
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (f *Fake) CopyFrom(context.Context, string, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("fake engine does not support copy")
}

// SetRunning simulates an out-of-band state change (crash, manual stop).
func (f *Fake) SetRunning(nameOrID string, running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(nameOrID); c != nil {
		c.Running = running
	}
}

// Delete simulates a manual `docker rm`.
func (f *Fake) Delete(nameOrID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(nameOrID); c != nil {
		delete(f.Containers, c.ID)
	}
}

// CallCount counts recorded calls with the given method prefix.
func (f *Fake) CallCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.Calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *Fake) Stats(_ context.Context, id string) (*Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(id); c == nil || !c.Running {
		return nil, nil
	}
	return &Stats{OnlineCPUs: 1, PIDs: 1, SampledAt: time.Now()}, nil
}

func (f *Fake) Top(_ context.Context, id string) ([]Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(id); c == nil || !c.Running {
		return nil, nil
	}
	return []Process{{PID: "1", PPID: "0", User: "root", Command: "init"}}, nil
}
