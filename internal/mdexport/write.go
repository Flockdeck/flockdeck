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

// WriteNew saves data in dir as a new file whose name Flockdeck makes from stem
// and never overwrites a file. dir must be an absolute path to a folder that
// exists; the file is created inside its resolved path with a name that has no
// separator, so nothing it is given can put the file anywhere else. It returns
// the path written.
func WriteNew(dir, stem string, data []byte) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", ErrNoDir
	}
	if len(data) > MaxBytes+(1<<20) {
		return "", ErrTooBig
	}
	if strings.ContainsRune(dir, 0) || !filepath.IsAbs(dir) {
		return "", ErrBadDir
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return "", ErrBadDir
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return "", ErrBadDir
	}
	base := "flockdeck-" + safeStem(stem)
	for n := 1; n <= 50; n++ {
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

// safeStem keeps letters, digits, dash and underscore, at most 40 of them.
func safeStem(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "conversation"
	}
	return b.String()
}
