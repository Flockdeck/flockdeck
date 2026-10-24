// Package webui holds the browser front end, compiled into the binary so the
// application ships as a single file with no assets to install alongside it.
//
// The front end is the team-graph shell in next/assets: ES modules on a
// vendored Preact, with the terminal library, the end-to-end script, the fonts
// and the icons beside them. The directory is still called next; it is the only
// front end there is.
package webui

import (
	"embed"
	"io/fs"
	"mime"
)

// The shell is ES modules, and a browser refuses a module served with a type
// other than JavaScript. Go takes the type of an extension from the system
// first, and Windows' registry has none for .mjs, so the file server answers
// text/plain there. The server's file handler and its gzip path both ask the
// mime package, so this one registration covers them. It overrides the
// system's answer.
func init() {
	if err := mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8"); err != nil {
		panic(err)
	}
}

//go:embed next/assets
var embedded embed.FS

// FS returns the front end's files rooted at the assets directory: index.html at
// the top, served at /, and the rest under /assets/.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "next/assets")
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
// same source image the Windows .syso and the PWA manifest already do.
func Icon() []byte {
	data, err := fs.ReadFile(embedded, "next/assets/icon-256.png")
	if err != nil {
		// The path is a compile-time constant naming a file this module
		// ships, so this cannot happen.
		panic(err)
	}
	return data
}
