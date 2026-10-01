package operations

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const testRepoID = "5f0c1e2d3c4b5a69788796a5b4c3d2e1f0a1b2c3d4e5f60718293a4b5c6d7e8f"

// resticHarness answers restic execs like a healthy repository. onExec, when
// set, handles a command first (return true to stop).
func resticHarness(t *testing.T, onExec func(args []string, req docker.ExecRequest) (docker.ExecResult, bool)) (*harness, domain.App) {
	t.Helper()
	h := newHarness(t)
	h.fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) == 0 || req.Cmd[0] != "restic" {
			return docker.ExecResult{Stdout: []byte("ready")}
		}
		args := req.Cmd[1:]
		if onExec != nil {
			if res, done := onExec(args, req); done {
				return res
			}
		}
		out := ""
		switch strings.Join(args[:min(2, len(args))], " ") {
		case "cat config":
			out = `{"version":2,"id":"` + testRepoID + `"}`
		case "snapshots --no-lock":
			out = `[{"id":"aaaaaaaabbbbbbbb","short_id":"aaaaaaaa","time":"2026-10-01T03:30:00Z","tags":["app=x"],"hostname":"bento"}]`
		case "key list":
			out = `[{"current":true,"id":"1111111122222222","userName":"root","hostName":"bento","created":"2026-10-01 00:00:00"}]`
		}
		if req.Stdout != nil {
			_, _ = req.Stdout.Write([]byte(out + "\n"))
		}
		return docker.ExecResult{}
	}
	if err := os.WriteFile(filepath.Join(h.layout.RcloneDir(), "rclone.conf"), []byte("[b2]\ntype = b2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := h.createApp("shop")
	s := domain.DefaultResticSettings()
	s.Repository = "b2:bento/apps/shop"
	s.Schedule.Enabled = true
	if _, err := h.c.SaveResticSettings(t.Context(), app.ID, s); err != nil {
		t.Fatal(err)
	}
	return h, app
}

func (h *harness) resticInit(app domain.App) string {
	h.t.Helper()
	op, key, err := h.c.SubmitResticInit(h.t.Context(), app.ID, "")
	h.mustSucceed(op, err)
	return key
}

func TestResticSettingsValidationAndSchedule(t *testing.T) {
	h, app := resticHarness(t, nil)
	ctx := t.Context()
	bad := domain.DefaultResticSettings()
	bad.Repository = "missing:path"
	var verrs domain.ValidationErrors
	if _, err := h.c.SaveResticSettings(ctx, app.ID, bad); !errors.As(err, &verrs) {
		t.Fatalf("a remote absent from rclone.conf must be refused, got %v", err)
	}
	sch, err := store.GetSchedule(ctx, h.store.DB(), resticScheduleID(app.ID))
	if err != nil || sch.Kind != "app-backup" || !sch.Enabled {
		t.Fatalf("schedule row: %+v %v", sch, err)
	}
	if _, err := h.c.SubmitResticBackup(ctx, app.ID, "manual", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("backup before init must be refused, got %v", err)
	}
}

func TestResticInitStoresKeyOnce(t *testing.T) {
	h, app := resticHarness(t, nil)
	ctx := t.Context()
	key := h.resticInit(app)
	if len(key) < 40 {
		t.Fatalf("weak key %q", key)
	}
	raw, err := os.ReadFile(h.c.resticKeyPath(app.ID))
	if err != nil || string(raw) != key {
		t.Fatalf("stored key mismatch: %v", err)
	}
	if info, _ := os.Stat(h.c.resticKeyPath(app.ID)); info.Mode().Perm() != 0o400 {
		t.Fatalf("key mode %v", info.Mode())
	}
	pending, _ := filepath.Glob(filepath.Join(h.c.resticKeyDir(), "*.pending-*"))
	if len(pending) != 0 {
		t.Fatalf("pending keys left behind: %v", pending)
	}
	v, _ := h.c.ResticSettings(ctx, app.ID)
	if v.State.RepositoryID != testRepoID || len(v.State.Snapshots) != 1 || len(v.State.Keys) != 1 {
		t.Fatalf("state %+v", v.State)
	}
	// The key never reaches argv or a persisted operation request.
	for _, call := range slices.Clone(h.fake.Calls) {
		if strings.Contains(call, key) {
			t.Fatalf("key leaked into %q", call)
		}
	}
	ops, _ := store.ListOperations(ctx, h.store.DB(), store.OpFilter{})
	for _, op := range ops {
		if strings.Contains(string(op.Request), key) {
			t.Fatalf("key persisted in operation %s", op.Kind)
		}
	}
	if _, _, err := h.c.SubmitResticInit(ctx, app.ID, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second init must be refused, got %v", err)
	}
}

func TestResticConnectRejectsWrongKey(t *testing.T) {
	h, app := resticHarness(t, func(args []string, _ docker.ExecRequest) (docker.ExecResult, bool) {
		if args[0] == "cat" {
			return docker.ExecResult{ExitCode: 12}, true
		}
		return docker.ExecResult{}, false
	})
	op, err := h.c.SubmitResticConnect(t.Context(), app.ID, "not-the-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "restic-key-rejected" {
		t.Fatalf("got %s %s", got.State, got.ErrorCode)
	}
	if _, err := os.Stat(h.c.resticKeyPath(app.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a rejected key must not be stored")
	}
}

func TestResticKeyAddNeedsConfirmationAndKeepsCurrent(t *testing.T) {
	h, app := resticHarness(t, nil)
	ctx := t.Context()
	h.resticInit(app)
	if _, _, err := h.c.SubmitResticKeyAdd(ctx, app.ID, "staging", "export", ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("want confirmation error, got %v", err)
	}
	op, key, err := h.c.SubmitResticKeyAdd(ctx, app.ID, "staging", "export shop", "")
	h.mustSucceed(op, err)
	if key == "" {
		t.Fatal("added key not returned")
	}
	if _, err := h.c.SubmitResticKeyRemove(ctx, app.ID, "11111111", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("removing Bento's own key must be refused, got %v", err)
	}
}

func TestResticBackupWritesManifestAndRedactsEnv(t *testing.T) {
	var manifest ResticManifest
	var spec AppSpec
	var excludes string
	var args []string
	h, app := resticHarness(t, nil)
	ctx := t.Context()
	h.fake.ExecHook = wrapHook(h.fake.ExecHook, func(a []string, req docker.ExecRequest) (docker.ExecResult, bool) {
		if a[0] != "backup" {
			return docker.ExecResult{}, false
		}
		args = a
		dirs, _ := filepath.Glob(filepath.Join(h.layout.StagingDir(), "restic-*"))
		for _, d := range dirs {
			readJSON(t, filepath.Join(d, "data", "manifest.json"), &manifest)
			readJSON(t, filepath.Join(d, "data", "app.json"), &spec)
			b, _ := os.ReadFile(filepath.Join(d, "ctl", "excludes"))
			excludes = string(b)
		}
		_, _ = req.Stdout.Write([]byte(`{"message_type":"summary","snapshot_id":"cccccccc","files_new":2,"total_files_processed":5,"data_added":1234}` + "\n"))
		return docker.ExecResult{}, true
	})
	app.Runtime.Env = []domain.EnvVar{{Key: "APP_KEY", Value: "base64:secret"}, {Key: "APP_ENV", Value: "production"}}
	must(t, store.UpdateApp(ctx, h.store.DB(), app))
	h.resticInit(app)
	op, err := h.c.SubmitResticBackup(ctx, app.ID, "manual", "")
	h.mustSucceed(op, err)
	if manifest.AppID != app.ID || manifest.FormatVersion != ResticFormatVersion || !slices.Equal(manifest.Paths, []string{"."}) {
		t.Fatalf("manifest %+v", manifest)
	}
	for _, e := range spec.Runtime.Env {
		if e.Key == "APP_KEY" && e.Value != domain.RedactedEnvValue {
			t.Fatal("APP_KEY not redacted")
		}
		if e.Key == "APP_ENV" && e.Value != "production" {
			t.Fatal("APP_ENV must be kept")
		}
	}
	if !strings.Contains(excludes, "/backup/home/"+domain.HomeSidecarName) {
		t.Fatalf("sidecar not excluded: %s", excludes)
	}
	if !slices.Contains(args, "/backup/home") || !slices.Contains(args, "/backup/bento") ||
		!slices.Contains(args, "app="+app.ID) {
		t.Fatalf("backup args %v", args)
	}
	v, _ := h.c.ResticSettings(ctx, app.ID)
	if v.State.LastBackup == nil || !v.State.LastBackup.OK || v.State.LastBackup.BytesAdded != 1234 {
		t.Fatalf("last backup %+v", v.State.LastBackup)
	}
	if v.State.LastPruneAt.IsZero() {
		t.Fatal("first backup should prune")
	}
	if len(v.State.History) != 1 || v.State.History[0].Trigger != "manual" || v.State.History[0].OpID != op.ID {
		t.Fatalf("history %+v", v.State.History)
	}
	overview, err := h.c.ResticOverview(ctx)
	if err != nil || len(overview) != 1 || overview[0].App.ID != app.ID {
		t.Fatalf("overview %+v %v", overview, err)
	}
}

func TestResticFailedBackupIsInHistory(t *testing.T) {
	h, app := resticHarness(t, func(a []string, _ docker.ExecRequest) (docker.ExecResult, bool) {
		if a[0] == "backup" {
			return docker.ExecResult{ExitCode: 1}, true
		}
		return docker.ExecResult{}, false
	})
	ctx := t.Context()
	h.resticInit(app)
	op, err := h.c.SubmitResticBackup(ctx, app.ID, "schedule", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed {
		t.Fatalf("backup should fail, got %s", got.State)
	}
	v, _ := h.c.ResticSettings(ctx, app.ID)
	if len(v.State.History) != 1 || v.State.History[0].OK || v.State.History[0].Error == "" ||
		v.State.History[0].Trigger != "schedule" {
		t.Fatalf("failed run not recorded: %+v", v.State.History)
	}
}

func TestResticRestoreSwapsHomeAndKeepsPrevious(t *testing.T) {
	h, app := resticHarness(t, nil)
	ctx := t.Context()
	home := h.layout.AppHome(app.Slug)
	if err := os.WriteFile(filepath.Join(home, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	sidecar, _ := os.ReadFile(h.layout.HomeSidecar(app.Slug))
	h.fake.ExecHook = wrapHook(h.fake.ExecHook, func(a []string, _ docker.ExecRequest) (docker.ExecResult, bool) {
		if a[0] != "restore" {
			return docker.ExecResult{}, false
		}
		dirs, _ := filepath.Glob(filepath.Join(h.layout.StagingDir(), "restic-*", "data"))
		for _, d := range dirs {
			must(t, os.MkdirAll(filepath.Join(d, "backup", "home", "app", "storage"), 0o755))
			must(t, os.WriteFile(filepath.Join(d, "backup", "home", "app", "storage", "u.jpg"), []byte("img"), 0o644))
			// A sidecar from the source stack must not replace the live one.
			must(t, os.WriteFile(filepath.Join(d, "backup", "home", domain.HomeSidecarName), []byte("{}"), 0o644))
			must(t, os.MkdirAll(filepath.Join(d, "backup", "bento"), 0o700))
			raw, _ := json.Marshal(ResticManifest{FormatVersion: ResticFormatVersion, AppID: "aother", Slug: "shop",
				Paths: []string{"."}, Dumps: []ResticDump{}, HomeSQLite: []string{}})
			must(t, os.WriteFile(filepath.Join(d, "backup", "bento", "manifest.json"), raw, 0o600))
		}
		return docker.ExecResult{}, true
	})
	h.resticInit(app)
	if _, err := h.c.SubmitResticRestore(ctx, app.ID, ResticRestoreRequest{Snapshot: "aaaaaaaa", Files: true},
		"restore", ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("want confirmation error, got %v", err)
	}
	op, err := h.c.SubmitResticRestore(ctx, app.ID, ResticRestoreRequest{Snapshot: "aaaaaaaa", Files: true},
		"restore shop", "")
	h.mustSucceed(op, err)
	restored := filepath.Join(home, "app", "storage", "u.jpg")
	o, _, err := platform.StatOwner(restored)
	if err != nil || o.UID != app.UID {
		t.Fatalf("restored file owner %+v %v", o, err)
	}
	if _, err := os.Stat(filepath.Join(home, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("whole-home restore must replace the home")
	}
	if got, _ := os.ReadFile(h.layout.HomeSidecar(app.Slug)); string(got) != string(sidecar) {
		t.Fatal("live identity record must be kept")
	}
	kept, _ := filepath.Glob(filepath.Join(h.layout.HomesDir(), ".pre-restore-shop-*", "home", "old.txt"))
	if len(kept) != 1 {
		t.Fatal("previous home must be kept aside")
	}
	if err := h.c.verifyHomeIdentity(app); err != nil {
		t.Fatalf("restored home must still verify: %v", err)
	}
}

func TestMapDumpRequiresSameVersion(t *testing.T) {
	app := domain.App{Slug: "shop", Bindings: []domain.Binding{
		{Engine: domain.EngineMySQL, Service: "mysql", Databases: []string{"shop_main"}},
	}}
	man := ResticManifest{Dumps: []ResticDump{{Engine: domain.EngineMySQL, Version: "8.4", Database: "legacy"}}}
	b, db, err := mapDump(app, man, man.Dumps[0], map[string]string{"mysql": "8.4"})
	if err != nil || db != "shop_main" || b.Service != "mysql" {
		t.Fatalf("single database should map: %v %s", err, db)
	}
	if _, _, err := mapDump(app, man, man.Dumps[0], map[string]string{"mysql": "8.0"}); err == nil {
		t.Fatal("a different major version must be refused")
	}
	man.Dumps = append(man.Dumps, ResticDump{Engine: domain.EngineMySQL, Version: "8.4", Database: "other"})
	if _, _, err := mapDump(app, man, man.Dumps[0], map[string]string{"mysql": "8.4"}); err == nil {
		t.Fatal("ambiguous mapping must be refused")
	}
}

func TestReadManifestRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	for _, m := range []ResticManifest{
		{FormatVersion: 99},
		{FormatVersion: ResticFormatVersion, Paths: []string{"../x"}},
		{FormatVersion: ResticFormatVersion, HomeSQLite: []string{"/etc/shadow"}},
		{FormatVersion: ResticFormatVersion, Dumps: []ResticDump{{File: "../../x.sql"}}},
	} {
		raw, _ := json.Marshal(m)
		must(t, os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600))
		if _, err := readManifest(dir); err == nil {
			t.Errorf("manifest %+v accepted", m)
		}
	}
}

func TestResticClaims(t *testing.T) {
	app := domain.App{ID: "a1", Bindings: []domain.Binding{{Engine: domain.EngineMySQL, Service: "mysql"}}}
	lookup := func(string) (domain.App, error) { return app, nil }
	bk := classify(store.Operation{Kind: KindResticBackup, TargetID: "a1"}, lookup)
	if bk.global || !slices.Contains(bk.shared, appClaim("a1")) || bk.pool != resticPool {
		t.Fatalf("backup claims %+v", bk)
	}
	start := classify(store.Operation{Kind: KindAppReconcile, TargetID: "a1"}, lookup)
	if !bk.conflicts(start) {
		t.Fatal("a backup must not overlap an app lifecycle operation")
	}
	other := classify(store.Operation{Kind: KindResticBackup, TargetID: "a2"}, lookup)
	if bk.conflicts(other) {
		t.Fatal("backups of different apps run in parallel")
	}
	rs := classify(store.Operation{Kind: KindResticRestore, TargetID: "a1"}, lookup)
	if !slices.Contains(rs.excl, appClaim("a1")) {
		t.Fatalf("restore claims %+v", rs)
	}
}

func wrapHook(
	next func(string, docker.ExecRequest) docker.ExecResult,
	first func([]string, docker.ExecRequest) (docker.ExecResult, bool),
) func(string, docker.ExecRequest) docker.ExecResult {
	return func(id string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) > 1 && req.Cmd[0] == "restic" {
			if res, done := first(req.Cmd[1:], req); done {
				return res
			}
		}
		return next(id, req)
	}
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestResticInitFailsFastOnUnusableRemote(t *testing.T) {
	h, app := resticHarness(t, nil)
	h.fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		if len(req.Cmd) > 1 && req.Cmd[0] == "rclone" {
			_, _ = req.Stderr.Write([]byte("ERROR : probe: Failed to touch: NoSuchBucket: The specified bucket does not exist\n"))
			return docker.ExecResult{ExitCode: 1}
		}
		if len(req.Cmd) > 1 && req.Cmd[0] == "restic" {
			t.Errorf("restic must not run against an unusable remote: %v", req.Cmd)
		}
		return docker.ExecResult{Stdout: []byte("ready")}
	}
	op, _, err := h.c.SubmitResticInit(t.Context(), app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || got.ErrorCode != "remote-unusable" || !strings.Contains(got.ErrorMessage, "NoSuchBucket") {
		t.Fatalf("got %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	if _, err := os.Stat(h.c.resticKeyPath(app.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no key may be adopted when init fails")
	}
}

func TestResticBackupWarnsWhenLockCannotBeDeleted(t *testing.T) {
	h, app := resticHarness(t, func(a []string, req docker.ExecRequest) (docker.ExecResult, bool) {
		if a[0] != "backup" {
			return docker.ExecResult{}, false
		}
		_, _ = req.Stdout.Write([]byte(`{"message_type":"summary","snapshot_id":"dddddddd"}` + "\n"))
		_, _ = req.Stderr.Write([]byte("rclone: ERROR : locks/abc: Delete request remove error: AccessDenied\nerror while unlocking: context canceled\n"))
		return docker.ExecResult{}, true
	})
	ctx := t.Context()
	h.resticInit(app)
	op := h.mustSucceed(h.c.SubmitResticBackup(ctx, app.ID, "manual", ""))
	events, err := store.ListEvents(ctx, h.store.DB(), op.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if strings.Contains(e.Message, "could not delete its repository lock") {
			return
		}
	}
	t.Fatalf("no lock warning in %+v", events)
}
