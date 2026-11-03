package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// restoreEnv drives app backups (restic, on a local rclone remote) and
// database restores of one app binding.
type restoreEnv struct {
	*env
	svc domain.DataService
	app domain.App
	b   domain.Binding
	db  string
}

func newRestoreEnv(t *testing.T, engine domain.Engine, version string, start bool) *restoreEnv {
	e := setup(t)
	ctx := context.Background()
	svc, op, err := e.c.CreateService(ctx, engine, version, "")
	e.wait(op, err)
	rt := domain.Runtime{Kind: domain.RuntimeHTTP,
		HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
	app, op, err := e.c.CreateApp(ctx, operations.CreateAppInput{Slug: "shop", Runtime: rt,
		Bindings: []operations.BindingRequest{{Engine: engine, Service: svc.Name}}}, "")
	e.wait(op, err)
	app, _ = store.GetApp(ctx, e.s.DB(), app.ID)
	writeFile(t, filepath.Join(e.layout.AppCode("shop"), "s.js"),
		"require('http').createServer((q,s)=>s.end('ok')).listen(3000,'0.0.0.0')", app.UID)
	if start {
		e.wait(e.c.StartApp(ctx, app.ID, ""))
	}
	if err := os.WriteFile(filepath.Join(e.layout.RcloneDir(), "rclone.conf"), []byte("[dest]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := domain.DefaultResticSettings()
	settings.Repository = "dest:/config/rclone/repo"
	if _, err := e.c.SaveResticSettings(ctx, app.ID, settings, ""); err != nil {
		t.Fatal(err)
	}
	e.wait(e.c.SubmitResticInit(ctx, app.ID, ""))
	return &restoreEnv{env: e, svc: svc, app: app, b: app.Bindings[0], db: app.Bindings[0].Databases[0]}
}

// asApp runs SQL in the binding database as the binding user.
func (r *restoreEnv) asApp(sql string) (string, bool) {
	r.t.Helper()
	ctx := context.Background()
	ins, err := r.sdk.Inspect(ctx, r.c.Names.ServiceContainer(r.svc.Name))
	if err != nil || ins == nil {
		r.t.Fatalf("inspect %s: %v", r.svc.Name, err)
	}
	req := docker.ExecRequest{Stdin: strings.NewReader(sql)}
	if r.svc.Engine == domain.EngineMySQL {
		// The password travels in the exec environment: test-only shortcut.
		req.Cmd = []string{"mysql", "-u" + r.b.Username, "-h127.0.0.1", "--protocol=tcp", "-N", "-B", r.db}
		req.Env = []string{"MYSQL_PWD=" + r.b.Password}
	} else {
		req.Cmd = []string{"psql", "-U", r.b.Username, "-d", r.db, "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1"}
	}
	res, err := r.sdk.Exec(ctx, ins.ID, req)
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.TrimSpace(string(res.Stdout) + string(res.Stderr)), res.ExitCode == 0
}

func (r *restoreEnv) mustApp(sql string) string {
	r.t.Helper()
	out, ok := r.asApp(sql)
	if !ok {
		r.t.Fatalf("as app %q: %s", sql, out)
	}
	return out
}

func (r *restoreEnv) admin(sql string) string {
	r.t.Helper()
	return r.sql(r.svc, sql)
}

// backup takes an app backup and returns its snapshot id.
func (r *restoreEnv) backup() string {
	r.t.Helper()
	r.wait(r.c.SubmitResticBackup(context.Background(), r.app.ID, "manual", ""))
	v, err := r.c.ResticSettings(context.Background(), r.app.ID)
	if err != nil || v.State.LastBackup == nil || !v.State.LastBackup.OK || v.State.LastBackup.SnapshotID == "" {
		r.t.Fatalf("backup: %+v %v", v.State.LastBackup, err)
	}
	return v.State.LastBackup.SnapshotID
}

// restore runs restore-db from a snapshot and returns its method, or the
// failure hint.
func (r *restoreEnv) restore(snapshot string, wantOK bool) string {
	r.t.Helper()
	ctx := context.Background()
	op, err := r.c.SubmitResticRestoreDB(ctx, r.app.ID,
		operations.ResticRestoreDBRequest{Snapshot: snapshot, Database: r.db}, "replace "+r.db, "")
	if err != nil {
		r.t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := store.GetOperation(ctx, r.s.DB(), op.ID)
		if err != nil {
			r.t.Fatal(err)
		}
		if !got.State.Terminal() {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if (got.State == store.OpSucceeded) != wantOK {
			r.t.Fatalf("restore %s: %s %s: %s", snapshot, got.State, got.ErrorCode, got.ErrorMessage)
		}
		if !wantOK {
			return got.Guidance + " | " + got.ErrorMessage
		}
		var res struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(got.Result, &res)
		return res.Method
	}
	r.t.Fatal("restore timed out")
	return ""
}

// replay restores a hand-made dump (one no Bento backup would produce)
// straight through the backup package, as restore-db does after download.
// It returns the method, or an error whose live-unchanged status matters.
func (r *restoreEnv) replay(dump string) (string, error) {
	r.t.Helper()
	p := filepath.Join(r.t.TempDir(), "crafted.sql")
	if err := os.WriteFile(p, []byte(dump), 0o600); err != nil {
		r.t.Fatal(err)
	}
	ctx := context.Background()
	return r.c.BackupDeps(nil).RestoreRelational(ctx, r.app, r.b, r.db, p, backup.RestoreOptions{
		Quiesce: func(ctx context.Context) error {
			return r.sdk.Stop(ctx, r.c.Names.AppContainer(r.app.ID), 10*time.Second)
		},
	})
}

func liveUnchanged(err error) bool {
	lu := (*backup.LiveUnchangedError)(nil)
	return errors.As(err, &lu)
}

func (r *restoreEnv) assertNoStaging() {
	r.t.Helper()
	if r.svc.Engine == domain.EngineMySQL {
		if got := r.admin(`SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'bento\_\_%';`); got != "0" {
			r.t.Fatalf("staging databases left: %s", got)
		}
		if got := r.admin(`SELECT COUNT(*) FROM mysql.db WHERE Db LIKE 'bento%';`); got != "0" {
			r.t.Fatalf("staging grants left: %s", got)
		}
		return
	}
	if got := r.admin(`SELECT COUNT(*) FROM pg_database WHERE datname LIKE 'bento\_\_%';`); got != "0" {
		r.t.Fatalf("staging databases left: %s", got)
	}
}

// TestIntegrationRestoreMySQL restores MySQL backups into a running app:
// a tables-only database is swapped in with the app running; a database with
// a routine is rebuilt in place with the app stopped and restarted; every
// restored object belongs to the binding user, never root; row data that
// looks like a DEFINER clause survives; a dump with a statement the binding
// user may not run fails with the live database unchanged.
func TestIntegrationRestoreMySQL(t *testing.T) {
	for _, version := range []string{"8.0", "8.4", "9.7"} {
		t.Run(version, func(t *testing.T) { restoreMySQLScenario(t, version) })
	}
}

func restoreMySQLScenario(t *testing.T, version string) {
	r := newRestoreEnv(t, domain.EngineMySQL, version, true)
	ctx := context.Background()
	r.mustApp("CREATE TABLE parent (id INT PRIMARY KEY);" +
		"CREATE TABLE child (id INT PRIMARY KEY, p INT, FOREIGN KEY (p) REFERENCES parent(id));" +
		"CREATE TABLE notes (v TEXT);" +
		"INSERT INTO parent VALUES (1); INSERT INTO child VALUES (10, 1);" +
		"INSERT INTO notes VALUES ('see DEFINER=root@localhost here');")
	tablesOnly := r.backup()
	r.mustApp("DELETE FROM child; INSERT INTO notes VALUES ('after the backup');")

	before, _ := r.c.Observe(ctx, r.app)
	if got := r.restore(tablesOnly, true); got != "swap" {
		t.Fatalf("tables-only restore method %q, want swap", got)
	}
	after, _ := r.c.Observe(ctx, r.app)
	if !after.Running || after.ContainerID != before.ContainerID {
		t.Fatalf("a swap must leave the app running: before %+v after %+v", before, after)
	}
	if got := r.mustApp("SELECT GROUP_CONCAT(v) FROM notes; SELECT COUNT(*) FROM child;"); got != "see DEFINER=root@localhost here\n1" {
		t.Fatalf("data after swap: %q", got)
	}
	if out, ok := r.asApp("INSERT INTO child VALUES (11, 99);"); ok {
		t.Fatalf("foreign key not enforced after swap: %s", out)
	}
	if got := r.admin("SELECT REFERENCED_TABLE_SCHEMA FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA = '" +
		r.db + "' AND REFERENCED_TABLE_NAME = 'parent';"); got != r.db {
		t.Fatalf("foreign key points at %q, want %q", got, r.db)
	}
	r.assertNoStaging()

	// A routine the tenant planted must come back as the tenant's, not root's.
	r.mustApp("CREATE PROCEDURE whoami() SQL SECURITY DEFINER SELECT CURRENT_USER();")
	withRoutine := r.backup()
	r.mustApp("DELETE FROM notes;")
	if got := r.restore(withRoutine, true); got != "in-place" {
		t.Fatalf("restore with a routine: method %q, want in-place", got)
	}
	if obs, _ := r.c.Observe(ctx, r.app); !obs.Running {
		t.Fatalf("app not restarted after an in-place restore: %+v", obs)
	}
	definer := r.b.Username + "@%"
	if got := r.admin("SELECT DEFINER FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = '" + r.db + "';"); got != definer {
		t.Fatalf("routine definer %q, want %q", got, definer)
	}
	if got := r.mustApp("CALL whoami();"); got != definer {
		t.Fatalf("restored routine runs as %q, want %q", got, definer)
	}
	if got := r.mustApp("SELECT COUNT(*) FROM notes;"); got != "1" {
		t.Fatalf("notes after in-place restore: %q", got)
	}
	r.assertNoStaging()

	// A dump naming root as DEFINER restores, owned by the binding user.
	if _, err := r.replay("CREATE TABLE notes (v TEXT);\n" +
		"INSERT INTO notes VALUES ('crafted');\n" +
		"CREATE DEFINER=`root`@`localhost` PROCEDURE evil() SQL SECURITY DEFINER SELECT CURRENT_USER();\n"); err != nil {
		t.Fatal(err)
	}
	if got := r.mustApp("CALL evil();"); got != definer {
		t.Fatalf("crafted routine runs as %q, want %q", got, definer)
	}
	r.restore(withRoutine, true)

	// Statements beyond the binding user's grants fail before the live
	// database is touched.
	if _, err := r.replay("CREATE TABLE notes (v TEXT);\n" +
		"GRANT ALL PRIVILEGES ON *.* TO '" + r.b.Username + "'@'%';\n"); !liveUnchanged(err) {
		t.Fatalf("escalating dump: %v", err)
	}
	if got := r.admin("SHOW GRANTS FOR '" + r.b.Username + "'@'%';"); strings.Contains(got, "ON *.*") &&
		!strings.Contains(got, "GRANT USAGE ON *.*") {
		t.Fatalf("binding user gained global grants: %s", got)
	}
	if got := r.mustApp("SELECT COUNT(*) FROM notes; SELECT COUNT(*) FROM child;"); got != "1\n1" {
		t.Fatalf("live data changed by a failed restore: %q", got)
	}
	r.assertNoStaging()
}

// TestIntegrationRestorePostgres restores PostgreSQL app backups by swapping a
// staging database in, replaying as the binding role (so RESET ROLE cannot
// regain superuser), and leaves the live database unchanged on failure.
func TestIntegrationRestorePostgres(t *testing.T) {
	for _, version := range []string{"16", "17"} {
		t.Run(version, func(t *testing.T) {
			r := newRestoreEnv(t, domain.EnginePostgres, version, false)
			r.mustApp("CREATE TABLE items (v text); INSERT INTO items VALUES ('one');")
			art := r.backup()
			r.mustApp("DELETE FROM items;")
			if got := r.restore(art, true); got != "swap" {
				t.Fatalf("method %q, want swap", got)
			}
			if got := r.mustApp("SELECT string_agg(v, ',') FROM items;"); got != "one" {
				t.Fatalf("data after restore: %q", got)
			}
			if got := r.mustApp("SELECT tableowner FROM pg_tables WHERE tablename = 'items';"); got != r.b.Username {
				t.Fatalf("table owner %q, want %q", got, r.b.Username)
			}
			r.assertNoStaging()

			if _, err := r.replay("CREATE TABLE items (v text);\n" +
				"RESET ROLE;\nCREATE ROLE bento_evil SUPERUSER;\n"); !liveUnchanged(err) {
				t.Fatalf("escalating dump: %v", err)
			}
			if got := r.admin("SELECT COUNT(*) FROM pg_roles WHERE rolname = 'bento_evil';"); got != "0" {
				t.Fatal("RESET ROLE in a dump regained superuser")
			}
			if got := r.mustApp("SELECT COUNT(*) FROM items;"); got != "1" {
				t.Fatalf("live data changed by a failed restore: %q", got)
			}
			r.assertNoStaging()
		})
	}
}
