package operations

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Runtime image builds.
//
// A runtime key (php-8.4, node-24) has any number of builds of its current
// Bento-owned inputs (runtime.ImageSpec.Hash). Their bases are floating tags
// and some tools follow release channels, so each build records what it
// resolved. One build per key is current: new apps get it, and an app whose
// assigned build no longer matches the inputs (a Bento upgrade) moves to it on
// its next replacement, which the reconciler rolls out automatically.
//
// An operator rebuild (image.rebuild) builds a fresh image without the layer
// cache. If it resolved exactly what the current build has, it is discarded;
// otherwise it becomes current, and apps stay on their build until the
// operator rolls them out (image.rollout). The reconciler then replaces them
// one at a time; when a replacement fails, image.rollout-stop puts every app
// not yet confirmed on the new build back on the build it ran before.
const (
	KindImageRebuild     = "image.rebuild"
	KindImageRollout     = "image.rollout"
	KindImageRolloutStop = "image.rollout-stop"
)

// RolloutRequest selects what an image.rollout assigns. BuildID 0 is the
// current build of the key when the operation runs; no AppIDs is every
// provisioned app of the key that is not on that build.
type RolloutRequest struct {
	BuildID int64    `json:"buildId,omitempty"`
	AppIDs  []string `json:"appIds,omitempty"`
}

func parseRuntimeKey(id string) (domain.ImageKey, error) {
	key, ok := domain.ParseImageKey(id)
	if !ok {
		return key, Fail("image-key", "The runtime is no longer supported; update the apps that use it.",
			"unknown runtime image key %q", id)
	}
	return key, nil
}

// usableBuild reports whether b can serve spec: the same key and inputs, and
// its image still present under its tag.
func (c *Controller) usableBuild(ctx context.Context, b store.RuntimeBuild, spec runtime.ImageSpec) (bool, error) {
	if b.ImageKey != spec.Key.String() || b.ContextHash != spec.Hash {
		return false, nil
	}
	id, ok, err := c.Engine.ImageID(ctx, b.Tag)
	return err == nil && ok && id == b.ImageID, err
}

// currentBuild returns the usable current build of spec, if any.
func (c *Controller) currentBuild(ctx context.Context, spec runtime.ImageSpec) (store.RuntimeBuild, bool, error) {
	cur, err := store.CurrentRuntimeBuild(ctx, c.Store.DB(), spec.Key.String(), spec.Hash)
	if errors.Is(err, store.ErrNotFound) {
		return cur, false, nil
	}
	if err != nil {
		return cur, false, err
	}
	ok, err := c.usableBuild(ctx, cur, spec)
	return cur, ok, err
}

// plannedBuild resolves the build app's instance should run, without building
// or writing anything: its assigned build when usable, otherwise the current
// build of its runtime. ok is false when neither exists.
func (c *Controller) plannedBuild(ctx context.Context, app domain.App) (store.RuntimeBuild, runtime.ImageSpec, bool, error) {
	spec, err := runtime.PlanImage(app.Runtime.ImageKey())
	if err != nil {
		return store.RuntimeBuild{}, spec, false, err
	}
	a, err := store.GetAppRuntimeBuild(ctx, c.Store.DB(), app.ID)
	switch {
	case err == nil:
		b, err := store.GetRuntimeBuild(ctx, c.Store.DB(), a.BuildID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return b, spec, false, err
		}
		if err == nil {
			ok, err := c.usableBuild(ctx, b, spec)
			if err != nil || ok {
				return b, spec, ok, err
			}
		}
	case !errors.Is(err, store.ErrNotFound):
		return store.RuntimeBuild{}, spec, false, err
	}
	b, ok, err := c.currentBuild(ctx, spec)
	return b, spec, ok, err
}

// ensureAppBuild returns the build app's instance runs, building its
// runtime's current build when none is usable, and records the assignment.
// The assignment is replaced only if no rollout changed it meanwhile; if one
// did, the instance is created on b and the reconciler replaces it onto the
// rollout's build.
func (c *Controller) ensureAppBuild(ctx context.Context, app domain.App, progress func(string)) (store.RuntimeBuild, error) {
	var seen int64
	switch a, err := store.GetAppRuntimeBuild(ctx, c.Store.DB(), app.ID); {
	case err == nil:
		seen = a.BuildID
	case !errors.Is(err, store.ErrNotFound):
		return store.RuntimeBuild{}, err
	}
	b, spec, ok, err := c.plannedBuild(ctx, app)
	if err != nil {
		return b, err
	}
	if !ok {
		if b, err = c.ensureCurrentBuild(ctx, spec, progress); err != nil {
			return b, err
		}
	}
	if seen != b.ID {
		if _, err := store.ReplaceAppRuntimeBuild(ctx, c.Store.DB(), app.ID, seen, b.ID); err != nil {
			return b, err
		}
	}
	return b, nil
}

// ensureCurrentBuild returns the current build of spec, building it if it is
// missing. Callers needing the same key wait for one build.
func (c *Controller) ensureCurrentBuild(
	ctx context.Context,
	spec runtime.ImageSpec,
	progress func(string),
) (store.RuntimeBuild, error) {
	release := c.Images.Hold(spec.Key)
	defer release()
	if cur, ok, err := c.currentBuild(ctx, spec); err != nil || ok {
		return cur, err
	}
	b, err := c.Images.Build(ctx, spec, time.Now(), false, progress)
	if err != nil {
		return store.RuntimeBuild{}, err
	}
	return c.recordBuild(ctx, spec, b)
}

// buildFailure turns a managed image build error into the operation's
// failure; a build refused for lack of memory gets its own code and guidance.
func buildFailure(err error, guidance string) error {
	if errors.Is(err, runtime.ErrBuildMemory) {
		return Fail("build-memory",
			"Free memory on the host (stop apps or services you do not need) or add memory or swap. Apps keep their current build; Bento retries with backoff, and Images → Rebuild retries now.",
			"managed image: %v", err)
	}
	return Fail("image", guidance, "managed image: %v", err)
}

func (c *Controller) recordBuild(ctx context.Context, spec runtime.ImageSpec, b runtime.Build) (store.RuntimeBuild, error) {
	rb := store.RuntimeBuild{ImageKey: spec.Key.String(), ContextHash: spec.Hash, Tag: b.Tag, ImageID: b.ID,
		Manifest: b.Manifest, BuiltAt: time.Now().UTC(), Current: true}
	err := c.Store.Tx(ctx, func(q store.Q) error {
		id, err := store.InsertCurrentRuntimeBuild(ctx, q, rb)
		rb.ID = id
		return err
	})
	return rb, err
}

// confirmAppBuild clears a rollout's mark once app is ready on its assigned
// build. A failure only delays it: the mark is cleared by the next readiness.
func (c *Controller) confirmAppBuild(ctx context.Context, r *Run, app domain.App) {
	obs, err := c.observe(ctx, app)
	if err == nil && obs.Image != "" {
		var done bool
		if done, err = store.ConfirmAppRuntimeBuild(ctx, c.Store.DB(), app.ID, obs.Image); done {
			r.Info(ctx, "instance confirmed on its rolled-out runtime build")
		}
	}
	if err != nil {
		r.Warn(ctx, "record runtime build rollout: %v", err)
	}
}

// ManifestChanges lists what differs between two build manifests, as
// "name from -> to" lines in name order.
func ManifestChanges(from, to map[string]string) []string {
	all := maps.Clone(from)
	maps.Copy(all, to)
	names := slices.Sorted(maps.Keys(all))
	out := []string{}
	for _, n := range names {
		a, b := from[n], to[n]
		if a == b {
			continue
		}
		if a == "" {
			a = "none"
		}
		if b == "" {
			b = "none"
		}
		out = append(out, fmt.Sprintf("%s %s -> %s", n, a, b))
	}
	return out
}

// RebuildImage accepts an operator rebuild of one runtime key.
func (c *Controller) RebuildImage(ctx context.Context, keyID, idem string) (store.Operation, error) {
	if _, ok := domain.ParseImageKey(keyID); !ok {
		return store.Operation{}, fmt.Errorf("%w: unknown runtime %q", store.ErrNotFound, keyID)
	}
	op, _, err := c.Submit(ctx, Submission{Kind: KindImageRebuild, TargetKind: "image", TargetID: keyID,
		IdempotencyKey: idem})
	return op, err
}

// RolloutImage accepts moving apps of one runtime key to a build of it: the
// current build (roll out) or an earlier one (roll back).
func (c *Controller) RolloutImage(ctx context.Context, keyID string, req RolloutRequest, idem string) (store.Operation, error) {
	if _, ok := domain.ParseImageKey(keyID); !ok {
		return store.Operation{}, fmt.Errorf("%w: unknown runtime %q", store.ErrNotFound, keyID)
	}
	if req.BuildID != 0 {
		b, err := store.GetRuntimeBuild(ctx, c.Store.DB(), req.BuildID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && b.ImageKey != keyID) {
			return store.Operation{}, fmt.Errorf("%w: no build %d of runtime %s", store.ErrNotFound, req.BuildID, keyID)
		}
		if err != nil {
			return store.Operation{}, err
		}
	}
	for _, id := range req.AppIDs {
		app, err := store.GetApp(ctx, c.Store.DB(), id)
		if err != nil {
			return store.Operation{}, err
		}
		if app.Runtime.ImageKey().String() != keyID {
			return store.Operation{}, Fail("runtime-mismatch", "Roll out apps on this runtime only.",
				"app %s runs %s, not %s", app.Slug, app.Runtime.ImageKey(), keyID)
		}
	}
	op, _, err := c.Submit(ctx, Submission{Kind: KindImageRollout, TargetKind: "image", TargetID: keyID,
		Request: req, IdempotencyKey: idem})
	return op, err
}

func (c *Controller) handleImageRebuild(ctx context.Context, r *Run) (any, error) {
	key, err := parseRuntimeKey(r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	spec, err := runtime.PlanImage(key)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "build-image"); err != nil {
		return nil, err
	}
	release := c.Images.Hold(key)
	defer release()
	cur, hasCur, err := c.currentBuild(ctx, spec)
	if err != nil {
		return nil, err
	}
	b, err := c.Images.Build(ctx, spec, time.Now(), true, func(s string) { r.Info(ctx, "%s", s) })
	if err != nil {
		return nil, buildFailure(err, "Check Docker connectivity and build output; apps keep their current build.")
	}
	if hasCur && maps.Equal(cur.Manifest, b.Manifest) {
		if err := r.Phase(ctx, "discard-image"); err != nil {
			return nil, err
		}
		// Built by this operation and used by nothing.
		if err := c.Engine.RemoveImage(context.WithoutCancel(ctx), b.ID, []string{b.Tag}); err != nil {
			r.Warn(ctx, "remove unchanged build %s: %v", b.Tag, err)
		}
		r.Info(ctx, "up to date: the rebuild resolved the same bases and versions as %s", cur.Tag)
		return map[string]any{"key": key.String(), "changed": false, "current": cur.Tag}, nil
	}
	if err := r.Phase(ctx, "record-build"); err != nil {
		return nil, err
	}
	rb, err := c.recordBuild(context.WithoutCancel(ctx), spec, b)
	if err != nil {
		return nil, err
	}
	changes := []string{}
	if hasCur {
		changes = ManifestChanges(cur.Manifest, rb.Manifest)
		for _, ch := range changes {
			r.Info(ctx, "changed: %s", ch)
		}
	}
	behind, err := c.appsBehind(ctx, key, rb.ID)
	if err != nil {
		return nil, err
	}
	if len(behind) > 0 {
		r.Info(ctx, "%d app(s) run an older build; roll them out from System > Images", len(behind))
	}
	return map[string]any{"key": key.String(), "changed": true, "tag": rb.Tag, "buildId": rb.ID,
		"changes": changes, "behind": len(behind)}, nil
}

// appsBehind lists the provisioned apps of key whose planned build is not
// buildID.
func (c *Controller) appsBehind(ctx context.Context, key domain.ImageKey, buildID int64) ([]domain.App, error) {
	apps, err := store.ListApps(ctx, c.Store.DB())
	if err != nil {
		return nil, err
	}
	var out []domain.App
	for _, app := range apps {
		if !app.Provisioned || app.Runtime.ImageKey() != key {
			continue
		}
		b, _, ok, err := c.plannedBuild(ctx, app)
		if err != nil {
			return nil, err
		}
		if !ok || b.ID != buildID {
			out = append(out, app)
		}
	}
	return out, nil
}

func (c *Controller) handleImageRollout(ctx context.Context, r *Run) (any, error) {
	key, err := parseRuntimeKey(r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	var req RolloutRequest
	if len(r.Op.Request) > 0 {
		if err := r.Decode(&req); err != nil {
			return nil, fmt.Errorf("decode rollout request: %w", err)
		}
	}
	spec, err := runtime.PlanImage(key)
	if err != nil {
		return nil, err
	}
	var target store.RuntimeBuild
	if req.BuildID == 0 {
		var ok bool
		if target, ok, err = c.currentBuild(ctx, spec); err != nil {
			return nil, err
		} else if !ok {
			return nil, Fail("build-missing", "Rebuild the runtime image first.", "runtime %s has no current build", key)
		}
	} else {
		if target, err = store.GetRuntimeBuild(ctx, c.Store.DB(), req.BuildID); err != nil {
			return nil, Fail("build-missing", "The build was pruned; roll out the current build instead.",
				"build %d: %v", req.BuildID, err)
		}
		if ok, err := c.usableBuild(ctx, target, spec); err != nil {
			return nil, err
		} else if !ok {
			return nil, Fail("build-missing", "The build's image is gone or predates this Bento release.",
				"build %s cannot serve runtime %s", target.Tag, key)
		}
	}
	if err := r.Phase(ctx, "assign"); err != nil {
		return nil, err
	}
	behind, err := c.appsBehind(ctx, key, target.ID)
	if err != nil {
		return nil, err
	}
	var rolling, assigned []string
	err = c.Store.Tx(ctx, func(q store.Q) error {
		for _, app := range behind {
			if len(req.AppIDs) > 0 && !slices.Contains(req.AppIDs, app.ID) {
				continue
			}
			// A running instance is replaced by the reconciler, which a failed
			// replacement stops; anything else simply starts on the build.
			run := app.DesiredRuntime == domain.DesiredRunning
			if err := store.AssignAppRuntimeBuild(ctx, q, app.ID, target.ID, run); err != nil {
				return err
			}
			assigned = append(assigned, app.Slug)
			if run {
				rolling = append(rolling, app.Slug)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(assigned) == 0 {
		r.Info(ctx, "every selected app is already on %s", target.Tag)
	} else {
		r.Info(ctx, "assigned %s to %s", target.Tag, strings.Join(assigned, ", "))
	}
	if len(rolling) > 0 {
		r.Info(ctx, "the reconciler replaces %d running app(s) one at a time and stops at the first failure",
			len(rolling))
	}
	if c.WakeReconciler != nil {
		c.WakeReconciler()
	}
	return map[string]any{"key": key.String(), "tag": target.Tag, "buildId": target.ID,
		"assigned": nonNilStrings(assigned), "rolling": nonNilStrings(rolling)}, nil
}

// handleImageRolloutStop stops a rollout after a failed replacement: every
// app of the key not yet confirmed on its rolled-out build goes back to the
// build it ran before. The reconciler submits it.
func (c *Controller) handleImageRolloutStop(ctx context.Context, r *Run) (any, error) {
	key, err := parseRuntimeKey(r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "revert"); err != nil {
		return nil, err
	}
	var ids []string
	err = c.Store.Tx(ctx, func(q store.Q) error {
		ids, err = store.RevertRollingRuntimeBuilds(ctx, q, key.String())
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		r.Warn(ctx, "rollout on %s stopped after a failed replacement; %d app(s) go back to their previous build",
			key, len(ids))
	}
	if c.WakeReconciler != nil {
		c.WakeReconciler()
	}
	return map[string]any{"key": key.String(), "reverted": nonNilStrings(ids)}, nil
}

// HasRollingBuilds reports whether a rollout on key has unconfirmed apps.
func (c *Controller) HasRollingBuilds(ctx context.Context, key string) (bool, error) {
	return store.HasRollingRuntimeBuilds(ctx, c.Store.DB(), key)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// RuntimeImageView is what the operator sees of one runtime key.
type RuntimeImageView struct {
	Key domain.ImageKey
	// Current is the current build of the key's inputs (nil: none usable).
	Current *store.RuntimeBuild
	// Builds are the usable builds of the key's inputs, newest first.
	Builds []store.RuntimeBuild
	Apps   []RuntimeImageApp
}

// RuntimeImageApp is one provisioned app of a runtime key.
type RuntimeImageApp struct {
	App domain.App
	// BuildID is the build the app is planned on (0: none built yet).
	BuildID         int64
	PreviousBuildID int64
	Rolling         bool
	// Changes lists what rolling out the current build changes, when the app
	// is on another build.
	Changes []string
}

// Behind reports whether the app is planned on another build than current.
func (v RuntimeImageView) Behind(a RuntimeImageApp) bool {
	return v.Current != nil && a.BuildID != v.Current.ID
}

// RuntimeImages lists the runtime keys in use by provisioned apps or with a
// usable build, in key order.
func (c *Controller) RuntimeImages(ctx context.Context) ([]RuntimeImageView, error) {
	db := c.Store.DB()
	apps, err := store.ListApps(ctx, db)
	if err != nil {
		return nil, err
	}
	builds, err := store.ListRuntimeBuilds(ctx, db)
	if err != nil {
		return nil, err
	}
	assigned, err := store.ListAppRuntimeBuilds(ctx, db)
	if err != nil {
		return nil, err
	}
	byApp := map[string]store.AppRuntimeBuild{}
	for _, a := range assigned {
		byApp[a.AppID] = a
	}
	views := map[string]*RuntimeImageView{}
	specs := map[string]runtime.ImageSpec{}
	view := func(key domain.ImageKey) (*RuntimeImageView, error) {
		if v, ok := views[key.String()]; ok {
			return v, nil
		}
		spec, err := runtime.PlanImage(key)
		if err != nil {
			return nil, err
		}
		v := &RuntimeImageView{Key: key, Builds: []store.RuntimeBuild{}, Apps: []RuntimeImageApp{}}
		views[key.String()], specs[key.String()] = v, spec
		return v, nil
	}
	for _, b := range builds {
		key, ok := domain.ParseImageKey(b.ImageKey)
		if !ok {
			continue
		}
		v, err := view(key)
		if err != nil {
			return nil, err
		}
		ok, err = c.usableBuild(ctx, b, specs[b.ImageKey])
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		v.Builds = append(v.Builds, b)
		if b.Current {
			cur := b
			v.Current = &cur
		}
	}
	for _, app := range apps {
		if !app.Provisioned {
			continue
		}
		v, err := view(app.Runtime.ImageKey())
		if err != nil {
			continue // an unsupported runtime is reported by the app itself
		}
		b, _, ok, err := c.plannedBuild(ctx, app)
		if err != nil {
			return nil, err
		}
		ra := RuntimeImageApp{App: app, Changes: []string{}}
		if ok {
			ra.BuildID = b.ID
		}
		if a, has := byApp[app.ID]; has && a.BuildID == ra.BuildID {
			ra.PreviousBuildID, ra.Rolling = a.PreviousBuildID, a.Rolling
		}
		if v.Behind(ra) && ok {
			ra.Changes = ManifestChanges(b.Manifest, v.Current.Manifest)
		}
		v.Apps = append(v.Apps, ra)
	}
	out := make([]RuntimeImageView, 0, len(views))
	for _, k := range slices.Sorted(maps.Keys(views)) {
		out = append(out, *views[k])
	}
	return out, nil
}
