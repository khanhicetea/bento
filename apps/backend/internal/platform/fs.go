package platform

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Owner is a numeric uid/gid pair. Root is {0,0}.
type Owner struct {
	UID int
	GID int
}

var RootOwner = Owner{0, 0}

// ErrSymlink is returned when a path component that must be a real directory
// or file is a symbolic link.
var ErrSymlink = errors.New("refusing to follow symbolic link")

// EnsureDir creates dir (and missing parents with mode 0755 root) and then
// forces the final component's mode and owner. An existing symlink at dir is
// refused rather than followed.
func EnsureDir(dir string, mode os.FileMode, owner Owner) error {
	info, err := os.Lstat(dir)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlink, dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", dir)
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		if err := os.Mkdir(dir, mode); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	default:
		return err
	}
	if err := os.Lchown(dir, owner.UID, owner.GID); err != nil {
		return err
	}
	return os.Chmod(dir, mode)
}

// AtomicWrite writes data to a private temporary file in the same directory,
// sets owner and mode, fsyncs, and renames it over path. Readers observe
// either the previous or the new bytes. A symlink at path is replaced, never
// followed.
func AtomicWrite(path string, data []byte, mode os.FileMode, owner Owner) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Lchown(name, owner.UID, owner.GID); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// WriteIfChanged writes only when bytes, mode, or owner differ. It reports
// whether anything changed.
func WriteIfChanged(path string, data []byte, mode os.FileMode, owner Owner) (bool, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == owner.UID && int(st.Gid) == owner.GID &&
			info.Mode().Perm() == mode.Perm() {
			if current, err := os.ReadFile(path); err == nil && string(current) == string(data) {
				return false, nil
			}
		}
	}
	return true, AtomicWrite(path, data, mode, owner)
}

// ContainedPath joins rel onto base and verifies the lexical result stays in
// base. It does not touch the filesystem.
func ContainedPath(base, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative", rel)
	}
	joined := filepath.Join(base, rel)
	if joined != base && !strings.HasPrefix(joined, base+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes %s", rel, base)
	}
	return joined, nil
}

// NoSymlinkBetween verifies that no component of target below base is a
// symbolic link. Missing trailing components are allowed.
func NoSymlinkBetween(base, target string) error {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside %s", target, base)
	}
	if rel == "." {
		return nil
	}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlink, cur)
		}
	}
	return nil
}

// PermissionIssue is one mismatch found by CheckTree.
type PermissionIssue struct {
	Path   string
	Reason string
}

// ChownTree changes ownership of every entry below root with lchown and never
// follows symbolic links (links themselves are re-owned, their targets are not).
// When dryRun is set it only reports entries that would change. maxEntries
// bounds the walk.
func ChownTree(root string, owner Owner, dryRun bool, maxEntries int, skip ...string) ([]PermissionIssue, error) {
	var issues []PermissionIssue
	count := 0
	skipSet := map[string]bool{}
	for _, s := range skip {
		skipSet[s] = true
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skipSet[path] {
			return nil
		}
		count++
		if maxEntries > 0 && count > maxEntries {
			return fmt.Errorf("permission walk exceeded %d entries", maxEntries)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		if int(st.Uid) == owner.UID && int(st.Gid) == owner.GID {
			return nil
		}
		issues = append(issues, PermissionIssue{Path: path, Reason: fmt.Sprintf("owner %d:%d", st.Uid, st.Gid)})
		if dryRun {
			return nil
		}
		return os.Lchown(path, owner.UID, owner.GID)
	})
	return issues, err
}

// StatOwner returns the numeric owner of path without following a final symlink.
func StatOwner(path string) (Owner, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Owner{}, 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Owner{}, 0, fmt.Errorf("stat unsupported for %s", path)
	}
	return Owner{int(st.Uid), int(st.Gid)}, info.Mode(), nil
}

// DirIsEmptyOrMissing reports whether dir does not exist or has no entries.
func DirIsEmptyOrMissing(dir string) (bool, error) {
	f, err := os.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// CopyFile copies a regular file without following a symlink at src.
func CopyFile(src, dst string, mode os.FileMode, owner Owner) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return AtomicWrite(dst, data, mode, owner)
}
