//go:build !windows

package artifacts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// devOf is the device the file is on, to tell a mount from a folder.
func devOf(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// checkRealName holds every component that is not plain ASCII to being the
// real name of a directory entry, byte for byte.
//
// With no link on the way (walk) the name that was opened is the file's name
// on a file system that compares names exactly. Many do not: APFS (the macOS
// default), ext4 with casefold, vfat, NTFS, SMB and other mounts ignore case,
// and some treat different spellings as one name, so that ".ſsh" (a long s)
// opens ".ssh" and "paßword.txt" opens "password.txt", after the name check
// has judged the spelling it was given. The name check folds such letters
// (internal/secretname), and this is the second line: a component that is not
// ASCII must be an entry of its folder exactly as spelled, which a file system
// that folded it to something else cannot say, and is refused (ReasonAlias)
// when it is not. A plain ASCII name needs no look: the name check reads it,
// with case ignored, as what any file system would.
//
// Nothing here is specific to a platform: the exact-entry test is hasEntry,
// which is tested on every OS. What is verified only by reading is that APFS,
// ext4 casefold and the other folding file systems do resolve such names as
// described; CI has none.
func (r *Root) checkRealName(_ *os.File, rel string) error {
	parts := strings.Split(rel, "/")
	for i, c := range parts {
		if isASCII(c) {
			continue
		}
		dir := "."
		if i > 0 {
			dir = strings.Join(parts[:i], "/")
		}
		ok, err := r.hasEntry(dir, c)
		if err != nil {
			return refuse(ReasonMissing)
		}
		if !ok {
			return refuse(ReasonAlias)
		}
	}
	return nil
}

// ownedBy is whether the file belongs to uid (always true if there is no uid).
func ownedBy(fi fs.FileInfo, uid int) bool {
	if uid < 0 {
		return true
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

// osProfileDir has no answer here: the password file is asked instead.
func osProfileDir() string { return "" }
