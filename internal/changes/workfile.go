package changes

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// parentsArePlain says whether every directory above rel is an ordinary
// directory of the checkout: not a symbolic link, and not a junction or another
// reparse point. git reads the working tree through such a link, and the diff of
// a file behind one would be the diff of something outside the checkout. A
// directory that is not there is plain, since the file may be a deleted one.
func parentsArePlain(cwd, rel string) bool {
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return false
	}
	defer root.Close()
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		fi, err := root.Lstat(filepath.FromSlash(strings.Join(parts[:i], "/")))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return true
			}
			return false
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !fi.IsDir() {
			return false
		}
	}
	return true
}

// readWorking reads a file of the checkout as the diff sees it. A link is its
// target text and is never followed. Everything is done through a root opened on
// the checkout, so a parent directory that is a link, or a junction on Windows,
// to somewhere else is refused and not followed out of the checkout; the file is
// examined and read through one handle, only a regular file is read, and no more
// than maxContextFile bytes are. size is the file's size when it could be looked
// at, and ok is false when the data was not read.
func readWorking(cwd, rel string) (data []byte, size int64, ok bool) {
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return nil, 0, false
	}
	defer root.Close()
	rel = filepath.FromSlash(rel)
	fi, err := root.Lstat(rel)
	if err != nil {
		return nil, 0, false
	}
	size = fi.Size()
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(rel)
		if err != nil {
			return nil, size, false
		}
		return []byte(target + "\n"), size, true
	}
	if !fi.Mode().IsRegular() || size > maxContextFile {
		return nil, size, false
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|openNoBlock, 0)
	if err != nil {
		return nil, size, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, size, false
	}
	size = st.Size()
	if size > maxContextFile {
		return nil, size, false
	}
	data, err = io.ReadAll(io.LimitReader(f, maxContextFile+1))
	if err != nil || len(data) > maxContextFile {
		return nil, size, false
	}
	return data, size, true
}
