// Package transfer implements stack export/import archives.
package transfer

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
)

// FormatName and FormatVersion identify the transfer manifest.
const (
	FormatName    = "bento-transfer"
	FormatVersion = 1
)

type ServiceEntry struct {
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	Version      string `json:"version"`
	Image        string `json:"image"`
	VolumeFile   string `json:"volumeFile"`
	SourceVolume string `json:"sourceVolume"`
}

// Manifest describes one export.
type Manifest struct {
	Format        string         `json:"format"`
	Version       int            `json:"version"`
	SchemaVersion int            `json:"schemaVersion"`
	StackID       string         `json:"stackId"`
	StackName     string         `json:"stackName"`
	CreatedAt     string         `json:"createdAt"`
	Arch          string         `json:"arch"`
	StateFile     string         `json:"stateFile"`
	RootArchive   string         `json:"rootArchive"`
	Services      []ServiceEntry `json:"services"`
}

// Validate checks the manifest; its state schema must be one of the accepted
// versions.
func (m Manifest) Validate(schemaVersions ...int) error {
	if m.Format != FormatName {
		return fmt.Errorf("not a Bento transfer (format %q)", m.Format)
	}
	if m.Version != FormatVersion {
		return fmt.Errorf("unsupported transfer format version %d (supported %d)", m.Version, FormatVersion)
	}
	if !slices.Contains(schemaVersions, m.SchemaVersion) {
		return fmt.Errorf("transfer holds state schema %d; this Bento supports %v", m.SchemaVersion, schemaVersions)
	}
	for _, f := range append([]string{m.StateFile, m.RootArchive}, volumeFiles(m)...) {
		if f == "" || strings.ContainsAny(f, "/\\") || strings.HasPrefix(f, ".") {
			return fmt.Errorf("manifest references unsafe file %q", f)
		}
	}
	return nil
}

func volumeFiles(m Manifest) []string {
	var out []string
	for _, s := range m.Services {
		out = append(out, s.VolumeFile)
	}
	return out
}

func WriteManifest(dir string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o600)
}

func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	return m, dec.Decode(&m)
}

// ArchiveRoot writes a zstd tar of root, excluding top-level names in skip.
// Symlinks are stored as links and never followed; ownership and modes are
// preserved numerically.
func ArchiveRoot(root string, w io.Writer, skip map[string]bool) error {
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		top, _, _ := strings.Cut(rel, string(filepath.Separator))
		if skip[top] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSocket|os.ModeNamedPipe|os.ModeDevice) != 0 {
			return nil // runtime sockets are not durable state
		}
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			h.Name += "/"
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			h.Uid, h.Gid = int(st.Uid), int(st.Gid)
		}
		h.Uname, h.Gname = "", ""
		h.Format = tar.FormatPAX
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// ErrUnsafeArchive reports traversal, absolute paths, or unexpected types.
var ErrUnsafeArchive = errors.New("unsafe archive entry")

// ExtractRoot safely extracts a root archive into an empty directory:
// relative clean names only, no parent traversal, existing files are never
// overwritten, and nothing is written through a symlink. Outside the app
// areas (inAppArea) symlinks must be relative and, followed the way the
// kernel follows them, stay inside the root (a lexical check alone lets
// `d -> .` plus `e -> d/d/d/..` escape). Owners and modes are set on open
// descriptors, never by a path that could be a link; setuid and setgid bits
// are dropped.
func ExtractRoot(r io.Reader, root string) error {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	type dirMeta struct {
		path     string
		mode     os.FileMode
		uid, gid int
		mtime    time.Time
	}
	var dirs []dirMeta
	var links []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return fmt.Errorf("%w: %q", ErrUnsafeArchive, h.Name)
		}
		clean := filepath.Clean(name)
		if clean != name || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("%w: %q", ErrUnsafeArchive, h.Name)
		}
		target := filepath.Join(root, clean)
		if err := noSymlinkParents(root, target); err != nil {
			return err
		}
		// Permission bits only, plus the sticky bit on directories (shared
		// tmp directories): setuid and setgid never survive an import.
		mode := os.FileMode(h.Mode) & 0o777
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(target, 0o700); errors.Is(err, fs.ErrExist) {
				if info, lerr := os.Lstat(target); lerr != nil || !info.IsDir() {
					return fmt.Errorf("%w: directory %q over a non-directory", ErrUnsafeArchive, h.Name)
				}
			} else if err != nil {
				return err
			}
			if h.Mode&0o1000 != 0 {
				mode |= os.ModeSticky
			}
			dirs = append(dirs, dirMeta{target, mode, h.Uid, h.Gid, h.ModTime})
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if err == nil {
				err = f.Chown(h.Uid, h.Gid)
			}
			if err == nil {
				err = f.Chmod(mode)
			}
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if inAppArea(clean) {
				// The app's own files: links point into its container (a
				// Python venv's bin/python3 -> /usr/local/bin/python3,
				// Laravel's public/storage). The app can create any such link
				// itself, and extraction never writes through one.
				if err := os.Symlink(h.Linkname, target); err != nil {
					return err
				}
				_ = os.Lchown(target, h.Uid, h.Gid)
				continue
			}
			if filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("%w: absolute symlink %q", ErrUnsafeArchive, h.Name)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(target), h.Linkname))
			if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
				return fmt.Errorf("%w: symlink %q escapes the root", ErrUnsafeArchive, h.Name)
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
			_ = os.Lchown(target, h.Uid, h.Gid)
			links = append(links, target)
		default:
			return fmt.Errorf("%w: unsupported type for %q", ErrUnsafeArchive, h.Name)
		}
	}
	// Links are checked once every entry exists, since a link may point
	// through links extracted after it.
	for _, l := range links {
		if err := resolvesInside(root, l); err != nil {
			return err
		}
	}
	for _, d := range slices.Backward(dirs) {
		if err := setDirMeta(d.path, d.uid, d.gid, d.mode); err != nil {
			return err
		}
	}
	return nil
}

// inAppArea reports whether a root-relative entry lies inside an app's own
// directory (homes/<slug>/… or sqlite/<dir>/…), whose contents the app
// controls at run time. The app directory itself is not in it.
func inAppArea(clean string) bool {
	parts := strings.Split(clean, string(filepath.Separator))
	return len(parts) >= 3 && (parts[0] == "homes" || parts[0] == "sqlite")
}

// setDirMeta sets a directory's owner and mode through a descriptor opened
// without following a final symlink.
func setDirMeta(path string, uid, gid int, mode os.FileMode) error {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("%w: directory %s: %v", ErrUnsafeArchive, path, err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := f.Chown(uid, gid); err != nil {
		return err
	}
	return f.Chmod(mode)
}

// resolvesInside follows path the way the kernel would, component by
// component, and fails if it ever leaves root. Links pointing at entries that
// do not exist are fine as long as their text stays inside.
func resolvesInside(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	pending := strings.Split(rel, string(filepath.Separator))
	var cur []string
	for hops := 0; len(pending) > 0; {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(cur) == 0 {
				return fmt.Errorf("%w: %s resolves outside the root", ErrUnsafeArchive, path)
			}
			cur = cur[:len(cur)-1]
			continue
		}
		next := filepath.Join(append([]string{root}, append(cur, c)...)...)
		info, err := os.Lstat(next)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			cur = append(cur, c)
			continue
		}
		if hops++; hops > 40 {
			return fmt.Errorf("%w: too many links resolving %s", ErrUnsafeArchive, path)
		}
		link, err := os.Readlink(next)
		if err != nil {
			return err
		}
		if filepath.IsAbs(link) {
			return fmt.Errorf("%w: %s resolves through absolute link %s", ErrUnsafeArchive, path, next)
		}
		pending = append(strings.Split(link, string(filepath.Separator)), pending...)
	}
	return nil
}

func noSymlinkParents(root, target string) error {
	rel, err := filepath.Rel(root, filepath.Dir(target))
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	cur := root
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return fmt.Errorf("%w: parent of %s missing", ErrUnsafeArchive, target)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: path through symlink %s", ErrUnsafeArchive, cur)
		}
	}
	return nil
}
