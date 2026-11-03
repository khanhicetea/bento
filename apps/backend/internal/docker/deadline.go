package docker

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Deadlines for Engine requests. Without them a wedged dockerd, registry or
// build blocks the calling operation, and every operation queued behind it,
// until the backend restarts. They are variables only so tests can shorten
// them.
var (
	// APITimeout bounds one request/response call (inspect, create, start,
	// remove, list, ...). Stop adds the container's own stop timeout.
	APITimeout = 2 * time.Minute
	// PullStallTimeout aborts a pull that reports no progress this long;
	// PullTimeout bounds the whole pull.
	PullStallTimeout = 5 * time.Minute
	PullTimeout      = time.Hour
	// BuildStallTimeout aborts a build that prints nothing this long (a
	// quiet compile step can run for minutes); BuildTimeout bounds it all.
	BuildStallTimeout = 20 * time.Minute
	BuildTimeout      = 2 * time.Hour
)

// Calls with no deadline of their own are bounded only by their context:
// Wait and Exec (jobs that legitimately run for hours, ended by operation
// cancellation), and the streams Logs, Events, ExecAttach and CopyFrom.

// ErrEngineTimeout marks an Engine request abandoned at its deadline.
var ErrEngineTimeout = errors.New("docker engine request timed out")

type timeoutError struct {
	what    string
	after   time.Duration
	stalled bool
}

func (e *timeoutError) Error() string {
	if e.stalled {
		return fmt.Sprintf("docker %s made no progress for %s", e.what, e.after)
	}
	return fmt.Sprintf("docker %s did not finish within %s", e.what, e.after)
}

func (e *timeoutError) Unwrap() error { return ErrEngineTimeout }

// bound gives one request/response call a deadline of d. The returned finish
// must be deferred with the call's error: it releases the context and names
// the deadline in the error when that is what ended the call.
func bound(ctx context.Context, d time.Duration, what string) (context.Context, func(*error)) {
	ctx, cancel := context.WithTimeoutCause(ctx, d, &timeoutError{what: what, after: d})
	return ctx, func(errp *error) {
		explain(ctx, errp)
		cancel()
	}
}

// progressBound is bound for streaming calls that report progress: the
// context ends when touch is not called for idle, or after total.
func progressBound(
	ctx context.Context,
	idle, total time.Duration,
	what string,
) (context.Context, func(), func(*error)) {
	ctx, cancelTotal := context.WithTimeoutCause(ctx, total, &timeoutError{what: what, after: total})
	ctx, cancel := context.WithCancelCause(ctx)
	stall := time.AfterFunc(idle, func() { cancel(&timeoutError{what: what, after: idle, stalled: true}) })
	touch := func() { stall.Reset(idle) }
	return ctx, touch, func(errp *error) {
		stall.Stop()
		explain(ctx, errp)
		cancel(nil)
		cancelTotal()
	}
}

func explain(ctx context.Context, errp *error) {
	if errp == nil || *errp == nil || ctx.Err() == nil {
		return
	}
	if cause := context.Cause(ctx); errors.Is(cause, ErrEngineTimeout) && !errors.Is(*errp, ErrEngineTimeout) {
		*errp = fmt.Errorf("%w: %v", cause, *errp)
	}
}
