// Command bento is the Bento control plane: the resident backend, offline
// stack commands, and a thin client for the running backend.
package main

import (
	"os"

	"github.com/khanhicetea/bento/apps/backend/internal/cli"
)

// version is set at release build time with -ldflags "-X main.version=...".
var version = "0.2.0-dev"

func main() {
	os.Exit(cli.Main(os.Args[1:], version))
}
