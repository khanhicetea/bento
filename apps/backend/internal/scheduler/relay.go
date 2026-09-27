// Package scheduler implements the per-app, UID-matched minicrond relay.
//
// minicrond authenticates Unix-socket callers by peer UID. The backend must
// not change its own process-wide UID, and must not weaken daemon auth, so
// for each app it creates a listening socket in a root-only directory and
// passes the listening descriptor to a small child process ("bento
// internal-relay") running as the app's UID/GID. The child accepts
// connections from the backend and dials only that app's own minicrond
// socket. Browsers never see the relay or any minicrond credential.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// SocketRel is the minicrond socket path relative to the app home.
const SocketRel = ".local/share/minicron/minicron.sock"

// IdleTimeout stops unused relays.
const IdleTimeout = 10 * time.Minute

type relay struct {
	cmd      *exec.Cmd
	socket   string
	lastUsed time.Time
	done     chan struct{}
}

type RelayManager struct {
	Layout platform.Layout
	// Executable is the bento binary used to spawn relay children.
	Executable string
	Log        *slog.Logger

	mu     sync.Mutex
	relays map[string]*relay
}

func NewRelayManager(layout platform.Layout, exe string, log *slog.Logger) *RelayManager {
	return &RelayManager{Layout: layout, Executable: exe, Log: log, relays: map[string]*relay{}}
}

// TargetSocket validates and returns the host path of the app's minicrond
// socket: no symlinks below the home, a real socket, owned by the app UID.
func (m *RelayManager) TargetSocket(app domain.App) (string, error) {
	home := m.Layout.AppHome(app.Slug)
	sock := filepath.Join(home, SocketRel)
	if err := platform.NoSymlinkBetween(home, sock); err != nil {
		return "", err
	}
	info, err := os.Lstat(sock)
	if err != nil {
		return "", fmt.Errorf("scheduler socket unavailable (is the app running?)")
	}
	if info.Mode()&os.ModeSocket == 0 {
		return "", fmt.Errorf("scheduler socket path is not a socket")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != app.UID {
		return "", fmt.Errorf("scheduler socket is not owned by the app identity")
	}
	return sock, nil
}

// Dial connects to the app's relay, starting it if needed.
func (m *RelayManager) Dial(ctx context.Context, app domain.App) (net.Conn, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("scheduler relay requires the backend to run as root")
	}
	target, err := m.TargetSocket(app)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	r, ok := m.relays[app.ID]
	if ok {
		select {
		case <-r.done:
			delete(m.relays, app.ID)
			ok = false
		default:
		}
	}
	if !ok {
		r, err = m.start(app, target)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		m.relays[app.ID] = r
	}
	r.lastUsed = time.Now()
	sock := r.socket
	m.mu.Unlock()
	var d net.Dialer
	return d.DialContext(ctx, "unix", sock)
}

func (m *RelayManager) start(app domain.App, target string) (*relay, error) {
	dir := m.Layout.RelayDir()
	if err := platform.EnsureDir(dir, 0o700, platform.RootOwner); err != nil {
		return nil, err
	}
	sock := m.Layout.RelaySocket(app.ID)
	_ = os.Remove(sock)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil, err
	}
	ln.SetUnlinkOnClose(false)
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	f, err := ln.File()
	ln.Close()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// The socket directory is opened here (as root) and inherited, so the
	// child needs no traversal rights on the stack root's ancestors.
	dirf, err := os.OpenFile(filepath.Dir(target), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer dirf.Close()
	cmd := exec.Command(m.Executable, "internal-relay")
	cmd.Env = []string{"BENTO_RELAY_TARGET=" + filepath.Base(target), "PATH=/usr/bin:/bin"}
	cmd.ExtraFiles = []*os.File{f, dirf}
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(app.UID), Gid: uint32(app.GID), Groups: []uint32{}},
		Pdeathsig:  syscall.SIGTERM,
		Setpgid:    true,
	}
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.Remove(sock)
		return nil, fmt.Errorf("start relay: %w", err)
	}
	r := &relay{cmd: cmd, socket: sock, lastUsed: time.Now(), done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(r.done)
	}()
	m.Log.Info("scheduler relay started", "app", app.Slug, "pid", cmd.Process.Pid)
	return r, nil
}

// Reap stops idle relays; call periodically.
func (m *RelayManager) Reap(all bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.relays {
		if all || time.Since(r.lastUsed) > IdleTimeout {
			_ = r.cmd.Process.Signal(syscall.SIGTERM)
			_ = os.Remove(r.socket)
			delete(m.relays, id)
		}
	}
}

// RunChild is the relay child's main loop: fd 3 is the inherited listener and
// fd 4 the app's minicrond socket directory.
func RunChild() error {
	base := os.Getenv("BENTO_RELAY_TARGET")
	if base == "" || strings.Contains(base, "/") || os.Geteuid() == 0 {
		return errors.New("internal-relay must be started by the backend as an app identity")
	}
	ln, err := net.FileListener(os.NewFile(3, "relay-listener"))
	if err != nil {
		return err
	}
	// Dial relative to the socket directory: no ancestor traversal and no
	// sun_path length limit.
	if err := syscall.Fchdir(4); err != nil {
		return err
	}
	_ = syscall.Close(4)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go serve(c, base)
	}
}

func serve(c net.Conn, base string) {
	defer c.Close()
	info, err := os.Lstat(base)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return
	}
	up, err := net.Dial("unix", base)
	if err != nil {
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(up, c)
		if uc, ok := up.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(c, up)
		if uc, ok := c.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}
