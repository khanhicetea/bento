package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// TestIntegrationResticAppBackup backs an app up into a restic repository
// on a local rclone remote, checks it, and connects a second app with an
// added key. App backups are never restored over the live app.
func TestIntegrationResticAppBackup(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(e.layout.RcloneDir(), "rclone.conf"), []byte("[dest]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}}
	app := e.create("shop", rt)
	home := e.layout.AppHome(app.Slug)
	writeFile(t, filepath.Join(home, "app", "storage", "u.jpg"), "image", app.UID)
	writeFile(t, filepath.Join(home, "app", "node_modules", "dep.js"), "dep", app.UID)
	if err := platform.CopyFile(e.layout.Database(), filepath.Join(home, "app", "data.sqlite"), 0o600,
		platform.Owner{UID: app.UID, GID: app.GID}); err != nil {
		t.Fatal(err)
	}

	settings := domain.DefaultResticSettings()
	settings.Repository = "dest:/config/rclone/repo"
	settings.Excludes = []string{"node_modules"}
	settings.SQLitePaths = []string{"app/data.sqlite"}
	if _, err := e.c.SaveResticSettings(ctx, app.ID, settings); err != nil {
		t.Fatal(err)
	}
	op, key, err := e.c.SubmitResticInit(ctx, app.ID, "")
	e.wait(op, err)
	e.wait(e.c.SubmitResticBackup(ctx, app.ID, "manual", ""))
	v, _ := e.c.ResticSettings(ctx, app.ID)
	if v.State.LastBackup == nil || !v.State.LastBackup.OK || len(v.State.Snapshots) != 1 {
		t.Fatalf("after backup: %+v", v.State)
	}
	snap := v.State.Snapshots[0].ID
	e.wait(e.c.SubmitResticSimple(ctx, operations.KindResticCheck, app.ID, ""))
	op, extra, err := e.c.SubmitResticKeyAdd(ctx, app.ID, "handover", "export shop", "")
	e.wait(op, err)
	if extra == "" || extra == key {
		t.Fatal("added key must be new")
	}

	// Another app connects with the handed-over key and sees the snapshot.
	other := e.create("copy", rt)
	settings.Schedule.Enabled = false
	if _, err := e.c.SaveResticSettings(ctx, other.ID, settings); err != nil {
		t.Fatal(err)
	}
	e.wait(e.c.SubmitResticConnect(ctx, other.ID, extra, ""))
	ov, _ := e.c.ResticSettings(ctx, other.ID)
	if len(ov.State.Snapshots) != 1 || ov.State.Snapshots[0].ID != snap {
		t.Fatalf("connected app should list the source snapshot: %+v", ov.State.Snapshots)
	}
}

func assertOwned(t *testing.T, path string, uid int, body string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != body {
		t.Fatalf("%s: %q %v", path, got, err)
	}
	o, _, err := platform.StatOwner(path)
	if err != nil || o.UID != uid {
		t.Fatalf("%s owner %+v, want uid %d", path, o, uid)
	}
}

// TestIntegrationResticUnreachableRemoteFailsFast: restic retries a failing
// backend for minutes; the rclone probe must fail init quickly instead.
func TestIntegrationResticUnreachableRemoteFailsFast(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	conf := "[dead]\ntype = s3\nprovider = Other\nendpoint = http://127.0.0.1:9\naccess_key_id = x\nsecret_access_key = y\n"
	if err := os.WriteFile(filepath.Join(e.layout.RcloneDir(), "rclone.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}}
	app := e.create("shop", rt)
	settings := domain.DefaultResticSettings()
	settings.Repository = "dead:nobucket/shop"
	if _, err := e.c.SaveResticSettings(ctx, app.ID, settings); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	op, _, err := e.c.SubmitResticInit(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for {
		got, err := store.GetOperation(ctx, e.s.DB(), op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State.Terminal() {
			if got.State != store.OpFailed || got.ErrorCode != "remote-unusable" {
				t.Fatalf("got %s %s: %s", got.State, got.ErrorCode, got.ErrorMessage)
			}
			break
		}
		if time.Since(started) > 3*time.Minute {
			t.Fatal("init against an unreachable remote did not fail fast")
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("failed after %s", time.Since(started).Round(time.Second))
}
