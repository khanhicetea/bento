package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseUnitList(t *testing.T) {
	out := "bento@prod.service loaded active running Bento control plane (stack prod)\n" +
		"bento@stage.service loaded active running Bento control plane (stack stage)\n" +
		"docker.service loaded active running Docker\n\n"
	if got := parseUnitList(out); !slices.Equal(got, []string{"bento@prod.service", "bento@stage.service"}) {
		t.Fatalf("units %v", got)
	}
}

// fakeProc writes the /proc entries inspectProcess reads.
func fakeProc(t *testing.T, exe string, env, argv []string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	join := func(v []string) []byte {
		var b []byte
		for _, s := range v {
			b = append(append(b, s...), 0)
		}
		return b
	}
	os.WriteFile(filepath.Join(dir, "environ"), join(env), 0o600)
	os.WriteFile(filepath.Join(dir, "cmdline"), join(argv), 0o600)
	return dir
}

func TestInspectProcessReadsRootAndUtilsListen(t *testing.T) {
	proc := fakeProc(t, "/usr/local/bin/bento (deleted)",
		[]string{"PATH=/usr/bin", "BENTO_STACK_ROOT=/srv/bento/prod", "BENTO_LISTEN=127.0.0.1:7780"},
		[]string{"/usr/local/bin/bento", "serve", "--listen", "127.0.0.1:7780", "--utils-listen", "127.0.0.1:7791",
			"--utils-listen=apps:7791"})
	s, err := inspectProcess("bento@prod.service", proc)
	if err != nil {
		t.Fatal(err)
	}
	if s.Exe != "/usr/local/bin/bento" || s.Root != "/srv/bento/prod" ||
		!slices.Equal(s.UtilsListen, []string{"127.0.0.1:7791", "apps:7791"}) {
		t.Fatalf("service %+v", s)
	}
	// --stack on the command line wins over the environment.
	proc = fakeProc(t, "/opt/bento", []string{"BENTO_STACK_ROOT=/srv/bento/a"},
		[]string{"/opt/bento", "--stack", "/data/b", "serve"})
	if s, err = inspectProcess("bento@b.service", proc); err != nil || s.Root != "/data/b" || s.UtilsListen != nil {
		t.Fatalf("service %+v %v", s, err)
	}
	proc = fakeProc(t, "/opt/bento", nil, []string{"/opt/bento", "serve"})
	if _, err := inspectProcess("bento@c.service", proc); err == nil {
		t.Fatal("a backend without a stack root must be refused")
	}
}

func TestInstallBinaryKeepsThePrevious(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bento")
	os.WriteFile(target, []byte("old"), 0o755)
	tmp := filepath.Join(dir, ".bento-upgrade-1")
	os.Mkdir(tmp, 0o700)
	newBin := filepath.Join(tmp, "bento-linux-amd64")
	os.WriteFile(newBin, []byte("new"), 0o700)
	if err := installBinary(target, newBin); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatalf("target holds %q", b)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o755 {
		t.Fatalf("target mode %v", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(target + ".prev"); string(b) != "old" {
		t.Fatalf("previous binary holds %q", b)
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag, installed string
		want           bool
	}{
		{"0.6.0", "0.5.0+96c9ad9", true},
		{"0.5.0", "0.5.0+96c9ad9", false},
		{"0.4.1", "0.5.0+96c9ad9", false},
		{"0.5.0", "0.5.0-rc1+abc", true},
		{"0.5.0", "dev", true},
		{"garbage", "0.5.0", false},
	}
	for _, c := range cases {
		if got := isNewer(c.tag, c.installed); got != c.want {
			t.Errorf("isNewer(%s, %s) = %v", c.tag, c.installed, got)
		}
	}
}
