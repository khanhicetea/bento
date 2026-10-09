package operations

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const sqliteHeader = "SQLite format 3\x00" + "padding-to-be-longer-than-the-header"

func addMySQLService(t *testing.T, h *harness) {
	t.Helper()
	must(t, store.InsertService(t.Context(), h.store.DB(), domain.DataService{Name: "mysql84", Engine: domain.EngineMySQL,
		Version: "8.4", Image: domain.MySQLVersions["8.4"], Volume: "v-mysql84", CreatedAt: time.Now().UTC()}))
}

func mysqlSpec(srcSlug string, dbs ...string) AppSpec {
	return AppSpec{FormatVersion: 2, Slug: srcSlug,
		Runtime:   domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}},
		Resources: domain.Resources{MemoryMB: 256, CPUMillis: 500, PIDs: 256}, Ingress: domain.IngressManaged,
		Domains:  []string{},
		Bindings: []AppSpecBinding{{Engine: domain.EngineMySQL, Service: "mysql84", Version: "8.4", Databases: dbs}}}
}

func TestPlanCloneMapsDatabaseNamesAndPasswords(t *testing.T) {
	h := newHarness(t)
	addMySQLService(t, h)
	ctx := t.Context()
	spec := mysqlSpec("shop", "shop_main", "shop_reports")
	spec.Runtime.Env = []domain.EnvVar{{Key: "APP_KEY", Value: domain.RedactedEnvValue}, {Key: "APP_ENV", Value: "production"}}
	man := ResticManifest{FormatVersion: 2, Slug: "shop", AppID: "asrc", HomePath: "/home/shop", Dumps: []ResticDump{
		{File: "db/a.sql", Engine: domain.EngineMySQL, Database: "shop_main", Binding: 0, Suffix: "main", Username: "uasrc"},
		{File: "db/b.sql", Engine: domain.EngineMySQL, Database: "shop_reports", Binding: 0, Suffix: "reports", Username: "uasrc"}}}

	// Without secrets: new password, secret-looking env values come back empty.
	p, err := h.c.planClone(ctx, cloneInput{Man: man, Spec: spec, Slug: "shop-staging", AppID: "anew"})
	must(t, err)
	if len(p.Blockers) != 0 {
		t.Fatalf("blockers %v", p.Blockers)
	}
	var targets []string
	for _, d := range p.Databases {
		targets = append(targets, d.Source+">"+d.Target)
		if d.PasswordKept || d.UsernameKept || d.Username != "uanew" {
			t.Fatalf("%+v", d)
		}
	}
	if !slices.Equal(targets, []string{"shop_main>shop_staging_main", "shop_reports>shop_staging_reports"}) {
		t.Fatalf("names %v", targets)
	}
	if !slices.Equal(p.EmptyEnv, []string{"APP_KEY"}) || p.App.Runtime.Env[0].Value != "" || p.App.Runtime.Env[1].Value != "production" {
		t.Fatalf("env %+v empty %v", p.App.Runtime.Env, p.EmptyEnv)
	}
	if p.App.HomePath != "/home/shop" || p.App.ContainerHome() != "/home/shop" {
		t.Fatalf("home %q", p.App.HomePath)
	}
	if p.App.DesiredRuntime != domain.DesiredStopped || p.App.Publication != domain.Unpublished {
		t.Fatal("clone must be stopped and unpublished")
	}
	generated := p.App.Bindings[0].Password

	// With secrets: the source password and env values are kept.
	sec := &ResticSecrets{Env: []domain.EnvVar{{Key: "APP_KEY", Value: "base64:abc"}},
		Bindings: []ResticSecretBinding{{Index: 0, Password: "SourcePassword1234"}}}
	p, err = h.c.planClone(ctx, cloneInput{Man: man, Spec: spec, Secrets: sec, Slug: "shop-staging", AppID: "anew"})
	must(t, err)
	if p.App.Bindings[0].Password != "SourcePassword1234" || generated == "SourcePassword1234" || !p.Databases[0].PasswordKept {
		t.Fatalf("password not kept: %+v", p.Databases[0])
	}
	if p.App.Runtime.Env[0].Value != "base64:abc" || len(p.EmptyEnv) != 0 {
		t.Fatalf("env %+v", p.App.Runtime.Env)
	}
}

func TestPlanCloneBlockers(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.createApp("shop")
	spec := mysqlSpec("shop", "shop_main")
	man := ResticManifest{FormatVersion: 2, Slug: "shop", AppID: "asrc"}
	p, err := h.c.planClone(ctx, cloneInput{Man: man, Spec: spec, Slug: "shop", AppID: "anew"})
	must(t, err)
	joined := strings.Join(p.Blockers, "\n")
	if !strings.Contains(joined, "already exists") || !strings.Contains(joined, "no mysql service of version 8.4") {
		t.Fatalf("blockers %v", p.Blockers)
	}
	// A retained home blocks the slug.
	must(t, os.MkdirAll(h.layout.AppHome("old-app"), 0o750))
	p, err = h.c.planClone(ctx, cloneInput{Man: man, Spec: mysqlSpec("shop"), Slug: "old-app", AppID: "anew"})
	must(t, err)
	if !strings.Contains(strings.Join(p.Blockers, "\n"), "retained home") {
		t.Fatalf("blockers %v", p.Blockers)
	}
	// An invalid stored home path is refused.
	man.HomePath = "/etc"
	p, err = h.c.planClone(ctx, cloneInput{Man: man, Spec: mysqlSpec("shop"), Slug: "fresh-app", AppID: "anew"})
	must(t, err)
	if !strings.Contains(strings.Join(p.Blockers, "\n"), "home path") {
		t.Fatalf("blockers %v", p.Blockers)
	}
}

func TestPlanCloneUsernameRule(t *testing.T) {
	h := newHarness(t)
	addMySQLService(t, h)
	ctx := t.Context()
	src := h.createApp("shop")
	must(t, store.InsertBinding(ctx, h.store.DB(), domain.Binding{ID: "bsrc", AppID: src.ID, Engine: domain.EngineMySQL,
		Service: "mysql84", Username: "u" + src.ID, Password: "pw", CreatedAt: time.Now().UTC()}))
	spec := mysqlSpec("shop", "shop_main")
	man := ResticManifest{FormatVersion: 2, Slug: "shop", AppID: src.ID, Dumps: []ResticDump{
		{File: "db/a.sql", Engine: domain.EngineMySQL, Database: "shop_main", Username: "u" + src.ID}}}

	// While the source exists its user is in use: a new one is used.
	p, err := h.c.planClone(ctx, cloneInput{Man: man, Spec: spec, Slug: "shop-copy", AppID: "anew", KeepUsername: true})
	must(t, err)
	d := p.Databases[0]
	if d.UsernameKept || d.Username != "uanew" || !strings.Contains(d.UsernameNote, "still used by app shop") {
		t.Fatalf("%+v", d)
	}

	// A retained, not yet pruned app that lists the user also blocks reuse.
	man.Dumps[0].Username = "uretired"
	must(t, store.InsertRetired(ctx, h.store.DB(), store.RetiredApp{AppID: "aold", Slug: "old", UID: 10999,
		Artifacts: store.RetainedArtifacts{Relational: []store.RetainedRelational{{Engine: domain.EngineMySQL,
			Service: "mysql84", Username: "uretired", Databases: []string{"old_main"}}}}}))
	p, err = h.c.planClone(ctx, cloneInput{Man: man, Spec: spec, Slug: "shop-copy", AppID: "anew", KeepUsername: true})
	must(t, err)
	if d := p.Databases[0]; d.UsernameKept || !strings.Contains(d.UsernameNote, "retained app old") {
		t.Fatalf("%+v", d)
	}

	// Without the option the default new user is used and no check runs.
	p, err = h.c.planClone(ctx, cloneInput{Man: man, Spec: spec, Slug: "shop-copy", AppID: "anew"})
	must(t, err)
	if p.Databases[0].Username != "uanew" || p.Databases[0].UsernameKept {
		t.Fatalf("%+v", p.Databases[0])
	}
}

func TestPlanCloneFormat1Snapshot(t *testing.T) {
	h := newHarness(t)
	addMySQLService(t, h)
	spec := mysqlSpec("my-shop", "my_shop_main", "my_shop_stats")
	spec.FormatVersion = 1
	// Format 1 dumps carry no binding, suffix or user; the home is /home/<slug>.
	man := ResticManifest{FormatVersion: 1, Slug: "my-shop", AppID: "asrc", Dumps: []ResticDump{
		{File: "db/a.sql", Engine: domain.EngineMySQL, Database: "my_shop_stats"},
		{File: "db/b.sql", Engine: domain.EngineMySQL, Database: "my_shop_main"}}}
	p, err := h.c.planClone(t.Context(), cloneInput{Man: man, Spec: spec, Slug: "other", AppID: "anew", KeepUsername: true})
	must(t, err)
	if len(p.Blockers) != 0 || p.App.ContainerHome() != "/home/my-shop" || p.App.HomePath != "/home/my-shop" {
		t.Fatalf("blockers %v home %q", p.Blockers, p.App.HomePath)
	}
	if d := p.Databases[0]; d.Target != "other_main" || d.UsernameKept || !strings.Contains(d.UsernameNote, "does not record") {
		t.Fatalf("%+v", d)
	}
	for _, d := range man.Dumps {
		b, target, err := p.resolveDump(man, d)
		want := "other_" + strings.TrimPrefix(d.Database, "my_shop_")
		if err != nil || target != want || b.AppID != "anew" || b.Service != "mysql84" {
			t.Fatalf("%s: %+v %q %v", d.Database, b, target, err)
		}
	}
	if _, _, err := p.resolveDump(man, ResticDump{Engine: domain.EngineMySQL, Database: "unknown"}); err == nil {
		t.Fatal("an unknown dump must not resolve")
	}
}

func TestParseCloneInputsAreStrict(t *testing.T) {
	if _, err := parseAppSpec([]byte(`{"formatVersion":2,"slug":"x","bogus":1}`)); err == nil {
		t.Fatal("unknown app.json field accepted")
	}
	if _, err := parseAppSpec([]byte(`{"formatVersion":4,"slug":"x"}`)); err == nil {
		t.Fatal("future app.json format accepted")
	}
	// Format 2 snapshots, written when apps owned their domains, still parse.
	old, err := parseAppSpec([]byte(`{"formatVersion":2,"slug":"x","ingress":"managed","domains":["a.example.com"],
		"primaryDomain":"a.example.com","route":{"tls":"acme","redirectHttps":true,"accessLog":true},"bindings":[]}`))
	if err != nil || old.Route == nil || !old.Route.AccessLog || old.PrimaryDomain != "a.example.com" {
		t.Fatalf("format 2 app.json: %+v %v", old, err)
	}
	if _, err := parseResticSecrets([]byte(`{"env":[],"bindings":[],"extra":true}`)); err == nil {
		t.Fatal("unknown secrets.json field accepted")
	}
	if _, err := parseResticSecrets([]byte(`{"env":[],"bindings":[{"index":0,"password":"has space'; DROP"}]}`)); err == nil {
		t.Fatal("unusable password accepted")
	}
}

func TestResticCloneClaimsAndConfirmation(t *testing.T) {
	app := domain.App{ID: "a1"}
	lookup := func(string) (domain.App, error) { return app, nil }
	cl := classify(store.Operation{Kind: KindAppCloneFromBackup, TargetID: "a1"}, lookup)
	other := classify(store.Operation{Kind: KindAppRestoreFromBackup, TargetID: "b2:x"}, lookup)
	if cl.global || cl.pool != clonePool || poolLimits[clonePool] != 1 || other.pool != clonePool {
		t.Fatalf("clones download in the clone pool, one at a time: %+v %+v", cl, other)
	}
	if !cl.conflicts(classify(store.Operation{Kind: KindResticBackup, TargetID: "a1"}, lookup)) {
		t.Fatal("a clone must not download while the repository is being backed up")
	}
	in := classify(store.Operation{Kind: KindResticInspect, TargetID: "a1"}, lookup)
	bk := classify(store.Operation{Kind: KindResticBackup, TargetID: "a1"}, lookup)
	if in.global || len(in.excl) != 0 || !in.conflicts(bk) {
		t.Fatalf("inspect claims %+v", in)
	}
	if in.conflicts(classify(store.Operation{Kind: KindResticInspect, TargetID: "a1"}, lookup)) {
		t.Fatal("two inspections may overlap")
	}

	h, src := resticHarness(t, nil)
	h.resticInit(src)
	ctx := t.Context()
	req := ResticCloneRequest{Snapshot: "aaaaaaaabbbbbbbb", Slug: "shop-copy"}
	if _, err := h.c.SubmitResticClone(ctx, src.ID, req, "clone shop", ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("want confirmation error, got %v", err)
	}
	if _, err := h.c.SubmitResticClone(ctx, src.ID, req, "", ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("want confirmation error, got %v", err)
	}
	bad := req
	bad.Slug = "Bad Slug"
	if _, err := h.c.SubmitResticClone(ctx, src.ID, bad, "clone Bad Slug", ""); err == nil {
		t.Fatal("invalid slug accepted")
	}
	bad = req
	bad.Snapshot = "latest"
	if _, err := h.c.SubmitResticClone(ctx, src.ID, bad, "clone shop-copy", ""); err == nil {
		t.Fatal("non-hex snapshot accepted")
	}
	bad = req
	bad.BackupAfter = "sideways"
	if _, err := h.c.SubmitResticClone(ctx, src.ID, bad, "clone shop-copy", ""); err == nil {
		t.Fatal("unknown backupAfter accepted")
	}
	bad = req
	bad.Slug = "shop"
	if _, err := h.c.SubmitResticClone(ctx, src.ID, bad, "clone shop", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("existing slug: %v", err)
	}
	op, err := h.c.SubmitResticClone(ctx, src.ID, req, "clone shop-copy", "")
	if err != nil {
		t.Fatal(err)
	}
	var persisted ResticCloneRequest
	must(t, json.Unmarshal(op.Request, &persisted))
	if persisted.AppID == "" || persisted.Slug != "shop-copy" || persisted.BackupAfter != CloneBackupNone {
		t.Fatalf("persisted request %+v", persisted)
	}
}

// cloneFixture is a snapshot of an app "shop" with a SQLite binding, a home
// and a scheduler database, served by a fake restic.
type cloneFixture struct {
	h       *harness
	src     domain.App
	secrets bool
	// breakSQLite makes the SQLite dump unusable so the clone fails midway.
	breakSQLite bool
}

func newCloneFixture(t *testing.T, secrets, breakSQLite bool) *cloneFixture {
	t.Helper()
	f := &cloneFixture{secrets: secrets, breakSQLite: breakSQLite}
	h, src := resticHarness(t, nil)
	f.h, f.src = h, src
	fileID := src.Bindings[0].SQLiteFileID
	man := ResticManifest{FormatVersion: ResticFormatVersion, StackID: "stest", AppID: src.ID, Slug: "shop",
		CreatedAt: time.Date(2026, 10, 1, 3, 30, 0, 0, time.UTC), HomePath: "/home/shop", Paths: []string{"."},
		Dumps: []ResticDump{{File: "sqlite/" + fileID + ".db", Engine: domain.EngineSQLite, Database: fileID,
			FileID: fileID, FileName: "shop.db"}},
		HomeSQLite: []string{".local/share/minicron/minicron.db"},
		Minicron:   &ResticMinicron{DB: "home-sqlite/.local/share/minicron/minicron.db"}, Secrets: secrets}
	spec := AppSpec{FormatVersion: ResticFormatVersion, Slug: "shop", Runtime: src.Runtime, Resources: src.Resources,
		Ingress: domain.IngressManaged, AccessLog: src.AccessLog, Domains: []string{"shop.example.com"},
		Bindings: []AppSpecBinding{{Engine: domain.EngineSQLite, SQLiteID: fileID}}}
	spec.Runtime.Env = []domain.EnvVar{{Key: "APP_KEY", Value: domain.RedactedEnvValue}, {Key: "APP_ENV", Value: "production"}}
	h.fake.ExecHook = wrapHook(h.fake.ExecHook, func(a []string, req docker.ExecRequest) (docker.ExecResult, bool) {
		switch a[0] {
		case "stats":
			_, _ = req.Stdout.Write([]byte(`{"total_size":4096}` + "\n"))
		case "dump":
			name := a[len(a)-1]
			var v any = man
			if strings.HasSuffix(name, "app.json") {
				v = spec
			}
			raw, _ := json.Marshal(v)
			_, _ = req.Stdout.Write(raw)
		case "restore":
			f.writeSnapshot(t, man, spec)
		default:
			return docker.ExecResult{}, false
		}
		return docker.ExecResult{}, true
	})
	h.resticInit(src)
	return f
}

func (f *cloneFixture) writeSnapshot(t *testing.T, man ResticManifest, spec AppSpec) {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(f.h.layout.StagingDir(), "restic-*", "data"))
	if len(dirs) != 1 {
		t.Fatalf("staging dirs %v", dirs)
	}
	root := filepath.Join(dirs[0], "backup")
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(body), mode))
	}
	write("home/app/index.js", "console.log('shop')", 0o644)
	write("home/app/uploads/a.jpg", "image", 0o644)
	write("home/.local/share/minicron/minicron-logs.db", "old logs", 0o600)
	write("home/"+domain.HomeSidecarName, `{"appId":"asrc"}`, 0o444)
	must(t, os.Symlink("/etc/passwd", filepath.Join(root, "home", "app", "link")))
	dump := sqliteHeader
	if f.breakSQLite {
		dump = "not sqlite"
	}
	write("bento/"+man.Dumps[0].File, dump, 0o600)
	write("bento/home-sqlite/.local/share/minicron/minicron.db", sqliteHeader+"jobs", 0o600)
	for name, v := range map[string]any{"manifest.json": man, "app.json": spec} {
		raw, _ := json.Marshal(v)
		write("bento/"+name, string(raw), 0o600)
	}
	if man.Secrets {
		raw, _ := json.Marshal(ResticSecrets{Env: []domain.EnvVar{{Key: "APP_KEY", Value: "super-secret-value"}},
			Bindings: []ResticSecretBinding{}})
		write("bento/secrets.json", string(raw), 0o600)
	}
}

func (f *cloneFixture) clone(t *testing.T) store.Operation {
	t.Helper()
	op, err := f.h.c.SubmitResticClone(t.Context(), f.src.ID,
		ResticCloneRequest{Snapshot: "aaaaaaaabbbbbbbb", Slug: "shop-copy"}, "clone shop-copy", "")
	if err != nil {
		t.Fatal(err)
	}
	return f.h.wait(op)
}

func TestCloneFromBackupCreatesStoppedApp(t *testing.T) {
	for _, secrets := range []bool{false, true} {
		name := map[bool]string{false: "secrets off", true: "secrets on"}[secrets]
		t.Run(name, func(t *testing.T) {
			f := newCloneFixture(t, secrets, false)
			h, ctx := f.h, t.Context()
			got := f.clone(t)
			if got.State != store.OpSucceeded {
				t.Fatalf("clone %s: %s: %s", got.State, got.ErrorCode, got.ErrorMessage)
			}
			clone, err := store.GetApp(ctx, h.store.DB(), "shop-copy")
			must(t, err)
			if clone.ID == f.src.ID || clone.UID == f.src.UID || clone.DesiredRuntime != domain.DesiredStopped ||
				clone.Publication != domain.Unpublished || !clone.Provisioned || len(clone.Hosts) != 0 {
				t.Fatalf("clone %+v", clone)
			}
			if clone.HomePath != "/home/shop" || clone.ContainerHome() != "/home/shop" {
				t.Fatalf("home path %q", clone.HomePath)
			}
			wantKey := ""
			if secrets {
				wantKey = "super-secret-value"
			}
			for _, e := range clone.Runtime.Env {
				if e.Key == "APP_KEY" && e.Value != wantKey || e.Key == "APP_ENV" && e.Value != "production" {
					t.Fatalf("env %+v", clone.Runtime.Env)
				}
			}
			home := h.layout.AppHome("shop-copy")
			assertFile := func(path, body string, mode os.FileMode) {
				t.Helper()
				b, err := os.ReadFile(path)
				if err != nil || string(b) != body {
					t.Fatalf("%s: %q %v", path, b, err)
				}
				o, m, err := platform.StatOwner(path)
				if err != nil || o.UID != clone.UID || (mode != 0 && m.Perm() != mode) {
					t.Fatalf("%s owner %+v mode %v %v", path, o, m, err)
				}
			}
			assertFile(filepath.Join(home, "app", "index.js"), "console.log('shop')", 0)
			assertFile(filepath.Join(home, "app", "uploads", "a.jpg"), "image", 0)
			assertFile(filepath.Join(home, ".local/share/minicron/minicron.db"), sqliteHeader+"jobs", 0o600)
			if _, err := os.Lstat(filepath.Join(home, ".local/share/minicron/minicron-logs.db")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("the scheduler log database must not be restored")
			}
			if o, _, err := platform.StatOwner(filepath.Join(home, "app", "link")); err != nil || o.UID != clone.UID {
				t.Fatalf("symlink must be re-owned (not followed): %+v %v", o, err)
			}
			var sc HomeSidecar
			readJSON(t, h.layout.HomeSidecar("shop-copy"), &sc)
			if sc.AppID != clone.ID || sc.UID != clone.UID {
				t.Fatalf("sidecar %+v", sc)
			}
			dbFile := filepath.Join(h.layout.SQLiteFileDir(clone.Bindings[0].SQLiteFileID), "shop-copy.db")
			assertFile(dbFile, sqliteHeader, 0)
			if clone.Bindings[0].SQLiteFileID == f.src.Bindings[0].SQLiteFileID {
				t.Fatal("the clone needs its own SQLite file")
			}
			// The source is untouched.
			var srcSC HomeSidecar
			readJSON(t, h.layout.HomeSidecar("shop"), &srcSC)
			if srcSC.AppID != f.src.ID {
				t.Fatalf("source sidecar %+v", srcSC)
			}

			var res ResticCloneResult
			must(t, json.Unmarshal(got.Result, &res))
			if res.Slug != "shop-copy" || !res.Stopped || res.HomePath != "/home/shop" || len(res.SQLite) != 1 ||
				!res.Minicron || len(res.Checklist) == 0 || res.Domains[0].Name != "shop.example.com" || !res.Domains[0].InUse {
				t.Fatalf("result %+v", res)
			}
			if secrets == (len(res.EmptyEnv) != 0) {
				t.Fatalf("empty env %v with secrets=%v", res.EmptyEnv, secrets)
			}
			evs, _ := store.ListEvents(ctx, h.store.DB(), got.ID, 0)
			all := string(got.Result)
			for _, e := range evs {
				all += e.Message
			}
			if strings.Contains(all, "super-secret-value") {
				t.Fatal("a secret leaked into the operation")
			}
			if strings.Contains(strings.Join(res.Checklist, "\n"), "[redacted]") {
				t.Fatal("checklist must not hold redacted placeholders")
			}
			for _, c := range h.fake.Containers {
				if c.Spec.Config.Labels[runtime.LabelAppID] == clone.ID && c.Running {
					t.Fatalf("the clone must not be started: %s", c.Name)
				}
			}
		})
	}
}

func TestCloneFromBackupRollsBackOnFailure(t *testing.T) {
	f := newCloneFixture(t, true, true)
	h, ctx := f.h, t.Context()
	got := f.clone(t)
	if got.State != store.OpFailed || got.ErrorCode != "restore-failed" {
		t.Fatalf("want restore-failed, got %s %s: %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	if strings.Contains(got.ErrorMessage, "super-secret-value") {
		t.Fatal("secret in error")
	}
	if _, err := store.GetApp(ctx, h.store.DB(), "shop-copy"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the clone must be removed: %v", err)
	}
	if _, err := os.Lstat(h.layout.AppHome("shop-copy")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home must be removed: %v", err)
	}
	entries, _ := os.ReadDir(h.layout.SQLiteDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "shop-copy_") {
			t.Fatalf("SQLite directory %s must be removed", e.Name())
		}
	}
	ledger, _ := store.ListLedger(ctx, h.store.DB())
	burned := 0
	for _, e := range ledger {
		if e.Slug == "shop-copy" && e.State == "burned" {
			burned++
		}
	}
	if burned != 1 {
		t.Fatalf("the clone's UID must be burned: %+v", ledger)
	}
	// The source app and its home are intact.
	src, err := store.GetApp(ctx, h.store.DB(), "shop")
	if err != nil || src.ID != f.src.ID {
		t.Fatalf("source: %v", err)
	}
	must(t, h.c.verifyHome(src))
	if leftovers, _ := filepath.Glob(filepath.Join(h.layout.StagingDir(), "restic-*")); len(leftovers) != 0 {
		t.Fatalf("staging not cleaned: %v", leftovers)
	}
}

func TestResticInspectReturnsPreview(t *testing.T) {
	f := newCloneFixture(t, true, false)
	h := f.h
	op, err := h.c.SubmitResticInspect(t.Context(), f.src.ID,
		ResticInspectRequest{Snapshot: "aaaaaaaabbbbbbbb", Slug: "shop-copy"}, "")
	got := h.mustSucceed(op, err)
	var pv ResticClonePreview
	must(t, json.Unmarshal(got.Result, &pv))
	if pv.SourceSlug != "shop" || pv.Slug != "shop-copy" || !pv.Secrets || pv.HomePath != "/home/shop" ||
		pv.SizeBytes != 4096 || !pv.Minicron || len(pv.SQLite) != 1 || len(pv.Blockers) != 0 || pv.FormatVersion != ResticFormatVersion {
		t.Fatalf("preview %+v", pv)
	}
	if !slices.Equal(pv.EnvKeys, []string{"APP_KEY", "APP_ENV"}) || len(pv.EmptyEnv) != 0 {
		t.Fatalf("env %v empty %v", pv.EnvKeys, pv.EmptyEnv)
	}
	if len(pv.Domains) != 1 || !pv.Domains[0].InUse {
		t.Fatalf("domains %+v", pv.Domains)
	}
	// Inspect is read-only: no app was created and no restore ran.
	if _, err := store.GetApp(t.Context(), h.store.DB(), "shop-copy"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("inspect must not create an app")
	}
	if _, err := h.c.SubmitResticInspect(t.Context(), f.src.ID, ResticInspectRequest{Snapshot: "latest"}, ""); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
}

func TestInstallRestoredHomeMovesOnlyListedPaths(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	restored := filepath.Join(t.TempDir(), "home")
	must(t, os.MkdirAll(filepath.Join(restored, "app", "storage"), 0o755))
	must(t, os.WriteFile(filepath.Join(restored, "app", "storage", "u.txt"), []byte("x"), 0o644))
	must(t, os.MkdirAll(filepath.Join(restored, "other"), 0o755))
	// The staging dir of the test and the stack root are on one filesystem
	// only by luck: place the tree inside the stack's staging directory.
	staged := filepath.Join(h.layout.StagingDir(), "home-test")
	must(t, os.RemoveAll(staged))
	must(t, os.Rename(restored, staged))
	must(t, h.c.installRestoredHome(app, staged, []string{"app/storage"}, "opx"))
	if _, err := os.Stat(filepath.Join(h.layout.AppHome("shop"), "app", "storage", "u.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(h.layout.AppHome("shop"), "other")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unlisted paths must not be restored")
	}
}

func TestRejectSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "f"), nil, 0o644))
	must(t, os.Symlink("/etc", filepath.Join(dir, "l")))
	must(t, rejectSpecialFiles(dir))
	if err := syscall.Mkfifo(filepath.Join(dir, "p"), 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	if err := rejectSpecialFiles(dir); err == nil {
		t.Fatal("a named pipe must be refused")
	}
}

func TestResticSnapshotTagMarksSecrets(t *testing.T) {
	for _, on := range []bool{false, true} {
		h, app := resticHarness(t, nil)
		v, _ := h.c.ResticSettings(t.Context(), app.ID)
		s := v.Settings
		s.IncludeSecrets = on
		if _, err := h.c.SaveResticSettings(t.Context(), app.ID, s, ""); err != nil {
			t.Fatal(err)
		}
		cap := runCapturedBackup(t, h, app, nil)
		tagged := false
		for _, a := range cap.args {
			if a[0] == "backup" {
				for i, x := range a {
					tagged = tagged || x == "--tag" && a[i+1] == ResticSecretsTag
				}
			}
		}
		if tagged != on {
			t.Fatalf("includeSecrets=%v but tag present=%v", on, tagged)
		}
	}
}

func TestRedactOpErrorScrubsSecrets(t *testing.T) {
	rep := strings.NewReplacer("s3cret", "[redacted]")
	var oe *OpError
	if !errors.As(redactOpError(Fail("x", "retry s3cret", "failed with s3cret"), rep), &oe) ||
		strings.Contains(oe.Message+oe.Guidance, "s3cret") {
		t.Fatalf("OpError not redacted: %+v", oe)
	}
	if got := redactOpError(errors.New("boom s3cret"), rep); strings.Contains(got.Error(), "s3cret") {
		t.Fatalf("plain error not redacted: %v", got)
	}
}
