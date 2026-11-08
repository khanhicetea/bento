package operations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Superseded runtime builds.
//
// Every Bento upgrade that changes a runtime's inputs, and every operator
// rebuild that changes what it resolved, adds a build of about 1 GB. A build
// is released once nothing can use it any more: no container (running or
// stopped) references its image, and it is neither the current build of its
// runtime nor an app's assigned or roll-back build that still matches this
// Bento's inputs. The reconciler marks released builds (released_at) and,
// once one has stayed released for BuildReleaseGrace, submits image.sweep for
// its runtime, which re-checks and removes the image. A build that is used
// again in the meantime (a roll back, a new container) is unmarked. The grace
// leaves the operator time to roll back or prune by hand; the Docker inventory
// shows when each released build goes.
const KindImageSweep = "image.sweep"

// BuildReleaseGrace is how long a released build is kept before image.sweep
// removes it.
const BuildReleaseGrace = 12 * time.Hour

// releasedBuilds reports, for every recorded build, whether nothing can use
// it any more (see KindImageSweep). Builds sharing an image are released only
// together.
func (c *Controller) releasedBuilds(ctx context.Context) ([]store.RuntimeBuild, map[int64]bool, error) {
	db := c.Store.DB()
	builds, err := store.ListRuntimeBuilds(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	assigned, err := store.ListAppRuntimeBuilds(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	containers, err := c.Engine.List(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	refs := map[int64]bool{}
	for _, a := range assigned {
		refs[a.BuildID] = true
		if a.PreviousBuildID != 0 {
			refs[a.PreviousBuildID] = true
		}
	}
	used := map[string]bool{}
	for _, ct := range containers {
		used[ct.ImageID] = true
	}
	hashes := map[string]string{} // image key -> this Bento's inputs; "" when unsupported
	for _, b := range builds {
		if _, ok := hashes[b.ImageKey]; ok {
			continue
		}
		if key, ok := domain.ParseImageKey(b.ImageKey); ok {
			if spec, err := runtime.PlanImage(key); err == nil {
				hashes[b.ImageKey] = spec.Hash
				continue
			}
		}
		hashes[b.ImageKey] = ""
	}
	for _, b := range builds {
		servable := hashes[b.ImageKey] != "" && b.ContextHash == hashes[b.ImageKey]
		if servable && (b.Current || refs[b.ID]) {
			used[b.ImageID] = true
		}
	}
	released := map[int64]bool{}
	for _, b := range builds {
		released[b.ID] = !used[b.ImageID]
	}
	return builds, released, nil
}

// MarkReleasedBuilds records when each build was first seen released and
// unmarks builds in use again. It returns the runtime keys with a build
// released for at least BuildReleaseGrace, in key order.
func (c *Controller) MarkReleasedBuilds(ctx context.Context) ([]string, error) {
	builds, released, err := c.releasedBuilds(ctx)
	if err != nil {
		return nil, err
	}
	now := c.Now()
	var due []string
	for _, b := range builds {
		switch {
		case released[b.ID] && b.ReleasedAt.IsZero():
			err = store.SetRuntimeBuildReleased(ctx, c.Store.DB(), b.ID, now)
		case !released[b.ID] && !b.ReleasedAt.IsZero():
			err = store.SetRuntimeBuildReleased(ctx, c.Store.DB(), b.ID, time.Time{})
		case released[b.ID] && now.Sub(b.ReleasedAt) >= BuildReleaseGrace && !slices.Contains(due, b.ImageKey):
			due = append(due, b.ImageKey)
		}
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(due)
	return due, nil
}

// BuildRemovals maps the image id of each released build to when image.sweep
// may remove it.
func BuildRemovals(ctx context.Context, q store.Q) (map[string]time.Time, error) {
	builds, err := store.ListRuntimeBuilds(ctx, q)
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for _, b := range builds {
		if b.ReleasedAt.IsZero() {
			continue
		}
		if at := b.ReleasedAt.Add(BuildReleaseGrace); at.After(out[b.ImageID]) {
			out[b.ImageID] = at
		}
	}
	return out, nil
}

// handleImageSweep removes the builds of one runtime key that have stayed
// released for BuildReleaseGrace. The target id is the key as recorded, which
// may be a runtime this Bento no longer supports.
func (c *Controller) handleImageSweep(ctx context.Context, r *Run) (any, error) {
	key := r.Op.TargetID
	if err := r.Phase(ctx, "remove-builds"); err != nil {
		return nil, err
	}
	builds, released, err := c.releasedBuilds(ctx)
	if err != nil {
		return nil, err
	}
	now := c.Now()
	due := map[string][]string{} // image id -> tags
	var order []string
	for _, b := range builds {
		if b.ImageKey != key || !released[b.ID] || b.ReleasedAt.IsZero() || now.Sub(b.ReleasedAt) < BuildReleaseGrace {
			continue
		}
		if _, ok := due[b.ImageID]; !ok {
			order = append(order, b.ImageID)
		}
		due[b.ImageID] = append(due[b.ImageID], b.Tag)
	}
	removed := []string{}
	for _, id := range order {
		img, err := c.Images.Remove(ctx, id)
		switch {
		case errors.Is(err, runtime.ErrImageNotPrunable) && img.ID == "":
			r.Info(ctx, "build %s was already removed", strings.Join(due[id], ", "))
		case errors.Is(err, runtime.ErrImageNotPrunable):
			r.Warn(ctx, "kept %s: it is in use or not built by this stack", strings.Join(due[id], ", "))
			continue
		case err != nil:
			return nil, fmt.Errorf("remove build %s: %w", strings.Join(due[id], ", "), err)
		default:
			r.Info(ctx, "removed superseded build %s", strings.Join(due[id], ", "))
		}
		if err := store.DeleteRuntimeBuildsByImage(context.WithoutCancel(ctx), c.Store.DB(), id); err != nil {
			return nil, err
		}
		removed = append(removed, due[id]...)
	}
	if len(order) == 0 {
		r.Info(ctx, "no build of %s is due for removal", key)
	}
	return map[string]any{"key": key, "removed": removed}, nil
}
