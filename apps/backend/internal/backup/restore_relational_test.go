package backup

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/dataservices"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// fakeServer answers the exec calls of a relational restore and records them.
type fakeServer struct {
	mu      sync.Mutex
	engine  domain.Engine
	dbs     map[string]bool
	objects map[string][]dbObject // schema objects per database
	owners  map[string]string     // PostgreSQL database owners
	grants  map[string]bool       // MySQL database-level grant patterns of the user
	replays []replay
	admin   []string // administrator SQL, in order
	fail    func(sql string) bool
	exit    int // exit code of replays
}

type replay struct {
	cmd   []string
	user  string
	stdin string
	cnf   string // option file contents at the time of the replay
}

var (
	reCreateDB = regexp.MustCompile("CREATE DATABASE [`\"]([a-z0-9_]+)[`\"]")
	reDropDB   = regexp.MustCompile("DROP DATABASE IF EXISTS [`\"]([a-z0-9_]+)[`\"]")
	reRenameDB = regexp.MustCompile(`ALTER DATABASE "([a-z0-9_]+)" RENAME TO "([a-z0-9_]+)"`)
	reGrant    = regexp.MustCompile("GRANT ALL PRIVILEGES ON `([^`]+)`")
	reQuoted   = regexp.MustCompile(`'([^']*)'`)
)

func (s *fakeServer) hook(layout platform.Layout) func(string, docker.ExecRequest) docker.ExecResult {
	return func(_ string, req docker.ExecRequest) docker.ExecResult {
		s.mu.Lock()
		defer s.mu.Unlock()
		var in []byte
		if req.Stdin != nil {
			in, _ = io.ReadAll(req.Stdin)
		}
		sql := string(in)
		isReplay := (req.Cmd[0] == "mysql" && !strings.Contains(strings.Join(req.Cmd, " "), "client.cnf")) ||
			(req.Cmd[0] == "psql" && req.Cmd[2] != "postgres")
		if isReplay {
			r := replay{cmd: req.Cmd, user: req.User, stdin: sql}
			for _, a := range req.Cmd {
				if p, ok := strings.CutPrefix(a, "--defaults-extra-file=/run/bento-secrets/"); ok {
					b, _ := os.ReadFile(filepath.Join(layout.ServicesDir(), "db", "secrets", p))
					r.cnf = string(b)
				}
			}
			s.replays = append(s.replays, r)
			return docker.ExecResult{ExitCode: s.exit}
		}
		s.admin = append(s.admin, sql)
		if s.fail != nil && s.fail(sql) {
			return docker.ExecResult{ExitCode: 1, Stderr: []byte("injected failure")}
		}
		names := reQuoted.FindAllStringSubmatch(sql, -1)
		switch {
		case strings.Contains(sql, "information_schema.SCHEMATA"), strings.Contains(sql, "FROM pg_database WHERE datname") &&
			strings.HasPrefix(sql, "SELECT COUNT"):
			if s.dbs[names[0][1]] {
				return docker.ExecResult{Stdout: []byte("1\n")}
			}
			return docker.ExecResult{Stdout: []byte("0\n")}
		case strings.Contains(sql, "FROM mysql.db"):
			if s.grants[names[2][1]] {
				return docker.ExecResult{Stdout: []byte("1\n")}
			}
			return docker.ExecResult{Stdout: []byte("0\n")}
		case strings.Contains(sql, "pg_get_userbyid"):
			if s.dbs[names[0][1]] {
				return docker.ExecResult{Stdout: []byte(s.owners[names[0][1]] + "\n")}
			}
			return docker.ExecResult{}
		case strings.Contains(sql, "information_schema.TABLES"):
			var out strings.Builder
			for _, o := range s.objects[names[1][1]] {
				out.WriteString(o.kind + "\t" + strings.ToUpper(hex.EncodeToString([]byte(o.name))) + "\n")
			}
			return docker.ExecResult{Stdout: []byte(out.String())}
		}
		for _, m := range reDropDB.FindAllStringSubmatch(sql, -1) {
			delete(s.dbs, m[1])
		}
		for _, m := range reCreateDB.FindAllStringSubmatch(sql, -1) {
			s.dbs[m[1]] = true
			s.owners[m[1]] = "uapp"
		}
		for _, m := range reGrant.FindAllStringSubmatch(sql, -1) {
			s.grants[m[1]] = true
		}
		if strings.Contains(sql, "COMMIT;") || len(reRenameDB.FindAllString(sql, -1)) == 1 {
			for _, m := range reRenameDB.FindAllStringSubmatch(sql, -1) {
				delete(s.dbs, m[1])
				s.dbs[m[2]] = true
				s.owners[m[2]] = s.owners[m[1]]
			}
		}
		return docker.ExecResult{}
	}
}

func (s *fakeServer) adminSQL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.admin, "")
}

type restoreHarness struct {
	t      *testing.T
	deps   Deps
	server *fakeServer
	b      domain.Binding
	root   string
}

func newRestoreHarness(t *testing.T, engine domain.Engine) *restoreHarness {
	t.Helper()
	root := t.TempDir()
	layout, err := platform.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(layout.ServicesDir(), "db", "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{engine: engine, dbs: map[string]bool{"shop_main": true}, objects: map[string][]dbObject{},
		owners: map[string]string{"shop_main": "uapp"}, grants: map[string]bool{"shop_main": true}}
	fake := docker.NewFake()
	fake.ExecHook = s.hook(layout)
	svc := domain.DataService{Name: "db", Engine: engine}
	deps := Deps{Engine: fake, Layout: layout, Data: &dataservices.Manager{Engine: fake, Layout: layout},
		ServiceContainer: func(context.Context, string) (string, domain.DataService, error) { return "cid", svc, nil }}
	b := domain.Binding{Engine: engine, Service: "db", Username: "uapp", Password: "Secret123", Databases: []string{"shop_main"}}
	return &restoreHarness{t: t, deps: deps, server: s, b: b, root: root}
}

// artifact writes a plain dump file, as app backup restores it from a
// snapshot.
func (h *restoreHarness) artifact(dump string) string {
	h.t.Helper()
	p := filepath.Join(h.root, "mysql-db-shop_main.sql")
	if err := os.WriteFile(p, []byte(dump), 0o600); err != nil {
		h.t.Fatal(err)
	}
	return p
}

const mysqlDump = "/*!50503 SET NAMES utf8mb4 */;\n" +
	"CREATE TABLE `a` (v TEXT);\n" +
	"INSERT INTO `a` VALUES ('see DEFINER=root@localhost here');\n" +
	"DELIMITER ;;\n" +
	"CREATE DEFINER=`root`@`localhost` PROCEDURE p() BEGIN END ;;\n" +
	"DELIMITER ;\n"

func TestRestoreMySQLSwapsStagedTablesAsBindingUser(t *testing.T) {
	h := newRestoreHarness(t, domain.EngineMySQL)
	stage, trash := stagingNames("shop_main")
	h.server.objects["shop_main"] = []dbObject{{"TABLE", "old"}, {"TRIGGER", "trg"}, {"VIEW", "v"}, {"EVENT", "ev"}}
	h.server.objects[stage] = []dbObject{{"TABLE", "a"}, {"TABLE", "we`ird"}}
	method, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
		h.artifact(mysqlDump), RestoreOptions{Quiesce: func(context.Context) error {
			t.Fatal("a swap must not stop the app")
			return nil
		}})
	if err != nil || method != RestoreSwapped {
		t.Fatalf("restore: %q %v", method, err)
	}
	if len(h.server.replays) != 1 {
		t.Fatalf("replays: %+v", h.server.replays)
	}
	r := h.server.replays[0]
	if r.cmd[len(r.cmd)-1] != stage {
		t.Fatalf("replayed into %v, want the staging database", r.cmd)
	}
	// The dump never reaches the client's own commands (system, source).
	if !slices.Contains(r.cmd, "--commands=OFF") || !slices.Contains(r.cmd, "--skip-system-command") {
		t.Fatalf("mysql replay with client commands enabled: %v", r.cmd)
	}
	if !strings.Contains(r.cnf, "user=uapp\n") || strings.Contains(r.cnf, "root") {
		t.Fatalf("replay must log in as the binding user, cnf:\n%s", r.cnf)
	}
	if !strings.Contains(r.stdin, "CREATE PROCEDURE p()") || strings.Contains(r.stdin, "DEFINER=`root`") {
		t.Fatalf("DEFINER not stripped from DDL:\n%.300s", r.stdin)
	}
	if !strings.Contains(r.stdin, "'see DEFINER=root@localhost here'") {
		t.Fatalf("row data was rewritten:\n%.300s", r.stdin)
	}
	entries, _ := os.ReadDir(filepath.Join(h.deps.Layout.ServicesDir(), "db", "secrets"))
	if len(entries) != 0 {
		t.Fatalf("option file left behind: %v", entries)
	}
	sql := h.server.adminSQL()
	if strings.Contains(sql, "DROP DATABASE IF EXISTS `shop_main`") {
		t.Fatal("a swap must never drop the live database")
	}
	rename := "RENAME TABLE `shop_main`.`old` TO `" + trash + "`.`old`, `" + stage + "`.`a` TO `shop_main`.`a`, `" +
		stage + "`.`we``ird` TO `shop_main`.`we``ird`;"
	iRename := strings.Index(sql, rename)
	iTrigger := strings.Index(sql, "DROP TRIGGER `shop_main`.`trg`;")
	iView := strings.Index(sql, "DROP VIEW `shop_main`.`v`;")
	iEvent := strings.Index(sql, "DROP EVENT `shop_main`.`ev`;")
	if iRename < 0 || iTrigger < 0 || iView < 0 || iEvent < 0 || iTrigger > iRename || iView > iRename || iEvent < iRename {
		t.Fatalf("swap SQL out of order or incomplete:\n%s", sql)
	}
	if !strings.Contains(sql, "GRANT ALL PRIVILEGES ON `bento\\_\\_rs\\_") {
		t.Fatalf("staging grant must escape _ wildcards:\n%s", sql)
	}
	if h.server.dbs[stage] || h.server.dbs[trash] {
		t.Fatalf("staging databases left: %v", h.server.dbs)
	}
}

func TestRestoreMySQLRejectedStatementLeavesLiveUnchanged(t *testing.T) {
	h := newRestoreHarness(t, domain.EngineMySQL)
	h.server.exit = 1
	_, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
		h.artifact(mysqlDump), RestoreOptions{})
	var lu *LiveUnchangedError
	if !errors.As(err, &lu) {
		t.Fatalf("want LiveUnchangedError, got %v", err)
	}
	if sql := h.server.adminSQL(); strings.Contains(sql, "RENAME TABLE") || strings.Contains(sql, "`shop_main`;") {
		t.Fatalf("live database touched:\n%s", sql)
	}
}

func TestRestoreMySQLCancelledBeforeSwapLeavesLiveUnchanged(t *testing.T) {
	h := newRestoreHarness(t, domain.EngineMySQL)
	cancelled := errors.New("cancelled")
	_, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
		h.artifact(mysqlDump), RestoreOptions{Phase: func(_ context.Context, name string) error {
			if name == "swap" {
				return cancelled
			}
			return nil
		}})
	var lu *LiveUnchangedError
	if !errors.As(err, &lu) || !errors.Is(err, cancelled) {
		t.Fatalf("want a LiveUnchangedError wrapping the cancellation, got %v", err)
	}
	stage, _ := stagingNames("shop_main")
	if strings.Contains(h.server.adminSQL(), "RENAME TABLE") || h.server.dbs[stage] {
		t.Fatalf("cancelled restore swapped or left staging: %v", h.server.dbs)
	}
}

func TestRestoreMySQLWithViewsRebuildsInPlaceAfterQuiesce(t *testing.T) {
	h := newRestoreHarness(t, domain.EngineMySQL)
	stage, _ := stagingNames("shop_main")
	h.server.objects[stage] = []dbObject{{"TABLE", "a"}, {"VIEW", "v"}}
	quiesced := false
	method, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
		h.artifact(mysqlDump), RestoreOptions{Quiesce: func(context.Context) error {
			if strings.Contains(h.server.adminSQL(), "DROP DATABASE IF EXISTS `shop_main`") {
				t.Fatal("live database dropped before the app was stopped")
			}
			quiesced = true
			return nil
		}})
	if err != nil || method != RestoreInPlace || !quiesced {
		t.Fatalf("restore: %q %v quiesced=%v", method, err, quiesced)
	}
	if len(h.server.replays) != 2 || h.server.replays[1].cmd[len(h.server.replays[1].cmd)-1] != "shop_main" ||
		!strings.Contains(h.server.replays[1].cnf, "user=uapp\n") {
		t.Fatalf("second replay must target the live database as the binding user: %+v", h.server.replays)
	}
	if strings.Contains(h.server.adminSQL(), "RENAME TABLE") {
		t.Fatal("in-place restore must not rename tables")
	}
}

func TestRestoreRefusesForeignStagingDatabase(t *testing.T) {
	for _, engine := range []domain.Engine{domain.EngineMySQL, domain.EnginePostgres} {
		t.Run(string(engine), func(t *testing.T) {
			h := newRestoreHarness(t, engine)
			stage, _ := stagingNames("shop_main")
			h.server.dbs[stage] = true
			h.server.owners[stage] = "someone"
			_, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
				h.artifact(mysqlDump), RestoreOptions{})
			if err == nil || !strings.Contains(err.Error(), "refusing to drop") {
				t.Fatalf("want refusal, got %v", err)
			}
			if !h.server.dbs[stage] || len(h.server.replays) != 0 {
				t.Fatal("a foreign staging database must be left alone")
			}
		})
	}
}

func TestRestorePostgresSwapsDatabasesAsBindingUser(t *testing.T) {
	h := newRestoreHarness(t, domain.EnginePostgres)
	stage, trash := stagingNames("shop_main")
	method, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
		h.artifact("CREATE TABLE a (v text);\nRESET ROLE;\n"), RestoreOptions{})
	if err != nil || method != RestoreSwapped {
		t.Fatalf("restore: %q %v", method, err)
	}
	r := h.server.replays[0]
	if strings.Join(r.cmd[:5], " ") != "psql -U uapp -d "+stage {
		t.Fatalf("replay must connect as the binding user to the staging database: %v", r.cmd)
	}
	// After the \restrict line, the dump itself, without a SET ROLE prefix.
	if _, dump, _ := strings.Cut(r.stdin, "\n"); dump != "CREATE TABLE a (v text);\nRESET ROLE;\n" {
		t.Fatalf("the dump must reach psql unchanged, without a SET ROLE prefix:\n%.80s", r.stdin)
	}
	sql := h.server.adminSQL()
	swap := `ALTER DATABASE "shop_main" RENAME TO "` + trash + `";` + "\n" + `ALTER DATABASE "` + stage + `" RENAME TO "shop_main";`
	if !strings.Contains(sql, swap) || !strings.Contains(sql, `ALTER DATABASE "shop_main" WITH ALLOW_CONNECTIONS false;`) {
		t.Fatalf("swap SQL missing:\n%s", sql)
	}
	if strings.Contains(sql, `DROP DATABASE IF EXISTS "shop_main"`) {
		t.Fatal("a swap must never drop the live database")
	}
	if !h.server.dbs["shop_main"] || h.server.dbs[stage] || h.server.dbs[trash] {
		t.Fatalf("databases after swap: %v", h.server.dbs)
	}
}

func TestRestorePostgresFailedSwapReopensLive(t *testing.T) {
	h := newRestoreHarness(t, domain.EnginePostgres)
	stage, _ := stagingNames("shop_main")
	h.server.fail = func(sql string) bool { return strings.Contains(sql, "COMMIT;") }
	_, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
		h.artifact("CREATE TABLE a (v text);\n"), RestoreOptions{})
	var lu *LiveUnchangedError
	if !errors.As(err, &lu) {
		t.Fatalf("want LiveUnchangedError, got %v", err)
	}
	if !strings.Contains(h.server.adminSQL(), `ALTER DATABASE "shop_main" WITH ALLOW_CONNECTIONS true;`) {
		t.Fatal("live database must accept connections again after a failed swap")
	}
	if h.server.dbs[stage] {
		t.Fatal("staging database left after a failed swap")
	}
}

// psql replays run as the image's database user in restricted mode with a
// key the dump cannot know; the dump's own \restrict lines are dropped.
func TestRestorePostgresReplayIsRestricted(t *testing.T) {
	h := newRestoreHarness(t, domain.EnginePostgres)
	dump := "\\restrict dumpkey\nCREATE TABLE t (a int);\n\\! id\nCOPY t FROM stdin;\n1\n\\.\n\\unrestrict dumpkey\n"
	if _, err := h.deps.RestoreRelational(context.Background(), domain.App{}, h.b, "shop_main",
		h.artifact(dump), RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(h.server.replays) != 1 {
		t.Fatalf("replays: %+v", h.server.replays)
	}
	r := h.server.replays[0]
	if r.user != "postgres" {
		t.Fatalf("psql replay ran as %q", r.user)
	}
	first, rest, _ := strings.Cut(r.stdin, "\n")
	key, ok := strings.CutPrefix(first, "\\restrict ")
	if !ok || len(key) != 32 || key == "dumpkey" {
		t.Fatalf("replay does not start restricted: %q", first)
	}
	if strings.Contains(rest, "restrict") {
		t.Fatalf("the dump's own restrict lines must be dropped:\n%s", rest)
	}
	if !strings.Contains(rest, "\\! id\n") || !strings.Contains(rest, "COPY t FROM stdin;\n1\n\\.\n") {
		t.Fatalf("other lines must pass unchanged (psql refuses \\! itself):\n%s", rest)
	}
}

func TestRestrictFilterHandlesLongLinesAndNoFinalNewline(t *testing.T) {
	long := strings.Repeat("x", 200<<10)
	in := "\\restrict k\n" + long + "\n  \\restrict kept\n\\unrestrict k"
	out, err := io.ReadAll(newRestrictFilter(strings.NewReader(in)))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != long+"\n  \\restrict kept\n" {
		t.Fatalf("filtered %d bytes: %.40q...%q", len(out), out, out[max(0, len(out)-30):])
	}
}
