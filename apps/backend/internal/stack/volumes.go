package stack

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// restoreVolume extracts a raw volume archive into a volume created by this
// import, using a scoped job container with no network.
func restoreVolume(
	ctx context.Context,
	engine docker.Engine,
	names runtime.Names,
	image, volume, dir, file string,
) error {
	if _, ok, err := engine.ImageID(ctx, image); err != nil {
		return err
	} else if !ok {
		if err := engine.PullImage(ctx, image, nil); err != nil {
			return err
		}
	}
	opID := "import-" + platform.RandomHex(5)
	id, err := engine.Create(ctx, docker.ContainerSpec{
		Name: names.BackupContainer(opID),
		Config: &container.Config{
			Image:      image,
			Entrypoint: []string{"tar"},
			Cmd:        []string{"-C", "/v", "-xpf", "/x/" + file},
			User:       "0:0",
			Labels:     names.Labels(runtime.RoleBackup, map[string]string{runtime.LabelOperation: opID}),
		},
		HostConfig: &container.HostConfig{NetworkMode: "none", Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: volume, Target: "/v"},
			{Type: mount.TypeBind, Source: dir, Target: "/x", ReadOnly: true},
		}},
	})
	if err != nil {
		return err
	}
	defer engine.Remove(context.WithoutCancel(ctx), id)
	if err := engine.Start(ctx, id); err != nil {
		return err
	}
	code, err := engine.Wait(ctx, id)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("restore of volume %s exited %d", volume, code)
	}
	return nil
}

func removeVolume(ctx context.Context, engine docker.Engine, name string) error {
	return engine.VolumeRemove(ctx, name)
}
