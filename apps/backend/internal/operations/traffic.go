package operations

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/traffic"
)

// Host traffic reports are housekeeping, not operations: the edge writes a
// filtered access log per host (traffic/logs/<host>.log) and RunTraffic, once
// per traffic.Interval, rotates those files into each host's pending
// directory, has the edge reopen its logs (USR1), and runs GoAccess on the
// closed files. Nothing here changes desired or runtime state; a failure is
// logged and shown on the host's report, and the next run retries.

// trafficReopenWait lets edge workers reopen their log files after USR1
// before the rotated files are read.
var trafficReopenWait = 2 * time.Second

// trafficReopenTimeout bounds the edge calls the traffic cycle makes while
// holding edgeMu. The cycle runs on the backend's lifetime context, which
// nothing cancels: a stalled exec would otherwise block every edge change
// until a restart. A missed reopen is retried by the next cycle.
var trafficReopenTimeout = 30 * time.Second

// trafficRunTimeout bounds one host's GoAccess run.
const trafficRunTimeout = 10 * time.Minute

// trafficMaxFiles bounds how many pending logs one run reads; the rest wait
// for the next run.
const trafficMaxFiles = 48

// trafficRefreshAge is how old a report may get with no new logs before it
// is rewritten from the database, so days older than traffic.KeepDays drop.
const trafficRefreshAge = 24 * time.Hour

// TrafficHostStatus is the outcome of the last traffic run that touched a
// host. It lives in memory: after a restart it is empty until the next run.
type TrafficHostStatus struct {
	LastRun time.Time
	Error   string
}

// TrafficStatus returns the last run status for host (zero when none).
func (c *Controller) TrafficStatus(host string) TrafficHostStatus {
	c.trafficStatusMu.Lock()
	defer c.trafficStatusMu.Unlock()
	return c.trafficStatus[host]
}

func (c *Controller) setTrafficStatus(host string, at time.Time, err error) {
	c.trafficStatusMu.Lock()
	defer c.trafficStatusMu.Unlock()
	if c.trafficStatus == nil {
		c.trafficStatus = map[string]TrafficHostStatus{}
	}
	st := TrafficHostStatus{LastRun: at}
	if err != nil {
		st.Error = err.Error()
	}
	c.trafficStatus[host] = st
}

// RunTraffic runs a traffic cycle shortly after every interval boundary
// (wall clock, so each rotated file holds one clock hour) until ctx ends.
func (c *Controller) RunTraffic(ctx context.Context) {
	for {
		now := c.Now()
		next := now.Truncate(traffic.Interval).Add(traffic.Interval + 2*time.Minute)
		t := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		c.safeTrafficCycle(ctx)
	}
}

func (c *Controller) safeTrafficCycle(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			c.Log.Error("traffic cycle panicked", "panic", p, "stack", string(debug.Stack()))
		}
	}()
	if err := c.TrafficCycle(ctx); err != nil && !errors.Is(err, context.Canceled) {
		c.Log.Warn("traffic cycle", "err", err)
	}
}

// TrafficCycle rotates the edge's traffic logs and updates every host's
// report. Rotated logs are read only once the edge is known to write
// elsewhere: after a delivered USR1, or when no edge is running.
func (c *Controller) TrafficCycle(ctx context.Context) error {
	c.trafficMu.Lock()
	defer c.trafficMu.Unlock()
	if err := c.prepareTrafficDirs(); err != nil {
		return err
	}
	hosts, err := store.ListHosts(ctx, c.Store.DB())
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		known[h.Name] = true
	}
	reopened, rotateErr := c.rotateTrafficLogs(ctx)
	if rotateErr != nil {
		c.Log.Warn("traffic logs not rotated; reports wait for the next run", "err", rotateErr)
	}
	if reopened {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(trafficReopenWait):
		}
	}
	entries, err := os.ReadDir(c.Layout.TrafficHostsDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		host := e.Name()
		if !e.IsDir() || !validTrafficHost(host) {
			continue
		}
		if !known[host] {
			// The host was removed: its report goes with it.
			if err := os.RemoveAll(c.Layout.TrafficHostDir(host)); err != nil {
				c.Log.Warn("remove traffic report of a removed host", "host", host, "err", err)
			}
			continue
		}
		if rotateErr != nil {
			c.setTrafficStatus(host, c.Now(), fmt.Errorf("logs not rotated: %w", rotateErr))
			continue
		}
		if err := c.updateTrafficReport(ctx, host); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			c.Log.Warn("traffic report not updated", "host", host, "err", err)
		}
	}
	return nil
}

func (c *Controller) prepareTrafficDirs() error {
	if err := platform.EnsureDir(c.Layout.TrafficDir(), 0o700, platform.RootOwner); err != nil {
		return err
	}
	// The edge's master process (root) creates the log files and hands them
	// to its workers on reopen.
	if err := platform.EnsureDir(c.Layout.TrafficLogsDir(), 0o755, platform.RootOwner); err != nil {
		return err
	}
	return platform.EnsureDir(c.Layout.TrafficHostsDir(), 0o700, platform.RootOwner)
}

// validTrafficHost accepts only normalized host names, which are safe single
// path elements.
func validTrafficHost(name string) bool {
	n, err := domain.NormalizeDomain(name)
	return err == nil && n == name
}

// rotateTrafficLogs moves every non-empty live log into its host's pending
// directory and has a running edge reopen its logs. reopened reports that a
// USR1 was delivered (the caller lets workers switch files before reading).
// Under edgeMu, so no edge recreate interleaves with the rename and signal.
func (c *Controller) rotateTrafficLogs(ctx context.Context) (reopened bool, err error) {
	c.edgeMu.Lock()
	defer c.edgeMu.Unlock()
	entries, err := os.ReadDir(c.Layout.TrafficLogsDir())
	if err != nil {
		return false, err
	}
	name := traffic.PendingName(c.Now())
	renamed := 0
	for _, e := range entries {
		host, ok := strings.CutSuffix(e.Name(), ".log")
		if !ok || !e.Type().IsRegular() || !validTrafficHost(host) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		pending := filepath.Join(c.Layout.TrafficHostDir(host), traffic.PendingDir)
		if err := platform.EnsureDir(c.Layout.TrafficHostDir(host), 0o700, platform.RootOwner); err != nil {
			return false, err
		}
		if err := platform.EnsureDir(pending, 0o700, platform.RootOwner); err != nil {
			return false, err
		}
		dst := filepath.Join(pending, name)
		if _, err := os.Lstat(dst); err == nil {
			continue // rotated this second already
		}
		if err := os.Rename(filepath.Join(c.Layout.TrafficLogsDir(), e.Name()), dst); err != nil {
			return false, err
		}
		renamed++
	}
	// Signal also when nothing was renamed but rotated files wait: an earlier
	// run may have renamed them and then failed to deliver the signal, so the
	// edge may still write to them. Without a running edge nothing writes.
	if renamed == 0 && !c.anyTrafficPending() {
		return false, nil
	}
	ectx, cancel := context.WithTimeout(ctx, trafficReopenTimeout)
	defer cancel()
	id, running, err := c.edgeContainer(ectx)
	if err != nil {
		return false, err
	}
	if !running {
		return false, nil
	}
	if err := c.edgeControl(ectx, id, "reopen"); err != nil {
		return false, fmt.Errorf("reopen edge logs: %w", err)
	}
	return true, nil
}

// anyTrafficPending reports whether any host has rotated logs waiting.
func (c *Controller) anyTrafficPending() bool {
	hosts, _ := os.ReadDir(c.Layout.TrafficHostsDir())
	for _, h := range hosts {
		files, _ := os.ReadDir(filepath.Join(c.Layout.TrafficHostsDir(), h.Name(), traffic.PendingDir))
		if len(files) > 0 {
			return true
		}
	}
	return false
}

// updateTrafficReport adds host's pending logs to its database and writes a
// new report. Pending files are removed only after GoAccess succeeded.
func (c *Controller) updateTrafficReport(ctx context.Context, host string) (err error) {
	dir := c.Layout.TrafficHostDir(host)
	pending, err := c.pendingTrafficLogs(host)
	if err != nil {
		c.setTrafficStatus(host, c.Now(), err)
		return err
	}
	if len(pending) == 0 {
		info, err := os.Stat(filepath.Join(dir, traffic.ReportFile))
		if err == nil && c.Now().Sub(info.ModTime()) < trafficRefreshAge {
			return nil
		}
		if _, err := os.Stat(filepath.Join(dir, traffic.DBDir)); err != nil {
			return nil // nothing logged yet
		}
	}
	defer func() { c.setTrafficStatus(host, c.Now(), err) }()
	if err := platform.EnsureDir(filepath.Join(dir, traffic.DBDir), 0o700, platform.RootOwner); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, traffic.NextReportFile))
	if err := c.runGoAccess(ctx, dir, pending); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dir, traffic.NextReportFile), filepath.Join(dir, traffic.ReportFile)); err != nil {
		return fmt.Errorf("goaccess wrote no report: %w", err)
	}
	for _, name := range pending {
		if err := os.Remove(filepath.Join(dir, traffic.PendingDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// pendingTrafficLogs lists host's rotated logs oldest first, at most
// trafficMaxFiles, after dropping any older than traffic.MaxPendingAge.
func (c *Controller) pendingTrafficLogs(host string) ([]string, error) {
	dir := filepath.Join(c.Layout.TrafficHostDir(host), traffic.PendingDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		at, ok := traffic.PendingTime(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		if c.Now().Sub(at) > traffic.MaxPendingAge {
			c.Log.Warn("dropping an unread traffic log", "host", host, "file", e.Name())
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return nil, err
			}
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	if len(names) > trafficMaxFiles {
		names = names[:trafficMaxFiles]
	}
	return names, nil
}

// runGoAccess runs one GoAccess job over a host's traffic directory: pinned
// image, no network, read-only root, no capabilities, bounded memory and
// CPU. It sees nothing but that directory.
func (c *Controller) runGoAccess(ctx context.Context, dir string, pending []string) error {
	ctx, cancel := context.WithTimeout(ctx, trafficRunTimeout)
	defer cancel()
	if err := docker.EnsureImage(ctx, c.Engine, domain.GoAccessImage, nil); err != nil {
		return fmt.Errorf("goaccess image: %w", err)
	}
	job := "traffic-" + platform.RandomHex(5)
	pids := int64(64)
	spec := docker.ContainerSpec{
		Name: c.Names.BackupContainer(job),
		Config: &container.Config{
			Image:  domain.GoAccessImage,
			Cmd:    traffic.Args(pending),
			Labels: c.Names.Labels(runtime.RoleTool, nil),
		},
		HostConfig: &container.HostConfig{
			NetworkMode:    "none",
			Mounts:         []mount.Mount{{Type: mount.TypeBind, Source: dir, Target: traffic.Mount}},
			ReadonlyRootfs: true,
			Tmpfs:          map[string]string{"/tmp": "rw,nosuid,nodev,size=16m"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyDisabled},
			Resources:      container.Resources{Memory: 512 << 20, NanoCPUs: 1e9, PidsLimit: &pids},
			LogConfig:      container.LogConfig{Type: "local", Config: map[string]string{"max-size": "1m", "max-file": "2"}},
		},
	}
	id, err := c.Engine.Create(ctx, spec)
	if err != nil {
		return fmt.Errorf("create goaccess job: %w", err)
	}
	defer c.Engine.Remove(context.WithoutCancel(ctx), id)
	if err := c.Engine.Start(ctx, id); err != nil {
		return fmt.Errorf("start goaccess job: %w", err)
	}
	code, err := c.Engine.Wait(ctx, id)
	if err != nil {
		return fmt.Errorf("goaccess job: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("goaccess exited %d: %s", code, c.jobLogTail(context.WithoutCancel(ctx), id))
	}
	return nil
}

// jobLogTail is the end of what a job container printed, on one line.
func (c *Controller) jobLogTail(ctx context.Context, id string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := strings.Join(strings.Fields(c.logTail(ctx, id, 5)), " ")
	if len(out) > 512 {
		out = out[len(out)-512:]
	}
	if out == "" {
		return "no output"
	}
	return out
}

// TrafficReport reads host's last report; ok is false when none was written.
func (c *Controller) TrafficReport(host string) (traffic.Report, time.Time, bool, error) {
	if !validTrafficHost(host) {
		return traffic.Report{}, time.Time{}, false, nil
	}
	p := filepath.Join(c.Layout.TrafficHostDir(host), traffic.ReportFile)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return traffic.Report{}, time.Time{}, false, nil
	}
	if err != nil {
		return traffic.Report{}, time.Time{}, false, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return traffic.Report{}, time.Time{}, false, err
	}
	r, err := traffic.Parse(b)
	if err != nil {
		return traffic.Report{}, time.Time{}, false, err
	}
	return r, info.ModTime(), true, nil
}

// TrafficPending is the size of host's rotated logs not yet in its report.
func (c *Controller) TrafficPending(host string) int64 {
	if !validTrafficHost(host) {
		return 0
	}
	var total int64
	entries, _ := os.ReadDir(filepath.Join(c.Layout.TrafficHostDir(host), traffic.PendingDir))
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	if info, err := os.Stat(filepath.Join(c.Layout.TrafficLogsDir(), edge.TrafficLogFile(host))); err == nil {
		total += info.Size()
	}
	return total
}
