package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// fakeBackend serves canned JSON for the listing endpoints on the stack's
// control socket.
func fakeBackend(t *testing.T) platform.Layout {
	t.Helper()
	// Unix socket paths are short; t.TempDir can exceed the limit.
	dir, err := os.MkdirTemp("", "bento-cli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	layout := platform.Layout{Root: dir}
	if err := os.MkdirAll(filepath.Dir(layout.ControlSocket()), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", layout.ControlSocket())
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{
		"/api/v1/system":     `{"stackName":"dev"}`,
		"/api/v1/apps":       `{"apps":[{"slug":"shop","hosts":[]}]}`,
		"/api/v1/operations": `{"operations":[{"id":"op1","kind":"app.start"}]}`,
		"/api/v1/services":   `{"services":[{"name":"mysql84"}]}`,
		"/api/v1/hosts":      `{"hosts":[{"name":"shop.example.com","target":{"kind":"app"}}]}`,
		"/api/v1/retired":    `{"retired":[]}`,
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return layout
}

// Every listing honors --json both before the command (global) and after it
// (e2e finding: a listing given a trailing --json printed the table).
func TestListCommandsHonorJSON(t *testing.T) {
	layout := fakeBackend(t)
	commands := [][]string{
		{"status"},
		{"apps"},
		{"ops"},
		{"services"},
		{"hosts"},
		{"retired"},
	}
	for _, cmd := range commands {
		for _, mode := range []string{"global", "trailing"} {
			var out bytes.Buffer
			r := &runner{layout: layout, wait: true, out: &out}
			args := cmd
			if mode == "global" {
				r.json = true
			} else {
				args = append(append([]string{}, cmd...), "--json")
			}
			if err := r.run(args); err != nil {
				t.Fatalf("%s (%s): %v", strings.Join(cmd, " "), mode, err)
			}
			var v map[string]any
			if err := json.Unmarshal(out.Bytes(), &v); err != nil {
				t.Fatalf("%s (%s) is not JSON: %v\n%s", strings.Join(cmd, " "), mode, err, out.String())
			}
		}
	}
}

// `bento backup password` reads the stack key on this host; it never asks
// the backend, which does not serve it.
func TestBackupPasswordReadsHostKey(t *testing.T) {
	layout := platform.Layout{Root: t.TempDir()}
	var out bytes.Buffer
	r := &runner{layout: layout, wait: true, out: &out}
	if err := r.run([]string{"backup", "password"}); err == nil || !strings.Contains(err.Error(), "no backup password yet") {
		t.Fatalf("missing key: %v", err)
	}
	if err := os.MkdirAll(layout.ResticKeyDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.StackBackupKey(), []byte("the-stack-password\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := r.run([]string{"backup", "password"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "the-stack-password\n" {
		t.Fatalf("printed %q", out.String())
	}
	for _, args := range [][]string{{"backup"}, {"backup", "list"}, {"backup", "password", "extra"}} {
		if err := r.run(args); err == nil {
			t.Fatalf("%v must be refused", args)
		}
	}
}
