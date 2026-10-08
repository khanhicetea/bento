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
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const remoteRepo = "b2:bento/apps/shop"

func pendingKeys(t *testing.T, h *harness) []string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(h.c.resticKeyDir(), "*.pending-*"))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func remoteInput(slug string) ResticRemoteInput {
	return ResticRemoteInput{Repository: remoteRepo, Key: "cross-stack-key-0123", Slug: slug}
}

func TestRestoreFromBackupSubmissionIsValidated(t *testing.T) {
	h, _ := resticHarness(t, nil)
	ctx := t.Context()
	in := remoteInput("shop-copy")

	if _, err := h.c.SubmitRestoreFromBackup(ctx, in, "clone shop", ""); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("want confirmation error, got %v", err)
	}
	for name, mod := range map[string]func(*ResticRemoteInput){
		"bad repository":      func(i *ResticRemoteInput) { i.Repository = "no spaces allowed" },
		"unknown remote":      func(i *ResticRemoteInput) { i.Repository = "missing:path" },
		"empty key":           func(i *ResticRemoteInput) { i.Key = "  " },
		"multi-line key":      func(i *ResticRemoteInput) { i.Key = "a\nb" },
		"bad slug":            func(i *ResticRemoteInput) { i.Slug = "Bad Slug" },
		"bad snapshot":        func(i *ResticRemoteInput) { i.Snapshot = "../x" },
		"unknown backupAfter": func(i *ResticRemoteInput) { i.BackupAfter = "sideways" },
	} {
		bad := in
		mod(&bad)
		if _, err := h.c.SubmitRestoreFromBackup(ctx, bad, "clone "+bad.Slug, ""); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	taken := in
	taken.Slug = "shop"
	if _, err := h.c.SubmitRestoreFromBackup(ctx, taken, "clone shop", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("existing slug: %v", err)
	}
	if got := pendingKeys(t, h); len(got) != 0 {
		t.Fatalf("a refused request left key files: %v", got)
	}

	op, err := h.c.SubmitRestoreFromBackup(ctx, in, "clone shop-copy", "idem-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(op.Request), in.Key) {
		t.Fatal("the key is persisted in the operation request")
	}
	var persisted ResticRemoteRequest
	must(t, json.Unmarshal(op.Request, &persisted))
	if persisted.Repository != remoteRepo || persisted.Slug != "shop-copy" || persisted.Snapshot != "latest" ||
		persisted.BackupAfter != CloneBackupNone || !remoteJobID.MatchString(persisted.AppID) {
		t.Fatalf("persisted request %+v", persisted)
	}
	if op.TargetKind != remoteTargetKind || op.TargetID != remoteRepo {
		t.Fatalf("target %s/%s", op.TargetKind, op.TargetID)
	}
	h.wait(op)

	// A replayed request never leaves a second key file behind.
	if _, err := h.c.SubmitRestoreFromBackup(ctx, in, "clone shop-copy", "idem-1"); err != nil {
		t.Fatal(err)
	}
	if got := pendingKeys(t, h); len(got) != 0 {
		t.Fatalf("key files left after a replay: %v", got)
	}
}

func TestRemoteClaims(t *testing.T) {
	lookup := func(string) (domain.App, error) { return domain.App{}, store.ErrNotFound }
	if cl := classify(store.Operation{Kind: KindAppRestoreFromBackup, TargetID: remoteRepo}, lookup); cl.global ||
		cl.pool != clonePool || !slices.Contains(cl.shared, "restic-remote:"+remoteRepo) {
		t.Fatalf("restore-from-backup downloads in the clone pool, sharing the repository: %+v", cl)
	}
	a := classify(store.Operation{Kind: KindResticInspectRemote, TargetID: remoteRepo}, lookup)
	b := classify(store.Operation{Kind: KindResticInspectRemote, TargetID: remoteRepo}, lookup)
	if a.global || len(a.excl) != 0 || a.conflicts(b) {
		t.Fatalf("two remote inspections may overlap: %+v", a)
	}
}

func TestResolveSnapshot(t *testing.T) {
	snaps := []domain.ResticSnapshot{{ID: "bbbbbbbb11111111", ShortID: "bbbbbbbb"}, {ID: "aaaaaaaa22222222", ShortID: "aaaaaaaa"}}
	for want, id := range map[string]string{"": "bbbbbbbb11111111", "latest": "bbbbbbbb11111111",
		"aaaaaaaa": "aaaaaaaa22222222", "aaaaaaaa22222222": "aaaaaaaa22222222"} {
		if got, err := resolveSnapshot(snaps, want); err != nil || got != id {
			t.Fatalf("resolve %q = %q, %v", want, got, err)
		}
	}
	if _, err := resolveSnapshot(snaps, "cccccccc"); err == nil {
		t.Fatal("unknown snapshot resolved")
	}
	if _, err := resolveSnapshot(nil, "latest"); err == nil {
		t.Fatal("an empty repository resolved")
	}
}

func TestResticInspectRemoteListsSnapshots(t *testing.T) {
	f := newCloneFixture(t, true, false)
	h := f.h
	in := remoteInput("")
	op, err := h.c.SubmitResticInspectRemote(t.Context(), in, "")
	got := h.mustSucceed(op, err)
	var pv ResticClonePreview
	must(t, json.Unmarshal(got.Result, &pv))
	if pv.SourceSlug != "shop" || pv.Slug != previewSlug || pv.Snapshot != "aaaaaaaabbbbbbbb" || len(pv.Snapshots) != 1 ||
		pv.Snapshots[0].ShortID != "aaaaaaaa" || !pv.Secrets || len(pv.Blockers) != 0 {
		t.Fatalf("preview %+v", pv)
	}
	if strings.Contains(string(got.Result), in.Key) {
		t.Fatal("the key is in the result")
	}
	if _, err := store.GetApp(t.Context(), h.store.DB(), "shop-copy"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("inspect must not create an app")
	}
	if left := pendingKeys(t, h); len(left) != 0 {
		t.Fatalf("pending key kept after inspect: %v", left)
	}
	if _, err := os.Stat(h.c.remoteCacheDir(persistedJobID(t, got))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the restic cache of an inspection must be removed")
	}
}

func persistedJobID(t *testing.T, op store.Operation) string {
	t.Helper()
	var req ResticRemoteRequest
	must(t, json.Unmarshal(op.Request, &req))
	return req.AppID
}

func TestRestoreFromBackupDeletesPendingKey(t *testing.T) {
	f := newCloneFixture(t, true, false)
	h, ctx := f.h, t.Context()
	op, err := h.c.SubmitRestoreFromBackup(ctx, remoteInput("shop-copy"), "clone shop-copy", "")
	got := h.mustSucceed(op, err)
	var res ResticCloneResult
	must(t, json.Unmarshal(got.Result, &res))
	cp, err := store.GetApp(ctx, h.store.DB(), "shop-copy")
	if err != nil {
		t.Fatal(err)
	}
	if cp.DesiredRuntime != domain.DesiredStopped || cp.Publication != domain.Unpublished || !res.Stopped ||
		cp.HomePath != "/home/shop" || res.Snapshot != "aaaaaaaabbbbbbbb" {
		t.Fatalf("clone %+v result %+v", cp, res)
	}
	if left := pendingKeys(t, h); len(left) != 0 {
		t.Fatalf("pending key kept: %v", left)
	}
	if _, err := os.Stat(h.c.resticKeyPath(cp.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the key must not be adopted without same-repo")
	}
	if _, err := os.Stat(h.c.remoteCacheDir(persistedJobID(t, got))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the restic cache must be removed when the repository is not adopted")
	}
	if v, _ := h.c.ResticSettings(ctx, cp.ID); v.Configured {
		t.Fatalf("backup settings created without backupAfter: %+v", v.Settings)
	}
	for _, s := range []string{string(got.Result), got.ErrorMessage} {
		if strings.Contains(s, "cross-stack-key-0123") || strings.Contains(s, "super-secret-value") {
			t.Fatal("a secret reached the operation record")
		}
	}
	events, _ := store.ListEvents(ctx, h.store.DB(), got.ID, 0)
	for _, ev := range events {
		if strings.Contains(ev.Message, "cross-stack-key-0123") || strings.Contains(ev.Message, "super-secret-value") {
			t.Fatalf("a secret reached the operation log: %s", ev.Message)
		}
	}
}

func TestRestoreFromBackupSameRepoAdoptsKey(t *testing.T) {
	f := newCloneFixture(t, false, false)
	h, ctx := f.h, t.Context()
	in := remoteInput("shop-copy")
	in.BackupAfter = CloneBackupSameRepo
	op, err := h.c.SubmitRestoreFromBackup(ctx, in, "clone shop-copy", "")
	got := h.mustSucceed(op, err)
	cp, err := store.GetApp(ctx, h.store.DB(), "shop-copy")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(h.c.resticKeyPath(cp.ID))
	if err != nil || string(raw) != "cross-stack-key-0123" {
		t.Fatalf("adopted key %q %v", raw, err)
	}
	if info, _ := os.Stat(h.c.resticKeyPath(cp.ID)); info.Mode().Perm() != 0o400 {
		t.Fatalf("adopted key mode %v", info.Mode())
	}
	v, err := h.c.ResticSettings(ctx, cp.ID)
	if err != nil || !v.Configured || v.Settings.Repository != remoteRepo || v.State.RepositoryID != testRepoID ||
		v.Settings.Schedule.Enabled || len(v.State.Snapshots) != 1 {
		t.Fatalf("adopted settings %+v %v", v, err)
	}
	if left := pendingKeys(t, h); len(left) != 0 {
		t.Fatalf("pending key kept: %v", left)
	}
	if _, err := os.Stat(h.c.remoteCacheDir(persistedJobID(t, got))); err != nil {
		t.Fatalf("the cache of an adopted repository must stay: %v", err)
	}
}

func TestRestoreFromBackupNewRepoDoesNotKeepKey(t *testing.T) {
	f := newCloneFixture(t, false, false)
	h, ctx := f.h, t.Context()
	in := remoteInput("shop-copy")
	in.BackupAfter = CloneBackupNewRepo
	h.mustSucceed(h.c.SubmitRestoreFromBackup(ctx, in, "clone shop-copy", ""))
	cp, _ := store.GetApp(ctx, h.store.DB(), "shop-copy")
	v, _ := h.c.ResticSettings(ctx, cp.ID)
	if !v.Configured || v.Settings.Repository != "b2:bento/apps/shop-copy" || v.State.RepositoryID != "" {
		t.Fatalf("new-repo settings %+v", v)
	}
	if _, err := os.Stat(h.c.resticKeyPath(cp.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new-repo must not store the old key")
	}
}

func TestRestoreFromBackupFailureRemovesKeyAndApp(t *testing.T) {
	f := newCloneFixture(t, true, true)
	h, ctx := f.h, t.Context()
	op, err := h.c.SubmitRestoreFromBackup(ctx, remoteInput("shop-copy"), "clone shop-copy", "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed {
		t.Fatalf("a broken snapshot must fail, got %s", got.State)
	}
	if _, err := store.GetApp(ctx, h.store.DB(), "shop-copy"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the partial clone was not removed")
	}
	if left := pendingKeys(t, h); len(left) != 0 {
		t.Fatalf("pending key kept after failure: %v", left)
	}
	if _, err := os.Stat(h.c.remoteCacheDir(persistedJobID(t, got))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the restic cache must be removed after a failure")
	}
}

func TestRestoreFromBackupRejectsWrongKey(t *testing.T) {
	h, _ := resticHarness(t, func(args []string, _ docker.ExecRequest) (docker.ExecResult, bool) {
		if args[0] == "cat" {
			return docker.ExecResult{ExitCode: 12}, true
		}
		return docker.ExecResult{}, false
	})
	op, err := h.c.SubmitRestoreFromBackup(t.Context(), remoteInput("shop-copy"), "clone shop-copy", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "restic-key-rejected" {
		t.Fatalf("got %s %s", got.State, got.ErrorCode)
	}
	if left := pendingKeys(t, h); len(left) != 0 {
		t.Fatalf("pending key kept: %v", left)
	}
	if _, err := store.GetApp(t.Context(), h.store.DB(), "shop-copy"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("no app may be created with a rejected key")
	}
}
