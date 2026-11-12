package reconcile

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/dataservices"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Actions a pass takes on one target. The pass and Plan share the deciding
// functions below, so a plan names what the next pass would submit.
const (
	ActionStart    = "start"    // desired running, but missing or stopped
	ActionStop     = "stop"     // desired stopped, but running
	ActionBuild    = "build"    // a runtime image to build before its apps are replaced
	ActionReplace  = "replace"  // a running app recreated in the one-at-a-time rollout
	ActionReload   = "reload"   // generated config applied in place, without recreation
	ActionRecreate = "recreate" // a service, edge, tunnel or dbadmin container recreated
	ActionNote     = "note"     // nothing changes now; worth knowing
)

// decideApp is the pass's verdict for one provisioned app, given its observed
// state: "" (leave it), ActionStart, ActionStop, ActionBuild (its planned image
// is not built; the instance keeps serving until it is), ActionReplace or
// ActionReload (both wait their turn in the rollout). An error is a drift check
// that failed; the pass records it on the target and moves on.
func decideApp(
	ctx context.Context,
	c *operations.Controller,
	app domain.App,
	obs operations.Observation,
) (string, string, error) {
	switch app.DesiredRuntime {
	case domain.DesiredStopped:
		if obs.Exists && obs.Running {
			return ActionStop, "running, but desired stopped", nil
		}
	case domain.DesiredRunning:
		if !obs.Exists {
			return ActionStart, "container missing", nil
		}
		if !obs.Running {
			return ActionStart, "container stopped", nil
		}
		drift, err := c.RunningAppDrift(ctx, app, obs.Generation)
		if err != nil {
			// Unhealthy readiness alone never triggers recreation: only a
			// missing image, a changed fingerprint or config drift does.
			return "", "", err
		}
		switch drift {
		case operations.DriftImageMissing:
			return ActionBuild, "runtime image inputs changed", nil
		case operations.DriftReplace:
			return ActionReplace, "container definition changed", nil
		case operations.DriftReload:
			return ActionReload, "generated config changed", nil
		}
	}
	return "", "", nil
}

// decideService is the pass's verdict for one initialized data service:
// ActionStart when its container is missing or stopped, ActionRecreate when
// its settings fingerprint is stale (a configure did not finish), or "".
func decideService(ctx context.Context, c *operations.Controller, s store.ServiceRow) (string, string, error) {
	ins, err := c.Engine.Inspect(ctx, c.Names.ServiceContainer(s.Name))
	if err != nil {
		return "", "", err
	}
	switch {
	case ins == nil:
		return ActionStart, "container missing", nil
	case ins.State == nil || !ins.State.Running:
		return ActionStart, "container stopped", nil
	case ins.Config != nil &&
		ins.Config.Labels[runtime.LabelServiceSettings] != dataservices.SettingsFingerprint(s.DataService):
		return ActionRecreate, "settings changed", nil
	}
	return "", "", nil
}

// Change is one thing the reconciler would do, or one thing worth knowing.
type Change struct {
	// Kind is app, image, service, edge, tunnel or dbadmin.
	Kind string `json:"kind"`
	// Target is the app slug, image key, service name, or the kind itself.
	Target string `json:"target"`
	// Action is one of the Action constants.
	Action string `json:"action"`
	Detail string `json:"detail"`
}

// Problem is a target whose check failed, so the plan cannot say what
// happens to it.
type Problem struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Error  string `json:"error"`
}

// Plan is what a reconcile pass would do now, without doing any of it.
type Plan struct {
	Changes  []Change  `json:"changes"`
	Problems []Problem `json:"problems"`
}

// PlanPass decides what a pass would do to every target, the way Pass
// decides, without submitting anything or tracking retry budgets. It needs
// only reads; run it on a controller whose engine is docker.ReadOnly and whose
// store is a copy to be sure. An app replacement waits for the build of its
// image first; replacements and reloads then run one app at a time.
func PlanPass(ctx context.Context, c *operations.Controller) (Plan, error) {
	p := Plan{Changes: []Change{}, Problems: []Problem{}}
	problem := func(kind, target string, err error) {
		p.Problems = append(p.Problems, Problem{Kind: kind, Target: target, Error: err.Error()})
	}
	db := c.Store.DB()
	services, err := store.ListServices(ctx, db)
	if err != nil {
		return p, err
	}
	for _, s := range services {
		if !s.Initialized {
			continue
		}
		action, why, err := decideService(ctx, c, s)
		if err != nil {
			problem("service", s.Name, err)
			continue
		}
		if action != "" {
			p.Changes = append(p.Changes, Change{Kind: "service", Target: s.Name, Action: action, Detail: why})
		}
		if img, ok := domain.ServiceImage(s.Engine, s.Version); ok && img != s.Image {
			p.Changes = append(p.Changes, Change{Kind: "service", Target: s.Name, Action: ActionNote,
				Detail: fmt.Sprintf("a newer patch image of %s applies on its next restart", strings.SplitN(img, "@", 2)[0])})
		}
	}
	apps, err := store.ListApps(ctx, db)
	if err != nil {
		return p, err
	}
	builds := map[string][]string{}
	var appChanges []Change
	for _, app := range apps {
		if !app.Provisioned {
			continue
		}
		obs, err := c.Observe(ctx, app)
		if err != nil {
			problem("app", app.Slug, err)
			continue
		}
		action, why, err := decideApp(ctx, c, app, obs)
		if err != nil {
			problem("app", app.Slug, err)
			continue
		}
		key := app.Runtime.ImageKey().String()
		if action == ActionStart {
			// A start builds the planned image first when there is none.
			_, ok, err := c.PlannedGeneration(ctx, app)
			if err != nil {
				problem("app", app.Slug, err)
				continue
			}
			if !ok {
				builds[key] = append(builds[key], app.Slug)
			}
		}
		if action == ActionBuild {
			builds[key] = append(builds[key], app.Slug)
			action, why = ActionReplace, why+"; replaced once its image is built"
		}
		if action != "" {
			appChanges = append(appChanges, Change{Kind: "app", Target: app.Slug, Action: action, Detail: why})
		}
	}
	for _, key := range slices.Sorted(maps.Keys(builds)) {
		p.Changes = append(p.Changes, Change{Kind: "image", Target: key, Action: ActionBuild,
			Detail: "for " + strings.Join(builds[key], ", ")})
	}
	p.Changes = append(p.Changes, appChanges...)
	if err := planEdge(ctx, c, &p, problem); err != nil {
		return p, err
	}
	return p, nil
}

// planEdge adds the edge, tunnel and dbadmin decisions of edgeAndTunnel.
func planEdge(ctx context.Context, c *operations.Controller, p *Plan, problem func(string, string, error)) error {
	es, err := c.EdgeSettings(ctx)
	if err != nil {
		return err
	}
	if es.Enabled {
		ins, err := c.Engine.Inspect(ctx, c.Names.EdgeContainer())
		switch {
		case err != nil:
			problem("edge", "edge", err)
		case ins == nil || ins.State == nil || !ins.State.Running:
			p.Changes = append(p.Changes, Change{Kind: "edge", Target: "edge", Action: ActionStart,
				Detail: "container missing or stopped"})
		default:
			switch d, err := c.EdgeDrift(ctx); {
			case err != nil:
				problem("edge", "edge", err)
			case d == operations.EdgeDriftRecreate:
				p.Changes = append(p.Changes, Change{Kind: "edge", Target: "edge", Action: ActionRecreate,
					Detail: "image or container changed; refuses connections for about a second"})
			case d == operations.EdgeDriftReload:
				p.Changes = append(p.Changes, Change{Kind: "edge", Target: "edge", Action: ActionReload,
					Detail: "routes or templates changed; reloads without dropping connections"})
			}
		}
	}
	ts, err := c.TunnelSettings(ctx)
	if err != nil {
		return err
	}
	if ts.Enabled {
		switch need, err := c.TunnelDrift(ctx); {
		case err != nil:
			problem("tunnel", "tunnel", err)
		case need:
			p.Changes = append(p.Changes, Change{Kind: "tunnel", Target: "tunnel", Action: ActionRecreate,
				Detail: "missing, stopped, or its token or image changed"})
		}
	}
	ds, err := c.DBAdminSettings(ctx)
	if err != nil {
		return err
	}
	if ds.Enabled {
		switch need, err := c.DBAdminDrift(ctx); {
		case err != nil:
			problem("dbadmin", "dbadmin", err)
		case need:
			p.Changes = append(p.Changes, Change{Kind: "dbadmin", Target: "dbadmin", Action: ActionRecreate,
				Detail: "missing, stopped, or its image or router changed"})
		}
	}
	return nil
}
