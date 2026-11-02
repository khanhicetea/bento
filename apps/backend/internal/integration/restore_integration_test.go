package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// restoreEnv drives relational backups and restores of one app binding.
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

func (r *restoreEnv) backup() string {
	r.t.Helper()
	op := r.wait(r.c.SubmitBackup(context.Background(), operations.BackupRequest{
		Scope: "database", AppID: r.app.ID, Databases: []string{r.db}}, ""))
	var res struct {
		Artifacts []struct {
			Path string `json:"path"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(op.Result, &res); err != nil || len(res.Artifacts) != 1 {
		r.t.Fatalf("backup result %s: %v", op.Result, err)
	}
	time.Sleep(5 * time.Millisecond) // artifact names have millisecond stamps
	return res.Artifacts[0].Path
}

// writeArtifact publishes a hand-made artifact for the binding database.
func (r *restoreEnv) writeArtifact(stamp string, raw []byte, compress bool) string {
	r.t.Helper()
	if compress {
		var buf bytes.Buffer
		zw, _ := zstd.NewWriter(&buf)
		zw.Write(raw)
		zw.Close()
		raw = buf.Bytes()
	}
	rel := filepath.Join("shop", string(r.svc.Engine)+"-"+r.db+"-"+stamp+".sql.zst")
	p := filepath.Join(r.layout.BackupsDir(), rel)
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		r.t.Fatal(err)
	}
	return rel
}

// restore runs a restore and returns its method, or the failure hint.
func (r *restoreEnv) restore(artifact string, wantOK bool) string {
	r.t.Helper()
	ctx := context.Background()
	op, err := r.c.SubmitRestore(ctx, operations.RestoreRequest{AppID: r.app.ID, Database: r.db, Artifact: artifact},
		"replace "+r.db, "")
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
			r.t.Fatalf("restore %s: %s %s: %s", artifact, got.State, got.ErrorCode, got.ErrorMessage)
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
// looks like a DEFINER clause survives; a truncated artifact and an artifact
// with a statement the binding user may not run fail with the live database
// unchanged.
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

	// An artifact naming root as DEFINER restores, owned by the binding user.
	crafted := r.writeArtifact("20260101T000000.001Z", []byte("CREATE TABLE notes (v TEXT);\n"+
		"INSERT INTO notes VALUES ('crafted');\n"+
		"CREATE DEFINER=`root`@`localhost` PROCEDURE evil() SQL SECURITY DEFINER SELECT CURRENT_USER();\n"), true)
	r.restore(crafted, true)
	if got := r.mustApp("CALL evil();"); got != definer {
		t.Fatalf("crafted routine runs as %q, want %q", got, definer)
	}
	r.restore(withRoutine, true)

	// Statements beyond the binding user's grants fail before the live
	// database is touched.
	escalate := r.writeArtifact("20260101T000000.002Z", []byte("CREATE TABLE notes (v TEXT);\n"+
		"GRANT ALL PRIVILEGES ON *.* TO '"+r.b.Username+"'@'%';\n"), true)
	if got := r.restore(escalate, false); !strings.Contains(got, "live database is unchanged") {
		t.Fatalf("escalating artifact: %q", got)
	}
	if got := r.admin("SHOW GRANTS FOR '" + r.b.Username + "'@'%';"); strings.Contains(got, "ON *.*") &&
		!strings.Contains(got, "GRANT USAGE ON *.*") {
		t.Fatalf("binding user gained global grants: %s", got)
	}

	// A truncated artifact fails before the live database is touched.
	raw, err := os.ReadFile(filepath.Join(r.layout.BackupsDir(), withRoutine))
	if err != nil {
		t.Fatal(err)
	}
	truncated := r.writeArtifact("20260101T000000.003Z", raw[:len(raw)/2], false)
	if got := r.restore(truncated, false); !strings.Contains(got, "live database is unchanged") {
		t.Fatalf("truncated artifact: %q", got)
	}
	if got := r.mustApp("SELECT COUNT(*) FROM notes; SELECT COUNT(*) FROM child;"); got != "1\n1" {
		t.Fatalf("live data changed by failed restores: %q", got)
	}
	r.assertNoStaging()
}

// TestIntegrationRestorePostgres restores PostgreSQL backups by swapping a
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

			escalate := r.writeArtifact("20260101T000000.001Z", []byte("CREATE TABLE items (v text);\n"+
				"RESET ROLE;\nCREATE ROLE bento_evil SUPERUSER;\n"), true)
			if got := r.restore(escalate, false); !strings.Contains(got, "live database is unchanged") {
				t.Fatalf("escalating artifact: %q", got)
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
