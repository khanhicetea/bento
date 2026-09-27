// Package webui embeds the built React control plane. `make web` copies
// apps/web/dist into dist/ before building; a placeholder page is served
// when the UI has not been built into the binary.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

//go:embed placeholder
var placeholder embed.FS

// Built reports whether a UI build is embedded.
func Built() bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}

// FS returns the built UI, or the placeholder page.
func FS() fs.FS {
	root, dir := fs.FS(dist), "dist"
	if !Built() {
		root, dir = placeholder, "placeholder"
	}
	sub, err := fs.Sub(root, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
