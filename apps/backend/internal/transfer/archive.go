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
// relative clean names only, no parent traversal, symlinks must be relative
// and stay inside the root, existing files are never overwritten.
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
		mode := os.FileMode(h.Mode) & 0o7777
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(target, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			dirs = append(dirs, dirMeta{target, mode, h.Uid, h.Gid, h.ModTime})
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close() // the copy error takes precedence
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			if err := os.Lchown(target, h.Uid, h.Gid); err != nil {
				return err
			}
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
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
		default:
			return fmt.Errorf("%w: unsupported type for %q", ErrUnsafeArchive, h.Name)
		}
	}
	for _, d := range slices.Backward(dirs) {
		if err := os.Lchown(d.path, d.uid, d.gid); err != nil {
			return err
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return err
		}
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
