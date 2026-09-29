package runtime

import (
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
)

// Ownership classifies a host Docker resource relative to this stack.
type Ownership string

const (
	OwnedStack      Ownership = "stack"       // labelled for this stack, or used by its containers
	OwnedOtherStack Ownership = "other-stack" // labelled for, or used by, another Bento stack
	OwnedForeign    Ownership = "foreign"     // not Bento's; view-only
)

type InventoryImage struct {
	docker.ImageSummary
	Built     bool // built by Bento (managed label), as opposed to pulled
	Ownership Ownership
	UsedBy    []string
	// Prunable: Bento-built and referenced by no container at all (any stack,
	// or unmanaged). Removal is safe because Ensure rebuilds by tag on demand.
	Prunable bool
}

type InventoryVolume struct {
	docker.VolumeInfo
	Service   string
	Ownership Ownership
	UsedBy    []string
}

type InventoryNetwork struct {
	docker.NetworkSummary
	Ownership Ownership
	UsedBy    []string
}

type Inventory struct {
	Images   []InventoryImage
	Volumes  []InventoryVolume
	Networks []InventoryNetwork
}

// usage records which consumers of each stack reference a resource.
type usage struct {
	stack, other map[string][]string
}

func newUsage() usage { return usage{stack: map[string][]string{}, other: map[string][]string{}} }

func (u usage) add(key, who string, ours bool) {
	m := u.other
	if ours {
		m = u.stack
	}
	if !slices.Contains(m[key], who) {
		m[key] = append(m[key], who)
	}
}

// ClassifyInventory attributes host images, volumes, and networks to this
// stack from labels and from what this stack's containers reference. appSlugs
// maps app IDs to display slugs. It is read-only: nothing here authorizes a
// destructive action.
func (n Names) ClassifyInventory(
	containers []container.Summary,
	images []docker.ImageSummary,
	volumes []docker.VolumeInfo,
	networks []docker.NetworkSummary,
	appSlugs map[string]string,
) Inventory {
	imgs, vols, nets := newUsage(), newUsage(), newUsage()
	anyUse := map[string]bool{}
	for _, c := range containers {
		anyUse[c.ImageID] = true
		if c.Labels[LabelManaged] != "true" {
			continue
		}
		ours := c.Labels[LabelStackID] == n.StackID
		who := strings.TrimPrefix(firstName(c), "/")
		if ours {
			who = consumer(c, appSlugs)
		}
		if c.ImageID != "" {
			imgs.add(c.ImageID, who, ours)
		}
		for _, m := range c.Mounts {
			if m.Type == mount.TypeVolume && m.Name != "" {
				vols.add(m.Name, who, ours)
			}
		}
		if c.NetworkSettings != nil {
			for name := range c.NetworkSettings.Networks {
				nets.add(name, who, ours)
			}
		}
	}

	var inv Inventory
	for _, i := range images {
		built := i.Labels[LabelManaged] == "true"
		inv.Images = append(inv.Images, InventoryImage{ImageSummary: i, Built: built,
			Ownership: n.classify(nil, built, imgs, i.ID), UsedBy: imgs.stack[i.ID], Prunable: built && !anyUse[i.ID]})
	}
	for _, v := range volumes {
		inv.Volumes = append(inv.Volumes, InventoryVolume{VolumeInfo: v, Service: v.Labels[LabelService],
			Ownership: n.classify(v.Labels, false, vols, v.Name), UsedBy: vols.stack[v.Name]})
	}
	for _, nw := range networks {
		inv.Networks = append(inv.Networks, InventoryNetwork{NetworkSummary: nw,
			Ownership: n.classify(nw.Labels, false, nets, nw.Name), UsedBy: nets.stack[nw.Name]})
	}
	return inv
}

// classify prefers explicit stack labels, then observed use. A Bento-built
// image carries no stack label (images may be shared between stacks), so an
// unused one is attributed to this stack.
func (n Names) classify(labels map[string]string, builtImage bool, u usage, key string) Ownership {
	if labels[LabelManaged] == "true" && labels[LabelStackID] != "" {
		if labels[LabelStackID] == n.StackID {
			return OwnedStack
		}
		return OwnedOtherStack
	}
	switch {
	case len(u.stack[key]) > 0:
		return OwnedStack
	case len(u.other[key]) > 0:
		return OwnedOtherStack
	case builtImage:
		return OwnedStack
	}
	return OwnedForeign
}

// PrunableImage reports whether id may be removed by an image prune: the
// image exists, was built by Bento, and no container references it.
func PrunableImage(
	containers []container.Summary,
	images []docker.ImageSummary,
	id string,
) (docker.ImageSummary, bool) {
	for _, i := range images {
		if i.ID != id {
			continue
		}
		if i.Labels[LabelManaged] != "true" {
			return i, false
		}
		for _, c := range containers {
			if c.ImageID == id {
				return i, false
			}
		}
		return i, true
	}
	return docker.ImageSummary{}, false
}

func consumer(c container.Summary, appSlugs map[string]string) string {
	if id := c.Labels[LabelAppID]; id != "" {
		if slug := appSlugs[id]; slug != "" {
			return slug
		}
		return "app " + id
	}
	if s := c.Labels[LabelService]; s != "" {
		return s
	}
	if r := c.Labels[LabelRole]; r != "" {
		return r
	}
	return strings.TrimPrefix(firstName(c), "/")
}

func firstName(c container.Summary) string {
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return c.ID
}
