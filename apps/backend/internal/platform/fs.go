package platform

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
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
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return fmt.Errorf("%w: %s", ErrSymlink, dir)
		}
		return &os.PathError{Op: "open", Path: dir, Err: err}
	}
	defer unix.Close(fd)
	if err := unix.Fchown(fd, owner.UID, owner.GID); err != nil {
		return &os.PathError{Op: "fchown", Path: dir, Err: err}
	}
	if err := unix.Fchmod(fd, uint32(mode.Perm())); err != nil {
		return &os.PathError{Op: "fchmod", Path: dir, Err: err}
	}
	return nil
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
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
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

// ChownTree changes ownership of every entry below root and never follows
// symbolic links (links themselves are re-owned, their targets are not). The
// walk holds a directory file descriptor for each level and resolves every
// entry relative to it with AT_SYMLINK_NOFOLLOW / O_NOFOLLOW, so a directory
// swapped for a symlink during the walk cannot redirect it outside root. A
// symlink at root itself is refused. When dryRun is set it only reports
// entries that would change. maxEntries bounds the walk. skip lists full
// paths whose own ownership is left untouched (a skipped directory is still
// descended).
func ChownTree(root string, owner Owner, dryRun bool, maxEntries int, skip ...string) ([]PermissionIssue, error) {
	w := &chownWalker{owner: owner, dryRun: dryRun, maxEntries: maxEntries, skip: map[string]bool{}}
	for _, s := range skip {
		w.skip[s] = true
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: %s", ErrSymlink, root)
		}
		return nil, &os.PathError{Op: "open", Path: root, Err: err}
	}
	defer unix.Close(fd)
	if !w.skip[root] {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return nil, &os.PathError{Op: "fstat", Path: root, Err: err}
		}
		if err := w.visit(root, &st, func() error { return unix.Fchown(fd, owner.UID, owner.GID) }); err != nil {
			return w.issues, err
		}
	}
	err = w.walkDir(fd, root)
	return w.issues, err
}

type chownWalker struct {
	owner      Owner
	dryRun     bool
	maxEntries int
	count      int
	skip       map[string]bool
	issues     []PermissionIssue
}

func (w *chownWalker) visit(path string, st *unix.Stat_t, chown func() error) error {
	w.count++
	if w.maxEntries > 0 && w.count > w.maxEntries {
		return fmt.Errorf("permission walk exceeded %d entries", w.maxEntries)
	}
	if int(st.Uid) == w.owner.UID && int(st.Gid) == w.owner.GID {
		return nil
	}
	w.issues = append(w.issues, PermissionIssue{Path: path, Reason: fmt.Sprintf("owner %d:%d", st.Uid, st.Gid)})
	if w.dryRun {
		return nil
	}
	if err := chown(); err != nil {
		return &os.PathError{Op: "chown", Path: path, Err: err}
	}
	return nil
}

// walkDir processes the entries of the directory open at dirfd (whose
// display path is dirPath). It does not take ownership of dirfd.
func (w *chownWalker) walkDir(dirfd int, dirPath string) error {
	names, err := readDirNames(dirfd)
	if err != nil {
		return &os.PathError{Op: "readdir", Path: dirPath, Err: err}
	}
	for _, name := range names {
		path := filepath.Join(dirPath, name)
		var st unix.Stat_t
		if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return &os.PathError{Op: "fstatat", Path: path, Err: err}
		}
		if !w.skip[path] {
			if err := w.visit(path, &st, func() error {
				return unix.Fchownat(dirfd, name, w.owner.UID, w.owner.GID, unix.AT_SYMLINK_NOFOLLOW)
			}); err != nil {
				return err
			}
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			continue
		}
		child, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
				return fmt.Errorf("%w: %s changed during walk", ErrSymlink, path)
			}
			return &os.PathError{Op: "openat", Path: path, Err: err}
		}
		err = w.walkDir(child, path)
		unix.Close(child)
		if err != nil {
			return err
		}
	}
	return nil
}

// readDirNames lists dirfd's entries (sorted, without . and ..) using a
// duplicate descriptor so dirfd's own offset and lifetime are untouched.
func readDirNames(dirfd int) ([]string, error) {
	dup, err := unix.Openat(dirfd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), ".")
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
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
