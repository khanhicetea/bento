package operations

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// removedShop creates and removes an app, leaving its data retained.
func removedShop(t *testing.T) (*harness, domain.App) {
	t.Helper()
	h := newHarness(t)
	app := h.createApp("shop")
	h.mustSucceed(h.c.RemoveApp(t.Context(), app.ID, "delete shop", ""))
	return h, app
}

func (h *harness) mustExist(path string) {
	h.t.Helper()
	if _, err := os.Stat(path); err != nil {
		h.t.Fatalf("%s should exist: %v", path, err)
	}
}

func (h *harness) mustNotExist(path string) {
	h.t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		h.t.Fatalf("%s should be gone: %v", path, err)
	}
}

func (h *harness) pruned(appID string) string {
	h.t.Helper()
	ret, err := store.GetRetired(h.t.Context(), h.store.DB(), appID)
	if err != nil {
		h.t.Fatal(err)
	}
	return ret.PrunedAt
}

func (h *harness) eventContains(opID, text string) bool {
	h.t.Helper()
	evs, err := store.ListEvents(h.t.Context(), h.store.DB(), opID, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, e := range evs {
		if strings.Contains(e.Message, text) {
			return true
		}
	}
	return false
}

func TestPruneRequiresExactConfirmation(t *testing.T) {
	h, app := removedShop(t)
	for _, bad := range []string{"Delete", "", "delete shop", "delete ", "yes"} {
		if _, err := h.c.PruneRetired(t.Context(), app.ID, bad, ""); !errors.Is(err, ErrConfirmation) {
			t.Errorf("%q: want ErrConfirmation, got %v", bad, err)
		}
	}
	h.mustExist(h.layout.AppHome("shop"))
	if h.pruned(app.ID) != "" {
		t.Fatal("a refused prune must not mark the app pruned")
	}
}

func TestPruneDeletesRetainedHomeAndMarksPruned(t *testing.T) {
	h, app := removedShop(t)
	ctx := t.Context()
	sqliteDirs := h.sqliteDirs(app.ID)
	if len(sqliteDirs) != 1 {
		t.Fatalf("want one retained sqlite directory, got %v", sqliteDirs)
	}
	h.mustExist(h.layout.AppHome("shop"))
	h.mustExist(sqliteDirs[0])
	op := h.mustSucceed(h.c.PruneRetired(ctx, app.ID, "delete", ""))
	h.mustNotExist(h.layout.AppHome("shop"))
	h.mustNotExist(sqliteDirs[0])
	if h.pruned(app.ID) == "" {
		t.Fatal("PrunedAt must be set")
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(op.Result), &res); err != nil || res["pruned"] != true {
		t.Fatalf("result %q %v", op.Result, err)
	}
}

func (h *harness) sqliteDirs(appID string) []string {
	h.t.Helper()
	ret, err := store.GetRetired(h.t.Context(), h.store.DB(), appID)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, id := range ret.Artifacts.SQLiteFileIDs {
		out = append(out, h.layout.SQLiteFileDir(id))
	}
	return out
}

func TestPruneTwiceIsRefused(t *testing.T) {
	h, app := removedShop(t)
	h.mustSucceed(h.c.PruneRetired(t.Context(), app.ID, "delete", ""))
	if _, err := h.c.PruneRetired(t.Context(), app.ID, "delete", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second prune: want ErrConflict, got %v", err)
	}
}

func TestPruneKeepsAHomeOfAnotherIncarnation(t *testing.T) {
	h, app := removedShop(t)
	raw, err := os.ReadFile(h.layout.HomeSidecar("shop"))
	if err != nil {
		t.Fatal(err)
	}
	var sc HomeSidecar
	if err := json.Unmarshal(raw, &sc); err != nil || sc.AppID != app.ID {
		t.Fatalf("sidecar %s: %v", raw, err)
	}
	sc.AppID = "app_other"
	raw, _ = json.Marshal(sc)
	if err := os.WriteFile(h.layout.HomeSidecar("shop"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	op := h.mustSucceed(h.c.PruneRetired(t.Context(), app.ID, "delete", ""))
	h.mustExist(h.layout.AppHome("shop"))
	if !h.eventContains(op.ID, "belongs to another incarnation") {
		t.Fatal("the kept home must be reported in the operation's events")
	}
}

func TestPruneRefusedWhileSlugIsActive(t *testing.T) {
	h, app := removedShop(t)
	ctx := t.Context()
	// A retained home blocks creating a new incarnation through CreateApp
	// (TestRetainedHomeIsNeverAdopted), so put the active row in directly.
	active := app
	active.ID = "app_active"
	uid, err := store.AllocateUID(ctx, h.store.DB(), domain.DefaultUIDRange(), h.c.HostIDs, active.ID, "shop")
	if err != nil {
		t.Fatal(err)
	}
	active.UID, active.GID = uid, uid
	if err := store.InsertApp(ctx, h.store.DB(), active); err != nil {
		t.Fatal(err)
	}
	op, err := h.c.PruneRetired(ctx, app.ID, "delete", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "slug-active" {
		t.Fatalf("want failed slug-active, got %s %s", got.State, got.ErrorCode)
	}
	h.mustExist(h.layout.AppHome("shop"))
	for _, dir := range h.sqliteDirs(app.ID) {
		h.mustExist(dir)
	}
	if h.pruned(app.ID) != "" {
		t.Fatal("a refused prune must not mark the app pruned")
	}
}

// A failed database drop stops the prune before any file is removed, and a
// retry (the injected failure is one-shot) finishes the job.
func TestPruneDropFailureLeavesFilesAndCanBeRetried(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	svc, op, err := h.c.CreateService(ctx, domain.EngineMySQL, "8.4", "")
	h.mustSucceed(op, err)
	app, op, err := h.c.CreateApp(ctx, CreateAppInput{
		Slug: "shop",
		Runtime: domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{
			Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"},
		}},
		Bindings: []BindingRequest{{Engine: domain.EngineSQLite}, {Engine: domain.EngineMySQL, Service: svc.Name}},
	}, "")
	h.mustSucceed(op, err)
	h.mustSucceed(h.c.RemoveApp(ctx, app.ID, "delete shop", ""))
	ret, err := store.GetRetired(ctx, h.store.DB(), app.ID)
	if err != nil || len(ret.Artifacts.Relational) != 1 {
		t.Fatalf("want one retained relational binding: %+v %v", ret, err)
	}

	h.fake.FailOn = map[string]error{"Exec": errors.New("injected drop failure")}
	failed, err := h.c.PruneRetired(ctx, app.ID, "delete", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(failed); got.State != store.OpFailed || !strings.Contains(got.ErrorMessage+got.ErrorCode, "injected drop failure") {
		t.Fatalf("want the injected drop failure, got %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	h.mustExist(h.layout.AppHome("shop"))
	for _, dir := range h.sqliteDirs(app.ID) {
		h.mustExist(dir)
	}
	if h.pruned(app.ID) != "" {
		t.Fatal("a failed prune must not mark the app pruned")
	}

	h.mustSucceed(h.c.PruneRetired(ctx, app.ID, "delete", ""))
	h.mustNotExist(h.layout.AppHome("shop"))
	for _, dir := range h.sqliteDirs(app.ID) {
		h.mustNotExist(dir)
	}
	if h.pruned(app.ID) == "" {
		t.Fatal("PrunedAt must be set after the retry")
	}
}
