package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// rclone runs only in containers of the pinned image. The config directory
// is the single operator-owned input; it is mounted read-write so rclone can
// save it atomically (temp file + rename) and persist refreshed OAuth tokens.
const (
	rcloneConfigMount = "/config/rclone"
	rcloneConfigFile  = rcloneConfigMount + "/rclone.conf"
	encryptedHeader   = "# Encrypted rclone configuration File"
)

var (
	remotePattern = regexp.MustCompile(`^([A-Za-z0-9_-]+):[A-Za-z0-9_./-]*$`)
	sectionLine   = regexp.MustCompile(`^\[([^\]]+)\]$`)
	typeLine      = regexp.MustCompile(`^type\s*=\s*(\S+)$`)
)

// ErrRcloneEncrypted means the config has a password: unattended uploads
// cannot unlock it, so it is refused rather than prompted for.
var ErrRcloneEncrypted = errors.New("rclone config is encrypted; unattended uploads cannot unlock it. Remove the password in the rclone shell: rclone config → s) Set configuration password → u) Unencrypt configuration")

// RcloneRemote is one configured remote. Only its name and backend type are
// ever read; every other key may be a credential.
type RcloneRemote struct {
	Name string
	Type string
}

// RcloneConfig summarizes rclone.conf without exposing its values.
type RcloneConfig struct {
	Present   bool
	Encrypted bool
	Remotes   []RcloneRemote
}

// ValidateRemote checks a "name:path" destination and returns the name.
func ValidateRemote(remote string) (string, error) {
	m := remotePattern.FindStringSubmatch(remote)
	if m == nil {
		return "", fmt.Errorf("invalid rclone remote %q; use name:path", remote)
	}
	return m[1], nil
}

// ReadRcloneConfig reads section names and types from dir/rclone.conf. The
// file is written from inside a shell container, so a symlink is refused
// instead of followed on the host.
func ReadRcloneConfig(dir string) (RcloneConfig, error) {
	var out RcloneConfig
	f, err := os.OpenFile(filepath.Join(dir, "rclone.conf"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("rclone config is not a regular file: %w", err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return out, fmt.Errorf("rclone config is not a regular file")
	}
	out.Present = true
	sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first && strings.HasPrefix(line, encryptedHeader) {
			out.Encrypted = true
			return out, nil
		}
		first = false
		if m := sectionLine.FindStringSubmatch(line); m != nil {
			out.Remotes = append(out.Remotes, RcloneRemote{Name: m[1]})
		} else if m := typeLine.FindStringSubmatch(line); m != nil && len(out.Remotes) > 0 {
			out.Remotes[len(out.Remotes)-1].Type = m[1]
		}
	}
	return out, sc.Err()
}

// checkRemote refuses destinations that cannot work unattended.
func (d Deps) checkRemote(remote string) error {
	name, err := ValidateRemote(remote)
	if err != nil {
		return err
	}
	cfg, err := ReadRcloneConfig(d.Layout.RcloneDir())
	if err != nil {
		return err
	}
	switch {
	case !cfg.Present:
		return fmt.Errorf("rclone config %s is missing", filepath.Join(d.Layout.RcloneDir(), "rclone.conf"))
	case cfg.Encrypted:
		return ErrRcloneEncrypted
	}
	for _, r := range cfg.Remotes {
		if r.Name == name {
			return nil
		}
	}
	return fmt.Errorf("rclone remote %q is not configured; add it in the rclone shell", name)
}

func (d Deps) ensureRcloneImage(ctx context.Context) error {
	if _, ok, err := d.Engine.ImageID(ctx, domain.RcloneImage); err != nil || ok {
		return err
	}
	return d.Engine.PullImage(ctx, domain.RcloneImage, nil)
}

// rcloneSpec is the hardened shape shared by upload, test, and shell
// containers: pinned image, config directory only, no capabilities.
func (d Deps) rcloneSpec(name string, role runtime.Role, opID string, mounts []mount.Mount) docker.ContainerSpec {
	mounts = append([]mount.Mount{{Type: mount.TypeBind, Source: d.Layout.RcloneDir(), Target: rcloneConfigMount}}, mounts...)
	return docker.ContainerSpec{
		Name: name,
		Config: &container.Config{Image: domain.RcloneImage,
			Env:    []string{"RCLONE_CONFIG=" + rcloneConfigFile, "HOME=/tmp", "XDG_CACHE_HOME=/tmp/.cache"},
			Labels: d.Names.Labels(role, map[string]string{runtime.LabelOperation: opID})},
		HostConfig: &container.HostConfig{Mounts: mounts, ReadonlyRootfs: true, Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,size=64m"},
			CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Init: new(true),
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			// The local driver compresses rotated files and refuses max-file 1.
			LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "5m", "max-file": "2"}}},
	}
}

// runRclone runs one rclone command to completion and returns its exit code
// and the tail of its output.
func (d Deps) runRclone(ctx context.Context, args []string, mounts []mount.Mount) (int64, string, error) {
	if err := d.ensureRcloneImage(ctx); err != nil {
		return 0, "", err
	}
	opID := "rclone-" + platform.RandomHex(5)
	spec := d.rcloneSpec(d.Names.BackupContainer(opID), runtime.RoleBackup, opID, mounts)
	spec.Config.Cmd = args
	id, err := d.Engine.Create(ctx, spec)
	if err != nil {
		return 0, "", err
	}
	defer d.Engine.Remove(context.WithoutCancel(ctx), id)
	if err := d.Engine.Start(ctx, id); err != nil {
		return 0, "", err
	}
	code, err := d.Engine.Wait(ctx, id)
	if err != nil || code == 0 {
		return code, "", err
	}
	return code, d.logTail(ctx, id), nil
}

func (d Deps) logTail(ctx context.Context, id string) string {
	rc, tty, err := d.Engine.Logs(ctx, id, "20", false, "")
	if err != nil {
		return ""
	}
	defer rc.Close()
	var b strings.Builder
	if tty {
		_, _ = io.Copy(&limitWriter{w: &b, n: 4096}, rc)
	} else {
		_, _ = stdcopy.StdCopy(&limitWriter{w: &b, n: 4096}, &limitWriter{w: &b, n: 4096}, rc)
	}
	return strings.TrimSpace(b.String())
}

// Upload copies exactly the given artifacts with rclone in an ephemeral
// container: the config directory and the selected files (read-only).
func (d Deps) Upload(ctx context.Context, remote string, artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	if err := d.checkRemote(remote); err != nil {
		return err
	}
	var mounts []mount.Mount
	for _, a := range artifacts {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: filepath.Join(d.Layout.BackupsDir(), a.Path), Target: "/upload/" + a.Path, ReadOnly: true})
	}
	code, tail, err := d.runRclone(ctx, []string{"copy", "/upload", remote, "--no-traverse"}, mounts)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("rclone exited %d: %s", code, tail)
	}
	return nil
}

// rcloneDirNotFound is rclone's documented exit code for a missing directory.
const rcloneDirNotFound = 3

// TestRemote lists the top level of remote without changing it. A missing
// path is fine: the first upload creates it.
func (d Deps) TestRemote(ctx context.Context, remote string) (string, error) {
	if err := d.checkRemote(remote); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	code, tail, err := d.runRclone(ctx, []string{"lsf", "--max-depth", "1", "--contimeout", "15s", "--timeout", "30s", "--retries", "1", remote}, nil)
	switch {
	case err != nil:
		return "", err
	case code == 0:
		return fmt.Sprintf("%s is reachable", remote), nil
	case code == rcloneDirNotFound:
		return fmt.Sprintf("%s is reachable; the path does not exist yet and will be created by the first upload", remote), nil
	}
	return "", fmt.Errorf("rclone exited %d: %s", code, tail)
}

// RcloneShellSpec plans an idle container for an interactive rclone shell.
// It sees only the config directory and exits on its own after lifetime.
func (d Deps) RcloneShellSpec(opID string, lifetime time.Duration) docker.ContainerSpec {
	spec := d.rcloneSpec(d.Names.ToolContainer("rclone", opID), runtime.RoleTool, opID, nil)
	spec.Config.Hostname = "rclone"
	spec.Config.Entrypoint = []string{"sleep"}
	spec.Config.Cmd = []string{strconv.Itoa(int(lifetime.Seconds()))}
	return spec
}

// RcloneShellExec is the shell exec for RcloneShellSpec.
func RcloneShellExec() docker.ExecRequest {
	return docker.ExecRequest{
		Cmd: []string{"sh"},
		Env: []string{"TERM=xterm-256color", "RCLONE_CONFIG=" + rcloneConfigFile, "HOME=/tmp", "XDG_CACHE_HOME=/tmp/.cache", `PS1=rclone:\w\$ `},
	}
}

// OpenRcloneShell pulls the image if needed and starts the shell container.
func (d Deps) OpenRcloneShell(ctx context.Context, opID string, lifetime time.Duration) (string, error) {
	if err := d.ensureRcloneImage(ctx); err != nil {
		return "", err
	}
	id, err := d.Engine.Create(ctx, d.RcloneShellSpec(opID, lifetime))
	if err != nil {
		return "", err
	}
	if err := d.Engine.Start(ctx, id); err != nil {
		_ = d.Engine.Remove(context.WithoutCancel(ctx), id)
		return "", err
	}
	return id, nil
}
