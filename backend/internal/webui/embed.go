//go:build release

package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var bundled embed.FS

var assets fs.FS = func() fs.FS { sub, _ := fs.Sub(bundled, "dist"); return sub }()
