// Package docker is the narrow Docker Engine SDK adapter. Domain code talks to
// the Engine interface; only this package imports the SDK client. Container
// specifications themselves use the Engine API types, produced by the
// planner in internal/runtime.
package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

// MinAPIVersion is the oldest Engine API this backend supports. Version
// negotiation selects the highest mutually supported version above it.
const MinAPIVersion = "1.44"

// ContainerSpec is a complete create request.
type ContainerSpec struct {
	Name       string
	Config     *container.Config
	HostConfig *container.HostConfig
	Networking *network.NetworkingConfig
}

type ExecRequest struct {
	User    string
	Cmd     []string
	Env     []string
	WorkDir string
	Stdin   io.Reader
	TTY     bool
	// OutputLimit bounds captured stdout and stderr (each). Zero means 1 MiB.
	OutputLimit int
	// Stdout/Stderr, when set, receive streamed output instead of capture.
	Stdout io.Writer
	Stderr io.Writer
}

type ExecResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Truncated bool
}

// ExecSession is an attached interactive exec (terminal).
type ExecSession struct {
	ID   string
	Conn net.Conn
	Read *bufio.Reader
}

type NetworkInfo struct {
	ID       string
	Name     string
	Internal bool
	Labels   map[string]string
	Subnets  []netip.Prefix
}

// NetworkSpec describes a stack-scoped user-defined bridge network.
type NetworkSpec struct {
	Name     string
	Internal bool
	Subnet   netip.Prefix
	IPRange  netip.Prefix
	Labels   map[string]string
}

type VolumeInfo struct {
	Name       string
	Mountpoint string
	Labels     map[string]string
}

type VersionInfo struct {
	ServerVersion string
	APIVersion    string
	Arch          string
}

// Engine is the capability surface used by Bento. It is deliberately not a
// generic Docker passthrough.
type Engine interface {
	Version(ctx context.Context) (VersionInfo, error)
	ImageID(ctx context.Context, ref string) (string, bool, error)
	PullImage(ctx context.Context, ref string, progress func(string)) error
	BuildImage(ctx context.Context, tag string, buildContext io.Reader, args, labels map[string]string, progress func(string)) (string, error)
	ReadImageFile(ctx context.Context, image, path string, labels map[string]string) ([]byte, error)
	EnsureNetwork(ctx context.Context, spec NetworkSpec) (NetworkInfo, error)
	UsedSubnets(ctx context.Context) ([]netip.Prefix, error)
	InspectNetwork(ctx context.Context, name string) (*NetworkInfo, error)
	Inspect(ctx context.Context, nameOrID string) (*container.InspectResponse, error)
	List(ctx context.Context, labels map[string]string) ([]container.Summary, error)
	Create(ctx context.Context, spec ContainerSpec) (string, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Remove(ctx context.Context, id string) error
	Signal(ctx context.Context, id, signal string) error
	SetRestartPolicy(ctx context.Context, id string, policy container.RestartPolicyMode) error
	Wait(ctx context.Context, id string) (int64, error)
	Exec(ctx context.Context, id string, req ExecRequest) (ExecResult, error)
	ExecAttach(ctx context.Context, id string, req ExecRequest, height, width uint) (*ExecSession, error)
	ExecResize(ctx context.Context, execID string, height, width uint) error
	ExecExitCode(ctx context.Context, execID string) (int, bool, error)
	Logs(ctx context.Context, id string, tail string, follow bool, since string) (io.ReadCloser, bool, error)
	Events(ctx context.Context, labels map[string]string) (<-chan events.Message, <-chan error)
	VolumeCreate(ctx context.Context, name string, labels map[string]string) (VolumeInfo, error)
	VolumeInspect(ctx context.Context, name string) (*VolumeInfo, error)
	// VolumeRemove is used only to undo volumes created by a failed import.
	VolumeRemove(ctx context.Context, name string) error
	CopyFrom(ctx context.Context, id, path string) (io.ReadCloser, error)
	Stats(ctx context.Context, id string) (*Stats, error)
	Top(ctx context.Context, id string) ([]Process, error)
}

// SDK implements Engine with the moby client.
type SDK struct {
	c *client.Client
}

func NewSDK() (*SDK, error) {
	c, err := client.New(client.WithHostFromEnv(), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &SDK{c: c}, nil
}

func (s *SDK) Close() error { return s.c.Close() }

func IsNotFound(err error) bool { return cerrdefs.IsNotFound(err) }

func (s *SDK) Version(ctx context.Context) (VersionInfo, error) {
	v, err := s.c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return VersionInfo{}, err
	}
	if versionLess(v.APIVersion, MinAPIVersion) {
		return VersionInfo{}, fmt.Errorf("docker engine API %s is older than the supported minimum %s", v.APIVersion, MinAPIVersion)
	}
	return VersionInfo{ServerVersion: v.Version, APIVersion: v.APIVersion, Arch: v.Arch}, nil
}

func versionLess(a, b string) bool {
	var amaj, amin, bmaj, bmin int
	fmt.Sscanf(a, "%d.%d", &amaj, &amin)
	fmt.Sscanf(b, "%d.%d", &bmaj, &bmin)
	return amaj < bmaj || (amaj == bmaj && amin < bmin)
}

func (s *SDK) ImageID(ctx context.Context, ref string) (string, bool, error) {
	res, err := s.c.ImageInspect(ctx, ref)
	if IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return res.ID, true, nil
}

func (s *SDK) PullImage(ctx context.Context, ref string, progress func(string)) error {
	resp, err := s.c.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer resp.Close()
	last := ""
	for msg, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return err
		}
		if msg.Error != nil {
			return fmt.Errorf("pull %s: %s", ref, msg.Error.Message)
		}
		if progress != nil && msg.Status != "" && msg.Status != last && msg.Progress == nil {
			last = msg.Status
			progress(msg.Status)
		}
	}
	return nil
}

type buildLine struct {
	Stream string `json:"stream"`
	Error  string `json:"error"`
	Aux    *struct {
		ID string `json:"ID"`
	} `json:"aux"`
}

func (s *SDK) BuildImage(ctx context.Context, tag string, buildContext io.Reader, args, labels map[string]string, progress func(string)) (string, error) {
	buildArgs := map[string]*string{}
	for k, v := range args {
		buildArgs[k] = &v
	}
	res, err := s.c.ImageBuild(ctx, buildContext, client.ImageBuildOptions{
		Tags: []string{tag}, Remove: true, ForceRemove: true, PullParent: false,
		BuildArgs: buildArgs, Labels: labels, Version: build.BuilderV1, Dockerfile: "Dockerfile",
	})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	var imageID string
	var tail []string
	for {
		var line buildLine
		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
		if line.Error != "" {
			return "", fmt.Errorf("build %s failed: %s\n%s", tag, line.Error, strings.Join(tail, ""))
		}
		if line.Aux != nil && line.Aux.ID != "" {
			imageID = line.Aux.ID
		}
		if line.Stream != "" {
			tail = append(tail, line.Stream)
			if len(tail) > 40 {
				tail = tail[1:]
			}
			if progress != nil && strings.HasPrefix(line.Stream, "Step ") {
				progress(strings.TrimSpace(line.Stream))
			}
		}
	}
	if imageID == "" {
		id, ok, err := s.ImageID(ctx, tag)
		if err != nil || !ok {
			return "", fmt.Errorf("build %s produced no image", tag)
		}
		imageID = id
	}
	return imageID, nil
}

// ReadImageFile reads one regular file from an image by creating (never
// starting) a throwaway container carrying the caller's ownership labels.
func (s *SDK) ReadImageFile(ctx context.Context, image, path string, labels map[string]string) ([]byte, error) {
	created, err := s.c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: image, Entrypoint: []string{"/bin/true"}, Labels: labels},
		HostConfig: &container.HostConfig{NetworkMode: "none"},
	})
	if err != nil {
		return nil, err
	}
	defer s.c.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
	rc, err := s.CopyFrom(ctx, created.ID, path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("read %s from %s: %w", path, image, err)
		}
		if h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 4<<20))
		}
	}
}

func (s *SDK) EnsureNetwork(ctx context.Context, spec NetworkSpec) (NetworkInfo, error) {
	if existing, err := s.InspectNetwork(ctx, spec.Name); err != nil {
		return NetworkInfo{}, err
	} else if existing != nil {
		return *existing, nil
	}
	opts := client.NetworkCreateOptions{Driver: "bridge", Internal: spec.Internal, Labels: spec.Labels}
	if spec.Subnet.IsValid() {
		cfg := network.IPAMConfig{Subnet: spec.Subnet}
		if spec.IPRange.IsValid() {
			cfg.IPRange = spec.IPRange
		}
		opts.IPAM = &network.IPAM{Driver: "default", Config: []network.IPAMConfig{cfg}}
	}
	res, err := s.c.NetworkCreate(ctx, spec.Name, opts)
	if err != nil {
		return NetworkInfo{}, err
	}
	return NetworkInfo{ID: res.ID, Name: spec.Name, Internal: spec.Internal, Labels: spec.Labels, Subnets: []netip.Prefix{spec.Subnet}}, nil
}

// UsedSubnets lists IPv4 subnets of every Docker network on the host.
func (s *SDK) UsedSubnets(ctx context.Context) ([]netip.Prefix, error) {
	res, err := s.c.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, n := range res.Items {
		for _, c := range n.IPAM.Config {
			if c.Subnet.IsValid() {
				out = append(out, c.Subnet)
			}
		}
	}
	return out, nil
}

func (s *SDK) InspectNetwork(ctx context.Context, name string) (*NetworkInfo, error) {
	res, err := s.c.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := &NetworkInfo{ID: res.Network.ID, Name: res.Network.Name, Internal: res.Network.Internal, Labels: res.Network.Labels}
	for _, c := range res.Network.IPAM.Config {
		if c.Subnet.IsValid() {
			info.Subnets = append(info.Subnets, c.Subnet)
		}
	}
	return info, nil
}

func (s *SDK) Inspect(ctx context.Context, nameOrID string) (*container.InspectResponse, error) {
	res, err := s.c.ContainerInspect(ctx, nameOrID, client.ContainerInspectOptions{})
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &res.Container, nil
}

func labelFilters(labels map[string]string) client.Filters {
	f := make(client.Filters)
	for k, v := range labels {
		if v == "" {
			f.Add("label", k)
		} else {
			f.Add("label", k+"="+v)
		}
	}
	return f
}

func (s *SDK) List(ctx context.Context, labels map[string]string) ([]container.Summary, error) {
	res, err := s.c.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: labelFilters(labels)})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (s *SDK) Create(ctx context.Context, spec ContainerSpec) (string, error) {
	res, err := s.c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: spec.Name, Config: spec.Config, HostConfig: spec.HostConfig, NetworkingConfig: spec.Networking,
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

func (s *SDK) Start(ctx context.Context, id string) error {
	_, err := s.c.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

func (s *SDK) Stop(ctx context.Context, id string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	_, err := s.c.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &secs})
	return err
}

// Remove removes a container. Anonymous volumes are never removed.
func (s *SDK) Remove(ctx context.Context, id string) error {
	_, err := s.c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: false})
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (s *SDK) Signal(ctx context.Context, id, signal string) error {
	_, err := s.c.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: signal})
	return err
}

func (s *SDK) SetRestartPolicy(ctx context.Context, id string, policy container.RestartPolicyMode) error {
	_, err := s.c.ContainerUpdate(ctx, id, client.ContainerUpdateOptions{RestartPolicy: &container.RestartPolicy{Name: policy}})
	return err
}

func (s *SDK) Wait(ctx context.Context, id string) (int64, error) {
	res := s.c.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case r := <-res.Result:
		if r.Error != nil {
			return r.StatusCode, errors.New(r.Error.Message)
		}
		return r.StatusCode, nil
	case err := <-res.Error:
		return -1, err
	}
}

// limitedBuffer keeps at most max bytes and records truncation.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	room := l.max - l.buf.Len()
	if room <= 0 {
		l.truncated = l.truncated || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		l.buf.Write(p[:room])
		l.truncated = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (s *SDK) Exec(ctx context.Context, id string, req ExecRequest) (ExecResult, error) {
	created, err := s.c.ExecCreate(ctx, id, client.ExecCreateOptions{
		User: req.User, Cmd: req.Cmd, Env: req.Env, WorkingDir: req.WorkDir, TTY: req.TTY,
		AttachStdin: req.Stdin != nil, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, err
	}
	att, err := s.c.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: req.TTY})
	if err != nil {
		return ExecResult{}, err
	}
	defer att.Close()
	limit := req.OutputLimit
	if limit <= 0 {
		limit = 1 << 20
	}
	stdout := &limitedBuffer{max: limit}
	stderr := &limitedBuffer{max: limit}
	var outW, errW io.Writer = stdout, stderr
	if req.Stdout != nil {
		outW = req.Stdout
	}
	if req.Stderr != nil {
		errW = req.Stderr
	}
	stdinErr := make(chan error, 1)
	if req.Stdin != nil {
		go func() {
			_, err := io.Copy(att.Conn, req.Stdin)
			if cw, ok := att.Conn.(interface{ CloseWrite() error }); ok {
				cw.CloseWrite()
			}
			stdinErr <- err
		}()
	}
	done := make(chan error, 1)
	go func() {
		if req.TTY {
			_, err := io.Copy(outW, att.Reader)
			done <- err
			return
		}
		_, err := stdcopy.StdCopy(outW, errW, att.Reader)
		done <- err
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		return ExecResult{}, ctx.Err()
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return ExecResult{}, err
	}
	if req.Stdin != nil {
		select {
		case e := <-stdinErr:
			if e != nil && !errors.Is(e, net.ErrClosed) {
				return ExecResult{}, fmt.Errorf("exec stdin: %w", e)
			}
		case <-time.After(5 * time.Second):
		}
	}
	for i := 0; i < 50; i++ {
		ins, err := s.c.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return ExecResult{}, err
		}
		if !ins.Running {
			return ExecResult{ExitCode: ins.ExitCode, Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes(),
				Truncated: stdout.truncated || stderr.truncated}, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ExecResult{}, fmt.Errorf("exec did not finish")
}

func (s *SDK) ExecAttach(ctx context.Context, id string, req ExecRequest, height, width uint) (*ExecSession, error) {
	created, err := s.c.ExecCreate(ctx, id, client.ExecCreateOptions{
		User: req.User, Cmd: req.Cmd, Env: req.Env, WorkingDir: req.WorkDir, TTY: true,
		AttachStdin: true, AttachStdout: true, AttachStderr: true,
		ConsoleSize: client.ConsoleSize{Height: height, Width: width},
	})
	if err != nil {
		return nil, err
	}
	att, err := s.c.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: client.ConsoleSize{Height: height, Width: width}})
	if err != nil {
		return nil, err
	}
	return &ExecSession{ID: created.ID, Conn: att.Conn, Read: att.Reader}, nil
}

func (s *SDK) ExecResize(ctx context.Context, execID string, height, width uint) error {
	_, err := s.c.ExecResize(ctx, execID, client.ExecResizeOptions{Height: height, Width: width})
	return err
}

func (s *SDK) ExecExitCode(ctx context.Context, execID string) (int, bool, error) {
	ins, err := s.c.ExecInspect(ctx, execID, client.ExecInspectOptions{})
	if err != nil {
		return 0, false, err
	}
	return ins.ExitCode, !ins.Running, nil
}

func (s *SDK) Logs(ctx context.Context, id string, tail string, follow bool, since string) (io.ReadCloser, bool, error) {
	ins, err := s.Inspect(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if ins == nil {
		return nil, false, fmt.Errorf("container not found")
	}
	rc, err := s.c.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: tail, Follow: follow, Since: since, Timestamps: true,
	})
	if err != nil {
		return nil, false, err
	}
	return rc, ins.Config != nil && ins.Config.Tty, nil
}

func (s *SDK) Events(ctx context.Context, labels map[string]string) (<-chan events.Message, <-chan error) {
	f := labelFilters(labels)
	f.Add("type", "container")
	res := s.c.Events(ctx, client.EventsListOptions{Filters: f})
	return res.Messages, res.Err
}

func (s *SDK) VolumeCreate(ctx context.Context, name string, labels map[string]string) (VolumeInfo, error) {
	res, err := s.c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Driver: "local", Labels: labels})
	if err != nil {
		return VolumeInfo{}, err
	}
	return volumeInfo(res.Volume), nil
}

func volumeInfo(v volume.Volume) VolumeInfo {
	return VolumeInfo{Name: v.Name, Mountpoint: v.Mountpoint, Labels: v.Labels}
}

func (s *SDK) VolumeInspect(ctx context.Context, name string) (*VolumeInfo, error) {
	res, err := s.c.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v := volumeInfo(res.Volume)
	return &v, nil
}

func (s *SDK) VolumeRemove(ctx context.Context, name string) error {
	_, err := s.c.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	return err
}

func (s *SDK) CopyFrom(ctx context.Context, id, path string) (io.ReadCloser, error) {
	res, err := s.c.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: path})
	if err != nil {
		return nil, err
	}
	return res.Content, nil
}

// Stats is a single resource-usage sample for a container.
type Stats struct {
	CPUPercent  float64
	OnlineCPUs  uint32
	MemoryUsage uint64
	MemoryLimit uint64
	NetworkRx   uint64
	NetworkTx   uint64
	BlockRead   uint64
	BlockWrite  uint64
	PIDs        uint64
	SampledAt   time.Time
}

// Process is one row of a container's process table.
type Process struct {
	PID        string
	PPID       string
	User       string
	CPUPercent float64
	MemPercent float64
	RSSKiB     uint64
	Elapsed    string
	Command    string
}

// Stats takes a two-point sample (about one second) so CPU usage is a real
// rate. Returns nil when the container is missing or not running.
func (s *SDK) Stats(ctx context.Context, id string) (*Stats, error) {
	res, err := s.c.ContainerStats(ctx, id, client.ContainerStatsOptions{IncludePreviousSample: true})
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var raw container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.Read.IsZero() || raw.PidsStats.Current == 0 {
		return nil, nil
	}
	return statsFrom(raw), nil
}

func statsFrom(raw container.StatsResponse) *Stats {
	st := &Stats{
		OnlineCPUs: raw.CPUStats.OnlineCPUs, MemoryLimit: raw.MemoryStats.Limit,
		PIDs: raw.PidsStats.Current, SampledAt: raw.Read,
	}
	if st.OnlineCPUs == 0 {
		st.OnlineCPUs = uint32(len(raw.CPUStats.CPUUsage.PercpuUsage))
	}
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage) - float64(raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemUsage) - float64(raw.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		st.CPUPercent = cpuDelta / sysDelta * float64(st.OnlineCPUs) * 100
	}
	// Match `docker stats`: exclude reclaimable page cache.
	mem := raw.MemoryStats.Usage
	cache := raw.MemoryStats.Stats["inactive_file"]
	if cache == 0 {
		cache = raw.MemoryStats.Stats["total_inactive_file"]
	}
	if cache < mem {
		mem -= cache
	}
	st.MemoryUsage = mem
	for _, n := range raw.Networks {
		st.NetworkRx += n.RxBytes
		st.NetworkTx += n.TxBytes
	}
	for _, e := range raw.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(e.Op) {
		case "read":
			st.BlockRead += e.Value
		case "write":
			st.BlockWrite += e.Value
		}
	}
	return st
}

var topColumns = []string{"pid", "ppid", "user", "pcpu", "pmem", "rss", "etime", "args"}

// Top lists the container's processes. Returns nil when the container is
// missing or not running.
func (s *SDK) Top(ctx context.Context, id string) ([]Process, error) {
	res, err := s.c.ContainerTop(ctx, id, client.ContainerTopOptions{Arguments: []string{"-o", strings.Join(topColumns, ",")}})
	if IsNotFound(err) || (err != nil && strings.Contains(err.Error(), "is not running")) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(res.Processes))
	for _, row := range res.Processes {
		if len(row) < len(topColumns) {
			continue
		}
		p := Process{PID: row[0], PPID: row[1], User: row[2], Elapsed: row[6], Command: strings.Join(row[7:], " ")}
		p.CPUPercent, _ = strconv.ParseFloat(row[3], 64)
		p.MemPercent, _ = strconv.ParseFloat(row[4], 64)
		p.RSSKiB, _ = strconv.ParseUint(row[5], 10, 64)
		out = append(out, p)
	}
	return out, nil
}
