package docker

import (
	"errors"
	"testing"
)

// TestReadOnlyRefusesEveryMutation pins that no call that changes Docker
// state reaches the wrapped engine, and that observing calls still do.
func TestReadOnlyRefusesEveryMutation(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	f.Images["img:1"] = "sha256:1"
	ro := ReadOnly{E: f}
	mutations := map[string]func() error{
		"PullImage": func() error { return ro.PullImage(ctx, "img:1", nil) },
		"BuildImage": func() error {
			_, err := ro.BuildImage(ctx, "t", nil, nil, nil, BuildOptions{}, nil)
			return err
		},
		"ReadImageFile": func() error { _, err := ro.ReadImageFile(ctx, "img:1", "/etc/passwd", nil); return err },
		"EnsureNetwork": func() error { _, err := ro.EnsureNetwork(ctx, NetworkSpec{Name: "n"}); return err },
		"Create":        func() error { _, err := ro.Create(ctx, ContainerSpec{Name: "c"}); return err },
		"Start":         func() error { return ro.Start(ctx, "c") },
		"Stop":          func() error { return ro.Stop(ctx, "c", 0) },
		"Remove":        func() error { return ro.Remove(ctx, "c") },
		"Rename":        func() error { return ro.Rename(ctx, "c", "d") },
		"SetRestartPolicy": func() error {
			return ro.SetRestartPolicy(ctx, "c", "no")
		},
		"Exec":         func() error { _, err := ro.Exec(ctx, "c", ExecRequest{}); return err },
		"ExecAttach":   func() error { _, err := ro.ExecAttach(ctx, "c", ExecRequest{}, 0, 0); return err },
		"ExecResize":   func() error { return ro.ExecResize(ctx, "e", 0, 0) },
		"VolumeCreate": func() error { _, err := ro.VolumeCreate(ctx, "v", nil); return err },
		"VolumeRemove": func() error { return ro.VolumeRemove(ctx, "v") },
		"RemoveImage":  func() error { return ro.RemoveImage(ctx, "sha256:1", nil) },
	}
	for name, call := range mutations {
		if err := call(); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: err %v, want ErrReadOnly", name, err)
		}
	}
	if len(f.Calls) != 0 {
		t.Fatalf("refused calls reached the engine: %v", f.Calls)
	}
	if id, ok, err := ro.ImageID(ctx, "img:1"); err != nil || !ok || id != "sha256:1" {
		t.Fatalf("ImageID through the read-only engine: %q %v %v", id, ok, err)
	}
	if _, err := ro.List(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
