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

	"github.com/khanhicetea/bento/apps/backend/internal/assets"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// ImageSpec fully determines one managed runtime image.
type ImageSpec struct {
	Key         domain.ImageKey
	ContextKind string
	Context     []byte
	Args        map[string]string
	Hash        string
}

func (s ImageSpec) Tag() string {
	return fmt.Sprintf("bento-runtime/%s:%s-%s", s.Key.Toolchain, s.Key.Version, s.Hash[:12])
}

// PlanImage computes the deterministic build inputs for a runtime key.
func PlanImage(key domain.ImageKey) (ImageSpec, error) {
	base, err := key.BaseImage()
	if err != nil {
		return ImageSpec{}, err
	}
	args := map[string]string{"DEBIAN_BASE": key.RuntimeDebianBase()}
	maps.Copy(args, domain.RuntimeArtifacts)
	kind := "http"
	if key.Kind == domain.RuntimePHP {
		kind = "php"
		args["PHP_BASE"] = base
	} else {
		args["TOOLCHAIN_BASE"] = base
		args["TOOLCHAIN"] = key.Toolchain + "-" + key.Version
		delete(args, "COMPOSER_VERSION")
		delete(args, "COMPOSER_SHA256")
	}
	ctxBytes, ctxHash, err := assets.BuildContext(kind)
	if err != nil {
		return ImageSpec{}, err
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var h bytes.Buffer
	h.WriteString(ctxHash)
	for _, k := range keys {
		fmt.Fprintf(&h, "\n%s=%s", k, args[k])
	}
	return ImageSpec{Key: key, ContextKind: kind, Context: ctxBytes, Args: args, Hash: platform.SHA256Hex(h.Bytes())}, nil
}

// ImageManager resolves managed images and caches their identity files.
// Operations run in parallel: builds of the same tag are serialized (one
// builds, the rest find it), builds of different tags run concurrently, and
// Remove excludes every Ensure.
type ImageManager struct {
	Engine docker.Engine
	Layout platform.Layout
	Names  Names

	prune sync.RWMutex // Ensure holds it shared, Remove exclusively
	mu    sync.Mutex   // guards tags
	tags  map[string]*sync.Mutex
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

// Ensure returns the image ID for key, building it through the Engine API if
// the deterministic tag is absent. It never shells out to the Docker CLI.
func (m *ImageManager) Ensure(ctx context.Context, key domain.ImageKey, progress func(string)) (string, ImageSpec, error) {
	spec, err := PlanImage(key)
	if err != nil {
		return "", spec, err
	}
	m.prune.RLock()
	defer m.prune.RUnlock()
	l := m.tagLock(spec.Tag())
	l.Lock()
	defer l.Unlock()
	if id, ok, err := m.Engine.ImageID(ctx, spec.Tag()); err != nil {
		return "", spec, err
	} else if ok {
		return id, spec, nil
	}
	if progress != nil {
		progress("building managed image " + spec.Tag())
	}
	labels := map[string]string{LabelManaged: "true", LabelImageKey: key.String(), "io.bento.context-hash": spec.Hash}
	id, err := m.Engine.BuildImage(ctx, spec.Tag(), bytes.NewReader(spec.Context), spec.Args, labels, progress)
	if err != nil {
		return "", spec, err
	}
	return id, spec, nil
}

// ErrImageNotPrunable reports an image that is missing, not Bento-built, or
// referenced by a container.
var ErrImageNotPrunable = errors.New("image is not prunable")

// Remove deletes an unused Bento-built image. It re-verifies under the build
// lock so it cannot race a concurrent Ensure; the Engine additionally refuses
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
	img, ok := PrunableImage(containers, images, id)
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
