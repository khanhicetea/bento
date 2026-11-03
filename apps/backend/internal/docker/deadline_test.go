package docker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// hungEngine serves the Engine API on a Unix socket and never finishes a
// response: plain requests get nothing, image pulls get headers and then
// silence, like a wedged dockerd or a stalled registry.
func hungEngine(t *testing.T) *SDK {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/images/create") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"Pulling from library/x"}` + "\n"))
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	c, err := client.New(client.WithHost("unix://"+sock), client.WithAPIVersion(MinAPIVersion))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &SDK{c: c}
}

func shorten(t *testing.T, v *time.Duration, d time.Duration) {
	old := *v
	*v = d
	t.Cleanup(func() { *v = old })
}

func TestHungEngineCallsEndAtTheirDeadline(t *testing.T) {
	shorten(t, &APITimeout, 200*time.Millisecond)
	shorten(t, &PullStallTimeout, 300*time.Millisecond)
	e := hungEngine(t)
	start := time.Now()
	_, err := e.Inspect(t.Context(), "anything")
	if !errors.Is(err, ErrEngineTimeout) || !strings.Contains(err.Error(), "container inspect did not finish within") {
		t.Fatalf("inspect: %v", err)
	}
	if err := e.Stop(t.Context(), "anything", 100*time.Millisecond); !errors.Is(err, ErrEngineTimeout) {
		t.Fatalf("stop: %v", err)
	}
	err = e.PullImage(t.Context(), "example.test/x:1", nil)
	if !errors.Is(err, ErrEngineTimeout) || !strings.Contains(err.Error(), "made no progress") {
		t.Fatalf("pull: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("deadlines took %s", elapsed)
	}
}

func TestCallerCancellationIsNotReportedAsTimeout(t *testing.T) {
	e := hungEngine(t)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := e.Inspect(ctx, "anything")
	if err == nil || errors.Is(err, ErrEngineTimeout) {
		t.Fatalf("cancelled inspect: %v", err)
	}
}
