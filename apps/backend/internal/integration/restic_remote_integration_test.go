package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func (e *env) mysql(sql string) string {
	e.t.Helper()
	ctx := context.Background()
	ins, err := e.sdk.Inspect(ctx, e.c.Names.ServiceContainer("mysql84"))
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := e.sdk.Exec(ctx, ins.ID, docker.ExecRequest{Cmd: []string{"mysql",
		"--defaults-extra-file=/run/bento-secrets/client.cnf", "-N", "-B", "-e", sql}})
	if err != nil || res.ExitCode != 0 {
		e.t.Fatalf("mysql %q: %v %d %s", sql, err, res.ExitCode, res.Stderr)
	}
	return strings.TrimSpace(string(res.Stdout))
}

func (e *env) appExec(a domain.App, argv ...string) string {
	e.t.Helper()
	ctx := context.Background()
	obs, err := e.c.Observe(ctx, a)
	if err != nil || !obs.Running {
		e.t.Fatalf("%s is not running: %+v %v", a.Slug, obs, err)
	}
	req, err := operations.ExecRequestFor(a, argv, "")
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := e.sdk.Exec(ctx, obs.ContainerID, req)
	if err != nil || res.ExitCode != 0 {
		e.t.Fatalf("%s exec %v: %v exit %d %s%s", a.Slug, argv, err, res.ExitCode, res.Stdout, res.Stderr)
	}
	return strings.TrimSpace(string(res.Stdout))
}

// TestIntegrationResticRestoreFromAnotherStack backs up an app "shop" on stack
// A into a restic repository on a local rclone remote, shares that remote
// with a second disposable stack B (a bind mount of the repository
// directory) and restores the snapshot there through the cross-stack
// operations, using a key made with Add key. The clone on B must be left
// stopped, keep the home path and (with secrets) the database user and
// password, carry the data, and the pending key must be gone afterwards.
func TestIntegrationResticRestoreFromAnotherStack(t *testing.T) {
	a := setupWithUIDs(t, 40000)
	ctx := context.Background()
	// Stack A creates its Docker networks first: a stack plans its subnets
	// when it starts, so B is created afterwards and picks free ones.
	_, op, err := a.c.CreateService(ctx, domain.EngineMySQL, "8.4", "")
	a.wait(op, err)
	b := setupWithUIDs(t, 41000)
	for _, e := range []*env{a, b} {
		if err := os.WriteFile(filepath.Join(e.layout.RcloneDir(), "rclone.conf"), []byte("[dest]\ntype = local\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// One remote for both stacks: B's rclone directory sees A's repository.
	repoA := filepath.Join(a.layout.RcloneDir(), "repo")
	repoB := filepath.Join(b.layout.RcloneDir(), "repo")
	for _, d := range []string{repoA, repoB} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mount(repoA, repoB, "", syscall.MS_BIND, ""); err != nil {
		t.Fatalf("bind mount of the shared remote: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Unmount(repoB, syscall.MNT_DETACH) })

	// Stack A: the source app with MySQL, SQLite, home files and a minicrond job.
	rt := domain.Runtime{Kind: domain.RuntimeHTTP,
		HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000},
		Env:  []domain.EnvVar{{Key: "APP_KEY", Value: "base64:topsecret"}, {Key: "APP_ENV", Value: "production"}}}
	src, op, err := a.c.CreateApp(ctx, operations.CreateAppInput{Slug: "shop", Runtime: rt,
		Bindings: []operations.BindingRequest{{Engine: domain.EngineMySQL, Service: "mysql84"}, {Engine: domain.EngineSQLite}}}, "")
	a.wait(op, err)
	src, _ = store.GetApp(ctx, a.s.DB(), src.ID)
	var srcMySQL domain.Binding
	for _, bd := range src.Bindings {
		if bd.Engine == domain.EngineMySQL {
			srcMySQL = bd
		}
	}
	srcDB := srcMySQL.Databases[0]
	home := a.layout.AppHome("shop")
	writeFile(t, filepath.Join(a.layout.AppCode("shop"), "s.js"),
		"require('http').createServer((q,s)=>s.end('ok')).listen(3000,'0.0.0.0')", src.UID)
	writeFile(t, filepath.Join(home, "app", "data.txt"), "home-file", src.UID)
	writeFile(t, filepath.Join(home, "jobs.toml"), `[[job]]
name = "touch-marker"
schedule = "0 0 1 1 *"
argv = ["sh", "-c", "date > /home/shop/app/ran.txt"]
timeout = 30
`, src.UID)
	owner := platform.Owner{UID: src.UID, GID: src.GID}
	for _, bd := range src.Bindings {
		if bd.Engine == domain.EngineSQLite {
			if err := platform.CopyFile(a.layout.Database(), filepath.Join(a.layout.SQLiteFileDir(bd.SQLiteFileID), "shop.db"), 0o600, owner); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.mysql(fmt.Sprintf("CREATE TABLE %s.items (v VARCHAR(32)); INSERT INTO %s.items VALUES ('hello-remote');", srcDB, srcDB))
	a.wait(a.c.StartApp(ctx, src.ID, ""))
	a.appExec(src, "minicrond", "import", "/home/shop/jobs.toml")

	settings := domain.DefaultResticSettings()
	settings.Repository = "dest:/config/rclone/repo"
	settings.IncludeSecrets = true
	if _, err := a.c.SaveResticSettings(ctx, src.ID, settings, ""); err != nil {
		t.Fatal(err)
	}
	op, _, err = a.c.SubmitResticInit(ctx, src.ID, "")
	a.wait(op, err)
	a.wait(a.c.SubmitResticBackup(ctx, src.ID, "manual", ""))
	op, key, err := a.c.SubmitResticKeyAdd(ctx, src.ID, "stack-b", "export shop", "")
	a.wait(op, err)
	if key == "" {
		t.Fatal("Add key returned no key")
	}

	// Stack B: only a MySQL service of the same version.
	_, op, err = b.c.CreateService(ctx, domain.EngineMySQL, "8.4", "")
	b.wait(op, err)
	pendingGlob := filepath.Join(b.layout.SecretsDir(), "restic", "*.pending-*")
	in := operations.ResticRemoteInput{Repository: "dest:/config/rclone/repo", Key: key}

	// A wrong key is rejected and nothing is kept.
	bad := in
	bad.Key = "not-a-key-of-this-repository"
	op, err = b.c.SubmitResticInspectRemote(ctx, bad, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitTerminal(t, b, op); got.State != store.OpFailed || got.ErrorCode != "restic-key-rejected" {
		t.Fatalf("wrong key: %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}

	// Inspect lists the snapshots and previews the clone.
	in.Slug, in.KeepUsername = "shop-b", true
	got := b.wait(b.c.SubmitResticInspectRemote(ctx, in, ""))
	var preview operations.ResticClonePreview
	if err := json.Unmarshal(got.Result, &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Snapshots) != 1 || len(preview.Blockers) != 0 || preview.SourceSlug != "shop" || !preview.Secrets ||
		preview.HomePath != "/home/shop" || len(preview.Databases) != 1 || !preview.Databases[0].UsernameKept ||
		preview.Databases[0].Target != "shop_b_main" {
		t.Fatalf("preview %+v", preview)
	}
	if strings.Contains(string(got.Result), key) {
		t.Fatal("the key is in the preview")
	}
	if left, _ := filepath.Glob(pendingGlob); len(left) != 0 {
		t.Fatalf("pending key kept after inspect: %v", left)
	}

	// Restore.
	got = b.wait(b.c.SubmitRestoreFromBackup(ctx, in, "clone shop-b", ""))
	var result operations.ResticCloneResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Stopped || len(result.Checklist) == 0 || result.HomePath != "/home/shop" {
		t.Fatalf("result %+v", result)
	}
	if strings.Contains(string(got.Result), key) || strings.Contains(string(got.Result), srcMySQL.Password) ||
		strings.Contains(string(got.Result), "topsecret") {
		t.Fatal("a secret leaked into the operation result")
	}
	if left, _ := filepath.Glob(pendingGlob); len(left) != 0 {
		t.Fatalf("pending key kept after the restore: %v", left)
	}
	cp, err := store.GetApp(ctx, b.s.DB(), "shop-b")
	if err != nil {
		t.Fatal(err)
	}
	if cp.DesiredRuntime != domain.DesiredStopped || cp.Publication != domain.Unpublished || !cp.Provisioned ||
		cp.HomePath != "/home/shop" {
		t.Fatalf("clone %+v", cp)
	}
	if obs, _ := b.c.Observe(ctx, cp); obs.Running || obs.Exists {
		t.Fatalf("the clone must not run before Start: %+v", obs)
	}
	if _, err := os.Stat(filepath.Join(b.layout.SecretsDir(), "restic", cp.ID+".key")); err == nil {
		t.Fatal("the key must not be adopted without same-repo")
	}
	var cpMySQL domain.Binding
	for _, bd := range cp.Bindings {
		if bd.Engine == domain.EngineMySQL {
			cpMySQL = bd
		}
	}
	// Across stacks the source's database user is free, so it is kept with
	// its password; only the database name changes.
	if cpMySQL.Username != srcMySQL.Username || cpMySQL.Password != srcMySQL.Password ||
		len(cpMySQL.Databases) != 1 || cpMySQL.Databases[0] != "shop_b_main" {
		t.Fatalf("binding %+v, source user %s", cpMySQL, srcMySQL.Username)
	}
	if out := b.mysql("SELECT v FROM shop_b_main.items;"); out != "hello-remote" {
		t.Fatalf("data after the restore: %q", out)
	}
	cpHome := b.layout.AppHome("shop-b")
	assertOwned(t, filepath.Join(cpHome, "app", "data.txt"), cp.UID, "home-file")
	if info, err := os.Stat(filepath.Join(cpHome, ".local/share/minicron/minicron.db")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("minicron.db: %v %v", info, err)
	}

	// Start the clone: original home path, restored job runs.
	b.wait(b.c.StartApp(ctx, cp.ID, ""))
	if out := b.appExec(cp, "printenv", "HOME"); out != "/home/shop" {
		t.Fatalf("HOME in the clone is %q", out)
	}
	if out := b.appExec(cp, "minicrond", "list"); !strings.Contains(out, "touch-marker") {
		t.Fatalf("job missing in the clone: %s", out)
	}
	b.appExec(cp, "minicrond", "run", "touch-marker")
	marker := filepath.Join(cpHome, "app", "ran.txt")
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the restored job did not run in the clone")
		}
		time.Sleep(time.Second)
	}
	// The source stack is untouched.
	if _, err := os.Stat(filepath.Join(home, "app", "ran.txt")); err == nil {
		t.Fatal("the clone's job must not run in the source home")
	}
	if obs, _ := a.c.Observe(ctx, src); !obs.Running {
		t.Fatal("the source stopped")
	}
}

// waitTerminal waits for an operation that is expected to fail.
func waitTerminal(t *testing.T, e *env, op store.Operation) store.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := store.GetOperation(context.Background(), e.s.DB(), op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State.Terminal() {
			return got
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("operation %s timed out", op.ID)
	return op
}
