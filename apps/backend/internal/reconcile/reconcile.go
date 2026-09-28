// Package reconcile converges observed Docker state toward durable intent.
// Docker events are hints, not a durable log; a periodic full resync repairs
// anything missed. Work is submitted as ordinary serialized operations.
package reconcile

import (
	"context"
	"log/slog"
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

	mu      sync.Mutex
	targets map[string]*target
	trigger chan struct{}
	passes  int
}

func New(c *operations.Controller, log *slog.Logger) *Reconciler {
	return &Reconciler{C: c, Log: log, Interval: 60 * time.Second, Debounce: 2 * time.Second, BaseBackoff: 30 * time.Second,
		targets: map[string]*target{}, trigger: make(chan struct{}, 1)}
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
	return TargetStatus{Failures: t.Failures, NextAttempt: t.NextAttempt, Blocked: t.Failures >= MaxAttempts, LastError: t.LastError, Pending: t.PendingOp}
}

// Passes reports completed passes (tests).
func (r *Reconciler) Passes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.passes
}

// Run watches events and resyncs periodically until ctx ends.
func (r *Reconciler) Run(ctx context.Context) {
	go r.watchEvents(ctx)
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
		if err := r.Pass(ctx); err != nil && ctx.Err() == nil {
			r.Log.Warn("reconcile pass failed", "err", err)
		}
	}
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

func (r *Reconciler) get(id string, gen int64) *target {
	t, ok := r.targets[id]
	if !ok || (gen != 0 && t.Generation != gen) {
		t = &target{Generation: gen}
		r.targets[id] = t
	}
	return t
}

// settle folds the outcome of a previously submitted reconcile operation.
func (r *Reconciler) settle(ctx context.Context, t *target) bool {
	if t.PendingOp == "" {
		return true
	}
	op, err := store.GetOperation(ctx, r.C.Store.DB(), t.PendingOp)
	if err != nil || !op.State.Terminal() {
		return false
	}
	t.PendingOp = ""
	if op.State == store.OpSucceeded {
		t.Failures, t.LastError = 0, ""
		return true
	}
	t.Failures++
	t.LastError = op.ErrorMessage
	delay := r.BaseBackoff << min(t.Failures-1, 6)
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	t.NextAttempt = time.Now().Add(delay)
	return true
}

func (r *Reconciler) submit(ctx context.Context, t *target, kind, targetKind, id string) {
	if t.Failures >= MaxAttempts || time.Now().Before(t.NextAttempt) {
		return
	}
	op, _, err := r.C.Submit(ctx, operations.Submission{Kind: kind, TargetKind: targetKind, TargetID: id, Origin: "reconciler",
		Request: map[string]any{"reason": "converge"}})
	if err != nil {
		t.LastError = err.Error()
		return
	}
	t.PendingOp = op.ID
	r.Log.Info("reconcile submitted", "kind", kind, "target", id, "op", op.ID)
}

// ResetBudget clears a target's failure budget (explicit operator action).
func (r *Reconciler) ResetBudget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.targets, id)
}

// Pass performs one full desired-versus-observed comparison.
func (r *Reconciler) Pass(ctx context.Context) error {
	r.mu.Lock()
	defer func() {
		r.passes++
		r.mu.Unlock()
	}()
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
		if busy, _ := store.ActiveForTarget(ctx, db, s.Name); busy {
			continue
		}
		ins, err := r.C.Engine.Inspect(ctx, r.C.Names.ServiceContainer(s.Name))
		if err != nil {
			return err
		}
		if ins == nil || ins.State == nil || !ins.State.Running {
			r.submit(ctx, t, operations.KindServiceEnsure, "service", s.Name)
		}
	}
	apps, err := store.ListApps(ctx, db)
	if err != nil {
		return err
	}
	for _, app := range apps {
		t := r.get(app.ID, app.ConfigGeneration)
		if !r.settle(ctx, t) {
			continue
		}
		if !app.Provisioned {
			continue
		}
		if busy, _ := store.ActiveForTarget(ctx, db, app.ID); busy {
			continue
		}
		obs, err := r.C.Observe(ctx, app)
		if err != nil {
			return err
		}
		need := false
		switch app.DesiredRuntime {
		case domain.DesiredStopped:
			need = obs.Exists && obs.Running
		case domain.DesiredRunning:
			if !obs.Exists || !obs.Running {
				need = true
			} else {
				gen, ok, err := r.C.PlannedGeneration(ctx, app)
				if err != nil {
					t.LastError = err.Error()
					continue
				}
				// Unhealthy readiness alone never triggers recreation.
				need = !ok || gen != obs.Generation
				if !need {
					// Template-only changes are applied by scoped reloads.
					if need, err = r.C.AppConfigDrift(ctx, app); err != nil {
						t.LastError = err.Error()
						continue
					}
				}
			}
		}
		if need {
			r.submit(ctx, t, operations.KindAppReconcile, "app", app.ID)
		}
	}
	if err := r.edgeAndTunnel(ctx); err != nil {
		return err
	}
	return r.collectTools(ctx)
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
			if busy, _ := store.ActiveForTarget(ctx, db, "edge"); !busy {
				ins, err := r.C.Engine.Inspect(ctx, r.C.Names.EdgeContainer())
				if err != nil {
					return err
				}
				need := ins == nil || ins.State == nil || !ins.State.Running
				if !need {
					// Re-rendered routes or templates differ from the live generation.
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
			if busy, _ := store.ActiveForTarget(ctx, db, "tunnel"); !busy {
				ins, err := r.C.Engine.Inspect(ctx, r.C.Names.TunnelContainer())
				if err != nil {
					return err
				}
				if ins == nil || ins.State == nil || !ins.State.Running {
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
			if busy, _ := store.ActiveForTarget(ctx, db, "dbadmin"); !busy {
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
// active operation claims them.
func (r *Reconciler) collectTools(ctx context.Context) error {
	list, err := r.C.Engine.List(ctx, map[string]string{runtime.LabelStackID: r.C.Stack.ID})
	if err != nil {
		return err
	}
	now := time.Now()
	for _, t := range list {
		if !collectable(string(t.State)) {
			continue
		}
		role := runtime.Role(t.Labels[runtime.LabelRole])
		if role != runtime.RoleTool && role != runtime.RoleBackup && role != runtime.RoleProbe {
			continue
		}
		if !r.C.Names.OwnedBy(t.Labels, role, "") {
			continue
		}
		if t.Created == 0 || now.Sub(time.Unix(t.Created, 0)) < orphanGrace {
			continue
		}
		if opID := t.Labels[runtime.LabelOperation]; opID != "" {
			op, err := store.GetOperation(ctx, r.C.Store.DB(), opID)
			if err == nil && !op.State.Terminal() {
				continue
			}
		}
		_ = r.C.Engine.Remove(ctx, t.ID)
	}
	return nil
}

func collectable(state string) bool {
	return state == "exited" || state == "created" || state == "dead"
}
