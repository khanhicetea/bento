package operations

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// restoreDBFixture serves one snapshot of app whose manifest is man. The
// fake `restic restore` writes only the --include'd file, with body.
type restoreDBFixture struct {
	h       *harness
	app     domain.App
	man     ResticManifest
	body    string
	restore []string // args of the last restic restore
}

func newRestoreDBFixture(t *testing.T) *restoreDBFixture {
	t.Helper()
	f := &restoreDBFixture{body: sqliteHeader + "restored"}
	h, app := resticHarness(t, nil)
	f.h, f.app = h, app
	fileID := sqliteBinding(t, app).SQLiteFileID
	f.man = ResticManifest{FormatVersion: ResticFormatVersion, StackID: "stest", AppID: app.ID, Slug: app.Slug,
		Dumps: []ResticDump{{File: "sqlite/" + fileID + ".db", Engine: domain.EngineSQLite, Database: fileID,
			FileID: fileID, FileName: app.Slug + ".db"}}}
	h.fake.ExecHook = wrapHook(h.fake.ExecHook, func(a []string, req docker.ExecRequest) (docker.ExecResult, bool) {
		switch a[0] {
		case "dump":
			var v any = f.man
			if strings.HasSuffix(a[len(a)-1], "app.json") {
				v = AppSpec{FormatVersion: ResticFormatVersion, Slug: app.Slug, Runtime: app.Runtime, Resources: app.Resources}
			}
			raw, _ := json.Marshal(v)
			_, _ = req.Stdout.Write(raw)
		case "restore":
			f.restore = slices.Clone(a)
			i := slices.Index(a, "--include")
			if i < 0 {
				t.Errorf("restore without --include downloads the whole snapshot: %v", a)
				return docker.ExecResult{}, true
			}
			dirs, _ := filepath.Glob(filepath.Join(h.layout.StagingDir(), "restic-*", "data"))
			if len(dirs) != 1 {
				t.Errorf("staging dirs %v", dirs)
				return docker.ExecResult{}, true
			}
			p := filepath.Join(dirs[0], strings.TrimPrefix(a[i+1], "/"))
			must(t, os.MkdirAll(filepath.Dir(p), 0o755))
			must(t, os.WriteFile(p, []byte(f.body), 0o600))
		default:
			return docker.ExecResult{}, false
		}
		return docker.ExecResult{}, true
	})
	return f
}

func (f *restoreDBFixture) run(t *testing.T, snapshot string) store.Operation {
	t.Helper()
	db := f.man.Dumps[0].Database
	op, err := f.h.c.SubmitResticRestoreDB(t.Context(), f.app.ID,
		ResticRestoreDBRequest{Snapshot: snapshot, Database: db}, "replace "+db, "")
	if err != nil {
		t.Fatal(err)
	}
	return f.h.wait(op)
}

func TestResticRestoreDBValidation(t *testing.T) {
	f := newRestoreDBFixture(t)
	h, ctx, app := f.h, t.Context(), f.app
	db := f.man.Dumps[0].Database
	if _, err := h.c.SubmitResticRestoreDB(ctx, app.ID, ResticRestoreDBRequest{Database: db}, "replace "+db, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("restore before the repository is initialized: %v", err)
	}
	h.resticInit(app)
	if _, err := h.c.SubmitResticRestoreDB(ctx, app.ID, ResticRestoreDBRequest{Database: db}, "replace", ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("inexact confirmation: %v", err)
	}
	if _, err := h.c.SubmitResticRestoreDB(ctx, app.ID, ResticRestoreDBRequest{Database: "other_db"}, "replace other_db", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a database of no binding of the app: %v", err)
	}
	var verrs domain.ValidationErrors
	if _, err := h.c.SubmitResticRestoreDB(ctx, app.ID, ResticRestoreDBRequest{Snapshot: "x*", Database: db}, "replace "+db, ""); !errors.As(err, &verrs) {
		t.Fatalf("a malformed snapshot reference: %v", err)
	}
}

// A stopped app's SQLite database is replaced by the snapshot's copy; only
// that file is downloaded and the staging area is removed afterwards.
func TestResticRestoreDBReplacesSQLite(t *testing.T) {
	f := newRestoreDBFixture(t)
	h := f.h
	h.resticInit(f.app)
	got := f.run(t, "latest")
	if got.State != store.OpSucceeded {
		t.Fatalf("restore %s: %s: %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	if want := backup.ResticBentoMount + "/" + f.man.Dumps[0].File; !slices.Contains(f.restore, want) || !slices.Contains(f.restore, "aaaaaaaabbbbbbbb") {
		t.Fatalf("restore args %v, want the resolved snapshot and --include %s", f.restore, want)
	}
	b := sqliteBinding(t, f.app)
	raw, err := os.ReadFile(filepath.Join(h.layout.SQLiteFileDir(b.SQLiteFileID), f.app.Slug+".db"))
	if err != nil || string(raw) != f.body {
		t.Fatalf("database not restored: %q %v", raw, err)
	}
	if left, _ := filepath.Glob(filepath.Join(h.layout.StagingDir(), "restic-*")); len(left) != 0 {
		t.Fatalf("staging left behind: %v", left)
	}
}

func TestResticRestoreDBRefusesUnusableSnapshots(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(f *restoreDBFixture)
		code string
	}{
		"another app":     {func(f *restoreDBFixture) { f.man.AppID = "aother" }, "snapshot-invalid"},
		"no dump":         {func(f *restoreDBFixture) { f.man.Dumps = nil }, "database-missing"},
		"glob in path":    {func(f *restoreDBFixture) { f.man.Dumps[0].File = "sqlite/*" }, "snapshot-invalid"},
		"path escapes":    {func(f *restoreDBFixture) { f.man.Dumps[0].File = "sqlite/../../x.db" }, "snapshot-invalid"},
		"not sqlite data": {func(f *restoreDBFixture) { f.body = "not sqlite" }, "restore-failed"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRestoreDBFixture(t)
			f.h.resticInit(f.app)
			db := f.man.Dumps[0].Database
			tc.edit(f)
			op, err := f.h.c.SubmitResticRestoreDB(t.Context(), f.app.ID,
				ResticRestoreDBRequest{Snapshot: "aaaaaaaa", Database: db}, "replace "+db, "")
			if err != nil {
				t.Fatal(err)
			}
			got := f.h.wait(op)
			if got.State != store.OpFailed || got.ErrorCode != tc.code {
				t.Fatalf("got %s %s: %s", got.State, got.ErrorCode, got.ErrorMessage)
			}
			b := sqliteBinding(t, f.app)
			if _, err := os.Lstat(filepath.Join(f.h.layout.SQLiteFileDir(b.SQLiteFileID), f.app.Slug+".db")); err == nil {
				t.Fatal("the database was written by a refused restore")
			}
		})
	}
}

// MySQL and PostgreSQL bindings both start with <slug>_main: a restore by
// name alone must not guess which one to replace.
func TestFindDatabaseRefusesAmbiguousName(t *testing.T) {
	app := domain.App{Slug: "shop", Bindings: []domain.Binding{
		{ID: "bmy", Engine: domain.EngineMySQL, Databases: []string{"shop_main", "shop_logs"}},
		{ID: "bpg", Engine: domain.EnginePostgres, Databases: []string{"shop_main"}},
		{ID: "bsq", Engine: domain.EngineSQLite, SQLiteFileID: "shop_f1"},
	}}
	var verrs domain.ValidationErrors
	if _, _, err := findDatabase(app, "", "shop_main"); !errors.As(err, &verrs) || verrs[0].Field != "binding" {
		t.Fatalf("ambiguous name: %v", err)
	}
	for _, tc := range []struct{ binding, db, want string }{
		{"bpg", "shop_main", "bpg"}, {"bmy", "shop_main", "bmy"}, {"", "shop_logs", "bmy"}, {"", "shop_f1", "bsq"},
	} {
		b, sqlite, err := findDatabase(app, tc.binding, tc.db)
		if err != nil || b.ID != tc.want || sqlite != (tc.want == "bsq") {
			t.Fatalf("%s/%s: %s %v %v", tc.binding, tc.db, b.ID, sqlite, err)
		}
	}
	for _, tc := range [][2]string{{"bpg", "shop_logs"}, {"nope", "shop_main"}, {"", "other"}} {
		if _, _, err := findDatabase(app, tc[0], tc[1]); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%v: %v", tc, err)
		}
	}
}
