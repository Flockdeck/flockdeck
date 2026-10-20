// Package webui holds the browser front end, compiled into the binary so the
// application ships as a single file with no assets to install alongside it.
//
// There are two front ends. The default is the one in assets. The other, in
// next/assets, is the new shell under construction, and is built only with
// `-tags next`; see fs_default.go and fs_next.go. A release is never built with
// the tag.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed assets
var embedded embed.FS

// assets is the old front end's files rooted at the assets directory. Both
// variants of FS read from it.
func assets() fs.FS {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		// The path is a compile-time constant, so this cannot happen.
		panic(err)
	}
	return sub
}

// Icon is the application's icon, as PNG bytes at a size sharp on a high-DPI
// display without being wasteful to decode. It is what the desktop window is
// given as its own icon -- see internal/appwindow -- rather than one read
// from a platform-specific resource, so every platform's window carries the
// same source image the Windows .syso and the PWA manifest already do. It is
// read from the old tree in both variants.
func Icon() []byte {
	data, err := fs.ReadFile(embedded, "assets/icon-256.png")
	if err != nil {
		// The path is a compile-time constant naming a file this module
		// ships, so this cannot happen.
		panic(err)
	}
	return data
}
