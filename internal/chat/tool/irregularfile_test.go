package tool

import (
	"io/fs"
	"testing"
	"time"
)

// modeInfo is a file described by nothing but its mode.
type modeInfo fs.FileMode

func (m modeInfo) Name() string       { return "f" }
func (m modeInfo) Size() int64        { return 0 }
func (m modeInfo) Mode() fs.FileMode  { return fs.FileMode(m) }
func (m modeInfo) ModTime() time.Time { return time.Time{} }
func (m modeInfo) IsDir() bool        { return fs.FileMode(m).IsDir() }
func (m modeInfo) Sys() any           { return nil }

// A file Go calls irregular -- on Windows, one behind a reparse point of an
// uncommon kind, a OneDrive placeholder -- reads like any other and is not
// refused; a pipe, a socket and a device are.
func TestOnlyPipesSocketsAndDevicesAreRefused(t *testing.T) {
	root := newRoot(t)
	for mode, refused := range map[fs.FileMode]bool{
		0:                                 false,
		fs.ModeIrregular:                  false,
		fs.ModeDir:                        false,
		fs.ModeNamedPipe:                  true,
		fs.ModeSocket:                     true,
		fs.ModeDevice | fs.ModeCharDevice: true,
	} {
		if err := regularFile(root, root.Dir(), modeInfo(mode)); (err != nil) != refused {
			t.Errorf("mode %v: regularFile = %v, want refused %v", mode, err, refused)
		}
	}
}
