package mdexport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The ways WriteNew refuses. None carries text from outside.
var (
	ErrNoDir    = errors.New("name a folder to save into")
	ErrBadDir   = errors.New("that folder is not a full path to a folder that exists")
	ErrTooBig   = errors.New("the export is over the size limit")
	ErrNoCreate = errors.New("the file could not be created in that folder")
)

// maxNameRunes is how much of the pane's name is kept in the file name, so that
// the time after it is always there and a long name cannot make two exports share
// a stem.
const maxNameRunes = 20

// WriteNew saves data in dir as a new file named from name and stamp, and never
// overwrites a file. The file keeps the start of name, then stamp. dir must be an absolute path to a folder that
// exists; the file is created inside its resolved path with a name that has no
// separator, so nothing it is given can put the file anywhere else. It returns
// the path written.
func WriteNew(dir, name, stamp string, data []byte) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", ErrNoDir
	}
	if len(data) > MaxBytes+(1<<20) {
		return "", ErrTooBig
	}
	if strings.ContainsRune(dir, 0) || !filepath.IsAbs(dir) || networkOrDevicePath(dir) {
		return "", ErrBadDir
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return "", ErrBadDir
	}
	if networkOrDevicePath(root) {
		return "", ErrBadDir
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return "", ErrBadDir
	}
	base := "flockdeck-" + safeStem(name, maxNameRunes) + "-" + safeStem(stamp, 40)
	for n := 1; n <= 1000; n++ {
		name := base + ".md"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.md", base, n)
		}
		path := filepath.Join(root, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", ErrNoCreate
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(path)
			return "", ErrNoCreate
		}
		return path, nil
	}
	return "", ErrNoCreate
}

// networkOrDevicePath says dir is a UNC share (two backslashes, host, share; or
// the same with slashes) or a device path (two backslashes, a dot): the file
// would go to another machine or a device. A drive path is fine, and so is the
// extended form with a question mark and a drive letter.
func networkOrDevicePath(dir string) bool {
	d := strings.ReplaceAll(dir, "/", "\\")
	if !strings.HasPrefix(d, "\\\\") {
		return false
	}
	rest := d[2:]
	if strings.HasPrefix(rest, "?\\") {
		r := rest[2:]
		return !(len(r) >= 3 && ((r[0] >= 'a' && r[0] <= 'z') || (r[0] >= 'A' && r[0] <= 'Z')) && r[1] == ':' && r[2] == '\\')
	}
	return true
}

// safeStem keeps letters, digits, dash and underscore, at most n of them.
func safeStem(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
		if b.Len() >= n {
			break
		}
	}
	if b.Len() == 0 {
		return "conversation"
	}
	return b.String()
}
