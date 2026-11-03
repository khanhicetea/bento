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

// Bases lists the floating base references a build of the spec pulls.
func (s ImageSpec) Bases() []string {
	var out []string
	for _, k := range []string{"DEBIAN_BASE", "PHP_BASE", "VALKEY_BUILD_BASE"} {
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
	args := map[string]string{
		"DEBIAN_BASE": key.RuntimeDebianBase(), "VALKEY_BUILD_BASE": domain.ValkeyBuildBase, "MISE_TOOLS": tools,
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
	keys := slices.Sorted(maps.Keys(args))
	var h bytes.Buffer
	h.WriteString(ctxHash)
	for _, k := range keys {
		fmt.Fprintf(&h, "\n%s=%s", k, args[k])
	}
	return ImageSpec{Key: key, ContextKind: kind, Context: ctxBytes, Args: args, Hash: platform.SHA256Hex(h.Bytes())}, nil
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

// ImageManager builds managed images and caches their identity files.
// Operations run in parallel: builds of one runtime key are serialized
// (Hold), builds of different keys run concurrently up to
// MaxConcurrentBuilds, and Remove excludes every held key. Which build an app
// runs is recorded in the store, not here.
type ImageManager struct {
	Engine docker.Engine
	Layout platform.Layout
	Names  Names

	prune  sync.RWMutex // Hold holds it shared, Remove exclusively
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
// floating bases. refresh also bypasses the layer cache, so every package
// install and download resolves again (an operator rebuild). The caller holds
// the key (Hold).
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
	if progress != nil {
		progress("building managed image " + tag)
	}
	labels := map[string]string{LabelManaged: "true", LabelImageKey: spec.Key.String(), "io.bento.context-hash": spec.Hash}
	if m.Names.StackID != "" {
		// Builds are recorded per stack, so the image belongs to this one.
		labels[LabelStackID] = m.Names.StackID
	}
	id, err := m.Engine.BuildImage(ctx, tag, bytes.NewReader(spec.Context), spec.Args, labels,
		docker.BuildOptions{Pull: true, NoCache: refresh}, progress)
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

// ErrImageNotPrunable reports an image that is missing, not Bento-built, or
// referenced by a container.
var ErrImageNotPrunable = errors.New("image is not prunable")

// Remove deletes an unused Bento-built image. It re-verifies under the build
// lock so it cannot race a concurrent Hold; the Engine additionally refuses
// removal while any container references the image.
func (m *ImageManager) Remove(ctx context.Context, id string) (docker.ImageSummary, error) {
	m.prune.Lock()
	defer m.prune.Unlock()
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
	ctxBytes, ctxHash, err := assets.StandaloneContext("restic")
	if err != nil {
		return ImageSpec{}, err
	}
	args := map[string]string{"RCLONE_IMAGE": domain.RcloneImage}
	maps.Copy(args, domain.ResticArtifacts)
	keys := slices.Sorted(maps.Keys(args))
	var h bytes.Buffer
	h.WriteString(ctxHash)
	for _, k := range keys {
		fmt.Fprintf(&h, "\n%s=%s", k, args[k])
	}
	return ImageSpec{ContextKind: "restic", Context: ctxBytes, Args: args, Hash: platform.SHA256Hex(h.Bytes())}, nil
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
		docker.BuildOptions{}, progress)
	return id, tag, err
}
