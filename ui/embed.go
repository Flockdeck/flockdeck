// Package webui holds the browser front end, compiled into the binary so the
// application ships as a single file with no assets to install alongside it.
//
// The front end is the team-graph shell in src: ES modules on a
// vendored Preact, with the terminal library, the end-to-end script, the fonts
// and the icons beside them. It is the only front end there is.
package webui

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"log"
	"mime"
	"os"
	"path/filepath"
	"sync"
	"time"

	bundle "github.com/jmwri/flockdeck/ui/build"
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

//go:embed src
var embedded embed.FS

// FS returns the front end's files rooted at the assets directory: index.html at
// the top, served at /, and the rest under /assets/.
//
// The modules and stylesheets are sent joined: main.js is the whole module
// graph in one file, app.css is every stylesheet in the page's order, and
// index.html names that one. On a phone's link a first load is round trips, and
// a hundred files were about ten seconds of them (docs/first-load.md). The files
// they were made from are still served under their own names. Set
// FLOCKDECK_UNBUNDLED to any value to get the separate files, which is easier
// to read in a browser's tools; the page works either way.
func FS() fs.FS {
	if os.Getenv(UnbundledEnv) != "" {
		return source()
	}
	joinOnce.Do(func() {
		files, err := Bundled()
		if err != nil {
			// The test of the bundle fails first; this keeps a binary that got
			// past it showing the page, slowly, and not showing nothing.
			log.Printf("webui: the front end could not be joined, so it is sent as separate files: %v", err)
			joined = source()
			return
		}
		joined = overlay{base: source(), files: files}
	})
	return joined
}

// UnbundledEnv is the environment variable that turns the joining off.
const UnbundledEnv = "FLOCKDECK_UNBUNDLED"

// DirEnv names a folder to serve the front end from instead of the files built
// into the program, so a change to ui/src shows on a reload without a build.
// It is for development and is off unless set.
const DirEnv = "FLOCKDECK_UI_DIR"

// DirFS is the folder named by FLOCKDECK_UI_DIR, as a file system laid out like
// FS: index.html at the top. It says so in the log when it is used, and when the
// folder cannot be used it says why and returns false, so the program starts
// with the files built into it. The files are sent as they are, not joined.
func DirFS() (fs.FS, bool) {
	dir := os.Getenv(DirEnv)
	if dir == "" {
		return nil, false
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		log.Printf("webui: %s=%q is ignored, there is no index.html in it (%v); serving the front end built into this program", DirEnv, dir, err)
		return nil, false
	}
	log.Printf("webui: serving the front end from %s (%s), not the one built into this program; windows reached through the relay still get the built-in one", dir, DirEnv)
	return os.DirFS(dir), true
}

var (
	joinOnce sync.Once
	joined   fs.FS
)

// source is the front end as it is written: one file per module.
func source() fs.FS {
	sub, err := fs.Sub(embedded, "src")
	if err != nil {
		// The path is a compile-time constant, so this cannot happen.
		panic(err)
	}
	return sub
}

// Bundled is the files the joining makes, by name relative to the assets
// directory: main.js, app.css and index.html.
func Bundled() (map[string][]byte, error) {
	src := source()
	js, err := bundle.Modules(src, "main.js")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(src, "index.html")
	if err != nil {
		return nil, err
	}
	css, page, err := bundle.Styles(src, index, "app.css")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"main.js": js, "app.css": css, "index.html": page}, nil
}

// overlay is the source with some files replaced by made ones.
type overlay struct {
	base  fs.FS
	files map[string][]byte
}

func (o overlay) Open(name string) (fs.File, error) {
	if data, ok := o.files[name]; ok {
		return &madeFile{Reader: bytes.NewReader(data), name: name, size: int64(len(data))}, nil
	}
	return o.base.Open(name)
}

// madeFile is a file held in memory. It can seek, which the file server needs
// to answer a request for part of it and to find its length.
type madeFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *madeFile) Stat() (fs.FileInfo, error) { return madeInfo{f.name, f.size}, nil }
func (f *madeFile) Close() error               { return nil }

var _ io.ReadSeeker = (*madeFile)(nil)

type madeInfo struct {
	name string
	size int64
}

func (i madeInfo) Name() string       { return i.name }
func (i madeInfo) Size() int64        { return i.size }
func (i madeInfo) Mode() fs.FileMode  { return 0o444 }
func (i madeInfo) ModTime() time.Time { return time.Time{} }
func (i madeInfo) IsDir() bool        { return false }
func (i madeInfo) Sys() any           { return nil }

// Icon is the application's icon, as PNG bytes at a size sharp on a high-DPI
// display without being wasteful to decode. It is what the desktop window is
// given as its own icon -- see internal/appwindow -- rather than one read
// from a platform-specific resource, so every platform's window carries the
// same source image the Windows .syso and the PWA manifest already do.
func Icon() []byte {
	data, err := fs.ReadFile(embedded, "src/icon-256.png")
	if err != nil {
		// The path is a compile-time constant naming a file this module
		// ships, so this cannot happen.
		panic(err)
	}
	return data
}
