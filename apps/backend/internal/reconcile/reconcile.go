// Package reconcile converges observed Docker state toward durable intent.
// Docker events are hints, not a durable log; a periodic full resync repairs
// anything missed. Work is submitted as ordinary serialized operations.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// MaxAttempts is the retry budget before a target is reported blocked. A new
// configuration generation or an explicit operator operation resets it.
const MaxAttempts = 5

type target struct {
	Failures    int
	NextAttempt time.Time
	PendingOp   string
	LastError   string
	Generation  int64
	// OpTarget and LastFailedAt let a later successful operation on the same
	// target, from any origin, restore the budget (see recovered).
	OpTarget     string
	LastFailedAt string
	// Rollout marks an app whose pending or last failed operation replaces a
	// running instance whose image or configuration drifted (see rollout).
	Rollout bool
	// Paused marks an app whose rollout waits on another app's failed one;
	// LastError then says so.
	Paused bool
}

// TargetStatus is the observable reconciliation state of one target.
type TargetStatus struct {
	Failures    int       `json:"failures"`
	NextAttempt time.Time `json:"nextAttempt"`
	Blocked     bool      `json:"blocked"`
	LastError   string    `json:"lastError"`
	Pending     string    `json:"pendingOperation"`
}

type Reconciler struct {
	C        *operations.Controller
	Log      *slog.Logger
	Interval time.Duration
	Debounce time.Duration
	// BaseBackoff is the first retry delay; it doubles per failure up to 30m.
	BaseBackoff time.Duration

	// passMu serializes passes. A pass works on its own copy of the targets
	// (work) and publishes it when it ends, so Status, Statuses and
	// ResetBudget never wait for its Docker and store calls.
	passMu sync.Mutex
	work   map[string]*target // guarded by passMu

	// mu guards the published targets, resets and passes. Published targets
	// are never mutated; a pass mutates only its copies.
	mu      sync.Mutex
	targets map[string]*target
	// resets holds the ids reset while a pass is in flight; the pass drops
	// them when it publishes, as if the reset came after the pass.
	resets  map[string]bool
	passes  int
	trigger chan struct{}
	// started is when this backend process began. Tool and probe containers
	// belong to sessions and operations of the process that created them.
	started time.Time
}

func New(c *operations.Controller, log *slog.Logger) *Reconciler {
	return &Reconciler{
		C:           c,
		Log:         log,
		Interval:    60 * time.Second,
		Debounce:    2 * time.Second,
		BaseBackoff: 30 * time.Second,
		targets:     map[string]*target{},
		resets:      map[string]bool{},
		trigger:     make(chan struct{}, 1),
		started:     time.Now(),
	}
}

// Trigger requests a pass soon (debounced).
func (r *Reconciler) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

func (r *Reconciler) Status(id string) TargetStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.targets[id]
	if !ok {
		return TargetStatus{}
	}
	return t.status()
}

func (t *target) status() TargetStatus {
	return TargetStatus{
		Failures:    t.Failures,
		NextAttempt: t.NextAttempt,
		Blocked:     t.Failures >= MaxAttempts,
		LastError:   t.LastError,
		Pending:     t.PendingOp,
	}
}

// Statuses reports every tracked target that is failing or has a pending
// operation, keyed by target id ("edge", "tunnel", "dbadmin", "service:<name>",
// or an app id).
func (r *Reconciler) Statuses() map[string]TargetStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]TargetStatus{}
	for id, t := range r.targets {
		if t.Failures > 0 || t.PendingOp != "" || t.LastError != "" {
			out[id] = t.status()
		}
	}
	return out
}

// Passes reports completed passes (tests).
func (r *Reconciler) Passes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.passes
}

// Run watches events and resyncs periodically until ctx ends. It returns
// once its helper goroutines have stopped too.
func (r *Reconciler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { r.watchEvents(ctx) })
	wg.Go(func() { r.pruneHistory(ctx) })
	wg.Go(func() { r.watchImageOps(ctx) })
	wg.Go(func() { r.sweepBuilds(ctx) })
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	r.Trigger()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.trigger:
			// Debounce bursts of events into one pass.
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.Debounce):
			}
			for len(r.trigger) > 0 {
				<-r.trigger
			}
		}
		if err := r.safePass(ctx); err != nil && ctx.Err() == nil {
			r.Log.Warn("reconcile pass failed", "err", err)
		}
	}
}

// safePass runs Pass, turning a panic into an error: the reconciler runs in
// the resident backend, where an unrecovered panic would stop the management
// plane. Operation handlers are guarded the same way by the executor.
func (r *Reconciler) safePass(ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.Log.Error("reconcile pass panicked", "panic", p, "stack", string(debug.Stack()))
			err = fmt.Errorf("reconcile pass panicked: %v", p)
		}
	}()
	return r.Pass(ctx)
}

// pruneInterval is how often finished operation and backup history is pruned.
const pruneInterval = 6 * time.Hour

// pruneHistory applies store.DefaultRetention at startup and periodically.
func (r *Reconciler) pruneHistory(ctx context.Context) {
	t := time.NewTicker(pruneInterval)
	defer t.Stop()
	for {
		ops, err := store.PruneHistory(ctx, r.C.Store.DB(), store.DefaultRetention)
		if err != nil && ctx.Err() == nil {
			r.Log.Warn("history prune failed", "err", err)
		} else if ops > 0 {
			r.Log.Info("pruned history", "operations", ops)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sweepInterval is how often released runtime builds are marked and the due
// ones removed, so a build goes between BuildReleaseGrace and an hour after.
const sweepInterval = time.Hour

// sweepBuilds marks runtime builds nothing can use any more and submits one
// image.sweep per runtime whose builds have stayed so for
// operations.BuildReleaseGrace (see operations.KindImageSweep).
func (r *Reconciler) sweepBuilds(ctx context.Context) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		if err := r.SweepBuilds(ctx); err != nil && ctx.Err() == nil {
			r.Log.Warn("runtime build sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepBuilds runs one sweep.
func (r *Reconciler) SweepBuilds(ctx context.Context) error {
	due, err := r.C.MarkReleasedBuilds(ctx)
	if err != nil {
		return err
	}
	for _, key := range due {
		busy, err := store.ActiveForTarget(ctx, r.C.Store.DB(), key)
		if err != nil {
			return err
		}
		if busy {
			continue // the next sweep tries again
		}
		op, _, err := r.C.Submit(ctx, operations.Submission{Kind: operations.KindImageSweep, TargetKind: "image",
			TargetID: key, Origin: "reconciler", Request: map[string]any{"reason": "superseded builds"}})
		if err != nil {
			return err
		}
		r.Log.Info("reconcile submitted", "kind", operations.KindImageSweep, "target", key, "op", op.ID)
	}
	return nil
}

func (r *Reconciler) watchEvents(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		msgs, errs := r.C.Engine.Events(ctx, r.C.Names.StackSelector())
		connected := time.Now()
	loop:
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-msgs:
				if !ok {
					break loop
				}
				r.Trigger()
			case err := <-errs:
				if ctx.Err() == nil {
					r.Log.Warn("docker event stream ended; will resync", "err", err)
				}
				break loop
			}
		}
		// After reconnecting, a full resync repairs missed events.
		r.Trigger()
		if time.Since(connected) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// get returns the in-flight pass's target for id. A new id starts fresh. A
// changed configuration generation restarts the retry budget, but an
// operation still pending for the target stays tracked: dropping it would
// let a rollout submit the next app while this one's replacement still runs,
// and would lose its failure.
func (r *Reconciler) get(id string, gen int64) *target {
	t, ok := r.work[id]
	if !ok {
		t = &target{Generation: gen}
		r.work[id] = t
		return t
	}
	if gen != 0 && t.Generation != gen {
		t = &target{Generation: gen, PendingOp: t.PendingOp, OpTarget: t.OpTarget, Rollout: t.PendingOp != "" && t.Rollout}
		r.work[id] = t
	}
	return t
}

// beginPass copies the published targets into the pass's working set.
// Callers hold passMu.
func (r *Reconciler) beginPass() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.resets)
	r.work = make(map[string]*target, len(r.targets))
	for id, t := range r.targets {
		c := *t
		r.work[id] = &c
	}
}

// endPass publishes the working set, minus targets reset during the pass.
// Callers hold passMu.
func (r *Reconciler) endPass() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.resets {
		delete(r.work, id)
	}
	clear(r.resets)
	r.targets, r.work = r.work, nil
	r.passes++
}

// settle folds the outcome of a previously submitted reconcile operation.
func (r *Reconciler) settle(ctx context.Context, t *target) bool {
	if t.PendingOp == "" {
		r.recovered(ctx, t)
		return true
	}
	op, err := store.GetOperation(ctx, r.C.Store.DB(), t.PendingOp)
	if err != nil || !op.State.Terminal() {
		return false
	}
	t.PendingOp = ""
	if op.State == store.OpSucceeded {
		t.Failures, t.LastError, t.Rollout = 0, "", false
		return true
	}
	t.Failures++
	t.LastError = op.ErrorMessage
	t.LastFailedAt = op.CreatedAt
	delay := min(r.BaseBackoff<<min(t.Failures-1, 6), 30*time.Minute)
	t.NextAttempt = time.Now().Add(delay)
	return true
}

// recovered clears a failing target's budget once any operation on it (an
// operator's PUT /edge, a manual service ensure, ...) has succeeded since the
// last reconcile failure, so a blocked target does not stay blocked for good.
func (r *Reconciler) recovered(ctx context.Context, t *target) {
	if t.Failures == 0 || t.OpTarget == "" {
		return
	}
	ops, err := store.ListOperations(
		ctx,
		r.C.Store.DB(),
		store.OpFilter{TargetID: t.OpTarget, States: []store.OpState{store.OpSucceeded}, Limit: 1},
	)
	if err != nil || len(ops) == 0 || ops[0].CreatedAt <= t.LastFailedAt {
		return
	}
	t.Failures, t.LastError, t.NextAttempt, t.Rollout = 0, "", time.Time{}, false
}

func (r *Reconciler) submit(ctx context.Context, t *target, kind, targetKind, id string) {
	if t.Failures >= MaxAttempts || time.Now().Before(t.NextAttempt) {
		return
	}
	op, _, err := r.C.Submit(
		ctx,
		operations.Submission{Kind: kind, TargetKind: targetKind, TargetID: id, Origin: "reconciler",
			Request: map[string]any{"reason": "converge"}},
	)
	if err != nil {
		t.LastError = err.Error()
		return
	}
	t.PendingOp = op.ID
	t.OpTarget = id
	r.Log.Info("reconcile submitted", "kind", kind, "target", id, "op", op.ID)
}

// imageTargetPrefix keys image.prepare targets, whose id is an ImageKey
// string.
const imageTargetPrefix = "image:"

// rolloutStopPrefix keys image.rollout-stop targets, whose id is an ImageKey
// string.
const rolloutStopPrefix = "image-rollout-stop:"

// settleImages folds the outcome of every submitted image.prepare, including
// those whose image is now built and that no app will look up again.
func (r *Reconciler) settleImages(ctx context.Context) {
	for id, t := range r.work {
		if strings.HasPrefix(id, imageTargetPrefix) {
			r.settle(ctx, t)
		}
		if strings.HasPrefix(id, rolloutStopPrefix) && r.settle(ctx, t) && t.PendingOp == "" {
			delete(r.work, id)
		}
	}
}

// stopRollout submits one image.rollout-stop for a key whose rollout failed
// while some of its apps are still unconfirmed on the rolled-out build.
func (r *Reconciler) stopRollout(ctx context.Context, key string) error {
	if t, ok := r.work[rolloutStopPrefix+key]; ok && t.PendingOp != "" {
		return nil
	}
	// Only a rollout with unconfirmed apps gets a target: a halt on a Bento
	// upgrade's rollout pauses instead and must not list one.
	rolling, err := r.C.HasRollingBuilds(ctx, key)
	if err != nil || !rolling {
		return err
	}
	busy, err := store.ActiveForTarget(ctx, r.C.Store.DB(), key)
	if err != nil || busy {
		return err
	}
	r.submit(ctx, r.get(rolloutStopPrefix+key, 0), operations.KindImageRolloutStop, "image", key)
	return nil
}

// forgetImages drops settled image targets whose image no running app still
// misses, so a key built by other means leaves no stale failure behind.
func (r *Reconciler) forgetImages(missing map[string]bool) {
	for id, t := range r.work {
		key, ok := strings.CutPrefix(id, imageTargetPrefix)
		if ok && t.PendingOp == "" && !missing[key] {
			delete(r.work, id)
		}
	}
}

// prepareImage submits one image.prepare per runtime key. Apps sharing the
// key wait for the same build; the executor bounds how many run at once.
func (r *Reconciler) prepareImage(ctx context.Context, key string) error {
	t := r.get(imageTargetPrefix+key, 0)
	if t.PendingOp != "" {
		return nil // settled at the start of the pass; still in flight
	}
	busy, err := store.ActiveForTarget(ctx, r.C.Store.DB(), key)
	if err != nil || busy {
		return err
	}
	r.submit(ctx, t, operations.KindImagePrepare, "image", key)
	return nil
}

// watchImageOps triggers a pass when a pending image.prepare or rollout
// replacement changes state, so apps roll out as soon as their image is
// built, and the next app as soon as the previous one is replaced, rather
// than on the next periodic pass.
func (r *Reconciler) watchImageOps(ctx context.Context) {
	ch, cancel := r.C.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-ch:
			if r.pendingImageOp(id) {
				r.Trigger()
			}
		}
	}
}

func (r *Reconciler) pendingImageOp(opID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, t := range r.targets {
		if (strings.HasPrefix(id, imageTargetPrefix) || t.Rollout) && t.PendingOp == opID {
			return true
		}
	}
	return false
}

// ResetBudget clears a target's failure budget (explicit operator action).
func (r *Reconciler) ResetBudget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.targets, id)
	r.resets[id] = true
}

// Pass performs one full desired-versus-observed comparison.
func (r *Reconciler) Pass(ctx context.Context) error {
	r.passMu.Lock()
	defer r.passMu.Unlock()
	r.beginPass()
	// Publish also after an error or a panic: what the pass learned so far
	// is kept, as when it updated the targets in place.
	defer r.endPass()
	db := r.C.Store.DB()
	services, err := store.ListServices(ctx, db)
	if err != nil {
		return err
	}
	for _, s := range services {
		if !s.Initialized {
			continue // explicit initialization owns first creation
		}
		t := r.get("service:"+s.Name, 0)
		if !r.settle(ctx, t) {
			continue
		}
		busy, err := store.ActiveForTarget(ctx, db, s.Name)
		if err != nil {
			return err
		}
		if busy {
			continue
		}
		// A stale settings fingerprint means a configure did not finish;
		// ensure replaces the container on its volume.
		action, _, err := decideService(ctx, r.C, s)
		if err != nil {
			return err
		}
		if action != "" {
			r.submit(ctx, t, operations.KindServiceEnsure, "service", s.Name)
		}
	}
	apps, err := store.ListApps(ctx, db)
	if err != nil {
		return err
	}
	r.forgetRemovedApps(apps)
	r.settleImages(ctx)
	missing := map[string]bool{}
	var ro rolloutPlan
	for _, app := range apps {
		t := r.get(app.ID, app.ConfigGeneration)
		if t.Paused {
			// Re-decided below; a paused app that no longer drifts must not
			// keep reporting the pause.
			t.Paused, t.LastError = false, ""
		}
		if !r.settle(ctx, t) {
			ro.inFlight = ro.inFlight || t.Rollout
			continue
		}
		if !app.Provisioned {
			continue
		}
		key := app.Runtime.ImageKey().String()
		if t.Rollout && t.Failures > 0 && app.DesiredRuntime == domain.DesiredRunning {
			// Its replacement failed and nothing has fixed it since: other
			// apps on the same runtime image wait for it.
			ro.halt(key, app.Slug)
		}
		busy, err := store.ActiveForTarget(ctx, db, app.ID)
		if err != nil {
			return err
		}
		if busy {
			continue
		}
		obs, err := r.C.Observe(ctx, app)
		if err != nil {
			return err
		}
		action, _, err := decideApp(ctx, r.C, app, obs)
		if err != nil {
			t.LastError = err.Error()
			continue
		}
		switch action {
		case ActionBuild:
			// The instance keeps serving on its current image while the
			// planned one builds; it is replaced only once the build has
			// succeeded.
			missing[key] = true
			if err := r.prepareImage(ctx, key); err != nil {
				return err
			}
		case ActionReplace, ActionReload:
			// A serving instance is changed only as part of the rollout.
			ro.candidates = append(ro.candidates, rolloutCandidate{app: app, t: t, key: key})
		case ActionStart, ActionStop:
			// A stopped or missing instance serves nothing: repair it now,
			// in parallel with other apps.
			r.submit(ctx, t, operations.KindAppReconcile, "app", app.ID)
		}
	}
	r.rollout(ctx, &ro)
	for key := range ro.halted {
		// An operator rollout stops at its first failure: the apps still
		// waiting go back to their previous build instead of pausing.
		if err := r.stopRollout(ctx, key); err != nil {
			return err
		}
	}
	// Only after a complete app scan: an aborted pass must not drop the
	// backoff of an image some app still misses.
	r.forgetImages(missing)
	if err := r.edgeAndTunnel(ctx); err != nil {
		return err
	}
	return r.collectTools(ctx)
}

// rolloutPlan collects one pass's view of running apps that need replacing.
type rolloutPlan struct {
	// inFlight is set while a rollout replacement is still running.
	inFlight bool
	// halted maps a runtime image key to the app whose replacement on it
	// failed.
	halted     map[string]string
	candidates []rolloutCandidate
}

type rolloutCandidate struct {
	app domain.App
	t   *target
	key string
}

func (p *rolloutPlan) halt(key, slug string) {
	if p.halted == nil {
		p.halted = map[string]string{}
	}
	if _, ok := p.halted[key]; !ok {
		p.halted[key] = slug
	}
}

// rollout replaces running instances whose image or configuration drifted
// (typically every app after a Bento upgrade) one at a time: a replacement
// stops the serving instance before the new one is ready, so running them in
// parallel would take several apps down at once, and a bad new image would
// break all of them before the first failure showed. When a replacement
// fails, the other apps on the same runtime image keep their working
// instances until that app recovers (a later reconcile or operator
// operation succeeds) or stops being desired running; apps on other images
// continue.
func (r *Reconciler) rollout(ctx context.Context, p *rolloutPlan) {
	for _, c := range p.candidates {
		if failed, ok := p.halted[c.key]; ok && failed != c.app.Slug {
			c.t.Paused = true
			c.t.LastError = fmt.Sprintf("rollout paused: replacing %s on runtime %s failed; this app keeps its "+
				"current instance until %s is fixed", failed, c.key, failed)
			continue
		}
		if p.inFlight {
			continue // next in line; the finished rollout triggers a pass
		}
		r.submit(ctx, c.t, operations.KindAppReconcile, "app", c.app.ID)
		if c.t.PendingOp != "" {
			c.t.Rollout = true
			p.inFlight = true
		}
	}
}

func (r *Reconciler) edgeAndTunnel(ctx context.Context) error {
	db := r.C.Store.DB()
	es, err := r.C.EdgeSettings(ctx)
	if err != nil {
		return err
	}
	if es.Enabled {
		t := r.get("edge", 0)
		if r.settle(ctx, t) {
			busy, err := store.ActiveForTarget(ctx, db, "edge")
			if err != nil {
				return err
			}
			if !busy {
				ins, err := r.C.Engine.Inspect(ctx, r.C.Names.EdgeContainer())
				if err != nil {
					return err
				}
				need := ins == nil || ins.State == nil || !ins.State.Running
				if !need {
					// Re-rendered routes or templates differ from the live
					// generation, the live generation was never loaded, or
					// the image pin or container shape changed.
					if need, err = r.C.EdgeConfigDrift(ctx); err != nil {
						t.LastError = err.Error()
					}
				}
				if need {
					r.submit(ctx, t, operations.KindEdgeApply, "edge", "edge")
				}
			}
		}
	}
	ts, err := r.C.TunnelSettings(ctx)
	if err != nil {
		return err
	}
	if ts.Enabled {
		t := r.get("tunnel", 0)
		if r.settle(ctx, t) {
			busy, err := store.ActiveForTarget(ctx, db, "tunnel")
			if err != nil {
				return err
			}
			if !busy {
				// Missing, stopped, or an outdated token or image pin.
				need, err := r.C.TunnelDrift(ctx)
				if err != nil {
					return err
				}
				if need {
					r.submit(ctx, t, operations.KindTunnelApply, "tunnel", "tunnel")
				}
			}
		}
	}
	ds, err := r.C.DBAdminSettings(ctx)
	if err != nil {
		return err
	}
	if ds.Enabled {
		t := r.get("dbadmin", 0)
		if r.settle(ctx, t) {
			busy, err := store.ActiveForTarget(ctx, db, "dbadmin")
			if err != nil {
				return err
			}
			if !busy {
				need, err := r.C.DBAdminDrift(ctx)
				if err != nil {
					t.LastError = err.Error()
				} else if need {
					r.submit(ctx, t, operations.KindDBAdminApply, "dbadmin", "dbadmin")
				}
			}
		}
	}
	return nil
}

// orphanGrace is how long a non-running ephemeral container is left alone
// after creation, so a container between Create and Start of an active
// operation (or a short-lived image probe) is never collected.
const orphanGrace = 2 * time.Minute

// collectTools removes non-running ephemeral containers (tool, backup job,
// image probe) this stack owns once they are past the grace period and no
// active operation claims them. A running backup job container is removed
// too once the operation named by its label has finished: restic and SQLite
// snapshot jobs idle indefinitely and would otherwise outlive a crashed
// backend, holding repository locks. Any running ephemeral container created
// before this backend started is removed as well: no session or operation of
// this process uses it, and whatever was exec'd in it keeps running (a
// deploy script interrupted by a crash went on changing the app for up to
// its 32 minutes while its operation already read interrupted).
func (r *Reconciler) collectTools(ctx context.Context) error {
	list, err := r.C.Engine.List(ctx, map[string]string{runtime.LabelStackID: r.C.Stack.ID})
	if err != nil {
		return err
	}
	now := time.Now()
	for _, t := range list {
		role := runtime.Role(t.Labels[runtime.LabelRole])
		if role != runtime.RoleTool && role != runtime.RoleBackup && role != runtime.RoleProbe {
			continue
		}
		if !r.C.Names.OwnedBy(t.Labels, role, "") {
			continue
		}
		created := time.Unix(t.Created, 0)
		if t.Created != 0 && created.Before(r.started) && !collectable(string(t.State)) {
			// Running since before this backend: no grace, the crash that
			// orphaned it may have left a deploy script going, and a SQLite
			// dump job (labelled with a session, not an operation) would
			// otherwise idle out its hour.
			if err := r.C.Engine.Remove(ctx, t.ID); err != nil {
				r.Log.Warn("remove tool container of a previous backend", "container", t.ID, "err", err)
			}
			continue
		}
		if t.Created == 0 || now.Sub(created) < orphanGrace {
			continue
		}
		if !collectable(string(t.State)) {
			if role != runtime.RoleBackup {
				continue
			}
			// Running backup job: only one of a known, finished operation.
			// Labels that are not operation ids (volume transfers) never match.
			opID := t.Labels[runtime.LabelOperation]
			if role != runtime.RoleBackup || opID == "" {
				continue
			}
			op, err := store.GetOperation(ctx, r.C.Store.DB(), opID)
			if err != nil || !op.State.Terminal() {
				continue
			}
			if err := r.C.Engine.Remove(ctx, t.ID); err != nil {
				r.Log.Warn("remove orphaned backup job container", "container", t.ID, "err", err)
			}
			continue
		}
		if opID := t.Labels[runtime.LabelOperation]; opID != "" {
			op, err := store.GetOperation(ctx, r.C.Store.DB(), opID)
			if err == nil && !op.State.Terminal() {
				continue
			}
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				// Owner state unknown: keep the container and retry next pass.
				continue
			}
		}
		if err := r.C.Engine.Remove(ctx, t.ID); err != nil {
			r.Log.Warn("remove orphaned tool container", "container", t.ID, "err", err)
		}
	}
	return nil
}

func collectable(state string) bool {
	return state == "exited" || state == "created" || state == "dead"
}

// forgetRemovedApps drops the targets of apps that no longer exist (every
// other target is keyed by a fixed name or a prefix with ":"), so a removed
// app's backoff and errors do not stay in the reconciler for the life of the
// backend. Callers hold passMu.
func (r *Reconciler) forgetRemovedApps(apps []domain.App) {
	live := make(map[string]bool, len(apps))
	for _, a := range apps {
		live[a.ID] = true
	}
	for id, t := range r.work {
		switch {
		case live[id], strings.Contains(id, ":"), id == "edge", id == "tunnel", id == "dbadmin":
		case t.PendingOp != "":
			// Keep it until its operation settles.
		default:
			delete(r.work, id)
		}
	}
}
