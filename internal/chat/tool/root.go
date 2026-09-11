package tool

import (
	"fmt"
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
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

// normCase folds a path for comparison on the platforms whose file names are
// case-insensitive, so that C:\Repo and c:\repo are not read as two places.
// Only the comparison is folded; the path handed back to the caller, and to
// the operating system, keeps the case it arrived with.
func normCase(p string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}

// evalExisting resolves the links in the longest prefix of p that exists, and
// puts the rest back on the end. A file about to be created has no links of
// its own to follow, but the directory it is being created in does.
func evalExisting(p string) string {
	cur, rest := p, ""
	for {
		if real, err := realPath(cur); err == nil {
			if rest == "" {
				return real
			}
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
