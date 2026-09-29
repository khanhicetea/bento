// Package operations plans and executes every runtime-changing mutation as a
// durable, journaled operation. HTTP handlers and CLI commands only validate,
// persist intent, and submit; the executor performs external effects, running
// operations with disjoint claims (see claims.go) in parallel.
package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// OpError is a diagnosable operation failure with operator guidance. Messages
// must already be redacted.
type OpError struct {
	Code     string
	Message  string
	Guidance string
}

func (e *OpError) Error() string { return e.Code + ": " + e.Message }

func Fail(code, guidance, format string, args ...any) *OpError {
	return &OpError{Code: code, Message: fmt.Sprintf(format, args...), Guidance: guidance}
}

var ErrCancelled = errors.New("operation cancelled at a safe boundary")

// Deps are the effects the controller uses.
type Deps struct {
	Store   *store.Store
	Engine  docker.Engine
	Layout  platform.Layout
	HostIDs platform.HostIDs
	Log     *slog.Logger
	// Probe performs the direct HTTP readiness request from the backend.
	Probe func(ctx context.Context, url string) (int, error)
	// ReadyTimeout bounds readiness waits.
	ReadyTimeout time.Duration
	// ServiceReadyTimeout bounds data-service readiness waits; a first MySQL
	// init on a slow disk can take several minutes.
	ServiceReadyTimeout time.Duration
	// PollInterval is the readiness poll cadence.
	PollInterval time.Duration
	// UtilsAppsPort is the port of the backend's utils listener on the apps
	// network gateway (0 when not listening there). The edge proxies
	// /_bento/webhook/* to it.
	UtilsAppsPort int
	// Concurrency bounds how many operations execute at once (default
	// DefaultConcurrency). 1 restores strictly serial execution.
	Concurrency int
}

// DefaultConcurrency is the default number of operations executing at once.
const DefaultConcurrency = 4

// queueScanLimit bounds how many queued operations one dispatch pass
// considers.
const queueScanLimit = 256

type handler func(ctx context.Context, r *Run) (any, error)

// Controller owns the executor loop.
type Controller struct {
	Deps
	Stack  store.StackIdentity
	Names  runtime.Names
	Images *runtime.ImageManager

	handlers map[string]handler
	wake     chan struct{}
	stopping atomic.Bool
	done     chan struct{}
	workers  sync.WaitGroup
	mu       sync.Mutex
	// listeners receive operation state change notifications (SSE).
	listeners map[chan string]struct{}

	// runMu guards running, warming and waitingOn.
	runMu sync.Mutex
	// running maps the id of each executing operation to its claims.
	running map[string]claims
	// warming tracks apps whose instance an operation is (re)starting and has
	// not yet seen ready. Edge renders treat such an app as not running, so
	// another operation's edge apply never routes to it early; the owning
	// operation re-applies the edge once the app is ready if that happened.
	warming map[string]*warmState
	// waitingOn maps a queued operation to the operation it waits behind, as
	// of the last dispatch pass.
	waitingOn map[string]string
	// passes counts completed dispatch passes (tests).
	passes atomic.Int64

	// Locks for state shared by operations that run in parallel. Acquisition
	// order when nested: images (runtime.ImageManager), edgeMu, netMu; aclMu
	// and runMu are leaves.
	netMu  sync.Mutex // network plan + network creation
	edgeMu sync.Mutex // edge generations, container and reload
	aclMu  sync.Mutex // Redis ACL file and reload
}

type warmState struct {
	op string
	// edgeStale is set when an edge apply rendered the app's route as
	// unavailable because it was warming.
	edgeStale bool
}

func NewController(d Deps) (*Controller, error) {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Probe == nil {
		d.Probe = defaultProbe
	}
	if d.ReadyTimeout == 0 {
		d.ReadyTimeout = 180 * time.Second
	}
	if d.ServiceReadyTimeout == 0 {
		d.ServiceReadyTimeout = 10 * time.Minute
	}
	if d.PollInterval == 0 {
		d.PollInterval = 2 * time.Second
	}
	if d.Concurrency <= 0 {
		d.Concurrency = DefaultConcurrency
	}
	stack, err := store.GetStackIdentity(context.Background(), d.Store.DB())
	if err != nil {
		return nil, fmt.Errorf("stack identity: %w", err)
	}
	names := runtime.Names{StackID: stack.ID, StackName: stack.Name}
	c := &Controller{
		Deps: d, Stack: stack, Names: names,
		Images:    &runtime.ImageManager{Engine: d.Engine, Layout: d.Layout, Names: names},
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
		listeners: map[chan string]struct{}{},
		running:   map[string]claims{},
		warming:   map[string]*warmState{},
		waitingOn: map[string]string{},
	}
	c.registerHandlers()
	return c, nil
}

var probeClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func defaultProbe(ctx context.Context, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "bento-readiness")
	resp, err := probeClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// Submission describes one accepted mutation.
type Submission struct {
	Kind           string
	TargetKind     string
	TargetID       string
	Request        any
	IdempotencyKey string
	Origin         string
	Generation     int64
	// Mutate persists intent in the same transaction that records the
	// operation, so acceptance is durable before it is acknowledged.
	Mutate func(ctx context.Context, q store.Q) error
}

// Submit persists intent and the operation atomically, then wakes the
// executor. A repeated idempotency key returns the original operation
// without re-applying Mutate.
func (c *Controller) Submit(ctx context.Context, s Submission) (store.Operation, bool, error) {
	if c.stopping.Load() {
		return store.Operation{}, false, Fail("shutting-down", "Retry after the backend restarts.", "backend is shutting down and not accepting new operations")
	}
	if _, ok := c.handlers[s.Kind]; !ok {
		return store.Operation{}, false, fmt.Errorf("unknown operation kind %q", s.Kind)
	}
	raw, err := json.Marshal(s.Request)
	if err != nil {
		return store.Operation{}, false, err
	}
	var op store.Operation
	var existed bool
	err = c.Store.Tx(ctx, func(q store.Q) error {
		if s.IdempotencyKey != "" {
			existing, err := store.FindByIdempotencyKey(ctx, q, s.IdempotencyKey)
			if err == nil {
				if existing.Kind != s.Kind || existing.TargetID != s.TargetID {
					return fmt.Errorf("%w: idempotency key was already used for a different operation", store.ErrConflict)
				}
				op, existed = existing, true
				return nil
			}
			if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		if s.Mutate != nil {
			if err := s.Mutate(ctx, q); err != nil {
				return err
			}
		}
		op, _, err = store.InsertOperation(ctx, q, store.Operation{
			ID: platform.NewOperationID(), Kind: s.Kind, TargetKind: s.TargetKind, TargetID: s.TargetID,
			IdempotencyKey: s.IdempotencyKey, Request: raw, Origin: s.Origin, TargetGeneration: s.Generation,
		})
		return err
	})
	if err != nil {
		return op, false, err
	}
	if !existed {
		c.notify(op.ID)
		c.Wake()
	}
	return op, existed, nil
}

func (c *Controller) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Recover marks operations left running by a previous process as interrupted.
// It must run while holding the stack lifetime lock, before Start.
func (c *Controller) Recover(ctx context.Context) error {
	ops, err := store.InterruptRunning(ctx, c.Store.DB())
	for _, o := range ops {
		c.Log.Warn("operation interrupted by previous shutdown", "op", o.ID, "kind", o.Kind, "phase", o.Phase)
	}
	return err
}

// Start runs the executor until ctx is cancelled or Shutdown is called.
func (c *Controller) Start(ctx context.Context) {
	go func() {
		defer close(c.done)
		defer c.workers.Wait()
		for {
			if c.stopping.Load() || ctx.Err() != nil {
				return
			}
			started, err := c.dispatch(ctx)
			if err != nil {
				c.Log.Error("dequeue operations", "err", err)
				select {
				case <-time.After(time.Second):
				case <-ctx.Done():
					return
				}
				continue
			}
			if started {
				continue
			}
			select {
			case <-c.wake:
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}()
}

// dispatch starts every queued operation that can run now, oldest first, up
// to the concurrency limit. An operation is skipped, and its claims held back
// from later operations, when it conflicts with a running operation or with
// an earlier queued one that could not start: work on the same resources
// stays FIFO and nothing overtakes a waiting exclusive operation. The whole
// queue is scanned even at the limit so waitingOn stays current. It reports
// whether it started anything.
func (c *Controller) dispatch(ctx context.Context) (bool, error) {
	defer c.passes.Add(1)
	queued, err := store.ListQueued(ctx, c.Store.DB(), queueScanLimit)
	if err != nil {
		return false, err
	}
	type pending struct {
		id string
		cl claims
	}
	lookup := c.appLookup(ctx)
	free := c.Concurrency - c.runningCount()
	var waiting []pending
	waitingOn := map[string]string{}
	started := false
	for _, op := range queued {
		if c.stopping.Load() || ctx.Err() != nil {
			break
		}
		cl := classify(op, lookup)
		blocker := c.blocker(cl)
		for _, w := range waiting {
			if blocker != "" {
				break
			}
			if cl.conflicts(w.cl) {
				blocker = w.id
			}
		}
		if blocker != "" {
			waitingOn[op.ID] = blocker
			waiting = append(waiting, pending{op.ID, cl})
			continue
		}
		if free <= 0 {
			// Out of capacity, not blocked: it keeps its place in line.
			waiting = append(waiting, pending{op.ID, cl})
			continue
		}
		claimed, err := store.MarkRunning(ctx, c.Store.DB(), op.ID)
		if err != nil {
			c.Log.Error("mark running", "op", op.ID, "err", err)
			waiting = append(waiting, pending{op.ID, cl})
			continue
		}
		if !claimed {
			continue // cancelled after it was listed
		}
		c.begin(op.ID, cl)
		free--
		started = true
		c.workers.Add(1)
		go func() {
			defer c.workers.Done()
			defer c.Wake() // a finished operation may unblock queued ones
			defer c.end(op.ID)
			c.execute(ctx, op)
		}()
	}
	c.runMu.Lock()
	c.waitingOn = waitingOn
	c.runMu.Unlock()
	return started, nil
}

// appLookup returns a per-pass app lookup that loads every app with one query
// the first time it is needed, instead of one query per queued operation.
func (c *Controller) appLookup(ctx context.Context) func(string) (domain.App, error) {
	var apps map[string]domain.App
	var err error
	return func(id string) (domain.App, error) {
		if apps == nil && err == nil {
			var list []domain.App
			if list, err = store.ListApps(ctx, c.Store.DB()); err == nil {
				apps = make(map[string]domain.App, len(list))
				for _, a := range list {
					apps[a.ID] = a
				}
			}
		}
		if err != nil {
			return domain.App{}, err
		}
		a, ok := apps[id]
		if !ok {
			return a, store.ErrNotFound
		}
		return a, nil
	}
}

func (c *Controller) runningCount() int {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	return len(c.running)
}

// blocker returns the id of a running operation whose claims conflict with
// cl, or "".
func (c *Controller) blocker(cl claims) string {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	ids := make([]string, 0, len(c.running))
	for id := range c.running {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic answer when several conflict
	for _, id := range ids {
		if cl.conflicts(c.running[id]) {
			return id
		}
	}
	return ""
}

// WaitingOn returns the operation a queued operation is waiting behind, or ""
// when it is not blocked (it is next in line, waiting for a free slot, or no
// longer queued).
func (c *Controller) WaitingOn(id string) string {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	return c.waitingOn[id]
}

func (c *Controller) begin(id string, cl claims) {
	c.runMu.Lock()
	c.running[id] = cl
	c.runMu.Unlock()
}

func (c *Controller) end(id string) {
	c.runMu.Lock()
	delete(c.running, id)
	for app, w := range c.warming {
		if w.op == id {
			delete(c.warming, app)
		}
	}
	c.runMu.Unlock()
}

// setWarming records that op is starting app's instance and it is not ready.
func (c *Controller) setWarming(appID, opID string) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if w, ok := c.warming[appID]; ok && w.op == opID {
		return
	}
	c.warming[appID] = &warmState{op: opID}
}

// clearWarming records that app's instance became ready. It reports whether
// an edge apply left the app's route unavailable meanwhile.
func (c *Controller) clearWarming(appID string) (edgeStale bool) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if w, ok := c.warming[appID]; ok {
		edgeStale = w.edgeStale
		delete(c.warming, appID)
	}
	return edgeStale
}

// holdRoute reports whether app's route must render as unavailable because
// its instance is warming. forApply records that the render will be promoted,
// so the warming operation refreshes the edge once the app is ready.
func (c *Controller) holdRoute(appID string, forApply bool) bool {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	w, ok := c.warming[appID]
	if ok && forApply {
		w.edgeStale = true
	}
	return ok
}

func (c *Controller) anyWarming() bool {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	return len(c.warming) > 0
}

func (c *Controller) runningIDs() []string {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	ids := make([]string, 0, len(c.running))
	for id := range c.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Shutdown stops accepting work and waits up to timeout for the executing
// operations to reach completion. It never tears down the data plane.
func (c *Controller) Shutdown(timeout time.Duration) {
	c.stopping.Store(true)
	c.Wake()
	select {
	case <-c.done:
	case <-time.After(timeout):
		c.Log.Warn("shutdown timeout; running operations will be marked interrupted on next start", "ops", c.runningIDs())
	}
}

// Idle reports whether no operation is executing (tests).
func (c *Controller) Idle() bool { return c.runningCount() == 0 }

// execute runs an operation the dispatcher already claimed (marked running).
func (c *Controller) execute(ctx context.Context, op store.Operation) {
	db := c.Store.DB()
	c.notify(op.ID)
	run := &Run{c: c, Op: op}
	h := c.handlers[op.Kind]
	c.Log.Info("operation started", "op", op.ID, "kind", op.Kind, "target", op.TargetID)
	result, err := func() (res any, err error) {
		defer func() {
			if p := recover(); p != nil {
				err = Fail("internal", "Report this failure with the operation id.", "unexpected failure: %v", p)
			}
		}()
		return h(ctx, run)
	}()
	var raw json.RawMessage
	if result != nil {
		raw, _ = json.Marshal(result)
	}
	state, code, msg, guidance := store.OpSucceeded, "", "", ""
	var oe *OpError
	switch {
	case err == nil:
	case errors.Is(err, ErrCancelled):
		state, code, msg = store.OpCancelled, "cancelled", err.Error()
		guidance = "Effects completed before cancellation remain in place; inspect the target status."
	case errors.As(err, &oe):
		state, code, msg, guidance = store.OpFailed, oe.Code, oe.Message, oe.Guidance
	default:
		state, code, msg = store.OpFailed, "failed", err.Error()
		guidance = "Inspect the operation events and target status, then retry explicitly if appropriate."
	}
	if err != nil {
		_ = store.AppendEvent(context.WithoutCancel(ctx), db, op.ID, "error", msg)
		c.Log.Warn("operation failed", "op", op.ID, "kind", op.Kind, "code", code, "err", msg)
	} else {
		c.Log.Info("operation succeeded", "op", op.ID, "kind", op.Kind)
	}
	if ferr := store.FinishOperation(context.WithoutCancel(ctx), db, op.ID, state, raw, code, msg, guidance); ferr != nil {
		c.Log.Error("finish operation", "op", op.ID, "err", ferr)
	}
	c.notify(op.ID)
}

// Subscribe returns a channel of operation ids whose state changed.
func (c *Controller) Subscribe() (chan string, func()) {
	ch := make(chan string, 64)
	c.mu.Lock()
	c.listeners[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.listeners, ch)
		c.mu.Unlock()
	}
}

func (c *Controller) notify(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.listeners {
		select {
		case ch <- id:
		default:
		}
	}
}

// Run is the execution context handed to a handler.
type Run struct {
	c  *Controller
	Op store.Operation
	// uncancellable is set on a Run used for cleanup that must finish even
	// after cancellation (for example restarting apps an export stopped).
	uncancellable bool
}

// Uncancellable returns a view of r whose phases and waits ignore a
// cancellation request.
func (r *Run) Uncancellable() *Run {
	u := *r
	u.uncancellable = true
	return &u
}

// Phase records a new phase. Phase boundaries are the only points where a
// requested cancellation is honored.
func (r *Run) Phase(ctx context.Context, phase string) error {
	db := r.c.Store.DB()
	if r.Cancelled(ctx) {
		return ErrCancelled
	}
	r.Op.Phase = phase
	_ = store.SetPhase(ctx, db, r.Op.ID, phase)
	_ = store.AppendEvent(ctx, db, r.Op.ID, "info", "phase: "+phase)
	r.c.notify(r.Op.ID)
	return nil
}

// Cancelled reports whether cancellation was requested. Handlers may call it
// inside long waits that have no pending effects, such as readiness polling.
func (r *Run) Cancelled(ctx context.Context) bool {
	return !r.uncancellable && store.CancelRequested(ctx, r.c.Store.DB(), r.Op.ID)
}

func (r *Run) Info(ctx context.Context, format string, args ...any) {
	_ = store.AppendEvent(ctx, r.c.Store.DB(), r.Op.ID, "info", fmt.Sprintf(format, args...))
	r.c.notify(r.Op.ID)
}

func (r *Run) Warn(ctx context.Context, format string, args ...any) {
	_ = store.AppendEvent(ctx, r.c.Store.DB(), r.Op.ID, "warn", fmt.Sprintf(format, args...))
	r.c.notify(r.Op.ID)
}

// Decode unmarshals the operation request.
func (r *Run) Decode(out any) error { return json.Unmarshal(r.Op.Request, out) }
