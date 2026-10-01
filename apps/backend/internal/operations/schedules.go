package operations

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// scheduleParser reads standard 5-field cron expressions plus descriptors.
var scheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// scheduleTick is how often due schedules are evaluated.
const scheduleTick = 30 * time.Second

// lateGrace is how late a slot may be picked up before it counts as missed
// while the backend was unavailable.
const lateGrace = 10 * time.Minute

// scheduleKind defines how the scheduler handles one schedule kind. The
// scheduler never performs work itself: Submit queues a durable operation.
type scheduleKind struct {
	// CatchUp runs a slot missed while the backend was down once on
	// startup; otherwise it is recorded as missed and skipped.
	CatchUp bool
	Submit  func(ctx context.Context, c *Controller, s store.Schedule) (store.Operation, error)
}

var scheduleKinds = map[string]scheduleKind{
	"backup":     {Submit: submitScheduledBackup},
	"app-backup": {Submit: submitScheduledResticBackup},
}

// NextRun is the first slot of s after now, or zero when it is disabled or
// invalid. Cron fields are wall-clock time in now's location (the server's
// local time zone, as with crontab).
func NextRun(s store.Schedule, now time.Time) time.Time {
	if !s.Enabled {
		return time.Time{}
	}
	sched, err := scheduleParser.Parse(s.Cron)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(now)
}

// RunSchedules evaluates every enabled schedule each scheduleTick until ctx
// ends.
func (c *Controller) RunSchedules(ctx context.Context) {
	tick := time.NewTicker(scheduleTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		c.checkSchedules(ctx, time.Now())
	}
}

func (c *Controller) checkSchedules(ctx context.Context, now time.Time) {
	list, err := store.ListSchedules(ctx, c.Store.DB(), true)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			c.Log.Warn("list schedules", "err", err)
		}
		return
	}
	for _, s := range list {
		c.safeCheckSchedule(ctx, s, now)
	}
}

// safeCheckSchedule keeps a panic in one schedule from stopping the loop or
// the others.
func (c *Controller) safeCheckSchedule(ctx context.Context, s store.Schedule, now time.Time) {
	defer func() {
		if p := recover(); p != nil {
			c.Log.Error("schedule check panicked", "schedule", s.ID, "panic", p, "stack", string(debug.Stack()))
		}
	}()
	c.checkSchedule(ctx, s, now)
}

// checkSchedule submits at most one operation for the latest slot of s at or
// before now. Slots are computed in now's location: LastSlot is stored in
// UTC and must be converted back, or cron fields would be read as UTC
// wall-clock.
func (c *Controller) checkSchedule(ctx context.Context, s store.Schedule, now time.Time) {
	kind, ok := scheduleKinds[s.Kind]
	if !ok {
		// Unknown kinds (for example written by a newer Bento) are left alone.
		return
	}
	sched, err := scheduleParser.Parse(s.Cron)
	if err != nil {
		return
	}
	last := platform.ParseTime(s.LastSlot)
	if last.IsZero() {
		last = now
	}
	due := sched.Next(last.In(now.Location()))
	if due.After(now) {
		if s.LastSlot == "" {
			s.LastSlot = platform.FormatTime(now)
			c.saveSchedule(ctx, s)
		}
		return
	}
	// Count slots missed beyond the most recent one; run only once.
	var missed int
	for next := sched.Next(due); !next.After(now) && missed < 10000; next = sched.Next(next) {
		missed++
		due = next
	}
	s.Missed += missed
	s.LastSlot = platform.FormatTime(due)
	if now.Sub(due) > lateGrace && !kind.CatchUp {
		// The backend was down across this slot: record, do not replay.
		s.Missed++
		s.LastState = "missed"
		c.saveSchedule(ctx, s)
		c.Log.Warn("scheduled slot missed while backend was unavailable", "schedule", s.ID, "slot", due)
		return
	}
	if c.scheduleBusy(ctx, s) {
		// Never stack runs: the previous one is still queued or running.
		s.Missed++
		s.LastState = "skipped"
		c.saveSchedule(ctx, s)
		c.Log.Warn("scheduled slot skipped; previous run still active", "schedule", s.ID, "op", s.LastOpID)
		return
	}
	s.LastRunAt = platform.FormatTime(now)
	op, err := kind.Submit(ctx, c, s)
	if err != nil {
		s.LastState = "submit-failed"
		c.Log.Warn("scheduled submit failed", "schedule", s.ID, "err", err)
	} else {
		s.LastState, s.LastOpID = "submitted", op.ID
	}
	c.saveSchedule(ctx, s)
}

// scheduleBusy reports whether the schedule's last operation is still active.
func (c *Controller) scheduleBusy(ctx context.Context, s store.Schedule) bool {
	if s.LastOpID == "" {
		return false
	}
	op, err := store.GetOperation(ctx, c.Store.DB(), s.LastOpID)
	if err != nil {
		// Pruned or unreadable history never blocks a schedule.
		return false
	}
	return !op.State.Terminal()
}

// saveSchedule persists bookkeeping. It runs on the background ticker, so a
// failure is logged rather than returned; a concurrent edit wins.
func (c *Controller) saveSchedule(ctx context.Context, s store.Schedule) {
	if _, err := store.SaveScheduleState(ctx, c.Store.DB(), s); err != nil && !errors.Is(err, context.Canceled) {
		c.Log.Warn("schedule state", "schedule", s.ID, "err", err)
	}
}
