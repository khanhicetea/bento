package backup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

const sampleConf = `[s3]
type = s3
provider = AWS
access_key_id = AKIAEXAMPLE
secret_access_key = do-not-leak

[secure]
type = crypt
remote = s3:bucket/bento
password = also-secret
`

func rcloneDeps(t *testing.T, conf string) (Deps, *docker.Fake) {
	t.Helper()
	layout := platform.Layout{Root: t.TempDir()}
	if err := os.MkdirAll(layout.RcloneDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if conf != "" {
		if err := os.WriteFile(filepath.Join(layout.RcloneDir(), "rclone.conf"), []byte(conf), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fake := docker.NewFake()
	return Deps{Engine: fake, Layout: layout, Names: runtime.Names{StackID: "s1", StackName: "dev"}}, fake
}

func TestReadRcloneConfigExposesOnlyNamesAndTypes(t *testing.T) {
	d, _ := rcloneDeps(t, sampleConf)
	cfg, err := ReadRcloneConfig(d.Layout.RcloneDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []RcloneRemote{{Name: "s3", Type: "s3"}, {Name: "secure", Type: "crypt"}}
	if !cfg.Present || cfg.Encrypted || !slices.Equal(cfg.Remotes, want) {
		t.Fatalf("got %+v", cfg)
	}
}

func TestReadRcloneConfigStates(t *testing.T) {
	d, _ := rcloneDeps(t, "")
	if cfg, err := ReadRcloneConfig(d.Layout.RcloneDir()); err != nil || cfg.Present {
		t.Fatalf("missing: %+v %v", cfg, err)
	}
	enc, _ := rcloneDeps(t, "\n# Encrypted rclone configuration File\n\nRCLONE_ENCRYPT_V0:\nabc\n")
	if cfg, err := ReadRcloneConfig(enc.Layout.RcloneDir()); err != nil || !cfg.Present || !cfg.Encrypted || len(cfg.Remotes) != 0 {
		t.Fatalf("encrypted: %+v %v", cfg, err)
	}
	link, _ := rcloneDeps(t, "")
	if err := os.Symlink("/etc/passwd", filepath.Join(link.Layout.RcloneDir(), "rclone.conf")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRcloneConfig(link.Layout.RcloneDir()); err == nil {
		t.Fatal("a symlinked config must not be followed on the host")
	}
}

func TestValidateRemote(t *testing.T) {
	for _, ok := range []string{"s3:", "s3:bucket/bento", "my-remote_1:a.b/c"} {
		if _, err := ValidateRemote(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "bucket", ":s3,provider=AWS:x", "s3:a b", "s3:$(id)", "s3:x --config=/etc", "s3:a=b"} {
		if _, err := ValidateRemote(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUploadSpecIsScoped(t *testing.T) {
	d, fake := rcloneDeps(t, sampleConf)
	// A failing Remove keeps the container in the fake so its spec can be read.
	fake.FailOn = map[string]error{"Remove": os.ErrPermission}
	arts := []Artifact{{Path: "shop/mysql-shop-20260101T000000.000Z.sql.zst"}}
	if err := d.Upload(context.Background(), "secure:daily", arts); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(fake.Calls, func(c string) bool { return strings.HasPrefix(c, "Remove ") }) {
		t.Fatal("upload container was not removed")
	}
	spec := onlyContainer(t, fake)
	if got := spec.Config.Cmd; !slices.Equal(got, []string{"copy", "/upload", "secure:daily", "--no-traverse"}) {
		t.Fatalf("cmd %v", got)
	}
	assertHardened(t, spec)
	mounts := map[string]bool{}
	for _, m := range spec.HostConfig.Mounts {
		mounts[m.Target] = m.ReadOnly
	}
	if ro, ok := mounts["/config/rclone"]; !ok || ro {
		t.Fatalf("config dir must be mounted writable for token refresh: %v", mounts)
	}
	if ro, ok := mounts["/upload/shop/mysql-shop-20260101T000000.000Z.sql.zst"]; !ok || !ro || len(mounts) != 2 {
		t.Fatalf("only the new artifact may be mounted, read-only: %v", mounts)
	}
}

func TestUploadRefusesUnusableConfig(t *testing.T) {
	arts := []Artifact{{Path: "shop/x.sql"}}
	cases := map[string]struct{ conf, remote, want string }{
		"missing":   {"", "s3:bucket", "missing"},
		"encrypted": {"# Encrypted rclone configuration File\nRCLONE_ENCRYPT_V0:\nabc\n", "s3:bucket", "encrypted"},
		"unknown":   {sampleConf, "gdrive:bento", "not configured"},
		"empty":     {sampleConf, "", "invalid"},
	}
	for name, tc := range cases {
		d, fake := rcloneDeps(t, tc.conf)
		err := d.Upload(context.Background(), tc.remote, arts)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
		if slices.ContainsFunc(fake.Calls, func(c string) bool { return strings.HasPrefix(c, "Create ") }) {
			t.Errorf("%s: a container was created", name)
		}
	}
}

func TestRcloneShellSpec(t *testing.T) {
	d, _ := rcloneDeps(t, "")
	spec := d.RcloneShellSpec("t1", time.Hour)
	assertHardened(t, spec)
	if !slices.Equal(spec.Config.Entrypoint, []string{"sleep"}) || !slices.Equal(spec.Config.Cmd, []string{"3600"}) {
		t.Fatalf("entrypoint %v cmd %v", spec.Config.Entrypoint, spec.Config.Cmd)
	}
	if len(spec.HostConfig.Mounts) != 1 || spec.HostConfig.Mounts[0].Source != d.Layout.RcloneDir() {
		t.Fatalf("shell must mount only the config dir: %+v", spec.HostConfig.Mounts)
	}
	if !d.Names.OwnedBy(spec.Config.Labels, runtime.RoleTool, "") {
		t.Fatal("shell container must be a collectable tool container of this stack")
	}
}

func onlyContainer(t *testing.T, fake *docker.Fake) docker.ContainerSpec {
	t.Helper()
	if len(fake.Containers) != 1 {
		t.Fatalf("%d containers", len(fake.Containers))
	}
	for _, c := range fake.Containers {
		return c.Spec
	}
	return docker.ContainerSpec{}
}

func assertHardened(t *testing.T, spec docker.ContainerSpec) {
	t.Helper()
	h := spec.HostConfig
	if !h.ReadonlyRootfs || !slices.Equal(h.CapDrop, []string{"ALL"}) || !slices.Contains(h.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("not hardened: %+v", h)
	}
	for _, e := range spec.Config.Env {
		if strings.Contains(strings.ToLower(e), "pass") || strings.Contains(strings.ToLower(e), "secret") {
			t.Fatalf("secret-looking env %q", e)
		}
	}
}
