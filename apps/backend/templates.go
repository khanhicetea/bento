// Package backend embeds the immutable templates shipped with every source
// and compiled release.
package backend

import "embed"

// Templates holds runtime image build contexts and generated-config templates.
//
//go:embed all:templates
var Templates embed.FS
