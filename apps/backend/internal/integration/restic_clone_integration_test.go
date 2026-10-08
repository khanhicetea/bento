package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// TestIntegrationResticCloneSameStack backs up an app "shop" (MySQL + SQLite +
// home files + a minicrond job using /home/shop/app) into a restic repository
// on a local rclone remote and restores the snapshot into a new app
// "shop-copy" on the same stack, once with secrets in the snapshot and once
// without. The clone must be left stopped, keep the in-container home path,
// map the database name and carry the data, and its scheduler job must run.
func TestIntegrationResticCloneSameStack(t *testing.T) {
	for _, secrets := range []bool{true, false} {
		t.Run(fmt.Sprintf("secrets=%v", secrets), func(t *testing.T) { cloneScenario(t, secrets) })
	}
}

func cloneScenario(t *testing.T, secrets bool) {
	e := setup(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(e.layout.RcloneDir(), "rclone.conf"), []byte("[dest]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, op, err := e.c.CreateService(ctx, domain.EngineMySQL, "8.4", "")
	e.wait(op, err)

	rt := domain.Runtime{Kind: domain.RuntimeHTTP,
		HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000},
		Env:  []domain.EnvVar{{Key: "APP_KEY", Value: "base64:topsecret"}, {Key: "APP_ENV", Value: "production"}}}
	app, op, err := e.c.CreateApp(ctx, operations.CreateAppInput{Slug: "shop", Runtime: rt,
		Bindings: []operations.BindingRequest{{Engine: domain.EngineMySQL, Service: "mysql84"}, {Engine: domain.EngineSQLite}}}, "")
	e.wait(op, err)
	app, _ = store.GetApp(ctx, e.s.DB(), app.ID)
	var mysqlBinding, sqliteBinding domain.Binding
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineMySQL {
			mysqlBinding = b
		} else {
			sqliteBinding = b
		}
	}
	srcDB := mysqlBinding.Databases[0]
	home := e.layout.AppHome("shop")
	writeFile(t, filepath.Join(e.layout.AppCode("shop"), "s.js"),
		"require('http').createServer((q,s)=>s.end('ok')).listen(3000,'0.0.0.0')", app.UID)
	writeFile(t, filepath.Join(home, "app", "data.txt"), "home-file", app.UID)
	writeFile(t, filepath.Join(home, "jobs.toml"), `[[job]]
name = "touch-marker"
schedule = "0 0 1 1 *"
argv = ["sh", "-c", "date > /home/shop/app/ran.txt"]
timeout = 30
`, app.UID)
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	srcSQLite := filepath.Join(e.layout.SQLiteFileDir(sqliteBinding.SQLiteFileID), "shop.db")
	if err := platform.CopyFile(e.layout.Database(), srcSQLite, 0o600, owner); err != nil {
		t.Fatal(err)
	}
	mysql := func(sql string) string {
		t.Helper()
		ins, err := e.sdk.Inspect(ctx, e.c.Names.ServiceContainer("mysql84"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.sdk.Exec(ctx, ins.ID, docker.ExecRequest{Cmd: []string{"mysql",
			"--defaults-extra-file=/run/bento-secrets/client.cnf", "-N", "-B", "-e", sql}})
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("mysql %q: %v %d %s", sql, err, res.ExitCode, res.Stderr)
		}
		return strings.TrimSpace(string(res.Stdout))
	}
	mysql(fmt.Sprintf("CREATE TABLE %s.items (v VARCHAR(32)); INSERT INTO %s.items VALUES ('hello-clone');", srcDB, srcDB))

	// Start the source so minicrond creates its database, then add a job.
	e.wait(e.c.StartApp(ctx, app.ID, ""))
	appExec := func(a domain.App, argv ...string) string {
		t.Helper()
		obs, err := e.c.Observe(ctx, a)
		if err != nil || !obs.Running {
			t.Fatalf("%s is not running: %+v %v", a.Slug, obs, err)
		}
		req, err := operations.ExecRequestFor(a, argv, "")
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.sdk.Exec(ctx, obs.ContainerID, req)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("%s exec %v: %v exit %d %s%s", a.Slug, argv, err, res.ExitCode, res.Stdout, res.Stderr)
		}
		return strings.TrimSpace(string(res.Stdout))
	}
	appExec(app, "minicrond", "import", "/home/shop/jobs.toml")
	if out := appExec(app, "minicrond", "list"); !strings.Contains(out, "touch-marker") {
		t.Fatalf("job not registered: %s", out)
	}

	settings := domain.DefaultResticSettings()
	settings.Repository = "dest:/config/rclone/repo"
	settings.IncludeSecrets = secrets
	if _, err := e.c.SaveResticSettings(ctx, app.ID, settings, ""); err != nil {
		t.Fatal(err)
	}
	op, _, err = e.c.SubmitResticInit(ctx, app.ID, "")
	e.wait(op, err)
	e.wait(e.c.SubmitResticBackup(ctx, app.ID, "manual", ""))
	v, _ := e.c.ResticSettings(ctx, app.ID)
	if v.State.LastBackup == nil || !v.State.LastBackup.OK || len(v.State.Snapshots) != 1 {
		t.Fatalf("after backup: %+v", v.State)
	}
	snap := v.State.Snapshots[0]
	if got := slicesContains(snap.Tags, operations.ResticSecretsTag); got != secrets {
		t.Fatalf("snapshot tags %v, secrets tag expected=%v", snap.Tags, secrets)
	}

	// Inspect previews the clone without changing anything.
	e.wait(e.c.SubmitResticInspect(ctx, app.ID, operations.ResticInspectRequest{Snapshot: snap.ID, Slug: "shop-copy"}, ""))
	if _, err := store.GetApp(ctx, e.s.DB(), "shop-copy"); err == nil {
		t.Fatal("inspect must not create an app")
	}
	got := e.wait(e.c.SubmitResticInspect(ctx, app.ID, operations.ResticInspectRequest{Snapshot: snap.ID, Slug: "shop-copy"}, ""))
	var preview operations.ResticClonePreview
	if err := json.Unmarshal(got.Result, &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Blockers) != 0 || preview.HomePath != "/home/shop" || preview.Secrets != secrets || !preview.Minicron ||
		len(preview.Databases) != 1 || preview.Databases[0].Target != "shop_copy_main" || preview.Databases[0].UsernameKept {
		t.Fatalf("preview %+v", preview)
	}

	// Clone.
	clone := operations.ResticCloneRequest{Snapshot: snap.ID, Slug: "shop-copy", KeepUsername: true}
	op, err = e.c.SubmitResticClone(ctx, app.ID, clone, "clone shop-copy", "")
	res := e.wait(op, err)
	var result operations.ResticCloneResult
	if err := json.Unmarshal(res.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Stopped || len(result.Checklist) == 0 || result.HomePath != "/home/shop" {
		t.Fatalf("result %+v", result)
	}
	cp, err := store.GetApp(ctx, e.s.DB(), "shop-copy")
	if err != nil {
		t.Fatal(err)
	}
	if cp.DesiredRuntime != domain.DesiredStopped || cp.Publication != domain.Unpublished || !cp.Provisioned ||
		cp.HomePath != "/home/shop" || cp.UID == app.UID {
		t.Fatalf("clone %+v", cp)
	}
	if obs, _ := e.c.Observe(ctx, cp); obs.Running || obs.Exists {
		t.Fatalf("the clone must not run before Start: %+v", obs)
	}
	var cpMySQL domain.Binding
	for _, b := range cp.Bindings {
		if b.Engine == domain.EngineMySQL {
			cpMySQL = b
		}
	}
	if len(cpMySQL.Databases) != 1 || cpMySQL.Databases[0] != "shop_copy_main" {
		t.Fatalf("database mapping: %+v", cpMySQL)
	}
	// On the same stack the source still owns its username.
	if cpMySQL.Username == mysqlBinding.Username {
		t.Fatal("the clone must not reuse the source's database user while the source exists")
	}
	if (cpMySQL.Password == mysqlBinding.Password) != secrets {
		t.Fatalf("password kept=%v with secrets=%v", cpMySQL.Password == mysqlBinding.Password, secrets)
	}
	wantKey := ""
	if secrets {
		wantKey = "base64:topsecret"
	}
	for _, ev := range cp.Runtime.Env {
		if ev.Key == "APP_KEY" && ev.Value != wantKey {
			t.Fatalf("APP_KEY %q, want %q", ev.Value, wantKey)
		}
	}
	if secrets == (len(result.EmptyEnv) > 0) {
		t.Fatalf("empty env %v with secrets=%v", result.EmptyEnv, secrets)
	}
	if strings.Contains(string(res.Result), "topsecret") || strings.Contains(string(res.Result), mysqlBinding.Password) {
		t.Fatal("secrets leaked into the operation result")
	}
	if out := mysql(fmt.Sprintf("SELECT v FROM shop_copy_main.items; SELECT COUNT(*) FROM %s.items;", srcDB)); out != "hello-clone\n1" {
		t.Fatalf("data after clone: %q", out)
	}
	cpHome := e.layout.AppHome("shop-copy")
	assertOwned(t, filepath.Join(cpHome, "app", "data.txt"), cp.UID, "home-file")
	if !backup.IsSQLiteFile(filepath.Join(e.layout.SQLiteFileDir(sqliteFileOf(cp)), "shop-copy.db")) {
		t.Fatal("SQLite binding not restored")
	}
	if info, err := os.Stat(filepath.Join(cpHome, ".local/share/minicron/minicron.db")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("minicron.db: %v %v", info, err)
	}

	// Start the clone: it runs with the original home path and its job works.
	e.wait(e.c.StartApp(ctx, cp.ID, ""))
	if out := appExec(cp, "printenv", "HOME"); out != "/home/shop" {
		t.Fatalf("HOME in the clone is %q", out)
	}
	if out := appExec(cp, "minicrond", "list"); !strings.Contains(out, "touch-marker") {
		t.Fatalf("job missing in the clone: %s", out)
	}
	appExec(cp, "minicrond", "run", "touch-marker")
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
	// The source is untouched and still running.
	if _, err := os.Stat(filepath.Join(home, "app", "ran.txt")); err == nil {
		t.Fatal("the clone's job must not run in the source home")
	}
	if obs, _ := e.c.Observe(ctx, app); !obs.Running {
		t.Fatal("the source stopped")
	}
}

func sqliteFileOf(a domain.App) string {
	for _, b := range a.Bindings {
		if b.Engine == domain.EngineSQLite {
			return b.SQLiteFileID
		}
	}
	return ""
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
