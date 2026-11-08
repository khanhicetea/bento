package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
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
	baseID, _, _ := fake.ImageID(t.Context(), domain.PHPVersions["8.4"])
	if b.Manifest["php"] != "8.4.30" || b.Manifest["composer"] != "2.9.1" ||
		b.Manifest[ManifestBasePrefix+domain.PHPVersions["8.4"]] != baseID || len(b.Manifest) != 2+len(spec.Bases()) {
		t.Fatalf("manifest %v", b.Manifest)
	}
	// Bento pulls the bases itself: a builder pull would also try to pull the
	// local Valkey image and fail.
	for _, ref := range spec.Bases() {
		if fake.CallCount("PullImage "+ref) != 1 {
			t.Fatalf("base %s not pulled: %v", ref, fake.Calls)
		}
	}
	got := fake.Builds[len(fake.Builds)-1]
	if got.Pull || !got.NoCache {
		t.Fatalf("a refresh build must bypass the cache without a builder pull: %+v", got)
	}
	// A build must not be able to take the host's memory or CPU from the
	// serving containers.
	if got.Memory < MinBuildMemory || got.CPUShares != BuildCPUShares {
		t.Fatalf("build without resource limits: %+v", got)
	}
	if total, ok := platform.HostMemory(); ok && total/4 > MinBuildMemory && got.Memory != total/4 {
		t.Fatalf("build memory %d, want a quarter of %d", got.Memory, total)
	}
}

// A host without room for a build refuses it before anything runs: a build
// that pushes a host without swap out of memory stops it from serving.
func TestBuildRefusedWithoutFreeMemory(t *testing.T) {
	fake := docker.NewFake()
	m := &ImageManager{Engine: fake}
	spec, err := PlanImage(phpKey)
	if err != nil {
		t.Fatal(err)
	}
	m.MemAvailable = func() (int64, bool) { return BuildMemoryReserve - 1, true }
	if _, err := m.Build(t.Context(), spec, time.Now(), false, nil); !errors.Is(err, ErrBuildMemory) {
		t.Fatalf("build on a full host: %v", err)
	}
	if len(fake.Builds) != 0 {
		t.Fatal("a refused build reached the engine")
	}
	m.MemAvailable = func() (int64, bool) { return BuildMemoryReserve, true }
	if _, err := m.Build(t.Context(), spec, time.Now(), false, nil); err != nil {
		t.Fatalf("build with room: %v", err)
	}
}

// Valkey compiles once for every runtime and every rebuild: runtime builds,
// refresh builds included, copy the shared image and build it only when it is
// missing.
func TestValkeyImageBuiltOnce(t *testing.T) {
	fake := docker.NewFake()
	m := &ImageManager{Engine: fake}
	php, err := PlanImage(phpKey)
	if err != nil {
		t.Fatal(err)
	}
	node, err := PlanImage(nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	valkey, err := ValkeyImageSpec()
	if err != nil {
		t.Fatal(err)
	}
	tag := valkey.ValkeyTag()
	if php.Args["VALKEY_IMAGE"] != tag || node.Args["VALKEY_IMAGE"] != tag {
		t.Fatalf("runtimes copy %q and %q, want %q", php.Args["VALKEY_IMAGE"], node.Args["VALKEY_IMAGE"], tag)
	}
	for _, s := range []ImageSpec{php, node, php} {
		if _, err := m.Build(t.Context(), s, time.Now(), true, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := fake.CallCount("BuildImage " + tag); n != 1 {
		t.Fatalf("valkey built %d times, want 1", n)
	}
	if id, _, _ := fake.ImageID(t.Context(), tag); fake.ImageLabels[id][LabelStackID] != "" {
		t.Fatal("the shared valkey image carries a stack label")
	}
	fake.SetImage(tag, "") // pruned
	if _, err := m.Build(t.Context(), node, time.Now(), false, nil); err != nil {
		t.Fatal(err)
	}
	if n := fake.CallCount("BuildImage " + tag); n != 2 {
		t.Fatalf("a pruned valkey image was not rebuilt (%d builds)", n)
	}
}
