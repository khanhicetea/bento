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
		"/api/v1/system":            `{"stackName":"dev"}`,
		"/api/v1/apps":              `{"apps":[{"slug":"shop","hosts":[]}]}`,
		"/api/v1/operations":        `{"operations":[{"id":"op1","kind":"app.start"}]}`,
		"/api/v1/services":          `{"services":[{"name":"mysql84"}]}`,
		"/api/v1/hosts":             `{"hosts":[{"name":"shop.example.com","target":{"kind":"app"}}]}`,
		"/api/v1/retired":           `{"retired":[]}`,
		"/api/v1/backups/artifacts": `{"artifacts":[{"path":"mysql84/shop.sql.zst","engine":"mysql"}]}`,
		"/api/v1/backups/runs":      `{"runs":[]}`,
		"/api/v1/backups/schedules": `{"timeZone":"UTC","schedules":[{"id":"s1","name":"nightly"}]}`,
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
// (e2e finding: `bento backup list --json` printed the table).
func TestListCommandsHonorJSON(t *testing.T) {
	layout := fakeBackend(t)
	commands := [][]string{
		{"status"},
		{"apps"},
		{"ops"},
		{"services"},
		{"hosts"},
		{"retired"},
		{"backup", "list"},
		{"backup", "runs"},
		{"backup", "schedule", "list"},
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

func TestBackupListTableWithoutJSON(t *testing.T) {
	layout := fakeBackend(t)
	var out bytes.Buffer
	r := &runner{layout: layout, wait: true, out: &out}
	if err := r.run([]string{"backup", "list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "PATH") || !strings.Contains(out.String(), "mysql84/shop.sql.zst") {
		t.Fatalf("table expected:\n%s", out.String())
	}
	if err := r.run([]string{"backup", "list", "extra"}); err == nil {
		t.Fatal("a stray argument must be refused")
	}
}
