package artifacts

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const isWindows = runtime.GOOS == "windows"

// foldCase is whether the platform's usual file systems ignore case, so that a
// candidate spelled with other capitals is still the same place.
var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// MaxCandidateBytes is the longest path Open will look at. Nothing a viewer
// shows needs more, and a longer one is refused before any work is done on it.
const MaxCandidateBytes = 4096

// MaxViewBytes is the most of one file that is ever read for a viewer. A larger
// file is shown clipped, never refused, and never read past this.
const MaxViewBytes = 5 << 20

// ErrUnavailable is the only thing a client is ever told about a refusal, so
// that asking cannot tell a file that is not there from one that is forbidden
// from one that went away.
var ErrUnavailable = errors.New("artifact unavailable")

// Refusal is an error that is ErrUnavailable to a client and says why to this
// machine. Use ReasonOf to read the reason, for a log; never send it anywhere.
//
// The reason is not exported and the error does not print or encode it, in any
// verb or format (%v, %+v, %#v, %s, JSON): a Refusal that is formatted or
// marshalled into a reply by mistake says only what a client may be told.
type Refusal struct{ reason Reason }

func (e *Refusal) Error() string { return ErrUnavailable.Error() }

// Is makes errors.Is(err, ErrUnavailable) true for every refusal.
func (e *Refusal) Is(target error) bool { return target == ErrUnavailable }

// Format prints the same words for every verb, so %+v and %#v cannot print
// the reason field.
func (e Refusal) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, ErrUnavailable.Error()) }

// GoString is for the same reason as Format.
func (e Refusal) GoString() string { return ErrUnavailable.Error() }

// MarshalJSON encodes a Refusal as the one thing a client may be told.
func (e Refusal) MarshalJSON() ([]byte, error) { return []byte(`"unavailable"`), nil }

func refuse(r Reason) error { return &Refusal{reason: r} }

// ReasonOf is why err refused a path, or "" if err is not a Refusal.
func ReasonOf(err error) Reason {
	var r *Refusal
	if errors.As(err, &r) {
		return r.reason
	}
	return ""
}

// Root is a directory files may be shown from: a pane's project or worktree,
// or the recordings folder. Nothing outside it is ever opened through it.
type Root struct {
	// path is the directory as the system really names it, with every link in
	// it followed; given is the same directory as the caller spelled it, which
	// is how an agent that was started in it will spell the files it writes.
	path, given string
	r           *os.Root
}

// NewRoot opens dir as a root. dir must be a directory.
//
// NewRoot follows every link in dir (a temporary folder, a drive's alias, a
// junction), because it is the caller's own choice of where to show files from.
// It must therefore only ever be built from a path the host itself chose -- a
// pane's own project or worktree, the recordings folder -- and never from one
// an agent, a transcript, a remote client or a file's contents could have
// influenced: a root made from such a path is a way to anywhere.
func NewRoot(dir string) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := canonical(abs)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	r, err := os.OpenRoot(real)
	if err != nil {
		return nil, err
	}
	return &Root{path: real, given: abs, r: r}, nil
}

// Close releases the root.
func (r *Root) Close() error { return r.r.Close() }

// Path is the root's real directory.
func (r *Root) Path() string { return r.path }

// File is an open, vetted, read-only file. The only way to read it is Limited.
// Everything read through it, by any number of readers, is counted against one
// total of MaxViewBytes, served in order from the start; the *os.File itself is
// never reachable, so no caller can seek, read past the total or write.
//
// Size is the file's size when it was opened. A file that grows or shrinks
// afterwards is read as it is, up to the total; the reader will not say that it
// was cut short (see the package documentation's list of what is not done).
type File struct {
	f *os.File
	// Rel is the file's path below the root, with forward slashes. It is what
	// a viewer is shown as the name; an absolute path is never sent.
	Rel     string
	Size    int64
	ModTime time.Time

	mu     sync.Mutex
	served int64 // bytes handed out so far, which is also the next offset
	closed bool
}

// Limited is a reader of at most n more bytes of the file, from where the
// previous reader stopped. However many readers are made, and however large n
// is, the total ever served is at most MaxViewBytes. The result is a private
// type: it holds no exported way to the file.
func (f *File) Limited(n int64) io.Reader { return &fileReader{file: f, left: n} }

type fileReader struct {
	file *File
	left int64
}

func (r *fileReader) Read(p []byte) (int, error) {
	f := r.file
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, os.ErrClosed
	}
	room := min(r.left, MaxViewBytes-f.served)
	if room <= 0 || len(p) == 0 {
		if room <= 0 {
			return 0, io.EOF
		}
		return 0, nil
	}
	if int64(len(p)) > room {
		p = p[:room]
	}
	n, err := f.f.ReadAt(p, f.served)
	f.served += int64(n)
	r.left -= int64(n)
	if n > 0 && err == io.EOF {
		err = nil // the end is reported by the next read
	}
	return n, err
}

// Close closes the file; readers made from it fail after that.
func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.f.Close()
}

// raceHook is called between the steps of Open, at "checked" (the path has
// been looked at, nothing is open) and "opened" (the file is open, nothing is
// vetted yet). It does nothing here; tests replace it to swap the path for a
// link at exactly those moments, which is how a race is made to happen on
// purpose. It is never set outside a test.
var raceHook = func(stage string) {}

// Open is how a file is shown: it opens candidate, a path the host discovered
// (absolute, or relative to the root), for reading, or refuses with an error
// for which errors.Is(err, ErrUnavailable) is true. The rules are in the
// package documentation. Everything is read back from the open handle, so a
// path swapped between a check and the open is caught.
func (r *Root) Open(candidate string) (*File, error) {
	if len(candidate) > MaxCandidateBytes || !cleanChars(candidate, isWindows) {
		return nil, refuse(ReasonLexical)
	}
	rel, ok := r.relativize(candidate)
	if !ok {
		return nil, refuse(ReasonOutside)
	}
	if isWindows {
		rel = filepath.ToSlash(rel)
	} else if strings.Contains(rel, `\`) {
		return nil, refuse(ReasonLexical)
	}
	rel = path.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
		return nil, refuse(ReasonOutside)
	}
	if !checkLexical(rel, isWindows) {
		return nil, refuse(ReasonLexical)
	}
	if denied(rel) {
		return nil, refuse(ReasonDenied)
	}
	// Refused before it is opened so that a device or a pipe is never opened at
	// all, and again afterwards because only the second look is about the
	// thing that was opened.
	if _, why := r.walk(rel); why != "" {
		return nil, refuse(why)
	}
	raceHook("checked")
	f, err := r.r.OpenFile(rel, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, refuse(missingOrOutside(err))
	}
	raceHook("opened")
	file, err := r.vet(f, rel)
	if err != nil {
		f.Close()
		return nil, err
	}
	return file, nil
}

// missingOrOutside is the reason an open failed, for the log.
func missingOrOutside(err error) Reason {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return ReasonMissing
	}
	// Anything else -- the root refusing a path that leaves it, a link refused
	// by the open, too many links -- is a refusal by the root. os.Root has no
	// error type for it, so it is not told apart by its message, which could
	// change; it is one reason.
	return ReasonOutside
}

// vet checks the open file is the plain file the path says, and fills in a File.
func (r *Root) vet(f *os.File, rel string) (*File, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, refuse(ReasonMissing)
	}
	if !fi.Mode().IsRegular() {
		return nil, refuse(ReasonNotRegular)
	}
	// The path has been looked at again now the file is open. Links on the way
	// are refused, and the thing at the end of the path must be the very file
	// that was opened: a path that was a link when it was opened and plain
	// again when it is looked at is not the same file.
	leaf, why := r.walk(rel)
	if why != "" {
		return nil, refuse(why)
	}
	if !os.SameFile(fi, leaf) {
		return nil, refuse(ReasonChanged)
	}
	n, err := linkCount(f)
	if err != nil {
		return nil, refuse(ReasonMissing)
	}
	if n != 1 {
		return nil, refuse(ReasonHardlink)
	}
	if err := r.checkRealName(f, rel); err != nil {
		return nil, err
	}
	return &File{f: f, Rel: rel, Size: fi.Size(), ModTime: fi.ModTime()}, nil
}

// walk looks at each part of rel, from the root down, without following
// anything: every folder on the way must be a plain folder and the last part a
// plain file. It returns the last part's information.
func (r *Root) walk(rel string) (fs.FileInfo, Reason) {
	parts := strings.Split(rel, "/")
	var last fs.FileInfo
	for i := range parts {
		fi, err := r.r.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				return nil, ReasonMissing
			}
			return nil, ReasonOutside // the root refused the path
		}
		m := fi.Mode()
		switch {
		case m&(fs.ModeSymlink|fs.ModeIrregular) != 0:
			return nil, ReasonLink // a symbolic link, or on Windows a junction or other reparse point
		case i < len(parts)-1 && !m.IsDir():
			return nil, ReasonNotRegular
		case i == len(parts)-1 && !m.IsRegular():
			return nil, ReasonNotRegular
		}
		last = fi
	}
	return last, ""
}

// relativize turns a candidate into a path below the root, by text. A
// relative candidate is already one. An absolute candidate must start with the
// root, spelled either as the system names it or as the caller gave it.
func (r *Root) relativize(c string) (string, bool) {
	if !absLike(c) {
		return c, true
	}
	for _, base := range []string{r.path, r.given} {
		if rel, ok := under(base, c); ok {
			return rel, true
		}
	}
	return "", false
}

// absLike is whether c names a place by itself, in either platform's syntax:
// the check is the same everywhere so a Windows path is not "relative" to a
// Unix host and so slipped past it.
func absLike(c string) bool {
	if c == "" {
		return false
	}
	if c[0] == '/' || c[0] == '\\' || filepath.IsAbs(c) {
		return true
	}
	return len(c) >= 2 && c[1] == ':' && isLetter(c[0])
}

// under is p below base, as a slash path, or false. Both are cleaned first;
// p being base itself is not below it.
func under(base, p string) (string, bool) {
	base, p = filepath.Clean(base), filepath.Clean(p)
	if len(p) <= len(base) {
		return "", false
	}
	head := p[:len(base)]
	if head != base && !(foldCase && strings.EqualFold(head, base)) {
		return "", false
	}
	rest := p[len(base):]
	sep := string(filepath.Separator)
	if !strings.HasSuffix(base, sep) {
		if !strings.HasPrefix(rest, sep) {
			return "", false
		}
		rest = rest[1:]
	}
	if rest == "" {
		return "", false
	}
	return filepath.ToSlash(rest), true
}
