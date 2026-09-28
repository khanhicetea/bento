// Package operations plans and executes every runtime-changing mutation as a
// durable, journaled operation. HTTP handlers and CLI commands only validate,
// persist intent, and submit; the single executor performs external effects.
package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
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
	// PollInterval is the readiness poll cadence.
	PollInterval time.Duration
	// UtilsAppsPort is the port of the backend's utils listener on the apps
	// network gateway (0 when not listening there). The edge proxies
	// /_bento/webhook/* to it.
	UtilsAppsPort int
}

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
	current  atomic.Value // string op id
	mu       sync.Mutex
	// listeners receive operation state change notifications (SSE).
	listeners map[chan string]struct{}
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
	if d.PollInterval == 0 {
		d.PollInterval = 2 * time.Second
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
	}
	c.current.Store("")
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
		for {
			if c.stopping.Load() || ctx.Err() != nil {
				return
			}
			op, err := store.NextQueued(ctx, c.Store.DB())
			if errors.Is(err, store.ErrNotFound) {
				select {
				case <-c.wake:
				case <-time.After(5 * time.Second):
				case <-ctx.Done():
					return
				}
				continue
			}
			if err != nil {
				c.Log.Error("dequeue operation", "err", err)
				time.Sleep(time.Second)
				continue
			}
			c.execute(ctx, op)
		}
	}()
}

// Shutdown stops accepting work and waits up to timeout for the current
// operation to reach completion. It never tears down the data plane.
func (c *Controller) Shutdown(timeout time.Duration) {
	c.stopping.Store(true)
	c.Wake()
	select {
	case <-c.done:
	case <-time.After(timeout):
		c.Log.Warn("shutdown timeout; current operation will be marked interrupted on next start", "op", c.current.Load())
	}
}

// Idle reports whether no operation is executing (tests).
func (c *Controller) Idle() bool { return c.current.Load() == "" }

func (c *Controller) execute(ctx context.Context, op store.Operation) {
	db := c.Store.DB()
	if err := store.MarkRunning(ctx, db, op.ID); err != nil {
		c.Log.Error("mark running", "op", op.ID, "err", err)
		return
	}
	c.current.Store(op.ID)
	defer c.current.Store("")
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
}

// Phase records a new phase. Phase boundaries are the only points where a
// requested cancellation is honored.
func (r *Run) Phase(ctx context.Context, phase string) error {
	db := r.c.Store.DB()
	if store.CancelRequested(ctx, db, r.Op.ID) {
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
	return store.CancelRequested(ctx, r.c.Store.DB(), r.Op.ID)
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
