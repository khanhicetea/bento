package transfer

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func archive(t *testing.T, entries []tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, h := range entries {
		if h.Typeflag == tar.TypeReg {
			h.Size = 1
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte("x"))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestExtractRejectsUnsafeEntries(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	cases := map[string][]tar.Header{
		"parent traversal": {{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644, Uid: uid, Gid: gid}},
		"absolute path":    {{Name: "/etc/evil", Typeflag: tar.TypeReg, Mode: 0o644, Uid: uid, Gid: gid}},
		"hidden traversal": {{Name: "a/../../evil", Typeflag: tar.TypeReg, Mode: 0o644, Uid: uid, Gid: gid}},
		"absolute symlink": {{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc", Uid: uid, Gid: gid}},
		"escaping symlink": {{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "../../etc", Uid: uid, Gid: gid}},
		"write through symlink": {
			{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: uid, Gid: gid},
			{Name: "d/l", Typeflag: tar.TypeSymlink, Linkname: ".", Uid: uid, Gid: gid},
			{Name: "d/l/x", Typeflag: tar.TypeReg, Mode: 0o644, Uid: uid, Gid: gid},
		},
		"device node": {{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o644, Uid: uid, Gid: gid}},
		// Lexically inside (root/d/d/d/.. is root/d/d), but d is the root, so
		// the kernel resolves e to the root's parent.
		"symlink chain escape": {
			{Name: "d", Typeflag: tar.TypeSymlink, Linkname: ".", Uid: uid, Gid: gid},
			{Name: "e", Typeflag: tar.TypeSymlink, Linkname: "d/d/d/..", Uid: uid, Gid: gid},
		},
		// A directory entry on top of a link: setting its mode must never
		// follow the link.
		"absolute link at app dir": {
			{Name: "homes/", Typeflag: tar.TypeDir, Mode: 0o711, Uid: uid, Gid: gid},
			{Name: "homes/shop", Typeflag: tar.TypeSymlink, Linkname: "/etc", Uid: uid, Gid: gid},
		},
		"escape via app link": {
			{Name: "homes/", Typeflag: tar.TypeDir, Mode: 0o711, Uid: uid, Gid: gid},
			{Name: "homes/shop/", Typeflag: tar.TypeDir, Mode: 0o750, Uid: uid, Gid: gid},
			{Name: "homes/shop/out", Typeflag: tar.TypeSymlink, Linkname: "/etc", Uid: uid, Gid: gid},
			{Name: "edge", Typeflag: tar.TypeSymlink, Linkname: "homes/shop/out", Uid: uid, Gid: gid},
		},
		"write through app link": {
			{Name: "homes/", Typeflag: tar.TypeDir, Mode: 0o711, Uid: uid, Gid: gid},
			{Name: "homes/shop/", Typeflag: tar.TypeDir, Mode: 0o750, Uid: uid, Gid: gid},
			{Name: "homes/shop/out", Typeflag: tar.TypeSymlink, Linkname: "/tmp", Uid: uid, Gid: gid},
			{Name: "homes/shop/out/x", Typeflag: tar.TypeReg, Mode: 0o644, Uid: uid, Gid: gid},
		},
		"directory over symlink": {
			{Name: "t/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: uid, Gid: gid},
			{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "t", Uid: uid, Gid: gid},
			{Name: "l/", Typeflag: tar.TypeDir, Mode: 0o777, Uid: uid, Gid: gid},
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			err := ExtractRoot(bytes.NewReader(archive(t, entries)), root)
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("expected unsafe archive error, got %v", err)
			}
		})
	}
}

func TestArchiveRoundTripPreservesModesAndSkips(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "homes/app/app/public"), 0o750)
	os.WriteFile(filepath.Join(src, "homes/app/app/public/index.php"), []byte("<?php"), 0o640)
	os.Symlink("public", filepath.Join(src, "homes/app/app/current"))
	os.WriteFile(filepath.Join(src, "bento.db"), []byte("live"), 0o600)
	var buf bytes.Buffer
	if err := ArchiveRoot(src, &buf, map[string]bool{"bento.db": true}); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := ExtractRoot(&buf, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "homes/app/app/public/index.php"))
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode not preserved: %v %v", info, err)
	}
	if l, _ := os.Readlink(filepath.Join(dst, "homes/app/app/current")); l != "public" {
		t.Fatal("symlink not preserved")
	}
	if _, err := os.Stat(filepath.Join(dst, "bento.db")); err == nil {
		t.Fatal("live database must be excluded")
	}
}

func TestManifestValidation(t *testing.T) {
	m := Manifest{Format: FormatName, Version: FormatVersion, SchemaVersion: 1, StateFile: "state.db", RootArchive: "stack.tar.zst",
		Services: []ServiceEntry{{VolumeFile: "../x"}}}
	if m.Validate(1) == nil {
		t.Fatal("unsafe volume file accepted")
	}
	m.Services = nil
	if m.Validate(2) == nil {
		t.Fatal("schema mismatch accepted")
	}
	if err := m.Validate(1); err != nil {
		t.Fatal(err)
	}
}

// setuid and setgid never survive an import; the sticky bit on a directory
// does, and links that stay inside the root are kept.
func TestExtractDropsSetuidAndKeepsInnerLinks(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	root := t.TempDir()
	entries := []tar.Header{
		{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o2755, Uid: uid, Gid: gid},
		{Name: "bin/tool", Typeflag: tar.TypeReg, Mode: 0o4755, Uid: uid, Gid: gid},
		{Name: "tmp/", Typeflag: tar.TypeDir, Mode: 0o1777, Uid: uid, Gid: gid},
		{Name: "a/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: uid, Gid: gid},
		{Name: "a/up", Typeflag: tar.TypeSymlink, Linkname: "../bin", Uid: uid, Gid: gid},
		{Name: "dangling", Typeflag: tar.TypeSymlink, Linkname: "missing/file", Uid: uid, Gid: gid},
		// An app's own links point into its container and are kept as they are.
		{Name: "homes/", Typeflag: tar.TypeDir, Mode: 0o711, Uid: uid, Gid: gid},
		{Name: "homes/py/", Typeflag: tar.TypeDir, Mode: 0o750, Uid: uid, Gid: gid},
		{Name: "homes/py/app/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: uid, Gid: gid},
		{Name: "homes/py/app/python3", Typeflag: tar.TypeSymlink, Linkname: "/usr/local/bin/python3", Uid: uid, Gid: gid},
		{Name: "homes/py/app/storage", Typeflag: tar.TypeSymlink, Linkname: "../../../..", Uid: uid, Gid: gid},
	}
	if err := ExtractRoot(bytes.NewReader(archive(t, entries)), root); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(root, "bin/tool")); info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || info.Mode().Perm() != 0o755 {
		t.Fatalf("file mode %v", info.Mode())
	}
	if info, _ := os.Stat(filepath.Join(root, "bin")); info.Mode()&os.ModeSetgid != 0 {
		t.Fatalf("directory mode %v", info.Mode())
	}
	if info, _ := os.Stat(filepath.Join(root, "tmp")); info.Mode()&os.ModeSticky == 0 || info.Mode().Perm() != 0o777 {
		t.Fatalf("sticky directory mode %v", info.Mode())
	}
	if l, _ := os.Readlink(filepath.Join(root, "homes/py/app/python3")); l != "/usr/local/bin/python3" {
		t.Fatalf("app link %q", l)
	}
}
