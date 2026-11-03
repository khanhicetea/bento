package store

import (
	"errors"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestRuntimeBuildAssignments(t *testing.T) {
	ctx := t.Context()
	s := newStore(t)
	if err := s.Tx(ctx, func(q Q) error {
		uid, err := AllocateUID(ctx, q, domain.UIDRange{First: 20000, Last: 29999}, nil, "a1", "shop")
		if err != nil {
			return err
		}
		return InsertApp(ctx, q, domain.App{ID: "a1", Slug: "shop", UID: uid, GID: uid,
			DesiredRuntime: domain.DesiredRunning, Ingress: domain.IngressNone, Publication: domain.Unpublished,
			CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	build := func(tag, image string) int64 {
		id, err := InsertCurrentRuntimeBuild(ctx, s.DB(), RuntimeBuild{ImageKey: "php-8.4", ContextHash: "h", Tag: tag,
			ImageID: image, Manifest: map[string]string{"php": tag}, BuiltAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	b1 := build("t1", "sha256:1")
	if _, err := InsertCurrentRuntimeBuild(ctx, s.DB(), RuntimeBuild{ImageKey: "php-8.4", ContextHash: "h", Tag: "t1",
		ImageID: "x", BuiltAt: time.Now()}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate tag: %v", err)
	}
	if ok, err := ReplaceAppRuntimeBuild(ctx, s.DB(), "a1", 0, b1); err != nil || !ok {
		t.Fatalf("first assignment: %v %v", ok, err)
	}
	b2 := build("t2", "sha256:2")
	if cur, _ := CurrentRuntimeBuild(ctx, s.DB(), "php-8.4", "h"); cur.ID != b2 {
		t.Fatal("the newest build must be current")
	}
	if old, _ := GetRuntimeBuild(ctx, s.DB(), b1); old.Current {
		t.Fatal("only one build is current")
	}
	// A rollout assigns b2; a stale writer that saw b1 must not undo it.
	if err := AssignAppRuntimeBuild(ctx, s.DB(), "a1", b2, true); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ReplaceAppRuntimeBuild(ctx, s.DB(), "a1", b1, b1); ok {
		t.Fatal("a stale compare-and-set wrote")
	}
	a, _ := GetAppRuntimeBuild(ctx, s.DB(), "a1")
	if a.BuildID != b2 || a.PreviousBuildID != b1 || !a.Rolling {
		t.Fatalf("after rollout %+v", a)
	}
	if rolling, _ := HasRollingRuntimeBuilds(ctx, s.DB(), "php-8.4"); !rolling {
		t.Fatal("rolling not reported")
	}
	if done, _ := ConfirmAppRuntimeBuild(ctx, s.DB(), "a1", "sha256:1"); done {
		t.Fatal("confirmed on the wrong image")
	}
	ids, err := RevertRollingRuntimeBuilds(ctx, s.DB(), "php-8.4")
	if err != nil || len(ids) != 1 {
		t.Fatalf("revert %v %v", ids, err)
	}
	if a, _ = GetAppRuntimeBuild(ctx, s.DB(), "a1"); a.BuildID != b1 || a.PreviousBuildID != b2 || a.Rolling {
		t.Fatalf("after revert %+v", a)
	}
	_ = AssignAppRuntimeBuild(ctx, s.DB(), "a1", b2, true)
	if done, _ := ConfirmAppRuntimeBuild(ctx, s.DB(), "a1", "sha256:2"); !done {
		t.Fatal("not confirmed on its build's image")
	}
	if ids, _ := RevertRollingRuntimeBuilds(ctx, s.DB(), "php-8.4"); len(ids) != 0 {
		t.Fatal("a confirmed app must not be reverted")
	}
	// Pruning the image of the assigned build drops the assignment; the
	// previous one is cleared when its build goes.
	if err := DeleteRuntimeBuildsByImage(ctx, s.DB(), "sha256:1"); err != nil {
		t.Fatal(err)
	}
	if a, _ = GetAppRuntimeBuild(ctx, s.DB(), "a1"); a.BuildID != b2 || a.PreviousBuildID != 0 {
		t.Fatalf("after pruning the previous build %+v", a)
	}
	if err := DeleteRuntimeBuildsByImage(ctx, s.DB(), "sha256:2"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetAppRuntimeBuild(ctx, s.DB(), "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assignment to a pruned build: %v", err)
	}
}
