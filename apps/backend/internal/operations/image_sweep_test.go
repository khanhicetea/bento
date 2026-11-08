package operations

import (
	"slices"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func (h *harness) rollTo(appID, key string, buildID int64) {
	h.t.Helper()
	h.mustSucceed(h.c.RolloutImage(h.t.Context(), key, RolloutRequest{BuildID: buildID, AppIDs: []string{appID}}, ""))
	h.mustSucceed(h.c.RestartApp(h.t.Context(), appID, ""))
}

func (h *harness) current(key string) store.RuntimeBuild {
	h.t.Helper()
	views, err := h.c.RuntimeImages(h.t.Context())
	if err != nil {
		h.t.Fatal(err)
	}
	for _, v := range views {
		if v.Key.String() == key && v.Current != nil {
			return *v.Current
		}
	}
	h.t.Fatalf("no current build of %s", key)
	return store.RuntimeBuild{}
}

func (h *harness) released() map[int64]time.Time {
	h.t.Helper()
	builds, err := store.ListRuntimeBuilds(h.t.Context(), h.c.Store.DB())
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[int64]time.Time{}
	for _, b := range builds {
		out[b.ID] = b.ReleasedAt
	}
	return out
}

func (h *harness) mark() []string {
	h.t.Helper()
	due, err := h.c.MarkReleasedBuilds(h.t.Context())
	if err != nil {
		h.t.Fatal(err)
	}
	return due
}

// A superseded build is removed only after it has stayed unusable for the
// whole grace period: the current build, an app's build and its roll-back
// build are kept, and a build used again restarts the clock.
func TestSweepRemovesBuildsReleasedForTheGracePeriod(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	clock := &testClock{now: t0}
	h := newHarnessWith(t, func(d *Deps) { d.Now = clock.Now })
	alpha := h.runningApp("alpha")
	key := alpha.Runtime.ImageKey().String()
	b1 := h.planned(alpha)
	h.fake.ImageFiles = map[string][]byte{runtime.ManifestPath: []byte("php=8.4.98\n")}
	h.rebuild(key)
	b2 := h.current(key)
	h.rollTo(alpha.ID, key, b2.ID)
	h.fake.ImageFiles = map[string][]byte{runtime.ManifestPath: []byte("php=8.4.99\n")}
	h.rebuild(key)
	b3 := h.current(key)
	h.rollTo(alpha.ID, key, b3.ID) // alpha on b3, roll-back b2; b1 is used by nothing

	if due := h.mark(); len(due) != 0 {
		t.Fatalf("due at once: %v", due)
	}
	if r := h.released(); !r[b1.ID].Equal(t0) || !r[b2.ID].IsZero() || !r[b3.ID].IsZero() {
		t.Fatalf("released %v", r)
	}
	removals, err := BuildRemovals(t.Context(), h.c.Store.DB())
	if err != nil || !removals[b1.ImageID].Equal(t0.Add(BuildReleaseGrace)) || len(removals) != 1 {
		t.Fatalf("removals %v %v", removals, err)
	}

	// Rolling back to b1 uses it again; b2 is now unused.
	h.rollTo(alpha.ID, key, b1.ID)
	clock.Add(time.Hour)
	h.mark()
	if r := h.released(); !r[b1.ID].IsZero() || !r[b2.ID].Equal(t0.Add(time.Hour)) {
		t.Fatalf("after roll back: %v", r)
	}
	h.rollTo(alpha.ID, key, b3.ID) // roll-back build b1 again; b2 stays released
	clock.Add(time.Hour)
	h.mark()
	clock.Add(11*time.Hour - time.Minute) // b2 released 11h59m ago
	if due := h.mark(); len(due) != 0 {
		t.Fatalf("due before the grace period: %v", due)
	}
	clock.Add(time.Minute)
	due := h.mark()
	if !slices.Equal(due, []string{key}) {
		t.Fatalf("due %v", due)
	}
	op, _, err := h.c.Submit(t.Context(), Submission{Kind: KindImageSweep, TargetKind: "image", TargetID: key})
	h.mustSucceed(op, err)
	if _, ok, _ := h.fake.ImageID(t.Context(), b2.Tag); ok {
		t.Fatal("the released build was not removed")
	}
	for _, b := range []store.RuntimeBuild{b1, b3} {
		if _, ok, _ := h.fake.ImageID(t.Context(), b.Tag); !ok {
			t.Fatalf("build %s removed", b.Tag)
		}
	}
	if _, err := store.GetRuntimeBuild(t.Context(), h.c.Store.DB(), b2.ID); err == nil {
		t.Fatal("the removed build is still recorded")
	}

	// A Bento upgrade changes the inputs: no build can serve any more, so the
	// roll-back build is released, and the one alpha's container runs is not.
	if _, err := h.c.Store.DB().ExecContext(t.Context(), "UPDATE runtime_builds SET context_hash = 'old'"); err != nil {
		t.Fatal(err)
	}
	h.mark()
	if r := h.released(); r[b1.ID].IsZero() || !r[b3.ID].IsZero() {
		t.Fatalf("after an upgrade: %v", r)
	}
}
