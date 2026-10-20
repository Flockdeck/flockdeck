//go:build next

package webui

import (
	"embed"
	"errors"
	"io/fs"
	"mime"
	"slices"
)

// Variant names the front end this binary carries: "next" in a build made with
// -tags next, "default" otherwise.
const Variant = "next"

// The new shell is ES modules, and a browser refuses a module served with a
// type other than JavaScript. Go takes the type of an extension from the
// system first, and Windows' registry has none for .mjs, so the file server
// answers text/plain there. The server's file handler and its gzip path both
// ask the mime package, so this one registration covers them. It overrides the
// system's answer.
func init() {
	if err := mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8"); err != nil {
		panic(err)
	}
}

//go:embed next/assets
var nextEmbedded embed.FS

// FS returns the new shell's files laid over the old front end's: a file in
// next/assets is served in place of the old one of the same name, and a file it
// lacks is read from the old tree. The test runner, the xterm bundle, the fonts
// and the icons are shared that way and not copied. Anything here is built only
// with -tags next, and a release is never built with it.
func FS() fs.FS {
	top, err := fs.Sub(nextEmbedded, "next/assets")
	if err != nil {
		// The path is a compile-time constant, so this cannot happen.
		panic(err)
	}
	return overlay{top: top, base: assets()}
}

// overlay is two file trees seen as one, the first winning.
type overlay struct{ top, base fs.FS }

// Open returns the new shell's file when it has one. Open(".") is therefore the
// new tree's root directory, and a ReadDir on that file lists only the new
// tree. Callers that list a directory use fs.ReadDir(FS(), ...), which goes
// through the merged method below; the server only opens files.
func (o overlay) Open(name string) (fs.File, error) {
	f, err := o.top.Open(name)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return o.base.Open(name)
}

// ReadDir lists a directory of either tree, each name once, the new shell's
// entry standing for a name both have.
func (o overlay) ReadDir(name string) ([]fs.DirEntry, error) {
	top, topErr := fs.ReadDir(o.top, name)
	base, baseErr := fs.ReadDir(o.base, name)
	if topErr != nil && baseErr != nil {
		return nil, baseErr
	}
	seen := map[string]bool{}
	var out []fs.DirEntry
	for _, e := range top {
		seen[e.Name()] = true
		out = append(out, e)
	}
	for _, e := range base {
		if !seen[e.Name()] {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b fs.DirEntry) int {
		switch {
		case a.Name() < b.Name():
			return -1
		case a.Name() > b.Name():
			return 1
		}
		return 0
	})
	return out, nil
}
