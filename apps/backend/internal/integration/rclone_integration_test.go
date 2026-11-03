package integration

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
)

// TestIntegrationRcloneShell configures a remote from the rclone shell
// container, then tests remotes with that same config. A local
// remote under the config mount stands in for cloud storage so the result is
// visible on the host.
func TestIntegrationRcloneShell(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tool, err := e.c.OpenRcloneShell(ctx, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer tool.Close()
	shell := func(argv ...string) docker.ExecResult {
		t.Helper()
		req := backup.RcloneShellExec()
		req.Cmd = argv
		res, err := e.sdk.Exec(ctx, tool.ContainerID, req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := shell("rclone", "config", "create", "dest", "local"); res.ExitCode != 0 {
		t.Fatalf("rclone config create: %d %s", res.ExitCode, res.Stderr)
	}
	if res := shell("sh", "-c", "touch /etc/x"); res.ExitCode == 0 {
		t.Fatal("shell root filesystem must be read-only")
	}
	// The interactive shell the UI attaches to.
	sess, err := e.sdk.ExecAttach(ctx, tool.ContainerID, backup.RcloneShellExec(), 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Conn.Write([]byte("rclone listremotes; exit\n")); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(sess.Read)
	_ = sess.Conn.Close()
	if !strings.Contains(string(out), "rclone:") || !strings.Contains(string(out), "dest:") {
		t.Fatalf("tty shell output: %q", out)
	}

	cfg, err := backup.ReadRcloneConfig(e.layout.RcloneDir())
	if err != nil || cfg.Encrypted || len(cfg.Remotes) != 1 || cfg.Remotes[0].Name != "dest" || cfg.Remotes[0].Type != "local" {
		t.Fatalf("config written by the shell: %+v %v", cfg, err)
	}

	deps := e.c.BackupDeps(nil)
	if err := os.MkdirAll(filepath.Join(e.layout.RcloneDir(), "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	if msg, err := deps.TestRemote(ctx, "dest:/config/rclone/out"); err != nil || !strings.Contains(msg, "reachable") {
		t.Fatalf("test existing: %q %v", msg, err)
	}
	if msg, err := deps.TestRemote(ctx, "dest:/config/rclone/missing"); err != nil || !strings.Contains(msg, "will be created") {
		t.Fatalf("test missing path: %q %v", msg, err)
	}
	if _, err := deps.TestRemote(ctx, "nope:x"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unknown remote: %v", err)
	}
}
