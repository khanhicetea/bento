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
