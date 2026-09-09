// Package webui holds the browser front end, compiled into the binary so the
// application ships as a single file with no assets to install alongside it.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed assets
var embedded embed.FS

// FS returns the front end's files rooted at the assets directory.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		// The path is a compile-time constant, so this cannot happen.
		panic(err)
	}
	return sub
}
