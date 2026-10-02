//go:build !windows

package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// openFlags: never block opening a file that turned into a pipe between the
// look at it and the open, never follow a link in the last part, and never
// let an opened terminal device become this process's controlling terminal.
const openFlags = syscall.O_NONBLOCK | syscall.O_NOFOLLOW | syscall.O_NOCTTY

// canonical is where dir really is, with every link followed.
func canonical(dir string) (string, error) { return filepath.EvalSymlinks(dir) }

// linkCount is how many names the open file has.
func linkCount(f *os.File) (uint64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no link count")
	}
	return uint64(st.Nlink), nil
}

// checkRealName has nothing to do here: with no link on the way (walk) the
// name that was opened is the file's name.
func (r *Root) checkRealName(*os.File, string) error { return nil }
