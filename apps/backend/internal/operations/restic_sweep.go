package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// stalePendingKeyAge is how old an unclaimed pending key file must be before
// the startup sweep removes it: no operation names it, so nothing says it is
// still needed (for example a submission that failed after writing the key).
const stalePendingKeyAge = 24 * time.Hour

// SweepPendingResticKeys removes pending restic key files
// (secrets/restic/<app>.pending-<hex>.key) that no queued or running operation
// can still consume: those named by a finished, failed, cancelled or
// interrupted operation, and unclaimed ones older than 24 hours. Adopted app
// keys (<app>.key) never match the pending name and are never touched. It
// returns the number of files removed.
func (c *Controller) SweepPendingResticKeys(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(c.resticKeyDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var pending []string
	for _, e := range entries {
		if e.Type().IsRegular() && pendingKeyName.MatchString(e.Name()) {
			pending = append(pending, e.Name())
		}
	}
	if len(pending) == 0 {
		return 0, nil
	}
	ops, err := store.ListOperations(ctx, c.Store.DB(), store.OpFilter{Limit: 500})
	if err != nil {
		return 0, err
	}
	active, finished := map[string]bool{}, map[string]bool{}
	for _, name := range pending {
		for _, o := range ops {
			if !strings.Contains(string(o.Request), name) {
				continue
			}
			if o.State == store.OpQueued || o.State == store.OpRunning {
				active[name] = true
			} else {
				finished[name] = true
			}
		}
	}
	removed := 0
	for _, name := range pending {
		if active[name] {
			continue
		}
		if !finished[name] {
			info, err := os.Stat(filepath.Join(c.resticKeyDir(), name))
			if err != nil || time.Since(info.ModTime()) < stalePendingKeyAge {
				continue
			}
		}
		if err := os.Remove(filepath.Join(c.resticKeyDir(), name)); err != nil && !os.IsNotExist(err) {
			c.Log.Warn("remove stale pending restic key", "file", name, "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		c.Log.Info("removed stale pending restic keys", "count", removed)
	}
	return removed, nil
}
