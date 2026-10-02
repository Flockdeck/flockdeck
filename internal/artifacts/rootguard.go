package artifacts

import (
	"errors"
	"io"
	"io/fs"
	"os"
)

// maxDirEntries bounds the look for one entry in a folder, so that a folder with
// an absurd number of names cannot hold a request.
const maxDirEntries = 1 << 20

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// hasEntry is whether the folder dir (a slash path below the root, "." for the
// root) has an entry named exactly name, byte for byte. It reads the folder's
// names and trusts no lookup, which is the point: a file system that ignores
// case or composition resolves a lookup of another spelling, and lists only the
// spelling the entry was made with.
//
// The folder is looked at without following a link first, and the folder that
// is then opened must be the same one: a folder swapped for a link in between
// is not read.
func (r *Root) hasEntry(dir, name string) (bool, error) {
	before, err := r.r.Lstat(dir)
	if err != nil {
		return false, err
	}
	if before.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 || !before.IsDir() {
		return false, errors.New("not a plain folder")
	}
	d, err := r.r.Open(dir)
	if err != nil {
		return false, err
	}
	defer d.Close()
	opened, err := d.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(before, opened) {
		return false, errors.New("the folder changed")
	}
	for seen := 0; ; {
		names, err := d.Readdirnames(256)
		for _, n := range names {
			if n == name {
				return true, nil
			}
		}
		seen += len(names)
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if seen > maxDirEntries {
			return false, errors.New("too many entries")
		}
	}
}
