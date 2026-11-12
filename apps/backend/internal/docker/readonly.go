package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
)

// ErrReadOnly is returned by every call of a ReadOnly engine that would change
// Docker state.
var ErrReadOnly = errors.New("read-only engine: this call would change Docker state")

// ReadOnly wraps an Engine so that only calls that observe Docker reach it.
// Every method is spelled out rather than embedded, so a new Engine method
// does not compile here until someone decides whether it may pass through.
type ReadOnly struct {
	E Engine
}

var _ Engine = ReadOnly{}

func refuse(call string) error { return fmt.Errorf("%w (%s)", ErrReadOnly, call) }

func (r ReadOnly) Version(ctx context.Context) (VersionInfo, error) { return r.E.Version(ctx) }

func (r ReadOnly) ImageID(ctx context.Context, ref string) (string, bool, error) {
	return r.E.ImageID(ctx, ref)
}

func (r ReadOnly) PullImage(context.Context, string, func(string)) error { return refuse("pull image") }

func (r ReadOnly) BuildImage(
	context.Context, string, io.Reader, map[string]string, map[string]string, BuildOptions, func(string),
) (string, error) {
	return "", refuse("build image")
}

// ReadImageFile creates a throwaway container on the real engine.
func (r ReadOnly) ReadImageFile(context.Context, string, string, map[string]string) ([]byte, error) {
	return nil, refuse("read image file")
}

func (r ReadOnly) EnsureNetwork(context.Context, NetworkSpec) (NetworkInfo, error) {
	return NetworkInfo{}, refuse("ensure network")
}

func (r ReadOnly) UsedSubnets(ctx context.Context) ([]netip.Prefix, error) {
	return r.E.UsedSubnets(ctx)
}

func (r ReadOnly) InspectNetwork(ctx context.Context, name string) (*NetworkInfo, error) {
	return r.E.InspectNetwork(ctx, name)
}

func (r ReadOnly) Inspect(ctx context.Context, nameOrID string) (*container.InspectResponse, error) {
	return r.E.Inspect(ctx, nameOrID)
}

func (r ReadOnly) List(ctx context.Context, labels map[string]string) ([]container.Summary, error) {
	return r.E.List(ctx, labels)
}

func (r ReadOnly) Create(context.Context, ContainerSpec) (string, error) { return "", refuse("create") }
func (r ReadOnly) Start(context.Context, string) error                   { return refuse("start") }
func (r ReadOnly) Stop(context.Context, string, time.Duration) error     { return refuse("stop") }
func (r ReadOnly) Remove(context.Context, string) error                  { return refuse("remove") }
func (r ReadOnly) Rename(context.Context, string, string) error          { return refuse("rename") }

func (r ReadOnly) SetRestartPolicy(context.Context, string, container.RestartPolicyMode) error {
	return refuse("set restart policy")
}

func (r ReadOnly) Wait(ctx context.Context, id string) (int64, error) { return r.E.Wait(ctx, id) }

func (r ReadOnly) Exec(context.Context, string, ExecRequest) (ExecResult, error) {
	return ExecResult{}, refuse("exec")
}

func (r ReadOnly) ExecAttach(context.Context, string, ExecRequest, uint, uint) (*ExecSession, error) {
	return nil, refuse("exec")
}

func (r ReadOnly) ExecResize(context.Context, string, uint, uint) error { return refuse("exec resize") }

func (r ReadOnly) ExecExitCode(ctx context.Context, execID string) (int, bool, error) {
	return r.E.ExecExitCode(ctx, execID)
}

func (r ReadOnly) Logs(ctx context.Context, id, tail string, follow bool, since string) (io.ReadCloser, bool, error) {
	return r.E.Logs(ctx, id, tail, follow, since)
}

func (r ReadOnly) Events(ctx context.Context, labels map[string]string) (<-chan events.Message, <-chan error) {
	return r.E.Events(ctx, labels)
}

func (r ReadOnly) VolumeCreate(context.Context, string, map[string]string) (VolumeInfo, error) {
	return VolumeInfo{}, refuse("create volume")
}

func (r ReadOnly) VolumeInspect(ctx context.Context, name string) (*VolumeInfo, error) {
	return r.E.VolumeInspect(ctx, name)
}

func (r ReadOnly) VolumeRemove(context.Context, string) error { return refuse("remove volume") }

func (r ReadOnly) ListImages(ctx context.Context) ([]ImageSummary, error) { return r.E.ListImages(ctx) }
func (r ReadOnly) ListVolumes(ctx context.Context) ([]VolumeInfo, error)  { return r.E.ListVolumes(ctx) }
func (r ReadOnly) ListNetworks(ctx context.Context) ([]NetworkSummary, error) {
	return r.E.ListNetworks(ctx)
}

func (r ReadOnly) RemoveImage(context.Context, string, []string) error { return refuse("remove image") }

func (r ReadOnly) CopyFrom(ctx context.Context, id, path string) (io.ReadCloser, error) {
	return r.E.CopyFrom(ctx, id, path)
}

func (r ReadOnly) Stats(ctx context.Context, id string) (*Stats, error)  { return r.E.Stats(ctx, id) }
func (r ReadOnly) Top(ctx context.Context, id string) ([]Process, error) { return r.E.Top(ctx, id) }
