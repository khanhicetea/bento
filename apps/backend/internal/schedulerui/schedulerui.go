// Package schedulerui embeds minicrond's built web UI, vendored from the
// minicrond release tag pinned by domain.RuntimeArtifacts["MINICROND_VERSION"]
// (`make vendor-minicrond-ui`). The scheduler gateway serves these files
// itself, so no HTML, script, or stylesheet on the management origin ever
// comes from the app-owned minicrond socket: a compromised app can replace its
// minicrond, but only its API answers (data) cross the relay.
//
// VERSION holds "<minicrond version> <sha256 of minicrond-web-<version>.tar.gz>".
// That release asset is minicrond's checked-in internal/api/assets, which the
// release binary of the same version embeds; minicrond's docs/embedding-ui.md
// pins the contract Index relies on.
package schedulerui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"html"
	"io/fs"
	"path"
	"strings"
	"sync"
)

//go:embed assets VERSION
var files embed.FS

// baseTag is the placeholder minicrond's index.html ships with; minicrond
// itself replaces it with BASE_PATH when it serves the page.
const baseTag = `<base href="/" />`

// Asset is one static file of the UI bundle.
type Asset struct {
	Body        []byte
	ContentType string
	ETag        string
}

var assets = sync.OnceValue(func() map[string]Asset {
	out := map[string]Asset{}
	entries, err := fs.ReadDir(files, "assets")
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		var ct string
		switch path.Ext(name) {
		case ".js":
			ct = "text/javascript; charset=utf-8"
		case ".css":
			ct = "text/css; charset=utf-8"
		default:
			continue
		}
		body, err := files.ReadFile("assets/" + name)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(body)
		out[name] = Asset{Body: body, ContentType: ct, ETag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	}
	return out
})

// Lookup returns a script or stylesheet of the bundle by file name.
func Lookup(name string) (Asset, bool) {
	a, ok := assets()[name]
	return a, ok
}

// Version is the vendored minicrond version.
func Version() string {
	b, _ := files.ReadFile("VERSION")
	v, _, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	return v
}

// Index renders the SPA shell for base, the URL path prefix the UI is served
// under (with a trailing slash), the same way minicrond's own handler does:
// the <base> tag carries the prefix and the entry links point at assets/.
func Index(base string) ([]byte, error) {
	b, err := files.ReadFile("assets/index.html")
	if err != nil {
		return nil, err
	}
	page := string(b)
	if strings.Count(page, baseTag) != 1 {
		return nil, errors.New("vendored minicrond index.html has no single base tag")
	}
	page = strings.Replace(page, baseTag, `<base href="`+html.EscapeString(base)+`" />`, 1)
	page = strings.ReplaceAll(page, `src="./`, `src="assets/`)
	page = strings.ReplaceAll(page, `href="./`, `href="assets/`)
	return []byte(page), nil
}
