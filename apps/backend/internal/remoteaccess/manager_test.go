package remoteaccess

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"tailscale.com/tailcfg"
)

func TestMain(m *testing.M) {
	MaybeRunTestChild()
	maybeRunLandlockHelper()
	os.Exit(m.Run())
}

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("remote access runs children as another uid and needs root")
	}
}

// echoUpstream stands in for the data service: it echoes each connection.
func echoUpstream(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String()
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	layout := platform.Layout{Root: t.TempDir()}
	m := NewManager(layout, "/proc/self/exe", domain.TailcatUID, domain.TailcatUID, nil)
	m.Args = []string{TestChildArg}
	up := echoUpstream(t)
	m.Upstream = func(context.Context, string) (string, error) { return up, nil }
	t.Cleanup(m.StopAll)
	return m
}

func startListening(t *testing.T, m *Manager, service string) Status {
	t.Helper()
	if _, _, err := EnsureKey(m.Layout, service, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(Target{Service: service, Port: 5432}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	st := m.WaitSettled(ctx, service)
	if st.State != StateListening {
		t.Fatalf("state %s: %s", st.State, st.Message)
	}
	return st
}

func dialSocket(t *testing.T, m *Manager, service string) net.Conn {
	t.Helper()
	c, err := net.Dial("unix", m.Layout.RemoteSocket(service))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// echoes reports whether a round trip through the proxy works.
func echoes(c net.Conn) bool {
	c.SetDeadline(time.Now().Add(3 * time.Second))
	defer c.SetDeadline(time.Time{})
	if _, err := c.Write([]byte("ping")); err != nil {
		return false
	}
	buf := make([]byte, 4)
	_, err := io.ReadFull(c, buf)
	return err == nil && string(buf) == "ping"
}

func TestKeyOnlyTravelsOnStdin(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	startListening(t, m, "pg17")
	k, err := ReadKey(m.Layout, "pg17")
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := k.Private.MarshalText()
	psk, _ := k.PresharedKey.MarshalText()
	m.mu.Lock()
	s := m.servers["pg17"]
	m.mu.Unlock()
	cmd := s.command(t.Context())
	for _, field := range append(append([]string{}, cmd.Args...), cmd.Env...) {
		if strings.Contains(field, string(priv)) || strings.Contains(field, string(psk)) ||
			strings.Contains(field, strings.TrimPrefix(string(priv), "privkey:")) {
			t.Fatalf("key material in argv/env: %q", field)
		}
	}
	if len(cmd.Env) != 1 || cmd.Env[0] != "PATH=/usr/bin:/bin" {
		t.Fatalf("child environment %q", cmd.Env)
	}
	cred := cmd.SysProcAttr.Credential
	if cred == nil || cred.Uid != domain.TailcatUID || cred.Gid != domain.TailcatUID || len(cred.Groups) != 0 ||
		cred.NoSetGroups {
		t.Fatalf("child credential %+v", cred)
	}
	if cmd.SysProcAttr.Pdeathsig == 0 || len(cmd.ExtraFiles) != 1 {
		t.Fatalf("child attributes %+v, %d extra files", cmd.SysProcAttr, len(cmd.ExtraFiles))
	}
	payload, err := s.payload()
	if err != nil || !strings.Contains(string(payload), string(priv)) || !strings.Contains(string(payload), string(psk)) {
		t.Fatalf("stdin payload lacks the key: %v", err)
	}
	// The key file is root-only and records the region the child picked.
	info, err := os.Stat(m.Layout.RemoteKey("pg17"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v %v", info.Mode(), err)
	}
	if k.RegionID != StubRegion {
		t.Fatalf("recorded region %d", k.RegionID)
	}
	want, _ := k.Address()
	if st := m.Status("pg17"); st.Address != string(want) {
		t.Fatalf("status address %q, key gives %q", st.Address, want)
	}
	// The socket is reachable only by its owner (the Tailcat uid) and root.
	sock, err := os.Stat(m.Layout.RemoteSocket("pg17"))
	if err != nil || sock.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v %v", sock.Mode(), err)
	}
	dir, _ := os.Stat(m.Layout.RemoteSocketDir())
	if dir.Mode().Perm() != 0o710 {
		t.Fatalf("socket dir mode %v", dir.Mode())
	}
}

func TestAddressIsStableAcrossRestarts(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	first := startListening(t, m, "pg17").Address
	m.Stop("pg17")
	if st := m.Status("pg17"); st.State != StateOff {
		t.Fatalf("after stop: %s", st.State)
	}
	if _, err := os.Stat(m.Layout.RemoteSocket("pg17")); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
	if again := startListening(t, m, "pg17").Address; again != first {
		t.Fatal("restart changed the address")
	}
	// A new generation (rotate) is a new key and a new address.
	m.Stop("pg17")
	if _, created, err := EnsureKey(m.Layout, "pg17", 1, true); err != nil || !created {
		t.Fatalf("rotate key: %v %v", created, err)
	}
	if err := m.Start(Target{Service: "pg17", Port: 5432, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	st := m.WaitSettled(t.Context(), "pg17")
	if st.State != StateListening || st.Address == first {
		t.Fatalf("rotated: %s %v", st.State, st.Address == first)
	}
}

func TestProxyConnectionCap(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	m.MaxConns, m.Rate, m.Burst = 3, 1000, 1000
	startListening(t, m, "pg17")
	var open []net.Conn
	for range 3 {
		c := dialSocket(t, m, "pg17")
		if !echoes(c) {
			t.Fatal("connection within the cap refused")
		}
		open = append(open, c)
	}
	if n := m.Status("pg17").Connections; n != 3 {
		t.Fatalf("connections %d", n)
	}
	if echoes(dialSocket(t, m, "pg17")) {
		t.Fatal("connection over the cap admitted")
	}
	open[0].Close()
	deadline := time.Now().Add(3 * time.Second)
	for m.Status("pg17").Connections != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !echoes(dialSocket(t, m, "pg17")) {
		t.Fatal("a freed slot was not reused")
	}
}

func TestProxyRateLimit(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	m.MaxConns, m.Rate, m.Burst = 100, 0.5, 2
	startListening(t, m, "pg17")
	ok := 0
	for range 4 {
		c := dialSocket(t, m, "pg17")
		if echoes(c) {
			ok++
		}
		c.Close()
	}
	if ok != 2 {
		t.Fatalf("%d of 4 rapid connections admitted, want the burst of 2", ok)
	}
}

func TestStopCutsOpenConnections(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	startListening(t, m, "pg17")
	c := dialSocket(t, m, "pg17")
	if !echoes(c) {
		t.Fatal("proxy does not work")
	}
	done := make(chan struct{})
	go func() { m.Stop("pg17"); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("stop hung")
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("connection survived the stop: %v", err)
	}
}

func TestCrashedChildRestartsOnSameAddress(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	first := startListening(t, m, "pg17").Address
	m.mu.Lock()
	s := m.servers["pg17"]
	m.mu.Unlock()
	s.mu.Lock()
	pid := s.pid
	s.mu.Unlock()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		s.mu.Lock()
		restarted := s.pid != pid && s.state == StateListening
		s.mu.Unlock()
		if restarted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not restarted: %+v", m.Status("pg17"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st := m.Status("pg17"); st.Address != first {
		t.Fatal("restart changed the address")
	}
}

func TestMissingKeyIsNotReplaced(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	err := m.Start(Target{Service: "pg17", Port: 5432})
	if !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("start without key: %v", err)
	}
	if st := m.Status("pg17"); st.State != StateFailed || st.Message == "" {
		t.Fatalf("status %+v", st)
	}
	if _, err := os.Stat(m.Layout.RemoteKey("pg17")); !os.IsNotExist(err) {
		t.Fatal("a key was created without an explicit enable")
	}
}

func TestKeyFileRefusals(t *testing.T) {
	requireRoot(t)
	layout := platform.Layout{Root: t.TempDir()}
	k, created, err := EnsureKey(layout, "pg17", 2, true)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	if again, created, err := EnsureKey(layout, "pg17", 2, false); err != nil || created || !again.Private.Equal(k.Private) {
		t.Fatal("same generation must reuse the key")
	}
	if _, _, err := EnsureKey(layout, "pg17", 1, false); err == nil {
		t.Fatal("a key newer than the service generation was accepted")
	}
	path := layout.RemoteKey("pg17")
	os.Chmod(path, 0o644)
	if _, err := ReadKey(layout, "pg17"); err == nil {
		t.Fatal("a group/world-readable key was accepted")
	}
	os.Chmod(path, 0o600)
	os.Chown(path, 1234, 0)
	if _, err := ReadKey(layout, "pg17"); err == nil {
		t.Fatal("a key not owned by root was accepted")
	}
	os.Chown(path, 0, 0)
	if _, err := ReadKey(layout, "mysql84"); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("other service: %v", err)
	}
	os.WriteFile(path, []byte(`{"version":1,"service":"other"}`), 0o600)
	if _, _, err := EnsureKey(layout, "pg17", 3, true); err == nil {
		t.Fatal("an unreadable key was overwritten")
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "other") {
		t.Fatal("the refused key file was changed")
	}
}

func TestReportMustMatchKey(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	k := NewKey("pg17", 0)
	k.RegionID = 7
	s := &server{m: m, key: k, target: Target{Service: "pg17"}, changed: make(chan struct{})}
	other := NewKey("pg17", 0)
	other.RegionID = 7
	bad, _ := other.Address()
	if err := s.readReport(strings.NewReader(`{"address":"` + string(bad) + `","regionId":7}` + "\n")); err == nil {
		t.Fatal("a foreign address was accepted")
	}
	good, _ := k.Address()
	if err := s.readReport(strings.NewReader(`{"address":"` + string(good) + `","regionId":8}` + "\n")); err == nil {
		t.Fatal("another region than recorded was accepted")
	}
	if err := s.readReport(strings.NewReader(`{"address":"` + string(good) + `","regionId":7}` + "\n")); err != nil {
		t.Fatal(err)
	}
}

// TestReportReturnsWhileChildRuns guards against waiting for the child's
// stdout to close: a healthy child keeps it open, so the report must be
// accepted as soon as its line arrives (it used to time out after 90 s and
// SIGTERM every child).
func TestReportReturnsWhileChildRuns(t *testing.T) {
	requireRoot(t)
	m := newTestManager(t)
	k := NewKey("pg17", 0)
	k.RegionID = 7
	s := &server{m: m, key: k, target: Target{Service: "pg17"}, changed: make(chan struct{})}
	good, _ := k.Address()
	pr, pw := io.Pipe()
	defer pw.Close()
	done := make(chan error, 1)
	go func() { done <- s.readReport(pr) }()
	go func() {
		_, _ = io.WriteString(pw, `{"address":"`+string(good)+`","regionId":7,"netSandbox":true}`+"\n")
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readReport waited for the child's stdout to close")
	}
	// The child keeps writing after its report; it must never block.
	if _, err := io.WriteString(pw, strings.Repeat("x", 64<<10)); err != nil {
		t.Fatal(err)
	}
}

const landlockHelperEnv = "BENTO_TEST_LANDLOCK_HELPER"

// maybeRunLandlockHelper runs in a re-executed test binary: it restricts
// itself to the allowed port, then dials both listeners and exits with
// 0 = allowed connected and denied refused, 3 = unsupported, 1 = otherwise.
func maybeRunLandlockHelper() {
	spec := os.Getenv(landlockHelperEnv)
	if spec == "" {
		return
	}
	allowed, denied, _ := strings.Cut(spec, ",")
	_, ap, _ := net.SplitHostPort(allowed)
	port, _ := strconv.Atoi(ap)
	if err := setNoNewPrivsAllThreads(); err != nil {
		os.Exit(1)
	}
	ok, err := restrictTCPConnect([]uint16{uint16(port)})
	if err != nil {
		os.Exit(1)
	}
	if !ok {
		os.Exit(3)
	}
	c, err := net.DialTimeout("tcp", allowed, 2*time.Second)
	if err != nil {
		os.Exit(1)
	}
	c.Close()
	if c, err := net.DialTimeout("tcp", denied, 2*time.Second); err == nil {
		c.Close()
		os.Exit(1)
	} else if !errors.Is(err, syscall.EACCES) {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRestrictTCPConnect(t *testing.T) {
	listen := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		return ln.Addr().String()
	}
	allowed, denied := listen(), listen()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), landlockHelperEnv+"="+allowed+","+denied)
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee) && ee.ExitCode() == 3:
		t.Skip("no Landlock network support on this kernel or build")
	default:
		t.Fatalf("Landlock helper: %v", err)
	}
}

func TestRelayTCPPorts(t *testing.T) {
	cfg := childConfig{DERPMapURL: "http://127.0.0.1:8123/derpmap"}
	region := &tailcfg.DERPRegion{Nodes: []*tailcfg.DERPNode{{DERPPort: 8443}, {DERPPort: 0}, nil}}
	got := relayTCPPorts(cfg, region)
	want := []uint16{443, 80, 8443, 8123}
	if len(got) != len(want) {
		t.Fatalf("ports = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ports = %v, want %v", got, want)
		}
	}
}

func TestChildRefusesRoot(t *testing.T) {
	requireRoot(t)
	if err := dropPrivilegeLeftovers(); err == nil {
		t.Fatal("the child accepted to run as root")
	}
}

func TestRedactsAddressShapes(t *testing.T) {
	addr := "tc" + strings.Repeat("A", 120)
	if got := redactTailcatAddr("listening on " + addr + " now"); strings.Contains(got, addr) {
		t.Fatalf("not redacted: %s", got)
	}
}
