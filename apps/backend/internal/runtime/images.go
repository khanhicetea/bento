package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/assets"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// ImageSpec fully determines the Bento-owned inputs of one managed runtime
// image: the build context and every build argument. Its bases are floating
// tags and some tools follow release channels, so building the same spec
// twice can produce different images; each build gets its own tag
// (BuildTag) and records what it resolved (Build.Manifest).
type ImageSpec struct {
	Key         domain.ImageKey
	ContextKind string
	Context     []byte
	Args        map[string]string
	Hash        string
}

// BuildTag names one build of the spec: inputs hash, then build time.
func (s ImageSpec) BuildTag(at time.Time) string {
	return fmt.Sprintf("bento-runtime/%s:%s-%s-%s", s.Key.Toolchain, s.Key.Version, s.Hash[:12],
		at.UTC().Format("20060102t150405.000"))
}

// Bases lists the floating base references a build of the spec pulls. The
// shared Valkey image (VALKEY_IMAGE) is local and built once, never pulled.
func (s ImageSpec) Bases() []string {
	var out []string
	for _, k := range []string{"DEBIAN_BASE", "PHP_BASE"} {
		if v := s.Args[k]; v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// PlanImage computes the Bento-owned build inputs for a runtime key.
func PlanImage(key domain.ImageKey) (ImageSpec, error) {
	base, err := key.BaseImage()
	if err != nil {
		return ImageSpec{}, err
	}
	tools, err := key.Tools()
	if err != nil {
		return ImageSpec{}, err
	}
	valkey, err := ValkeyImageSpec()
	if err != nil {
		return ImageSpec{}, err
	}
	// The Valkey image's tag is content-addressed, so its inputs are part of
	// this image's inputs.
	args := map[string]string{
		"DEBIAN_BASE": key.RuntimeDebianBase(), "VALKEY_IMAGE": valkey.ValkeyTag(), "MISE_TOOLS": tools,
	}
	maps.Copy(args, domain.RuntimeArtifacts)
	kind := "http"
	if key.Kind == domain.RuntimePHP {
		kind = "php"
		args["COMPOSER_CHANNEL"] = domain.ComposerChannel
		if minPkg, ok := key.SuryMinVersion(); ok {
			kind = "php-sury"
			args["PHP_VERSION"] = key.Version
			args["PHP_MIN_PKG_VERSION"] = minPkg
			args["SURY_KEY_SHA256"] = domain.SuryKeySHA256
		} else {
			args["PHP_BASE"] = base
			args["PHP_MIN_VERSION"] = domain.PHPMinVersions[key.Version]
		}
	} else {
		args["TOOLCHAIN"] = key.Toolchain + "-" + key.Version
	}
	ctxBytes, ctxHash, err := assets.BuildContext(kind)
	if err != nil {
		return ImageSpec{}, err
	}
	return ImageSpec{Key: key, ContextKind: kind, Context: ctxBytes, Args: args, Hash: inputsHash(ctxHash, args)}, nil
}

// inputsHash identifies a build context and its build arguments.
func inputsHash(ctxHash string, args map[string]string) string {
	var h bytes.Buffer
	h.WriteString(ctxHash)
	for _, k := range slices.Sorted(maps.Keys(args)) {
		fmt.Fprintf(&h, "\n%s=%s", k, args[k])
	}
	return platform.SHA256Hex(h.Bytes())
}

// standaloneSpec plans a non-runtime image from images-standalone/<name>.
func standaloneSpec(name string, args map[string]string) (ImageSpec, error) {
	ctxBytes, ctxHash, err := assets.StandaloneContext(name)
	if err != nil {
		return ImageSpec{}, err
	}
	return ImageSpec{ContextKind: name, Context: ctxBytes, Args: args, Hash: inputsHash(ctxHash, args)}, nil
}

// ValkeyImageSpec plans the shared Valkey image: the pinned source tarball
// compiled once on domain.ValkeyBuildBase and copied into every runtime image,
// so rebuilding a runtime image never compiles Valkey again.
func ValkeyImageSpec() (ImageSpec, error) {
	args := map[string]string{"VALKEY_BUILD_BASE": domain.ValkeyBuildBase}
	maps.Copy(args, domain.ValkeyArtifacts)
	return standaloneSpec("valkey", args)
}

// ValkeyTag is the deterministic tag of the shared Valkey image.
func (s ImageSpec) ValkeyTag() string {
	return "bento-build/valkey:" + s.Args["VALKEY_VERSION"] + "-" + s.Hash[:12]
}

// ManifestPath is the file in which every runtime image records the versions
// it resolved (templates/images/common/write-manifest.sh).
const ManifestPath = "/usr/local/share/bento-manifest"

// ManifestBasePrefix prefixes the manifest entries holding the local image
// id each floating base reference resolved to.
const ManifestBasePrefix = "base:"

// ParseManifest reads key=value lines; anything else is ignored.
func ParseManifest(raw []byte) map[string]string {
	out := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

// MaxConcurrentBuilds bounds runtime image builds running at once, whichever
// operation needs them: a build is CPU- and I/O-heavy and shares the host
// with the apps it will replace.
const MaxConcurrentBuilds = 2

// Builds run next to the apps and databases they rebuild for, and compile
// from source (PHP extensions, Valkey once). Unbounded, two builds on a small host took
// all of its memory: the host stopped serving and answering SSH. Each build
// step is therefore capped at a quarter of host memory (at least
// MinBuildMemory, so a 2 GiB host can still build), without swap, so a build
// that needs more fails alone, and runs at a quarter of the default CPU weight
// so the serving containers keep their share.
const (
	MinBuildMemory   = 1 << 30
	BuildCPUShares   = 256
	buildMemoryShare = 4
)

// ErrBuildMemory refuses a build on a host without room for it. Without swap,
// a host that runs out of memory thrashes for many minutes before the kernel
// kills something (on the arm64 test host: every app unreachable for 53
// minutes, then mysqld killed), so a build that may not fit is not started:
// it fails, apps keep serving on their current build, and the operation is
// retried later.
var ErrBuildMemory = errors.New("not enough free memory to build safely")

// BuildMemoryReserve is the available memory (free swap included) a build
// needs before it starts: room for a build at the cap's floor. The largest
// steps measured on the VPSes peak below it, page cache included (898 MiB on
// amd64, 766 MiB on arm64; with Python still compiled, one step reached the
// 1 GiB cap).
const BuildMemoryReserve = MinBuildMemory

// checkBuildMemory refuses a build when the host lacks BuildMemoryReserve.
func (m *ImageManager) checkBuildMemory() error {
	if m.MemAvailable == nil {
		return nil
	}
	if avail, ok := m.MemAvailable(); ok && avail < BuildMemoryReserve {
		return fmt.Errorf("%w: %d MiB available, a build needs %d MiB", ErrBuildMemory, avail>>20, BuildMemoryReserve>>20)
	}
	return nil
}

// BuildLimits adds the build resource limits to opts.
func BuildLimits(opts docker.BuildOptions) docker.BuildOptions {
	opts.Memory = MinBuildMemory
	if total, ok := platform.HostMemory(); ok && total/buildMemoryShare > opts.Memory {
		opts.Memory = total / buildMemoryShare
	}
	opts.CPUShares = BuildCPUShares
	return opts
}

// ImageManager builds managed images and caches their identity files.
// Operations run in parallel: builds of one runtime key are serialized
// (Hold), builds of different keys run concurrently up to
// MaxConcurrentBuilds, and Remove excludes every held key. Which build an app
// runs is recorded in the store, not here.
type ImageManager struct {
	Engine docker.Engine
	Layout platform.Layout
	Names  Names
	// MemAvailable reports the memory a build may use (free swap included);
	// nil skips the check (tests).
	MemAvailable func() (int64, bool)

	prune  sync.RWMutex // Hold holds it shared, Remove exclusively; RemoveForKey takes only the key lock
	mu     sync.Mutex   // guards tags and builds
	tags   map[string]*sync.Mutex
	builds chan struct{}
}

// buildSlots returns the semaphore that bounds concurrent builds.
func (m *ImageManager) buildSlots() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.builds == nil {
		m.builds = make(chan struct{}, MaxConcurrentBuilds)
	}
	return m.builds
}

func (m *ImageManager) tagLock(tag string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tags == nil {
		m.tags = map[string]*sync.Mutex{}
	}
	l, ok := m.tags[tag]
	if !ok {
		l = &sync.Mutex{}
		m.tags[tag] = l
	}
	return l
}

// Hold serializes everything that decides whether key needs a build: one
// caller builds while the others wait and then find its result. It excludes
// Remove until release is called.
func (m *ImageManager) Hold(key domain.ImageKey) (release func()) {
	m.prune.RLock()
	l := m.tagLock("key:" + key.String())
	l.Lock()
	return func() {
		l.Unlock()
		m.prune.RUnlock()
	}
}

// Build is one built runtime image.
type Build struct {
	Tag string
	ID  string
	// Manifest holds the tool versions the image recorded and, under
	// ManifestBasePrefix, the base image ids the build resolved.
	Manifest map[string]string
}

// Build builds spec under a new tag through the Engine API, always pulling its
// floating bases first. refresh also bypasses the layer cache, so every
// package install and download resolves again (an operator rebuild); the
// shared Valkey image is built only when missing. The caller holds the key
// (Hold).
func (m *ImageManager) Build(
	ctx context.Context,
	spec ImageSpec,
	at time.Time,
	refresh bool,
	progress func(string),
) (Build, error) {
	slots := m.buildSlots()
	select {
	case slots <- struct{}{}:
	default:
		if progress != nil {
			progress("waiting for a free image build slot")
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return Build{}, ctx.Err()
		}
	}
	defer func() { <-slots }()
	if err := m.checkBuildMemory(); err != nil {
		return Build{}, err
	}
	tag := spec.BuildTag(at)
	for {
		// A tag names exactly one build: never move an existing one.
		_, taken, err := m.Engine.ImageID(ctx, tag)
		if err != nil {
			return Build{}, err
		}
		if !taken {
			break
		}
		at = at.Add(time.Millisecond)
		tag = spec.BuildTag(at)
	}
	// The bases are pulled here rather than by the builder: a builder pull
	// also tries to pull the local Valkey image and fails.
	for _, ref := range spec.Bases() {
		if err := m.Engine.PullImage(ctx, ref, progress); err != nil {
			return Build{}, fmt.Errorf("pull %s: %w", ref, err)
		}
	}
	if tag := spec.Args["VALKEY_IMAGE"]; tag != "" {
		if err := m.ensureValkey(ctx, tag, progress); err != nil {
			return Build{}, err
		}
	}
	if progress != nil {
		progress("building managed image " + tag)
	}
	labels := map[string]string{LabelManaged: "true", LabelImageKey: spec.Key.String(), "io.bento.context-hash": spec.Hash}
	if m.Names.StackID != "" {
		// Builds are recorded per stack, so the image belongs to this one.
		labels[LabelStackID] = m.Names.StackID
	}
	id, err := m.Engine.BuildImage(ctx, tag, bytes.NewReader(spec.Context), spec.Args, labels,
		BuildLimits(docker.BuildOptions{NoCache: refresh}), progress)
	if err != nil {
		return Build{}, err
	}
	raw, err := m.Engine.ReadImageFile(ctx, id, ManifestPath, m.Names.Labels(RoleProbe, nil))
	if err != nil {
		return Build{}, fmt.Errorf("read image manifest: %w", err)
	}
	b := Build{Tag: tag, ID: id, Manifest: ParseManifest(raw)}
	for _, ref := range spec.Bases() {
		if baseID, ok, err := m.Engine.ImageID(ctx, ref); err != nil {
			return Build{}, err
		} else if ok {
			b.Manifest[ManifestBasePrefix+ref] = baseID
		}
	}
	return b, nil
}

// ensureValkey builds the shared Valkey image under tag unless it exists. It
// runs inside a runtime build (its slot, memory check and Hold), and builds
// of different keys wait for each other here, so Valkey compiles once.
func (m *ImageManager) ensureValkey(ctx context.Context, tag string, progress func(string)) error {
	spec, err := ValkeyImageSpec()
	if err != nil {
		return err
	}
	if spec.ValkeyTag() != tag {
		return fmt.Errorf("valkey image %s is not this release's %s", tag, spec.ValkeyTag())
	}
	l := m.tagLock(tag)
	l.Lock()
	defer l.Unlock()
	if _, ok, err := m.Engine.ImageID(ctx, tag); err != nil || ok {
		return err
	}
	if progress != nil {
		progress("building shared Valkey image " + tag)
	}
	// Like the backup image, it carries no stack label: stacks on one host
	// share it.
	labels := map[string]string{LabelManaged: "true", "io.bento.context-hash": spec.Hash}
	_, err = m.Engine.BuildImage(ctx, tag, bytes.NewReader(spec.Context), spec.Args, labels,
		BuildLimits(docker.BuildOptions{}), progress)
	return err
}

// ErrImageNotPrunable reports an image that is missing, not Bento-built, or
// referenced by a container.
var ErrImageNotPrunable = errors.New("image is not prunable")

// Remove deletes an unused Bento-built image. It re-verifies under the build
// lock so it cannot race a concurrent Hold; the Engine additionally refuses
// removal while any container references the image.
func (m *ImageManager) Remove(ctx context.Context, id string) (docker.ImageSummary, error) {
	m.prune.Lock()
	defer m.prune.Unlock()
	return m.removeLocked(ctx, id)
}

// RemoveForKey is Remove for a superseded build of one runtime key (the
// String form of a domain.ImageKey). It excludes only Hold of that key, so a
// sweep never waits for a build of another runtime, and a waiting sweep never
// stalls restic jobs or other builds behind the stack-wide lock. The Engine
// still refuses to remove an image a container references.
func (m *ImageManager) RemoveForKey(ctx context.Context, key, id string) (docker.ImageSummary, error) {
	l := m.tagLock("key:" + key)
	l.Lock()
	defer l.Unlock()
	return m.removeLocked(ctx, id)
}

func (m *ImageManager) removeLocked(ctx context.Context, id string) (docker.ImageSummary, error) {
	containers, err := m.Engine.List(ctx, nil)
	if err != nil {
		return docker.ImageSummary{}, err
	}
	images, err := m.Engine.ListImages(ctx)
	if err != nil {
		return docker.ImageSummary{}, err
	}
	img, ok := PrunableImage(containers, images, id, m.Names.StackID)
	if !ok {
		return img, ErrImageNotPrunable
	}
	return img, m.Engine.RemoveImage(ctx, img.ID, img.Tags)
}

type identityCache struct {
	Passwd string `json:"passwd"`
	Group  string `json:"group"`
}

// IdentityBase returns the image's own /etc/passwd and /etc/group, cached per
// image ID under the stack cache directory.
func (m *ImageManager) IdentityBase(ctx context.Context, imageID string) ([]byte, []byte, error) {
	safe := strings.NewReplacer(":", "_", "/", "_").Replace(imageID)
	cachePath := filepath.Join(m.Layout.CacheDir(), "image-identity-"+safe+".json")
	if raw, err := os.ReadFile(cachePath); err == nil {
		var c identityCache
		if json.Unmarshal(raw, &c) == nil && c.Passwd != "" {
			return []byte(c.Passwd), []byte(c.Group), nil
		}
	}
	passwd, err := m.Engine.ReadImageFile(ctx, imageID, "/etc/passwd", m.Names.Labels(RoleProbe, nil))
	if err != nil {
		return nil, nil, err
	}
	group, err := m.Engine.ReadImageFile(ctx, imageID, "/etc/group", m.Names.Labels(RoleProbe, nil))
	if err != nil {
		return nil, nil, err
	}
	// The cache only saves a probe container next time; a failed write is
	// harmless, and marshaling two strings cannot fail.
	raw, _ := json.Marshal(identityCache{Passwd: string(passwd), Group: string(group)})
	_ = platform.AtomicWrite(cachePath, raw, 0o600, platform.RootOwner)
	return passwd, group, nil
}

// ResticImageSpec plans the app backup image: the pinned rclone image with a
// pinned restic binary. Its tag is content-addressed like runtime images.
func ResticImageSpec() (ImageSpec, error) {
	args := map[string]string{"RCLONE_IMAGE": domain.RcloneImage}
	maps.Copy(args, domain.ResticArtifacts)
	return standaloneSpec("restic", args)
}

// ResticTag is the deterministic tag of the app backup image.
func (s ImageSpec) ResticTag() string {
	return "bento-backup/restic:" + s.Args["RESTIC_VERSION"] + "-" + s.Hash[:12]
}

// EnsureRestic returns the app backup image ID, building it once per tag.
func (m *ImageManager) EnsureRestic(ctx context.Context, progress func(string)) (string, string, error) {
	spec, err := ResticImageSpec()
	if err != nil {
		return "", "", err
	}
	tag := spec.ResticTag()
	m.prune.RLock()
	defer m.prune.RUnlock()
	l := m.tagLock(tag)
	l.Lock()
	defer l.Unlock()
	if id, ok, err := m.Engine.ImageID(ctx, tag); err != nil || ok {
		return id, tag, err
	}
	if progress != nil {
		progress("building backup image " + tag)
	}
	labels := map[string]string{LabelManaged: "true", "io.bento.context-hash": spec.Hash}
	id, err := m.Engine.BuildImage(ctx, tag, bytes.NewReader(spec.Context), spec.Args, labels,
		BuildLimits(docker.BuildOptions{}), progress)
	return id, tag, err
}
