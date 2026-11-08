package operations

import (
	"errors"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func (h *harness) runningApp(slug string) domain.App {
	h.t.Helper()
	app := h.createApp(slug)
	h.mustSucceed(h.c.StartApp(h.t.Context(), app.ID, ""))
	app, err := store.GetApp(h.t.Context(), h.c.Store.DB(), app.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return app
}

func (h *harness) planned(app domain.App) store.RuntimeBuild {
	h.t.Helper()
	b, _, ok, err := h.c.plannedBuild(h.t.Context(), app)
	if err != nil || !ok {
		h.t.Fatalf("no planned build for %s: %v", app.Slug, err)
	}
	return b
}

func (h *harness) rebuild(key string) store.Operation {
	h.t.Helper()
	return h.mustSucceed(h.c.RebuildImage(h.t.Context(), key, ""))
}

func TestRebuildDiscardsAnUnchangedBuild(t *testing.T) {
	h := newHarness(t)
	app := h.runningApp("shop")
	before := h.planned(app)
	key := app.Runtime.ImageKey().String()
	pulls := h.fake.CallCount("PullImage " + domain.DebianBase)
	h.rebuild(key)
	if got := h.fake.Builds[len(h.fake.Builds)-1]; !got.NoCache || h.fake.CallCount("PullImage "+domain.DebianBase) != pulls+1 {
		t.Fatalf("a rebuild must pull its bases and bypass the cache: %+v", got)
	}
	if after := h.planned(app); after.ID != before.ID || !after.Current {
		t.Fatalf("an unchanged rebuild replaced the current build: %+v", after)
	}
	builds, _ := store.ListRuntimeBuilds(t.Context(), h.c.Store.DB())
	if len(builds) != 1 {
		t.Fatalf("an unchanged rebuild must not be recorded: %d builds", len(builds))
	}
	if h.fake.CallCount("RemoveImage") == 0 {
		t.Fatal("the unchanged image must be removed")
	}
}

func TestRebuildKeepsAppsOnTheirBuildUntilRollout(t *testing.T) {
	h := newHarness(t)
	alpha, beta := h.runningApp("alpha"), h.runningApp("beta")
	old := h.planned(alpha)
	key := alpha.Runtime.ImageKey().String()
	creates := h.fake.CallCount("Create")
	h.fake.ImageFiles = map[string][]byte{runtime.ManifestPath: []byte("php=8.4.99\n")}
	h.rebuild(key)
	if h.fake.CallCount("Create") != creates {
		t.Fatal("a rebuild must not replace any instance")
	}
	if h.planned(alpha).ID != old.ID || h.planned(beta).ID != old.ID {
		t.Fatal("apps must stay on their build after a rebuild")
	}
	views, err := h.c.RuntimeImages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var v RuntimeImageView
	for _, x := range views {
		if x.Key.String() == key {
			v = x
		}
	}
	if v.Current == nil || v.Current.ID == old.ID || len(v.Apps) != 2 || !v.Behind(v.Apps[0]) ||
		len(v.Apps[0].Changes) != 1 || v.Apps[0].Changes[0] != "php none -> 8.4.99" {
		t.Fatalf("view %+v", v)
	}
	// A new app gets the current build.
	gamma := h.runningApp("gamma")
	if h.planned(gamma).ID != v.Current.ID {
		t.Fatal("a new app must start on the current build")
	}
	// Roll out alpha only: it is marked rolling until it is ready on the build.
	h.mustSucceed(h.c.RolloutImage(t.Context(), key, RolloutRequest{AppIDs: []string{alpha.ID}}, ""))
	a, _ := store.GetAppRuntimeBuild(t.Context(), h.c.Store.DB(), alpha.ID)
	if a.BuildID != v.Current.ID || a.PreviousBuildID != old.ID || !a.Rolling {
		t.Fatalf("alpha assignment %+v", a)
	}
	if h.planned(beta).ID != old.ID {
		t.Fatal("beta was not selected")
	}
	h.mustSucceed(h.c.RestartApp(t.Context(), alpha.ID, ""))
	if a, _ = store.GetAppRuntimeBuild(t.Context(), h.c.Store.DB(), alpha.ID); a.Rolling {
		t.Fatal("a ready instance confirms its rolled-out build")
	}
	// Roll back to the previous build.
	h.mustSucceed(h.c.RolloutImage(t.Context(), key, RolloutRequest{BuildID: old.ID, AppIDs: []string{alpha.ID}}, ""))
	if h.planned(alpha).ID != old.ID {
		t.Fatal("rollback did not assign the previous build")
	}
}

func TestRolloutStopRevertsUnconfirmedApps(t *testing.T) {
	h := newHarness(t)
	alpha, beta := h.runningApp("alpha"), h.runningApp("beta")
	old := h.planned(alpha)
	key := alpha.Runtime.ImageKey().String()
	h.fake.ImageFiles = map[string][]byte{runtime.ManifestPath: []byte("php=8.4.99\n")}
	h.rebuild(key)
	h.mustSucceed(h.c.RolloutImage(t.Context(), key, RolloutRequest{}, ""))
	if rolling, _ := h.c.HasRollingBuilds(t.Context(), key); !rolling {
		t.Fatal("rollout must mark its apps")
	}
	// alpha is confirmed on the new build; beta is not yet replaced.
	h.mustSucceed(h.c.RestartApp(t.Context(), alpha.ID, ""))
	op, _, err := h.c.Submit(t.Context(), Submission{Kind: KindImageRolloutStop, TargetKind: "image", TargetID: key})
	if err != nil {
		t.Fatal(err)
	}
	h.mustSucceed(op, nil)
	if h.planned(beta).ID != old.ID {
		t.Fatal("an unconfirmed app must go back to its previous build")
	}
	if h.planned(alpha).ID == old.ID {
		t.Fatal("a confirmed app keeps the rolled-out build")
	}
	if rolling, _ := h.c.HasRollingBuilds(t.Context(), key); rolling {
		t.Fatal("a stopped rollout leaves no rolling apps")
	}
}

func TestRolloutRefusesBadInput(t *testing.T) {
	h := newHarness(t)
	app := h.runningApp("shop")
	if _, err := h.c.RolloutImage(t.Context(), "php-9.9", RolloutRequest{}, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	other := "node-24"
	if app.Runtime.ImageKey().String() == other {
		other = "php-8.4"
	}
	if _, err := h.c.RolloutImage(t.Context(), other, RolloutRequest{AppIDs: []string{app.ID}}, ""); err == nil {
		t.Fatal("an app of another runtime must be refused")
	}
	if _, err := h.c.RolloutImage(t.Context(), app.Runtime.ImageKey().String(), RolloutRequest{BuildID: 999}, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown build: %v", err)
	}
	if _, err := h.c.RebuildImage(t.Context(), "../php", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown rebuild key: %v", err)
	}
	h.fake.FailOn = map[string]error{"BuildImage": errors.New("injected")}
	op, err := h.c.RebuildImage(t.Context(), app.Runtime.ImageKey().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "image" {
		t.Fatalf("failed rebuild: %s %s", got.State, got.ErrorCode)
	}
	if h.planned(app).Current != true {
		t.Fatal("a failed rebuild keeps the current build")
	}
}

func TestManifestChanges(t *testing.T) {
	got := ManifestChanges(map[string]string{"php": "8.4.1", "node": "24.1.0", "gone": "1"},
		map[string]string{"php": "8.4.2", "node": "24.1.0", "new": "2"})
	want := []string{"gone 1 -> none", "new none -> 2", "php 8.4.1 -> 8.4.2"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}
