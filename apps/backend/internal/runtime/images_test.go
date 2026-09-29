package runtime

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// slowBuilds records how many builds run at once and per tag.
type slowBuilds struct {
	docker.Engine
	inflight, peak atomic.Int32
	mu             sync.Mutex
	builds         map[string]int
	removing       atomic.Bool
	overlapRemove  atomic.Bool
}

func (e *slowBuilds) BuildImage(ctx context.Context, tag string, r io.Reader, args, labels map[string]string, p func(string)) (string, error) {
	n := e.inflight.Add(1)
	for p := e.peak.Load(); n > p && !e.peak.CompareAndSwap(p, n); p = e.peak.Load() {
	}
	if e.removing.Load() {
		e.overlapRemove.Store(true)
	}
	e.mu.Lock()
	e.builds[tag]++
	e.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	defer e.inflight.Add(-1)
	return e.Engine.BuildImage(ctx, tag, r, args, labels, p)
}

func (e *slowBuilds) ListImages(ctx context.Context) ([]docker.ImageSummary, error) {
	e.removing.Store(true)
	defer e.removing.Store(false)
	if e.inflight.Load() > 0 {
		e.overlapRemove.Store(true)
	}
	time.Sleep(30 * time.Millisecond)
	return e.Engine.ListImages(ctx)
}

var (
	phpKey  = domain.ImageKey{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.4"}
	nodeKey = domain.ImageKey{Kind: domain.RuntimeHTTP, Toolchain: "node", Version: "24"}
)

func TestEnsureBuildsDifferentTagsConcurrentlyAndEachTagOnce(t *testing.T) {
	eng := &slowBuilds{Engine: docker.NewFake(), builds: map[string]int{}}
	m := &ImageManager{Engine: eng}
	var wg sync.WaitGroup
	for range 3 {
		for _, k := range []domain.ImageKey{phpKey, nodeKey} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, _, err := m.Ensure(context.Background(), k, nil); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	if eng.peak.Load() != 2 {
		t.Fatalf("peak concurrent builds %d, want 2 (one per tag)", eng.peak.Load())
	}
	for tag, n := range eng.builds {
		if n != 1 {
			t.Fatalf("tag %s built %d times", tag, n)
		}
	}
}

func TestRemoveExcludesEnsure(t *testing.T) {
	eng := &slowBuilds{Engine: docker.NewFake(), builds: map[string]int{}}
	m := &ImageManager{Engine: eng}
	var wg sync.WaitGroup
	for _, k := range []domain.ImageKey{phpKey, nodeKey} {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.Ensure(context.Background(), k, nil)
		}()
		go func() {
			defer wg.Done()
			m.Remove(context.Background(), "sha256:none")
		}()
	}
	wg.Wait()
	if eng.overlapRemove.Load() {
		t.Fatal("Remove ran while a build was in progress")
	}
}
