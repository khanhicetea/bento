package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// TestIntegrationMySQLGrantsAreExact checks tenant isolation on one MySQL
// service. The database names of apps "a-b" and "a1b" (a_b_main, a1b_main)
// differ only where "_" stands; an unescaped grant on a_b_main is a pattern
// that also covers a1b_main. App a-b's user must neither read a1b_main nor
// create databases matching its pattern, and provisioning must replace a
// wildcard grant left by an earlier version.
func TestIntegrationMySQLGrantsAreExact(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	svc, op, err := e.c.CreateService(ctx, domain.EngineMySQL, "8.4", "")
	e.wait(op, err)
	newApp := func(slug string) (domain.App, domain.Binding) {
		rt := domain.Runtime{Kind: domain.RuntimeHTTP,
			HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
		app, op, err := e.c.CreateApp(ctx, operations.CreateAppInput{Slug: slug, Runtime: rt,
			Bindings: []operations.BindingRequest{{Engine: domain.EngineMySQL, Service: svc.Name}}}, "")
		e.wait(op, err)
		app, _ = store.GetApp(ctx, e.s.DB(), app.ID)
		return app, app.Bindings[0]
	}
	ab, abBinding := newApp("a-b")
	_, otherBinding := newApp("a1b")
	if abBinding.Databases[0] != "a_b_main" || otherBinding.Databases[0] != "a1b_main" {
		t.Fatalf("unexpected database names %v %v", abBinding.Databases, otherBinding.Databases)
	}
	e.sql(svc, "CREATE TABLE a1b_main.secret (v TEXT); INSERT INTO a1b_main.secret VALUES ('other tenant');")

	asAB := func(sql string) (string, bool) {
		t.Helper()
		ins, err := e.sdk.Inspect(ctx, e.c.Names.ServiceContainer(svc.Name))
		if err != nil || ins == nil {
			t.Fatalf("inspect: %v", err)
		}
		// The password travels in the exec environment: test-only shortcut.
		res, err := e.sdk.Exec(ctx, ins.ID, docker.ExecRequest{
			Cmd: []string{"mysql", "-u" + abBinding.Username, "-h127.0.0.1", "--protocol=tcp", "-N", "-B"},
			Env: []string{"MYSQL_PWD=" + abBinding.Password}, Stdin: strings.NewReader(sql)})
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(res.Stdout) + string(res.Stderr)), res.ExitCode == 0
	}
	isolated := func(when string) {
		t.Helper()
		if out, ok := asAB("SELECT v FROM a1b_main.secret;"); ok {
			t.Fatalf("%s: a-b read a1b's database: %s", when, out)
		}
		if out, ok := asAB("CREATE DATABASE axb_main;"); ok {
			t.Fatalf("%s: a-b created a database matching its grant: %s", when, out)
		}
		if out, ok := asAB("CREATE TABLE a_b_main.mine (i INT); DROP TABLE a_b_main.mine;"); !ok {
			t.Fatalf("%s: a-b lost its own database: %s", when, out)
		}
	}
	isolated("after provisioning")

	// A wildcard grant as earlier versions issued it leaks; provisioning (here
	// through Add database) replaces it with the exact grant.
	e.sql(svc, "GRANT ALL PRIVILEGES ON `a_b_main`.* TO '"+abBinding.Username+"'@'%';")
	if out, ok := asAB("SELECT v FROM a1b_main.secret;"); !ok || out != "other tenant" {
		t.Fatalf("the legacy grant should reproduce the leak: %q %v", out, ok)
	}
	e.wait(e.c.AddDatabase(ctx, ab.ID, abBinding.ID, "extra", ""))
	isolated("after reprovisioning")
	// Batch output escapes backslashes, so they are shown as "/".
	got := e.sql(svc, "SELECT GROUP_CONCAT(REPLACE(Db, '\\\\', '/') ORDER BY Db) FROM mysql.db WHERE User = '"+
		abBinding.Username+"';")
	if got != "a/_b/_extra,a/_b/_main" {
		t.Fatalf("grants of a-b: %q", got)
	}
}
