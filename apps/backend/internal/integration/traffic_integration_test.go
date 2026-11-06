package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/traffic"
)

// The real edge writes only page requests of an access-logged host to its
// traffic log (no query strings, static files or dotfiles); a traffic cycle
// rotates the file, has the edge reopen it (USR1), and the pinned GoAccess
// turns it into a report. Requests after the cycle land in the new file and
// are added by the next cycle.
func TestIntegrationTrafficReport(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	port := freePort(t)
	if op := e.configureEdge(port, freePort(t)); op.State != store.OpSucceeded {
		t.Fatalf("enable edge: %s: %s", op.ErrorCode, op.ErrorMessage)
	}
	const host = "traffic.example.com"
	op := e.wait(func() (store.Operation, error) {
		_, op, err := e.c.CreateHost(ctx, operations.HostInput{Name: host, Target: domain.HostTargetRedirect,
			RedirectTo: "elsewhere.example.com", Enabled: true,
			Route: domain.Route{TLS: domain.TLSNone, AccessLog: true}}, "")
		return op, err
	}())
	if op.State != store.OpSucceeded {
		t.Fatalf("create host: %s: %s", op.ErrorCode, op.ErrorMessage)
	}
	client := http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	get := func(path string) {
		t.Helper()
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		req.Host = host
		req.Header.Set("User-Agent", "bento-integration/1.0")
		req.Header.Set("Referer", "https://ref.example.org/from?secret=1")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("GET %s -> %d", path, res.StatusCode)
		}
	}
	for _, p := range []string{"/", "/blog/post?token=hunter2", "/blog/post", "/app.css", "/img/logo.PNG", "/.env", "/.git/config"} {
		get(p)
	}
	live := filepath.Join(e.layout.TrafficLogsDir(), edge.TrafficLogFile(host))
	logged := waitForLines(t, live, 3)
	for _, banned := range []string{"hunter2", "secret", ".css", ".PNG", ".env", ".git"} {
		if strings.Contains(logged, banned) {
			t.Fatalf("traffic log holds %q:\n%s", banned, logged)
		}
	}
	if !strings.Contains(logged, `"GET /blog/post HTTP/1.1" 301`) || !strings.Contains(logged, `"https://ref.example.org/from"`) {
		t.Fatalf("unexpected traffic log:\n%s", logged)
	}

	if err := e.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.c.TrafficStatus(host); st.Error != "" {
		t.Fatalf("traffic run failed: %s", st.Error)
	}
	r, _, ok, err := e.c.TrafficReport(host)
	if err != nil || !ok {
		t.Fatalf("no report: %v", err)
	}
	if r.Requests != 3 || r.Invalid != 0 || r.Paths[0].Path != "/blog/post" || r.Paths[0].Requests != 2 {
		t.Fatalf("report = %+v", r)
	}
	pending, _ := os.ReadDir(filepath.Join(e.layout.TrafficHostDir(host), traffic.PendingDir))
	if len(pending) != 0 {
		t.Fatalf("pending logs left: %d", len(pending))
	}

	// The edge reopened its log: a new request goes to a new live file.
	get("/after")
	if got := waitForLines(t, live, 1); !strings.Contains(got, "/after") {
		t.Fatalf("new live log:\n%s", got)
	}
	if err := e.c.TrafficCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := e.c.TrafficReport(host); r.Requests != 4 {
		t.Fatalf("second run: requests = %d, want 4", r.Requests)
	}
	if _, running := e.edgeID(); !running {
		t.Fatal("the edge must keep running")
	}
}

// waitForLines waits until path holds at least n lines (nginx writes the
// access log line after the response).
func waitForLines(t *testing.T, path string, n int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if strings.Count(string(b), "\n") >= n || time.Now().After(deadline) {
			if strings.Count(string(b), "\n") < n {
				t.Fatalf("%s has fewer than %d lines:\n%s", path, n, b)
			}
			return string(b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
