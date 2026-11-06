package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// edgeAnswers reports whether the edge answers HTTP on 127.0.0.1:port (an
// unknown host gets the edge's default 404).
func edgeAnswers(port int) bool {
	c := http.Client{Timeout: 2 * time.Second}
	for range 20 {
		res, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err == nil {
			res.Body.Close()
			return res.StatusCode == http.StatusNotFound
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func (e *env) configureEdge(httpPort, httpsPort int) store.Operation {
	e.t.Helper()
	s := domain.DefaultEdgeSettings()
	s.Enabled, s.Bind, s.HTTPPort, s.HTTPSPort = true, "127.0.0.1", httpPort, httpsPort
	op, err := e.c.ConfigureEdge(context.Background(), s, "")
	if err != nil {
		e.t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := store.GetOperation(context.Background(), e.s.DB(), op.ID)
		if err != nil {
			e.t.Fatal(err)
		}
		if got.State.Terminal() {
			return got
		}
		time.Sleep(250 * time.Millisecond)
	}
	e.t.Fatalf("edge apply %s timed out", op.ID)
	return op
}

func (e *env) edgeID() (string, bool) {
	e.t.Helper()
	ins, err := e.sdk.Inspect(context.Background(), e.c.Names.EdgeContainer())
	if err != nil || ins == nil {
		e.t.Fatalf("edge container missing: %v", err)
	}
	return ins.ID, ins.State != nil && ins.State.Running
}

func (e *env) assertNoPreviousEdge() {
	e.t.Helper()
	if ins, _ := e.sdk.Inspect(context.Background(), e.c.Names.EdgePreviousContainer()); ins != nil {
		e.t.Fatal("previous edge container left behind")
	}
}

// A port change recreates the edge on the new ports. A recreate whose new
// ports are taken fails at start and puts the previous edge back, serving on
// its old ports.
func TestIntegrationEdgeRecreateAndRollback(t *testing.T) {
	e := setup(t)
	p1, p2 := freePort(t), freePort(t)
	if op := e.configureEdge(p1, p2); op.State != store.OpSucceeded {
		t.Fatalf("enable edge: %s: %s", op.ErrorCode, op.ErrorMessage)
	}
	first, running := e.edgeID()
	if !running || !edgeAnswers(p1) {
		t.Fatal("edge must serve on its first port")
	}

	p3, p4 := freePort(t), freePort(t)
	if op := e.configureEdge(p3, p4); op.State != store.OpSucceeded {
		t.Fatalf("move edge ports: %s: %s", op.ErrorCode, op.ErrorMessage)
	}
	second, running := e.edgeID()
	if second == first || !running {
		t.Fatal("a port change must recreate the edge")
	}
	if !edgeAnswers(p3) {
		t.Fatal("recreated edge must serve on the new port")
	}
	if c := (http.Client{Timeout: time.Second}); func() bool {
		res, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/", p1))
		if err == nil {
			res.Body.Close()
		}
		return err == nil
	}() {
		t.Fatal("the old port must be released")
	}
	e.assertNoPreviousEdge()

	// Hold a port on the host so the new edge cannot bind it at start.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	taken := busy.Addr().(*net.TCPAddr).Port
	op := e.configureEdge(taken, freePort(t))
	if op.State != store.OpFailed || op.ErrorCode != "edge-start-failed" {
		t.Fatalf("want edge-start-failed, got %s %s: %s", op.State, op.ErrorCode, op.ErrorMessage)
	}
	restored, running := e.edgeID()
	if restored != second || !running {
		t.Fatal("the previous edge must be restored and running")
	}
	if !edgeAnswers(p3) {
		t.Fatal("the restored edge must serve on its previous port")
	}
	e.assertNoPreviousEdge()
}

// edgeGet fetches / for host through the edge on 127.0.0.1:port.
func edgeGet(t *testing.T, port int, host string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	c := &http.Client{Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", host, err)
	}
	res.Body.Close()
	return res.StatusCode
}

// An upstream named by DNS is resolved at run time: a name that does not
// resolve gives that host 502s and never fails the edge config, so the edge
// keeps serving every other host (and would start after a reboot without
// DNS).
func TestIntegrationEdgeUnresolvableUpstream(t *testing.T) {
	e := setup(t)
	p1, p2 := freePort(t), freePort(t)
	if op := e.configureEdge(p1, p2); op.State != store.OpSucceeded {
		t.Fatalf("enable edge: %s: %s", op.ErrorCode, op.ErrorMessage)
	}
	ctx := context.Background()
	_, op, err := e.c.CreateHost(ctx, operations.HostInput{Name: "gone.example.com", Target: domain.HostTargetUpstream,
		Upstreams: []string{"http://no-such-host.invalid:8080"}, Enabled: true,
		Route: domain.Route{TLS: domain.TLSNone}}, "")
	if got := e.wait(op, err); got.State != store.OpSucceeded {
		t.Fatalf("an unresolvable upstream must not fail the edge apply: %s: %s", got.ErrorCode, got.ErrorMessage)
	}
	_, op, err = e.c.CreateHost(ctx, operations.HostInput{Name: "away.example.com", Target: domain.HostTargetRedirect,
		RedirectTo: "elsewhere.example.com", Enabled: true, Route: domain.Route{TLS: domain.TLSNone}}, "")
	if got := e.wait(op, err); got.State != store.OpSucceeded {
		t.Fatalf("a second host next to the unresolvable one: %s: %s", got.ErrorCode, got.ErrorMessage)
	}
	if _, running := e.edgeID(); !running {
		t.Fatal("the edge must keep running")
	}
	if code := edgeGet(t, p1, "gone.example.com"); code != http.StatusBadGateway {
		t.Fatalf("the unresolvable host: want 502, got %d", code)
	}
	if code := edgeGet(t, p1, "away.example.com"); code < 300 || code > 399 {
		t.Fatalf("the other host must keep serving its redirect, got %d", code)
	}
}
