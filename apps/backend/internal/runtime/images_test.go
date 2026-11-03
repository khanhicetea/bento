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

func (e *slowBuilds) BuildImage(ctx context.Context, tag string, r io.Reader, args, labels map[string]string,
	opts docker.BuildOptions, p func(string)) (string, error) {
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
	return e.Engine.BuildImage(ctx, tag, r, args, labels, opts, p)
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

// build is what a caller of Hold does: one build per key at a time.
func build(t *testing.T, m *ImageManager, k domain.ImageKey) Build {
	spec, err := PlanImage(k)
	if err != nil {
		t.Error(err)
		return Build{}
	}
	release := m.Hold(k)
	defer release()
	b, err := m.Build(t.Context(), spec, time.Now(), false, nil)
	if err != nil {
		t.Error(err)
	}
	return b
}

func TestHoldSerializesOneKeyAndBuildsKeysConcurrently(t *testing.T) {
	eng := &slowBuilds{Engine: docker.NewFake(), builds: map[string]int{}}
	m := &ImageManager{Engine: eng}
	var wg sync.WaitGroup
	tags := sync.Map{}
	for range 3 {
		for _, k := range []domain.ImageKey{phpKey, nodeKey} {
			wg.Go(func() {
				b := build(t, m, k)
				if _, dup := tags.LoadOrStore(b.Tag, true); dup {
					t.Errorf("tag %s built twice", b.Tag)
				}
			})
		}
	}
	wg.Wait()
	if eng.peak.Load() != 2 {
		t.Fatalf("peak concurrent builds %d, want 2 (one per key)", eng.peak.Load())
	}
}

func TestRemoveExcludesHold(t *testing.T) {
	eng := &slowBuilds{Engine: docker.NewFake(), builds: map[string]int{}}
	m := &ImageManager{Engine: eng}
	var wg sync.WaitGroup
	for _, k := range []domain.ImageKey{phpKey, nodeKey} {
		wg.Go(func() { build(t, m, k) })
		wg.Go(func() { m.Remove(t.Context(), "sha256:none") })
	}
	wg.Wait()
	if eng.overlapRemove.Load() {
		t.Fatal("Remove ran while a build was in progress")
	}
}

func TestBuildRecordsManifestAndBases(t *testing.T) {
	fake := docker.NewFake()
	fake.ImageFiles = map[string][]byte{ManifestPath: []byte("php=8.4.30\ncomposer=2.9.1\nbogus\n")}
	fake.SetImage(domain.PHPVersions["8.4"], "sha256:php-base")
	m := &ImageManager{Engine: fake}
	spec, err := PlanImage(phpKey)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 3, 4, 5, 6, 0, time.UTC)
	fake.SetImage(spec.BuildTag(at), "sha256:taken") // an existing tag is never moved
	b, err := m.Build(t.Context(), spec, at, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Tag == spec.BuildTag(at) {
		t.Fatal("build reused an existing tag")
	}
	if b.Manifest["php"] != "8.4.30" || b.Manifest["composer"] != "2.9.1" ||
		b.Manifest[ManifestBasePrefix+domain.PHPVersions["8.4"]] != "sha256:php-base" || len(b.Manifest) != 3 {
		t.Fatalf("manifest %v", b.Manifest)
	}
	if got := fake.Builds[len(fake.Builds)-1]; !got.Pull || !got.NoCache {
		t.Fatalf("a refresh build must pull and bypass the cache: %+v", got)
	}
}
