// Package assets exposes embedded immutable templates and builds
// deterministic Docker build contexts from them.
package assets

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"text/template"
	"time"

	backend "github.com/khanhicetea/bento/apps/backend"
)

// FS is the embedded template tree rooted at templates/.
var FS = mustSub(backend.Templates, "templates")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// fixedTime makes build-context tar headers reproducible.
var fixedTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// executable reports whether a context file must be mode 0755. embed.FS does
// not preserve file modes, so the rule is explicit and deterministic.
func executable(name string) bool {
	return strings.HasSuffix(name, ".sh") || strings.HasPrefix(name, "rootfs/usr/local/bin/") ||
		strings.HasSuffix(name, "/run") || strings.HasSuffix(name, "/finish") || strings.HasSuffix(name, "/up")
}

// BuildContext assembles the tar build context for a managed runtime image:
// images/common/* overlaid with images/<kind>/*. It returns the tar bytes and
// the content hash that identifies them.
func BuildContext(kind string) ([]byte, string, error) {
	files := map[string][]byte{}
	for _, root := range []string{"images/common", "images/" + kind} {
		err := fs.WalkDir(FS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(FS, p)
			if err != nil {
				return err
			}
			files[strings.TrimPrefix(p, root+"/")] = data
			return nil
		})
		if err != nil {
			return nil, "", fmt.Errorf("build context %s: %w", kind, err)
		}
	}
	if _, ok := files["Dockerfile"]; !ok {
		return nil, "", fmt.Errorf("no Dockerfile for image kind %q", kind)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	for _, n := range names {
		for dir := path.Dir(n); dir != "." && !dirs[dir]; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	for _, d := range dirList {
		if err := tw.WriteHeader(&tar.Header{Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: fixedTime, Format: tar.FormatPAX}); err != nil {
			return nil, "", err
		}
	}
	for _, n := range names {
		mode := int64(0o644)
		if executable(n) {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(files[n])), ModTime: fixedTime, Format: tar.FormatPAX}); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write(files[n]); err != nil {
			return nil, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"join":  strings.Join,
	"quote": nginxQuote,
}).ParseFS(FS, "config/*.tmpl", "edge/*.tmpl"))

// Render executes a named embedded config template.
func Render(name string, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// nginxQuote renders a double-quoted Nginx string literal.
func nginxQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "", "\r", "").Replace(s) + `"`
}
