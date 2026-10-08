package operations

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/moby/moby/api/types/container"
)

func TestRejectLinkedTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "backup")
	bento := filepath.Join(root, "bento")
	must(t, os.MkdirAll(filepath.Join(bento, "db"), 0o700))
	must(t, os.WriteFile(filepath.Join(bento, "manifest.json"), []byte("{}"), 0o600))
	must(t, rejectLinkedTree(bento))

	// A dump that points at the stack's state database.
	must(t, os.Symlink("/etc/hostname", filepath.Join(bento, "db", "x.sql")))
	if err := rejectLinkedTree(bento); err == nil {
		t.Fatal("a symlinked dump must be refused")
	}
	must(t, os.Remove(filepath.Join(bento, "db", "x.sql")))
	must(t, os.Symlink("/etc", filepath.Join(bento, "home-sqlite")))
	if err := rejectLinkedTree(bento); err == nil {
		t.Fatal("a symlinked directory must be refused")
	}

	// The bento directory itself replaced by a link.
	other := filepath.Join(t.TempDir(), "backup")
	must(t, os.MkdirAll(other, 0o700))
	must(t, os.Symlink(bento, filepath.Join(other, "bento")))
	if err := rejectLinkedTree(filepath.Join(other, "bento")); err == nil {
		t.Fatal("a symlinked bento directory must be refused")
	}
}

func TestInstallRestoredHomeRefusesSymlinkedParent(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	// A host directory the snapshot tries to reach through "x -> outside".
	outside := filepath.Join(h.layout.StagingDir(), "outside")
	must(t, os.MkdirAll(outside, 0o700))
	must(t, os.WriteFile(filepath.Join(outside, "passwd"), []byte("root"), 0o600))
	staged := filepath.Join(h.layout.StagingDir(), "home-test")
	must(t, os.MkdirAll(staged, 0o755))
	must(t, os.Symlink(outside, filepath.Join(staged, "x")))

	if err := h.c.installRestoredHome(app, staged, []string{"x/passwd"}, "opx"); err == nil {
		t.Fatal("a path through a symlinked directory must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "passwd")); err != nil {
		t.Fatalf("the host file must stay in place: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.layout.AppHome("shop"), "x")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing may be moved into the home")
	}
}

func TestInstallRestoredHomeOverlappingPaths(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	staged := filepath.Join(h.layout.StagingDir(), "home-test")
	must(t, os.MkdirAll(filepath.Join(staged, "app", "storage"), 0o755))
	must(t, os.WriteFile(filepath.Join(staged, "app", "storage", "u.txt"), []byte("x"), 0o644))
	must(t, h.c.installRestoredHome(app, staged, []string{"app/storage", "app"}, "opx"))
	if _, err := os.Stat(filepath.Join(h.layout.AppHome("shop"), "app", "storage", "u.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreHomeSQLiteRefusesSymlinkedSource(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	bento := filepath.Join(h.layout.StagingDir(), "bento")
	host := filepath.Join(h.layout.StagingDir(), "host")
	must(t, os.MkdirAll(filepath.Join(host, "data"), 0o700))
	must(t, os.WriteFile(filepath.Join(host, "data", "x.db"), []byte(sqliteHeader), 0o600))
	must(t, os.MkdirAll(filepath.Join(bento, "home-sqlite"), 0o700))
	must(t, os.Symlink(filepath.Join(host, "data"), filepath.Join(bento, "home-sqlite", "data")))
	owner := platform.Owner{UID: app.UID, GID: app.GID}
	if err := h.c.restoreHomeSQLite(app, bento, "data/x.db", owner); err == nil {
		t.Fatal("a SQLite copy reached through a symlink must be refused")
	}
}

func TestPlanCloneKeepsBindingsOnSeparateServices(t *testing.T) {
	h := newHarness(t)
	addMySQLService(t, h)
	spec := mysqlSpec("shop", "shop_main")
	spec.Bindings = append(spec.Bindings,
		AppSpecBinding{Engine: domain.EngineMySQL, Service: "mysql84b", Version: "8.4", Databases: []string{"shop_logs"}})
	man := ResticManifest{FormatVersion: 2, Slug: "shop", AppID: "asrc"}
	p, err := h.c.planClone(t.Context(), cloneInput{Man: man, Spec: spec, Slug: "copy", AppID: "anew"})
	must(t, err)
	if len(p.Blockers) != 1 || !strings.Contains(p.Blockers[0], "several mysql") {
		t.Fatalf("two bindings on one service must be blocked: %v", p.Blockers)
	}

	must(t, store.InsertService(t.Context(), h.store.DB(), domain.DataService{Name: "mysql84b", Engine: domain.EngineMySQL,
		Version: "8.4", Image: domain.MySQLVersions["8.4"], Volume: "v-mysql84b", CreatedAt: time.Now().UTC()}))
	p, err = h.c.planClone(t.Context(), cloneInput{Man: man, Spec: spec, Slug: "copy", AppID: "anew"})
	must(t, err)
	if len(p.Blockers) != 0 || p.App.Bindings[0].Service == p.App.Bindings[1].Service {
		t.Fatalf("blockers %v bindings %+v", p.Blockers, p.App.Bindings)
	}
}

func TestPlanCloneEmptiesOnlyRedactedEnv(t *testing.T) {
	h := newHarness(t)
	spec := mysqlSpec("shop")
	spec.Bindings = nil
	spec.Runtime.Env = []domain.EnvVar{
		{Key: "APP_KEY", Value: domain.RedactedEnvValue},
		{Key: "CACHE_KEY_PREFIX", Value: "shop"},
		{Key: "SORT_KEY", Value: "name"}, // a snapshot taken before keys were classified by value
	}
	man := ResticManifest{FormatVersion: 2, Slug: "shop", AppID: "asrc"}
	p, err := h.c.planClone(t.Context(), cloneInput{Man: man, Spec: spec, Slug: "copy", AppID: "anew"})
	must(t, err)
	got := map[string]string{}
	for _, e := range p.App.Runtime.Env {
		got[e.Key] = e.Value
	}
	if got["APP_KEY"] != "" || got["CACHE_KEY_PREFIX"] != "shop" || got["SORT_KEY"] != "name" ||
		len(p.EmptyEnv) != 1 || p.EmptyEnv[0] != "APP_KEY" {
		t.Fatalf("env %v empty %v", got, p.EmptyEnv)
	}
}

func TestResticRedactsConnectionStrings(t *testing.T) {
	app := domain.App{Runtime: domain.Runtime{Env: []domain.EnvVar{
		{Key: "DATABASE_URL", Value: "mysql://u:hunter22@db/app"},
		{Key: "SMTP_PASS", Value: "mailpw"},
		{Key: "APP_URL", Value: "https://shop.example.com"},
	}}}
	red := redactedEnv(app.Runtime.Env)
	if red[0].Value != domain.RedactedEnvValue || red[1].Value != domain.RedactedEnvValue || red[2].Value == domain.RedactedEnvValue {
		t.Fatalf("redacted %+v", red)
	}
	if len(resticSecrets(app).Env) != 2 {
		t.Fatalf("secrets %+v", resticSecrets(app).Env)
	}
	if out := secretRedactor(app).Replace("connect mysql://u:hunter22@db/app failed"); strings.Contains(out, "hunter22") {
		t.Fatalf("not redacted: %s", out)
	}
}

func TestEscalateWaitsForRunningOperations(t *testing.T) {
	h := newHarness(t)
	c := h.c
	c.begin("op_self", claims{shared: []string{"restic:a"}, pool: clonePool})
	c.begin("op_other", claims{excl: []string{"app:b"}})
	t.Cleanup(func() { c.end("op_self"); c.end("op_other") })
	r := &Run{c: c, Op: store.Operation{ID: "op_self"}}
	done := make(chan error, 1)
	go func() { done <- r.Escalate(t.Context()) }()
	select {
	case err := <-done:
		t.Fatalf("escalate returned while another operation runs: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if c.blocker(claims{excl: []string{"app:c"}}) != "op_self" {
		t.Fatal("no operation may start once an operation escalates")
	}
	c.end("op_other")
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("escalate did not return after the other operation finished")
	}
}

func TestRecoverRollsBackInterruptedClone(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	partial := h.createApp("shop-copy")
	// The clone that created it was running when the backend stopped.
	raw, _ := json.Marshal(ResticCloneRequest{Snapshot: "aaaaaaaabbbbbbbb", Slug: "shop-copy", AppID: partial.ID})
	op, _, err := store.InsertOperation(ctx, h.store.DB(), store.Operation{ID: platform.NewOperationID(),
		Kind: KindAppCloneFromBackup, TargetKind: "app", TargetID: "asource", Request: raw})
	must(t, err)
	if _, err := store.MarkRunning(ctx, h.store.DB(), op.ID); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(h.layout.StagingDir(), "restic-"+op.ID, "data", "backup", "bento")
	must(t, os.MkdirAll(staging, 0o700))
	must(t, os.WriteFile(filepath.Join(staging, "secrets.json"), []byte("{}"), 0o600))
	aside := filepath.Join(h.layout.HomesDir(), ".clone-"+op.ID)
	must(t, os.MkdirAll(aside, 0o700))
	jobID, err := h.fake.Create(ctx, docker.ContainerSpec{Name: "job", Config: &container.Config{
		Labels: h.c.Names.Labels(runtime.RoleBackup, map[string]string{runtime.LabelOperation: op.ID})}})
	must(t, err)
	h.fake.Containers[jobID].State = "running"

	must(t, h.c.Recover(ctx))
	ops, err := store.ListOperations(ctx, h.store.DB(), store.OpFilter{})
	must(t, err)
	var rec store.Operation
	for _, o := range ops {
		if o.Kind == KindResticRecover {
			rec = o
		}
	}
	if rec.ID == "" {
		t.Fatal("recover must submit restic.recover")
	}
	if got := h.wait(rec); got.State != store.OpSucceeded {
		t.Fatalf("recover failed: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	if _, err := store.GetApp(ctx, h.store.DB(), partial.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the partial clone must be removed: %v", err)
	}
	if _, err := os.Lstat(h.layout.AppHome("shop-copy")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the partial home must be removed")
	}
	for _, p := range []string{filepath.Join(h.layout.StagingDir(), "restic-"+op.ID), aside} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s must be removed", p)
		}
	}
	if _, ok := h.fake.Containers[jobID]; ok {
		t.Fatal("the orphaned job container must be removed")
	}
}

func TestRecoverSubmitsNothingWhenClean(t *testing.T) {
	h := newHarness(t)
	must(t, h.c.Recover(t.Context()))
	ops, _ := store.ListOperations(t.Context(), h.store.DB(), store.OpFilter{})
	for _, o := range ops {
		if o.Kind == KindResticRecover {
			t.Fatal("nothing to recover, yet restic.recover was submitted")
		}
	}
}

func TestResticRepositoryChangeNeedsConfirmationAndKeepsKey(t *testing.T) {
	h, app := resticHarness(t, nil)
	h.resticInit(app)
	ctx := t.Context()
	v, err := h.c.ResticSettings(ctx, app.ID)
	must(t, err)
	s := v.Settings
	s.Repository = "b2:bento/apps/elsewhere"
	if _, err := h.c.SaveResticSettings(ctx, app.ID, s, ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("want confirmation error, got %v", err)
	}
	// Other settings change without a confirmation.
	same := v.Settings
	same.Excludes = []string{"tmp"}
	if _, err := h.c.SaveResticSettings(ctx, app.ID, same, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.SaveResticSettings(ctx, app.ID, s, "disconnect shop"); err != nil {
		t.Fatal(err)
	}
	h.resticInit(app)
	retired, _ := filepath.Glob(filepath.Join(h.c.resticKeyDir(), app.ID+".retired-*.key"))
	if len(retired) != 1 {
		t.Fatalf("the old repository's key must be kept: %v", retired)
	}
	if _, err := os.Stat(h.c.resticKeyPath(app.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestResticStateUpdateForOldRepositoryIsDropped(t *testing.T) {
	h, app := resticHarness(t, nil)
	h.resticInit(app)
	ctx := t.Context()
	h.c.updateResticState(ctx, app.ID, "someotherrepo", func(s *domain.ResticState) { s.LastPruneAt = time.Now() })
	v, err := h.c.ResticSettings(ctx, app.ID)
	must(t, err)
	if !v.State.LastPruneAt.IsZero() {
		t.Fatal("an update for another repository must be dropped")
	}
	h.c.updateResticState(ctx, app.ID, v.State.RepositoryID, func(s *domain.ResticState) { s.LastPruneAt = time.Now() })
	if v, _ = h.c.ResticSettings(ctx, app.ID); v.State.LastPruneAt.IsZero() {
		t.Fatal("an update for the current repository must apply")
	}
}

func TestResticRefreshListsOnlyTheAppsSnapshots(t *testing.T) {
	var got []string
	h, app := resticHarness(t, func(args []string, _ docker.ExecRequest) (docker.ExecResult, bool) {
		if args[0] == "snapshots" {
			got = args
		}
		return docker.ExecResult{}, false
	})
	h.resticInit(app)
	if strings.Join(got, " ") != "snapshots --no-lock --tag app="+app.ID+" --json" {
		t.Fatalf("snapshots args %q", got)
	}
}
