// Package remoteaccess runs the experimental remote access to MySQL and
// PostgreSQL services over Tailcat (github.com/tailscale/tailcat).
//
// For each service with remote access on, the backend runs one long-lived
// child, `/proc/self/exe internal-tailcat`, as the unprivileged Tailcat
// identity (domain.TailcatUID) in the host network namespace: it needs
// outbound UDP and HTTPS to the DERP relays, and the data network has no
// egress. The child gets its key on stdin, reports its address as one JSON
// line on stdout, and forwards each tunnel connection to the engine port into
// a Unix socket the backend owns (run/tailcat/<service>.sock). The backend
// proxies each accepted stream to the service's address on the data network,
// within per-service caps. The child holds no database credential, and
// Landlock (Linux 6.7+) limits its outbound TCP to the relay ports so it
// cannot open connections to the data network; the database password is the
// access control.
package remoteaccess

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// Per-service limits on connections through the tunnel.
const (
	DefaultMaxConns = 32
	// DefaultRate and DefaultBurst bound new connections: about 10 per
	// second, with short bursts of 10.
	DefaultRate  = 10.0
	DefaultBurst = 10
)

// Child restart backoff: doubling from backoffMin to backoffMax, reset after
// a child that stayed up for backoffReset.
const (
	backoffMin   = time.Second
	backoffMax   = 5 * time.Minute
	backoffReset = 2 * time.Minute
	// reportTimeout bounds the wait for the child's address line.
	reportTimeout = 90 * time.Second
	// stopGrace is how long a child gets after SIGTERM before SIGKILL.
	stopGrace   = 5 * time.Second
	dialTimeout = 5 * time.Second
)

type State string

const (
	StateOff       State = "off"
	StateStarting  State = "starting"
	StateListening State = "listening"
	StateFailed    State = "failed"
)

// Status is what the API shows for one service. Address is a bearer
// capability (it embeds the pre-shared key); callers decide who may see it.
type Status struct {
	State       State
	Address     string
	Connections int
	Message     string
}

// Target is a service whose remote access should run.
type Target struct {
	Service    string
	Port       int
	Generation int64
}

// Manager supervises the Tailcat children and proxies their streams.
type Manager struct {
	Layout platform.Layout
	// Executable and Args start a child (default /proc/self/exe
	// internal-tailcat; tests substitute a stub).
	Executable string
	Args       []string
	Log        *slog.Logger
	// Upstream returns host:port of the service's engine on the data
	// network, after checking the container is Bento's own and running.
	Upstream func(ctx context.Context, service string) (string, error)
	// UID and GID are the Tailcat identity the children run as.
	UID, GID int
	// HostIDs, when set, refuses to start while a host account uses UID.
	HostIDs platform.HostIDs
	// MaxConns, Rate and Burst are the per-service caps (defaults above).
	MaxConns int
	Rate     float64
	Burst    int
	// DERPMapURL overrides tailcat's default DERP map (tests).
	DERPMapURL string

	mu      sync.Mutex
	servers map[string]*server
}

// NewManager returns a manager for layout running children as uid:gid.
func NewManager(layout platform.Layout, exe string, uid, gid int, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{Layout: layout, Executable: exe, Args: []string{ChildCommand}, Log: log, UID: uid, GID: gid,
		MaxConns: DefaultMaxConns, Rate: DefaultRate, Burst: DefaultBurst, servers: map[string]*server{}}
}

type server struct {
	m      *Manager
	target Target
	ctx    context.Context
	cancel context.CancelFunc
	ln     *net.UnixListener
	dir    *os.File
	wg     sync.WaitGroup

	mu       sync.Mutex
	key      Key
	pid      int // of the current child
	state    State
	address  string
	message  string
	changed  chan struct{}
	conns    map[*stream]struct{}
	tokens   float64
	refilled time.Time
}

type stream struct{ down, up net.Conn }

func (s *stream) close() {
	_ = s.down.Close()
	if s.up != nil {
		_ = s.up.Close()
	}
}

// Status reports a service's remote access as the manager sees it.
func (m *Manager) Status(service string) Status {
	m.mu.Lock()
	s := m.servers[service]
	m.mu.Unlock()
	if s == nil {
		return Status{State: StateOff}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{State: s.state, Address: s.address, Connections: len(s.conns), Message: s.message}
}

// Start runs target's server, replacing one of another generation or port.
// A missing or invalid key leaves a failed entry, so the state is visible,
// and returns the error. Start never creates a key (see EnsureKey).
func (m *Manager) Start(t Target) error {
	m.mu.Lock()
	if m.servers == nil {
		m.servers = map[string]*server{}
	}
	old := m.servers[t.Service]
	if old != nil && old.target == t && old.ln != nil {
		m.mu.Unlock()
		return nil
	}
	delete(m.servers, t.Service)
	m.mu.Unlock()
	if old != nil {
		old.shutdown()
	}
	s, err := m.newServer(t)
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur := m.servers[t.Service]; cur != nil {
		// A concurrent Start won; keep it.
		if s.ln != nil {
			go s.shutdown()
		}
		return err
	}
	m.servers[t.Service] = s
	if err == nil {
		s.wg.Add(2)
		go s.accept()
		go s.supervise()
	}
	return err
}

// newServer prepares the key, the socket and the directory descriptor. On
// error it returns a failed server holding no resources.
func (m *Manager) newServer(t Target) (*server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &server{m: m, target: t, ctx: ctx, cancel: cancel, state: StateStarting, changed: make(chan struct{}),
		conns: map[*stream]struct{}{}, tokens: float64(m.burst()), refilled: time.Now()}
	fail := func(err error) (*server, error) {
		cancel()
		s.state, s.message = StateFailed, err.Error()
		return s, err
	}
	if t.Port <= 0 || t.Port > 65535 {
		return fail(errors.New("invalid engine port"))
	}
	if os.Geteuid() != 0 {
		return fail(errors.New("remote access requires the backend to run as root"))
	}
	if m.UID <= 0 || m.GID <= 0 {
		return fail(errors.New("remote access has no unprivileged identity configured"))
	}
	if m.HostIDs != nil && m.HostIDs.Taken(m.UID) {
		return fail(fmt.Errorf("uid/gid %d is used by a host account; remote access refuses to share it", m.UID))
	}
	k, _, err := EnsureKey(m.Layout, t.Service, t.Generation, false)
	if err != nil {
		return fail(err)
	}
	s.key = k
	dir := m.Layout.RemoteSocketDir()
	if err := platform.EnsureDir(m.Layout.RunDir(), 0o700, platform.RootOwner); err != nil {
		return fail(err)
	}
	// Only root and the Tailcat group may traverse it; the child enters it
	// through an inherited descriptor.
	if err := platform.EnsureDir(dir, 0o710, platform.Owner{UID: 0, GID: m.GID}); err != nil {
		return fail(err)
	}
	dirf, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fail(err)
	}
	sock := m.Layout.RemoteSocket(t.Service)
	_ = os.Remove(sock) // stale socket of an earlier run; Listen reports real problems
	// Bind through the directory descriptor: a deep stack root would exceed
	// sun_path's 108 bytes.
	bindPath := fmt.Sprintf("/proc/self/fd/%d/%s", dirf.Fd(), filepath.Base(sock))
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: bindPath, Net: "unix"})
	if err != nil {
		_ = dirf.Close()
		return fail(err)
	}
	ln.SetUnlinkOnClose(false) // shutdown removes the real path
	err = os.Chown(sock, m.UID, m.GID)
	if err == nil {
		err = os.Chmod(sock, 0o600)
	}
	if err != nil {
		_ = ln.Close()
		_ = dirf.Close()
		_ = os.Remove(sock)
		return fail(err)
	}
	s.ln, s.dir = ln, dirf
	return s, nil
}

func (m *Manager) burst() int {
	if m.Burst > 0 {
		return m.Burst
	}
	return DefaultBurst
}

// Stop ends a service's remote access: the child is stopped, the socket
// closed and removed, and every open connection cut.
func (m *Manager) Stop(service string) {
	m.mu.Lock()
	s := m.servers[service]
	delete(m.servers, service)
	m.mu.Unlock()
	if s != nil {
		s.shutdown()
	}
}

// StopAll stops every server (backend shutdown).
func (m *Manager) StopAll() {
	m.mu.Lock()
	all := m.servers
	m.servers = map[string]*server{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Go(s.shutdown)
	}
	wg.Wait()
}

// WaitSettled waits until service's server is listening or failed, or ctx
// ends, and returns its status.
func (m *Manager) WaitSettled(ctx context.Context, service string) Status {
	for {
		m.mu.Lock()
		s := m.servers[service]
		m.mu.Unlock()
		if s == nil {
			return Status{State: StateOff}
		}
		s.mu.Lock()
		st := Status{State: s.state, Address: s.address, Connections: len(s.conns), Message: s.message}
		ch := s.changed
		s.mu.Unlock()
		if st.State == StateListening || st.State == StateFailed {
			return st
		}
		select {
		case <-ctx.Done():
			return st
		case <-ch:
		}
	}
}

func (s *server) setState(state State, address, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.address, s.message = state, address, message
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *server) shutdown() {
	s.cancel()
	if s.ln != nil {
		_ = s.ln.Close()
		_ = os.Remove(s.m.Layout.RemoteSocket(s.target.Service))
	}
	s.mu.Lock()
	for st := range s.conns {
		st.close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	if s.dir != nil {
		_ = s.dir.Close()
	}
}

// ---- child supervision ----

func (s *server) supervise() {
	defer s.wg.Done()
	backoff := backoffMin
	for {
		started := time.Now()
		err := s.runChild()
		if s.ctx.Err() != nil {
			return
		}
		msg := "the Tailcat server stopped"
		if err != nil {
			msg = err.Error()
		}
		s.setState(StateFailed, "", msg)
		s.m.Log.Warn("remote access server stopped; restarting", "service", s.target.Service, "err", msg,
			"retryIn", backoff.String())
		if time.Since(started) > backoffReset {
			backoff = backoffMin
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, backoffMax)
		s.setState(StateStarting, "", "")
	}
}

// command builds the child process without its key: the key travels only on
// stdin (see payload).
func (s *server) command(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.m.Executable, s.m.Args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Dir = "/"
	cmd.ExtraFiles = []*os.File{s.dir}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(s.m.UID), Gid: uint32(s.m.GID), Groups: []uint32{}},
		Pdeathsig:  syscall.SIGTERM,
		Setpgid:    true,
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = stopGrace
	return cmd
}

func (s *server) payload() ([]byte, error) {
	s.mu.Lock()
	k := s.key
	s.mu.Unlock()
	return json.Marshal(childConfig{Private: k.Private, PresharedKey: k.PresharedKey, RegionID: k.RegionID,
		Port: uint16(s.target.Port), Socket: filepath.Base(s.m.Layout.RemoteSocket(s.target.Service)),
		DERPMapURL: s.m.DERPMapURL})
}

func (s *server) runChild() error {
	payload, err := s.payload()
	if err != nil {
		return err
	}
	cmd := s.command(s.ctx)
	cmd.Stdin = bytes.NewReader(payload)
	// Plain pipes, so the readers see EOF when the child exits and Wait
	// never races them.
	stdout, outw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, errw, err := os.Pipe()
	if err != nil {
		_ = outw.Close()
		return err
	}
	defer stderr.Close()
	cmd.Stdout, cmd.Stderr = outw, errw
	err = cmd.Start()
	_ = outw.Close()
	_ = errw.Close()
	if err != nil {
		return fmt.Errorf("start the Tailcat server: %w", err)
	}
	s.mu.Lock()
	s.pid = cmd.Process.Pid
	s.mu.Unlock()
	var last lastLine
	var pipes sync.WaitGroup
	pipes.Go(func() { s.readStderr(stderr, &last) })
	reported := make(chan error, 1)
	pipes.Go(func() { reported <- s.readReport(stdout) })
	timer := time.NewTimer(reportTimeout)
	defer timer.Stop()
	select {
	case err := <-reported:
		if err != nil {
			_ = cmd.Cancel()
		}
	case <-timer.C:
		_ = cmd.Cancel()
	case <-s.ctx.Done():
	}
	werr := cmd.Wait()
	pipes.Wait()
	if s.ctx.Err() != nil {
		return nil
	}
	if msg := last.get(); msg != "" {
		return errors.New(msg)
	}
	if werr != nil {
		return fmt.Errorf("the Tailcat server exited: %w", werr)
	}
	return errors.New("the Tailcat server exited")
}

// readReport reads the child's address line, checks it against the key, and
// records a newly picked region so the address stays stable. The address is
// never logged.
func (s *server) readReport(r io.Reader) error {
	br := bufio.NewReaderSize(io.LimitReader(r, 8<<10), 8<<10)
	line, err := br.ReadBytes('\n')
	if err != nil {
		_, _ = io.Copy(io.Discard, br)
		return errors.New("the Tailcat server ended before reporting its address")
	}
	var rep childReport
	if err := json.Unmarshal(line, &rep); err != nil || rep.RegionID <= 0 {
		_, _ = io.Copy(io.Discard, br)
		return errors.New("the Tailcat server sent an unreadable report")
	}
	s.mu.Lock()
	k := s.key
	s.mu.Unlock()
	recorded := k.RegionID
	if recorded != 0 && recorded != rep.RegionID {
		return errors.New("the Tailcat server listened in another region than its key records")
	}
	k.RegionID = rep.RegionID
	want, err := k.Address()
	if err != nil || string(want) != rep.Address {
		return errors.New("the Tailcat server reported an address that does not match its key")
	}
	if recorded == 0 {
		if err := WriteKey(s.m.Layout, k); err != nil {
			return fmt.Errorf("record the Tailcat region: %w", err)
		}
		s.mu.Lock()
		s.key = k
		s.mu.Unlock()
	}
	s.setState(StateListening, rep.Address, "")
	s.m.Log.Info("remote access listening", "service", s.target.Service, "region", rep.RegionID)
	if !rep.NetSandbox {
		s.m.Log.Warn("remote access server runs without the Landlock TCP restriction (needs Linux 6.7+); "+
			"its user can open TCP to the data network", "service", s.target.Service)
	}
	// Keep draining stdout so the child never blocks on a full pipe, without
	// holding up the report: the caller waits on our return, not on EOF.
	go func() {
		_, _ = io.Copy(io.Discard, br)
		_, _ = io.Copy(io.Discard, r) // past the report's 8 KiB limit
	}()
	return nil
}

// lastLine keeps the child's last error line for the status message.
type lastLine struct {
	mu   sync.Mutex
	line string
}

func (l *lastLine) set(s string) { l.mu.Lock(); l.line = s; l.mu.Unlock() }
func (l *lastLine) get() string  { l.mu.Lock(); defer l.mu.Unlock(); return l.line }

// readStderr logs the child's own error lines (Tailscale's logs are
// discarded in the child). The address is redacted defensively.
func (s *server) readStderr(r io.Reader, last *lastLine) {
	sc := bufio.NewScanner(io.LimitReader(r, 1<<20))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	lines := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.mu.Lock()
		addr := s.address
		s.mu.Unlock()
		if addr != "" {
			line = strings.ReplaceAll(line, addr, "[address]")
		}
		line = redactTailcatAddr(line)
		if len(line) > 300 {
			line = line[:300] + "…"
		}
		last.set(line)
		if lines++; lines <= 20 {
			s.m.Log.Warn("remote access server", "service", s.target.Service, "line", line)
		}
	}
	_, _ = io.Copy(io.Discard, r)
}

// redactTailcatAddr replaces anything shaped like a Tailcat address.
func redactTailcatAddr(line string) string {
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '"' || r == '\'' })
	for _, f := range fields {
		if len(f) > 60 && strings.HasPrefix(f, "tc") {
			line = strings.ReplaceAll(line, f, "[address]")
		}
	}
	return line
}

// ---- stream proxy ----

func (s *server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			if s.ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				s.m.Log.Warn("remote access socket", "service", s.target.Service, "err", err)
			}
			return
		}
		s.wg.Go(func() { s.serve(c) })
	}
}

// admit applies the connection caps and registers the stream.
func (s *server) admit(st *stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return false
	}
	maxConns := s.m.MaxConns
	if maxConns <= 0 {
		maxConns = DefaultMaxConns
	}
	if len(s.conns) >= maxConns {
		return false
	}
	rate := s.m.Rate
	if rate <= 0 {
		rate = DefaultRate
	}
	now := time.Now()
	s.tokens = min(float64(s.m.burst()), s.tokens+now.Sub(s.refilled).Seconds()*rate)
	s.refilled = now
	if s.tokens < 1 {
		return false
	}
	s.tokens--
	s.conns[st] = struct{}{}
	return true
}

func (s *server) release(st *stream) {
	s.mu.Lock()
	delete(s.conns, st)
	s.mu.Unlock()
	st.close()
}

func (s *server) serve(c *net.UnixConn) {
	if uid, err := peerUID(c); err != nil || (uid != s.m.UID && uid != 0) {
		_ = c.Close()
		return
	}
	st := &stream{down: c}
	if !s.admit(st) {
		_ = c.Close()
		return
	}
	defer s.release(st)
	ctx, cancel := context.WithTimeout(s.ctx, dialTimeout)
	defer cancel()
	addr, err := s.m.Upstream(ctx, s.target.Service)
	if err != nil {
		return
	}
	d := net.Dialer{}
	up, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		_ = up.Close()
		return
	}
	st.up = up
	s.mu.Unlock()
	splice(c, up)
}

// splice copies both ways, propagating half-closes, until both directions
// end.
func splice(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Go(func() { cp(a, b) })
	wg.Go(func() { cp(b, a) })
	wg.Wait()
}

func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if serr != nil {
		return -1, serr
	}
	return int(cred.Uid), nil
}
