package runtime

import (
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
)

func TestClassifyInventory(t *testing.T) {
	n := Names{StackID: "s1", StackName: "prod"}
	other := Names{StackID: "s2", StackName: "stage"}
	nets := func(names ...string) *container.NetworkSettingsSummary {
		m := map[string]*network.EndpointSettings{}
		for _, name := range names {
			m[name] = &network.EndpointSettings{}
		}
		return &container.NetworkSettingsSummary{Networks: m}
	}
	containers := []container.Summary{
		{Names: []string{"/bento-prod-app-a1"}, ImageID: "sha256:built", Labels: n.Labels(RoleRuntime, map[string]string{LabelAppID: "a1"}),
			NetworkSettings: nets("bento-prod-apps")},
		{Names: []string{"/bento-prod-mysql84"}, ImageID: "sha256:mysql", Labels: n.Labels(RoleDatabase, map[string]string{LabelService: "mysql84"}),
			Mounts:          []container.MountPoint{{Type: mount.TypeVolume, Name: "bento-prod-mysql84-data"}, {Type: mount.TypeBind, Source: "/srv"}},
			NetworkSettings: nets("bento-prod-data")},
		{Names: []string{"/bento-stage-redis"}, ImageID: "sha256:redis", Labels: other.Labels(RoleCache, nil), NetworkSettings: nets("bento-stage-data")},
		{Names: []string{"/unrelated"}, ImageID: "sha256:nginx", NetworkSettings: nets("bridge")},
	}
	images := []docker.ImageSummary{
		{ID: "sha256:built", Labels: map[string]string{LabelManaged: "true"}},
		{ID: "sha256:oldbuild", Labels: map[string]string{LabelManaged: "true"}},
		{ID: "sha256:mysql"},
		{ID: "sha256:redis"},
		{ID: "sha256:nginx"},
	}
	volumes := []docker.VolumeInfo{
		{Name: "bento-prod-mysql84-data", Labels: n.Labels(RoleVolume, map[string]string{LabelService: "mysql84"})},
		{Name: "bento-prod-pg-data", Labels: n.Labels(RoleVolume, map[string]string{LabelService: "pg"})},
		{Name: "bento-stage-redis-data", Labels: other.Labels(RoleVolume, nil)},
		{Name: "random"},
	}
	networks := []docker.NetworkSummary{
		{Name: "bento-prod-apps", Labels: n.Labels(RoleNetwork, nil)},
		{Name: "bento-prod-data", Labels: n.Labels(RoleNetwork, nil)},
		{Name: "bento-stage-data"},
		{Name: "bridge"},
	}
	inv := n.ClassifyInventory(containers, images, volumes, networks, map[string]string{"a1": "shop"})

	wantImg := map[string]Ownership{"sha256:built": OwnedStack, "sha256:oldbuild": OwnedStack, "sha256:mysql": OwnedStack,
		"sha256:redis": OwnedOtherStack, "sha256:nginx": OwnedForeign}
	for _, i := range inv.Images {
		if i.Ownership != wantImg[i.ID] {
			t.Errorf("image %s: got %s want %s", i.ID, i.Ownership, wantImg[i.ID])
		}
	}
	if img := inv.Images[0]; !img.Built || !slices.Equal(img.UsedBy, []string{"shop"}) {
		t.Errorf("built image: %+v", img)
	}
	for _, img := range inv.Images {
		if img.Prunable != (img.ID == "sha256:oldbuild") {
			t.Errorf("image %s prunable=%v", img.ID, img.Prunable)
		}
	}
	if _, ok := PrunableImage(containers, images, "sha256:built"); ok {
		t.Error("in-use image reported prunable")
	}
	if _, ok := PrunableImage(containers, images, "sha256:mysql"); ok {
		t.Error("pulled image reported prunable")
	}
	if _, ok := PrunableImage(containers, images, "sha256:oldbuild"); !ok {
		t.Error("unused build not prunable")
	}
	if img := inv.Images[1]; len(img.UsedBy) != 0 {
		t.Errorf("unused build should have no users: %+v", img)
	}
	if img := inv.Images[3]; len(img.UsedBy) != 0 {
		t.Errorf("other-stack users must not be listed: %+v", img)
	}

	wantVol := map[string]Ownership{"bento-prod-mysql84-data": OwnedStack, "bento-prod-pg-data": OwnedStack,
		"bento-stage-redis-data": OwnedOtherStack, "random": OwnedForeign}
	for _, v := range inv.Volumes {
		if v.Ownership != wantVol[v.Name] {
			t.Errorf("volume %s: got %s want %s", v.Name, v.Ownership, wantVol[v.Name])
		}
	}
	if v := inv.Volumes[0]; v.Service != "mysql84" || !slices.Equal(v.UsedBy, []string{"mysql84"}) {
		t.Errorf("mysql volume: %+v", v)
	}

	wantNet := map[string]Ownership{"bento-prod-apps": OwnedStack, "bento-prod-data": OwnedStack,
		"bento-stage-data": OwnedOtherStack, "bridge": OwnedForeign}
	for _, nw := range inv.Networks {
		if nw.Ownership != wantNet[nw.Name] {
			t.Errorf("network %s: got %s want %s", nw.Name, nw.Ownership, wantNet[nw.Name])
		}
	}
}
