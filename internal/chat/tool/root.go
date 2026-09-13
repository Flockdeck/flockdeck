package tool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Root is the pane's working directory and the boundary of everything the
// tools can reach.
//
// The confinement is deliberately not a permission question. A model that
// asks to read C:\Users\someone\.ssh\id_rsa, or to write outside the worktree
// it was given, is refused outright: offering that for approval would put the
// decision in front of somebody who is approving a dozen calls a minute, and
// the one time it matters is the time it slips through.
type Root struct {
	dir string
}

// NewRoot resolves dir into a root, following symbolic links so that later
// comparisons are between real paths.
func NewRoot(dir string) (*Root, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("no working directory for the tools")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}
	if real, err := realPath(abs); err == nil {
		abs = real
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("working directory %s is not a directory", abs)
	}
	return &Root{dir: filepath.Clean(abs)}, nil
}

// Dir is the root itself.
func (r *Root) Dir() string { return r.dir }

// Resolve turns a path from the model into an absolute path inside the root,
// or refuses it. A relative path is taken from the root; an absolute one is
// accepted only if it is already inside it; "" means the root.
func (r *Root) Resolve(p string) (string, error) {
	p = strings.TrimSpace(p)
	var abs string
	switch {
	case p == "" || p == ".":
		return r.dir, nil
	case filepath.IsAbs(p):
		abs = filepath.Clean(p)
	default:
		abs = filepath.Join(r.dir, p)
	}
	if !within(r.dir, abs) {
		return "", fmt.Errorf("%s: %w", p, ErrOutsideRoot)
	}
	// A path may be inside the root by name and outside it in fact, because a
	// link somewhere along it points away. The link has to be followed to see
	// that, and only the part of the path that exists can be followed, so the
	// check is made against the longest existing prefix.
	if real := evalExisting(abs); !within(r.dir, real) {
		return "", fmt.Errorf("%s: %w", p, ErrOutsideRoot)
	}
	return abs, nil
}

// ResolveFile is Resolve for the tools that name one file. An empty path is
// refused rather than quietly meaning the working directory, because a call
// that forgot to say which file is a mistake, and a mistake should be sent
// back to the model rather than put in front of the user as a question about
// creating a file called nothing.
func (r *Root) ResolveFile(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	return r.Resolve(p)
}

// explain turns an error from the file system into one that names the path the
// way the model and the user do -- relative to the pane -- rather than as the
// absolute path the system reported, which is long, the same for every file,
// and not what anybody asked for. A missing file is said the same way on every
// platform, rather than in each system's own words.
func (r *Root) explain(err error) error {
	var pe *fs.PathError
	if !errors.As(err, &pe) || !filepath.IsAbs(pe.Path) {
		return err
	}
	cause := pe.Err
	if errors.Is(cause, fs.ErrNotExist) {
		cause = fs.ErrNotExist
	}
	return fmt.Errorf("%s: %w", r.Rel(pe.Path), cause)
}

// Rel is a path as it should be shown: relative to the root, with forward
// slashes, so that a message reads the same whichever platform produced it.
func (r *Root) Rel(abs string) string {
	rel, err := filepath.Rel(r.dir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// within reports whether p is root or sits under it.
func within(root, p string) bool {
	root, p = normCase(filepath.Clean(root)), normCase(filepath.Clean(p))
	if root == p {
		return true
	}
	// Compared as strings, not with filepath.Rel: on Windows that matches the
	// parts of two paths with strings.EqualFold, which takes the Kelvin sign
	// for a k just as strings.ToLower did (see normCase).
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(p, root)
}

// normCase folds a path for comparison on the platforms whose file names are
// case-insensitive, so that C:\Repo and c:\repo are not read as two places.
// Only the comparison is folded; the path handed back to the caller, and to
// the operating system, keeps the case it arrived with.
//
// Only ASCII letters are folded. Unicode lower-cases the Kelvin sign to k and
// a dotted capital I to i, but the file system keeps each apart from the
// letter it resembles: folded with strings.ToLower, a folder named with one
// of them in place of a k or an i in the root's name compared equal to the
// root, and a write to a file in it -- a sibling of the root, outside it --
// was let through. Folding less can only make a path spelled another way
// outside the ASCII letters compare unequal, which refuses it.
func normCase(p string) string {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return p
	}
	b := []byte(p)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// evalExisting resolves the links in the longest prefix of p that exists, and
// puts the rest back on the end. A file about to be created has no links of
// its own to follow, but the directory it is being created in does.
//
// A link whose target is not there cannot be resolved, but it is there itself,
// and it is not a name that is free: a file written through it is created at
// its target, wherever that is. So such a link is replaced by its target,
// which is resolved in turn, rather than stepped over as though it were a
// name yet to be made -- which judged a link in the pane to a file not yet
// made outside it as inside, and let the write make the file out there.
func evalExisting(p string) string {
	cur, rest := p, ""
	for hops := 0; ; {
		if real, err := realPath(cur); err == nil {
			if rest == "" {
				return real
			}
			return filepath.Join(real, rest)
		}
		if target, err := os.Readlink(cur); err == nil {
			// Links that lead round in a circle can be neither resolved nor
			// written through; "" is inside no root, and so refused.
			if hops++; hops > 255 {
				return ""
			}
			if !filepath.IsAbs(target) {
				// A relative target is taken from where the link really is.
				dir := filepath.Dir(cur)
				if real, err := realPath(dir); err == nil {
					dir = real
				}
				target = filepath.Join(dir, target)
			}
			cur = filepath.Clean(target)
			continue
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
