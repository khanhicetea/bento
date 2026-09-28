package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDirRefusesSymlink(t *testing.T) {
	d := t.TempDir()
	os.Symlink("/etc", filepath.Join(d, "link"))
	if err := EnsureDir(filepath.Join(d, "link"), 0o700, Owner{os.Getuid(), os.Getgid()}); err == nil {
		t.Fatal("symlink followed")
	}
}

func TestNoSymlinkBetweenAndContained(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "a/b"), 0o755)
	os.Symlink(filepath.Join(d, "a"), filepath.Join(d, "l"))
	if err := NoSymlinkBetween(d, filepath.Join(d, "a/b/c")); err != nil {
		t.Fatal(err)
	}
	if err := NoSymlinkBetween(d, filepath.Join(d, "l/b")); err == nil {
		t.Fatal("symlink component accepted")
	}
	if err := NoSymlinkBetween(d, "/etc"); err == nil {
		t.Fatal("outside path accepted")
	}
	if _, err := ContainedPath(d, "../x"); err == nil {
		t.Fatal("escape accepted")
	}
}

func TestChownTreeNeverFollowsSymlinks(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	outside := t.TempDir()
	target := filepath.Join(outside, "victim")
	os.WriteFile(target, []byte("x"), 0o600)
	home := t.TempDir()
	os.Symlink(target, filepath.Join(home, "evil"))
	os.WriteFile(filepath.Join(home, "f"), []byte("x"), 0o600)
	if _, err := ChownTree(home, Owner{12345, 12345}, false, 100); err != nil {
		t.Fatal(err)
	}
	o, _, _ := StatOwner(target)
	if o.UID != 0 {
		t.Fatal("recursive repair followed a symlink out of the home")
	}
	o, _, _ = StatOwner(filepath.Join(home, "f"))
	if o.UID != 12345 {
		t.Fatal("regular file not repaired")
	}
	os.WriteFile(filepath.Join(home, "sidecar"), []byte("x"), 0o444)
	ChownTree(home, Owner{12345, 12345}, false, 100, filepath.Join(home, "sidecar"))
	if o, _, _ := StatOwner(filepath.Join(home, "sidecar")); o.UID != 0 {
		t.Fatal("skipped path was re-owned")
	}
	issues, _ := ChownTree(home, Owner{12345, 12345}, true, 100, filepath.Join(home, "sidecar"))
	if len(issues) != 0 {
		t.Fatalf("dry run after repair found %v", issues)
	}
}

func TestChownTreeDirSymlinkNotDescended(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	outside := t.TempDir()
	target := filepath.Join(outside, "victim")
	os.WriteFile(target, []byte("x"), 0o600)
	os.Mkdir(filepath.Join(outside, "vdir"), 0o700)
	os.WriteFile(filepath.Join(outside, "vdir", "f"), []byte("y"), 0o600)

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a/b"), 0o755)
	os.WriteFile(filepath.Join(root, "a/b/file"), []byte("z"), 0o644)
	os.Symlink(target, filepath.Join(root, "a/filelink"))
	os.Symlink(filepath.Join(outside, "vdir"), filepath.Join(root, "a/dirlink"))
	skipped := filepath.Join(root, "a/b/file")

	want := Owner{12345, 12346}
	issues, err := ChownTree(root, want, true, 0)
	if err != nil || len(issues) != 6 {
		t.Fatalf("dry run: %v %v", issues, err)
	}
	if o, _, _ := StatOwner(filepath.Join(root, "a")); o == want {
		t.Fatal("dry run changed owner")
	}
	if _, err := ChownTree(root, want, false, 0, skipped); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, filepath.Join(root, "a"), filepath.Join(root, "a/b"),
		filepath.Join(root, "a/filelink"), filepath.Join(root, "a/dirlink")} {
		if o, _, _ := StatOwner(p); o != want {
			t.Fatalf("%s owner %v", p, o)
		}
	}
	for _, p := range []string{target, filepath.Join(outside, "vdir"), filepath.Join(outside, "vdir", "f"), skipped} {
		if o, _, _ := StatOwner(p); o != RootOwner {
			t.Fatalf("%s owner changed to %v", p, o)
		}
	}
	if _, err := ChownTree(root, RootOwner, false, 3); err == nil {
		t.Fatal("maxEntries not enforced")
	}
}

func TestChownTreeRefusesSymlinkRoot(t *testing.T) {
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, "real"), 0o755)
	os.Symlink(filepath.Join(d, "real"), filepath.Join(d, "link"))
	if _, err := ChownTree(filepath.Join(d, "link"), Owner{os.Getuid(), os.Getgid()}, false, 0); err == nil {
		t.Fatal("symlink root followed")
	}
}

func TestAtomicWriteReplacesSymlinkNotTarget(t *testing.T) {
	d := t.TempDir()
	victim := filepath.Join(d, "victim")
	os.WriteFile(victim, []byte("keep"), 0o600)
	os.Symlink(victim, filepath.Join(d, "f"))
	if err := AtomicWrite(filepath.Join(d, "f"), []byte("new"), 0o600, Owner{os.Getuid(), os.Getgid()}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(victim)
	if string(b) != "keep" {
		t.Fatal("write followed symlink")
	}
}

func TestLockExclusion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.lock")
	a, err := TryLock(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(p); err == nil {
		t.Fatal("second lock acquired")
	}
	a.Release()
	b, err := TryLock(p)
	if err != nil {
		t.Fatal(err)
	}
	b.Release()
}
