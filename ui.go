package fasedapp

import (
	"embed"
	"io/fs"
)

// Build the shared React UI with npm run build before building the binary.
//
//go:embed dist
var bundledUI embed.FS

func UI() fs.FS {
	ui, err := fs.Sub(bundledUI, "dist")
	if err != nil {
		panic(err)
	}
	return ui
}
