package operations

import (
	"context"
	"errors"
	"slices"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// claims describes what an operation needs exclusive or shared access to while
// it runs. The executor starts an operation only when its claims do not
// conflict with any running operation or with any earlier queued operation
// that has not started yet, so operations that touch the same resources keep
// their FIFO order and a waiting exclusive operation is never starved.
//
// An operation kind is parallel-safe only when it appears in claimsFor. Every
// other kind, and any kind added later, claims everything: it runs alone.
type claims struct {
	global bool
	excl   []string
	shared []string
	// pool names a bounded slot pool: at most poolLimits[pool] operations of
	// the pool run at once, even when their other claims are disjoint.
	pool string
}

// imageBuildPool bounds image.prepare operations to the runtime's own build
// limit, so a waiting prepare never occupies an executor slot.
const imageBuildPool = "image-build"

var poolLimits = map[string]int{imageBuildPool: runtime.MaxConcurrentBuilds, resticPool: 2}

// globalClaims runs alone: nothing starts before it finishes and it starts
// only when nothing else is running.
var globalClaims = claims{global: true}

func appClaim(id string) string       { return "app:" + id }
func serviceClaim(name string) string { return "service:" + name }
func imageClaim(key string) string    { return "image:" + key }

// conflicts reports whether a and b must not run at the same time. Shared
// claims coexist; an exclusive claim conflicts with any claim on the resource.
func (a claims) conflicts(b claims) bool {
	if a.global || b.global {
		return true
	}
	for _, k := range a.excl {
		if slices.Contains(b.excl, k) || slices.Contains(b.shared, k) {
			return true
		}
	}
	for _, k := range a.shared {
		if slices.Contains(b.excl, k) {
			return true
		}
	}
	return false
}

// serviceDeps lists the data services an app's instance connects to at boot.
func serviceDeps(app domain.App) []string {
	var out []string
	for _, b := range app.Bindings {
		if b.Engine != domain.EngineSQLite && !slices.Contains(out, serviceClaim(b.Service)) {
			out = append(out, serviceClaim(b.Service))
		}
	}
	if app.Redis.Username != "" && !slices.Contains(out, serviceClaim("redis")) {
		out = append(out, serviceClaim("redis"))
	}
	return out
}

// claimsFor classifies op, reading the app it targets from the store.
func (c *Controller) claimsFor(ctx context.Context, op store.Operation) claims {
	return classify(op, func(id string) (domain.App, error) { return store.GetApp(ctx, c.Store.DB(), id) })
}

// classify maps an operation to its claims. Parallel kinds:
//
//   - app.reconcile|start|restart|update|deploy|stop hold their app
//     exclusively. The shared resources they touch (image builds, network
//     planning, edge generations, the Redis ACL) are serialized by locks
//     inside the handlers. All but stop also share every data service the
//     app is bound to, so an app never boots, migrates or reloads while an
//     earlier queued operation is still bringing its database up. A start or
//     update of an unprovisioned app provisions (grants on shared data
//     services) and therefore runs alone.
//   - service.create|reconcile hold their own service exclusively.
//   - restic.backup shares its app (the app keeps running; lifecycle
//     operations wait) and every bound data service, and holds the app's
//     repository exclusively, in the restic pool. restic.restore holds the
//     app exclusively. Other restic.* kinds hold only the repository.
//   - image.prepare holds its runtime key exclusively, in the image-build
//     pool. It only builds (ImageManager serializes per tag and excludes
//     prune); image.prune stays global.
//
// Everything else runs alone: edge/tunnel/dbadmin apply, provision, publish,
// unpublish, remove, bindings, permissions, backup, restore, export, image
// prune.
func classify(op store.Operation, lookup func(id string) (domain.App, error)) claims {
	switch op.Kind {
	case KindAppReconcile, KindAppStart, KindAppRestart, KindAppUpdate, KindAppDeploy, KindAppStop:
		cl := claims{excl: []string{appClaim(op.TargetID)}}
		app, err := lookup(op.TargetID)
		if errors.Is(err, store.ErrNotFound) {
			return cl // the handler fails fast with app-not-found
		}
		if err != nil {
			return globalClaims
		}
		if (op.Kind == KindAppStart || op.Kind == KindAppUpdate) && !app.Provisioned {
			return globalClaims
		}
		if op.Kind != KindAppStop {
			cl.shared = serviceDeps(app)
		}
		return cl
	case KindServiceCreate, KindServiceEnsure:
		return claims{excl: []string{serviceClaim(op.TargetID)}}
	case KindResticBackup, KindResticRestore:
		app, err := lookup(op.TargetID)
		if errors.Is(err, store.ErrNotFound) {
			return claims{excl: []string{resticClaim(op.TargetID)}}
		}
		if err != nil {
			return globalClaims
		}
		cl := claims{excl: []string{resticClaim(op.TargetID)}, shared: serviceDeps(app), pool: resticPool}
		if op.Kind == KindResticBackup {
			cl.shared = append(cl.shared, appClaim(op.TargetID))
		} else {
			cl.excl = append(cl.excl, appClaim(op.TargetID))
		}
		return cl
	case KindResticInit, KindResticConnect, KindResticRefresh, KindResticKeyAdd, KindResticKeyRemove, KindResticCheck, KindResticUnlock:
		return claims{excl: []string{resticClaim(op.TargetID)}}
	case KindImagePrepare:
		return claims{excl: []string{imageClaim(op.TargetID)}, pool: imageBuildPool}
	}
	return globalClaims
}
