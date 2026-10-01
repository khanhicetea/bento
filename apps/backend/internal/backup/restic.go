package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// In-container paths of an app restic job. They are identical for every app
// and stack, so snapshots restore anywhere.
const (
	ResticHomeMount    = "/backup/home"
	ResticBentoMount   = "/backup/bento"
	ResticRestoreMount = "/restore"
	resticCtlMount     = "/run/bento-ctl"
	resticCacheMount   = "/cache"
	// ResticKeyFile is the repository password inside the ctl directory.
	ResticKeyFile = "restic.key"
	// ResticNewKeyFile is a key being added to the repository.
	ResticNewKeyFile = "new.key"
	// ResticExcludeFile is the generated exclude file in the ctl directory.
	ResticExcludeFile = "excludes"
)

// ResticMounts selects what a restic job can see. CtlDir (key, excludes) is
// always mounted read-only. Home and Bento are read-only snapshot sources;
// RestoreDir is the only writable data mount and is never the live home.
type ResticMounts struct {
	CtlDir     string
	Home       string
	Bento      string
	RestoreDir string
}

// ResticJob is an idle job container of the backup image that runs restic
// commands by exec.
type ResticJob struct {
	d  Deps
	id string
	// unlockFailed records that a command could not delete its lock.
	unlockFailed bool
}

// LockLeaked reports whether any command so far exited without removing its
// repository lock (typically the remote refused the delete).
func (j *ResticJob) LockLeaked() bool { return j.unlockFailed }

func lockLeaked(stderr string) bool {
	return strings.Contains(stderr, "error while unlocking") || strings.Contains(stderr, "unable to remove lock") ||
		(strings.Contains(stderr, "locks/") && strings.Contains(stderr, "Delete"))
}

// StartResticJob starts the job container. Backups get read-only access to
// every file (DAC_READ_SEARCH) and nothing else; restores additionally get
// CHOWN/FOWNER/DAC_OVERRIDE to recreate ownership inside RestoreDir. The
// repository key is a file in CtlDir and never appears in argv or env.
func (d Deps) StartResticJob(
	ctx context.Context,
	image, appID, opID, repository string,
	m ResticMounts,
) (*ResticJob, error) {
	if err := d.checkRemote(repository); err != nil {
		return nil, err
	}
	cache := filepath.Join(d.Layout.CacheDir(), "restic", appID)
	if err := platform.EnsureDir(filepath.Dir(cache), 0o700, platform.RootOwner); err != nil {
		return nil, err
	}
	if err := platform.EnsureDir(cache, 0o700, platform.RootOwner); err != nil {
		return nil, err
	}
	mounts := []mount.Mount{
		{Type: mount.TypeBind, Source: m.CtlDir, Target: resticCtlMount, ReadOnly: true},
		{Type: mount.TypeBind, Source: cache, Target: resticCacheMount},
	}
	caps := []string{"DAC_READ_SEARCH"}
	if m.Home != "" {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: m.Home, Target: ResticHomeMount, ReadOnly: true})
	}
	if m.Bento != "" {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: m.Bento, Target: ResticBentoMount, ReadOnly: true})
	}
	if m.RestoreDir != "" {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: m.RestoreDir, Target: ResticRestoreMount})
		caps = append(caps, "CHOWN", "FOWNER", "DAC_OVERRIDE")
	}
	spec := d.rcloneSpec(d.Names.BackupContainer("restic-"+platform.RandomHex(5)), runtime.RoleBackup, opID, mounts)
	spec.Config.Image = image
	spec.Config.Entrypoint = []string{"sleep"}
	spec.Config.Cmd = []string{"86400"}
	spec.Config.Env = append(spec.Config.Env,
		"RESTIC_REPOSITORY=rclone:"+repository,
		"RESTIC_PASSWORD_FILE="+resticCtlMount+"/"+ResticKeyFile,
		"RESTIC_CACHE_DIR="+resticCacheMount,
		"RESTIC_PROGRESS_FPS=0.2",
	)
	spec.HostConfig.CapAdd = caps
	spec.HostConfig.Tmpfs = map[string]string{"/tmp": "rw,nosuid,nodev,size=256m"}
	id, err := d.Engine.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	if err := d.Engine.Start(ctx, id); err != nil {
		_ = d.Engine.Remove(context.WithoutCancel(ctx), id)
		return nil, err
	}
	return &ResticJob{d: d, id: id}, nil
}

// Close removes the job container.
func (j *ResticJob) Close(ctx context.Context) {
	_ = j.d.Engine.Remove(context.WithoutCancel(ctx), j.id)
}

// ResticError is a failed restic command with the tail of its stderr.
type ResticError struct {
	Args   []string
	Exit   int
	Stderr string
}

func (e *ResticError) Error() string {
	return fmt.Sprintf("restic %s failed (exit %d): %s", strings.Join(e.Args, " "), e.Exit, e.Stderr)
}

// Exit codes documented by restic.
const (
	ResticExitRepoMissing = 10
	ResticExitLocked      = 11
	ResticExitBadPassword = 12
)

// ResticQuickTimeout bounds metadata commands (init, cat config, listings,
// keys). restic retries a failing backend for up to 15 minutes, so a wrong
// bucket or credential would otherwise look like a hang.
const ResticQuickTimeout = 3 * time.Minute

// ErrResticTimeout means a bounded restic command did not finish in time.
var ErrResticTimeout = errors.New("restic did not answer in time; the remote is probably failing and restic keeps retrying")

// RunQuick is Run bounded by ResticQuickTimeout.
func (j *ResticJob) RunQuick(ctx context.Context, args []string, stdout io.Writer) error {
	qctx, cancel := context.WithTimeout(ctx, ResticQuickTimeout)
	defer cancel()
	err := j.Run(qctx, args, stdout)
	if err != nil && errors.Is(qctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return ErrResticTimeout
	}
	return err
}

// rcloneFast fails a probe quickly instead of retrying like a transfer.
var rcloneFast = []string{"--retries", "1", "--low-level-retries", "2", "--contimeout", "15s", "--timeout", "60s"}

// ProbeRemote checks the repository location with rclone before restic
// touches it, so a missing bucket or a denied credential fails in seconds
// with rclone's own error. With write it creates and deletes a probe object
// (needed to initialize); otherwise it lists the location, which must exist.
func (j *ResticJob) ProbeRemote(ctx context.Context, repository string, write bool) error {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	run := func(args ...string) error {
		var stderr strings.Builder
		res, err := j.d.Engine.Exec(pctx, j.id, docker.ExecRequest{
			Cmd:    append(append([]string{"rclone"}, args...), rcloneFast...),
			Stderr: &limitWriter{w: &stderr, n: 4096},
			Stdout: io.Discard,
		})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s", rcloneErrorLine(stderr.String()))
		}
		return nil
	}
	if !write {
		return run("lsf", "--max-depth", "1", repository)
	}
	probe := strings.TrimSuffix(repository, "/") + "/.bento-probe"
	if err := run("touch", probe); err != nil {
		return err
	}
	if err := run("deletefile", probe); err != nil {
		return fmt.Errorf("the remote allows writing but not deleting (restic deletes its lock files and pruned data); grant delete permission: %w", err)
	}
	return nil
}

// rcloneErrorLine returns the most useful line of rclone's stderr.
func rcloneErrorLine(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "ERROR") || strings.Contains(lines[i], "Failed") {
			return strings.TrimSpace(lines[i])
		}
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		return strings.TrimSpace(lines[len(lines)-1])
	}
	return "rclone failed"
}

// Run executes one restic command. stdout receives the command's output
// (JSON for --json commands).
func (j *ResticJob) Run(ctx context.Context, args []string, stdout io.Writer) error {
	var stderr strings.Builder
	defer func() { j.unlockFailed = j.unlockFailed || lockLeaked(stderr.String()) }()
	res, err := j.d.Engine.Exec(ctx, j.id, docker.ExecRequest{
		Cmd:    append([]string{"restic"}, args...),
		Stdout: stdout,
		Stderr: &limitWriter{w: &stderr, n: 8192},
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &ResticError{Args: args, Exit: res.ExitCode, Stderr: strings.TrimSpace(stderr.String())}
	}
	return nil
}

// RunJSON runs a --json command and returns its stdout (at most 16 MiB).
func (j *ResticJob) RunJSON(ctx context.Context, args []string) ([]byte, error) {
	var out bytes.Buffer
	lw := &limitWriter{w: &out, n: 16 << 20}
	if err := j.RunQuick(ctx, append(args, "--json"), lw); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// LineWriter calls fn for each complete line written to it. Lines longer
// than 64 KiB are split.
type LineWriter struct {
	Fn  func(line []byte)
	buf []byte
}

func (l *LineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			if len(l.buf) > 64<<10 {
				l.Fn(l.buf)
				l.buf = l.buf[:0]
			}
			return len(p), nil
		}
		l.Fn(l.buf[:i])
		l.buf = l.buf[i+1:]
	}
}

// CheckRemote verifies a "name:path" destination is usable unattended.
func (d Deps) CheckRemote(remote string) error { return d.checkRemote(remote) }

// DumpPlain writes one binding database uncompressed to path (0600): SQL for
// MySQL/PostgreSQL, a .backup copy for SQLite. Uncompressed dumps let restic
// deduplicate unchanged rows between snapshots.
func (d Deps) DumpPlain(ctx context.Context, t Target, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	switch t.Binding.Engine {
	case domain.EngineMySQL, domain.EnginePostgres:
		err = d.dumpRelational(ctx, t, w)
	case domain.EngineSQLite:
		err = d.dumpSQLite(ctx, t, w)
	default:
		err = fmt.Errorf("unsupported engine %s", t.Binding.Engine)
	}
	if err == nil {
		err = w.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// SnapshotHomeSQLite copies SQLite files that live in the app home with
// sqlite3 .backup, as the app UID, in a network-less job container that
// mounts only the home and an output directory. rels are clean
// home-relative paths; out[i] receives a copy of rels[i]. Missing files are
// skipped and reported in the returned list.
func (d Deps) SnapshotHomeSQLite(ctx context.Context, app domain.App, rels []string, outDir string) ([]string, error) {
	if len(rels) == 0 {
		return nil, nil
	}
	spec, err := d.ToolSpec(ctx, app)
	if err != nil {
		return nil, err
	}
	staging := filepath.Join(d.Layout.StagingDir(), "home-sqlite-"+platform.RandomHex(6))
	if err := platform.EnsureDir(staging, 0o700, platform.Owner{UID: app.UID, GID: app.GID}); err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	var mounts []mount.Mount
	for _, m := range spec.HostConfig.Mounts {
		if m.Target == app.ContainerHome() || m.Target == "/etc/passwd" || m.Target == "/etc/group" {
			mounts = append(mounts, m)
		}
	}
	mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: staging, Target: "/bento-backup-out"})
	spec.HostConfig.Mounts = mounts
	spec.Config.Labels[runtime.LabelRole] = string(runtime.RoleBackup)
	spec.Config.Entrypoint = []string{"sleep"}
	spec.Config.Cmd = []string{"3600"}
	spec.Config.WorkingDir = "/"
	spec.Name = d.Names.BackupContainer("home-sqlite-" + platform.RandomHex(5))
	spec.Networking = nil
	spec.HostConfig.NetworkMode = "none"
	id, err := d.Engine.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer d.Engine.Remove(context.WithoutCancel(ctx), id)
	if err := d.Engine.Start(ctx, id); err != nil {
		return nil, err
	}
	var skipped []string
	for i, rel := range rels {
		src := app.ContainerHome() + "/" + rel
		out := fmt.Sprintf("/bento-backup-out/%d.db", i)
		res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{
			User: fmt.Sprintf("%d:%d", app.UID, app.GID),
			Cmd: []string{"sh", "-c", `[ -f "$1" ] || exit 3; exec sqlite3 -bail -cmd ".timeout 30000" "$1" ".backup '$2'"`,
				"bento-sqlite", src, out},
		})
		if err != nil {
			return skipped, err
		}
		if res.ExitCode == 3 {
			skipped = append(skipped, rel)
			continue
		}
		if res.ExitCode != 0 {
			return skipped, fmt.Errorf("sqlite backup of %s failed: %s", rel, strings.TrimSpace(string(res.Stderr)))
		}
		snap := filepath.Join(staging, fmt.Sprintf("%d.db", i))
		info, err := os.Lstat(snap)
		if err != nil || !info.Mode().IsRegular() {
			return skipped, fmt.Errorf("sqlite backup of %s produced no snapshot", rel)
		}
		dst, err := platform.ContainedPath(outDir, rel)
		if err != nil {
			return skipped, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return skipped, err
		}
		if err := platform.CopyFile(snap, dst, 0o600, platform.RootOwner); err != nil {
			return skipped, err
		}
	}
	return skipped, nil
}

// IsSQLiteFile reports whether path is a regular (non-symlink) file starting
// with the SQLite header.
func IsSQLiteFile(path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return false
	}
	head := make([]byte, 16)
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return string(head[:15]) == "SQLite format 3"
}
