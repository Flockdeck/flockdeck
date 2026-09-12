// Package gitx wraps the few git commands the worktree manager needs.
//
// Worktrees are what let several agents work in parallel without fighting over
// one checkout: each pane gets its own directory and branch.
package gitx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// Worktree is one entry from `git worktree list`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short branch name, empty when detached
	Bare     bool
	Detached bool
	Locked   bool
	// Main reports whether this is the repository's primary worktree.
	Main bool
	// Prunable reports that the worktree's directory is gone -- deleted by
	// hand, or on a drive that is not there -- and only git's record of it is
	// left. Its Status is empty, which is not the same thing as clean.
	Prunable bool
	// Status is filled in by ListDetailed.
	Status Status
}

// Label returns a human-readable name for the worktree.
func (w Worktree) Label() string {
	if w.Branch != "" {
		return w.Branch
	}
	if w.Detached && len(w.Head) >= 7 {
		return "detached@" + w.Head[:7]
	}
	return filepath.Base(w.Path)
}

// Available reports whether git is installed.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Root returns the top level of the working tree containing dir.
//
// git answers with forward slashes even on Windows, so the path is cleaned to
// the platform's own form: List does the same, and the two are compared
// against each other to tell the main worktree from the linked ones.
func Root(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", &gitError{"git did not report a working tree root"}
	}
	return filepath.Clean(root), nil
}

// IsRepo reports whether dir is inside a git repository.
func IsRepo(dir string) bool {
	_, err := Root(dir)
	return err == nil
}

// CurrentBranch returns the checked-out branch, or "" when detached.
//
// symbolic-ref reads the branch HEAD points at without needing a commit on it,
// where `rev-parse --abbrev-ref HEAD` fails outright in a repository nobody
// has committed to yet -- which made a fresh repository look detached, and its
// first push report "cannot push a detached HEAD".
func CurrentBranch(dir string) string {
	out, err := run(dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "" // detached, or not a repository
	}
	return strings.TrimSpace(out)
}

// List returns every worktree of the repository containing dir.
func List(dir string) ([]Worktree, error) {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var (
		res []Worktree
		cur *Worktree
	)
	flush := func() {
		if cur != nil && cur.Path != "" {
			res = append(res, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: filepath.Clean(val)}
		case "HEAD":
			if cur != nil {
				cur.Head = val
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	flush()

	// git lists the main worktree first.
	if len(res) > 0 {
		res[0].Main = true
	}
	return res, nil
}

// BranchExists reports whether a local branch is already present.
func BranchExists(dir, branch string) bool { return branchExists(dir, branch) }

// branchExists reports whether a local branch is already present.
func branchExists(dir, branch string) bool {
	_, err := run(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// Remove deletes a worktree. force discards uncommitted changes in it.
//
// When the directory has already been deleted by hand -- which is how these
// usually disappear -- git refuses to remove a path that is not there. Pruning
// the record it left behind is what the person pressing remove meant, and it
// is the only thing left to do.
//
// That prune is why the path is checked against the repository's own list
// first. It is not selective: it discards the record of every worktree whose
// directory is missing, including one sitting on a drive that happens to be
// unplugged. A path git has never heard of should not be able to set that off,
// and reporting "removed" for it was a lie besides.
func Remove(repoDir, path string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return &gitError{"which worktree? no path was given"}
	}
	wt, known, err := findWorktree(repoDir, path)
	if err != nil {
		return err
	}
	if !known {
		return &gitError{path + " is not a worktree of this repository"}
	}
	// git refuses a locked worktree even when forced, and says to run
	// "remove -f -f", which is nothing a person pressing a button can do.
	// A lock is somebody saying on purpose that this one stays, so undoing
	// it is left to them, with the command that does it.
	if wt.Locked && !wt.Prunable {
		return &gitError{fmt.Sprintf("%s is locked, so it is kept; unlock it first with: git worktree unlock %q",
			wt.Label(), wt.Path)}
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		_, err := Prune(repoDir)
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "--", path)
	_, err = run(repoDir, args...)
	return err
}

// findWorktree returns the repository's record of the worktree at path, and
// whether it has one.
func findWorktree(repoDir, path string) (Worktree, bool, error) {
	wts, err := List(repoDir)
	if err != nil {
		return Worktree{}, false, err
	}
	for _, wt := range wts {
		if samePath(wt.Path, path) {
			return wt, true, nil
		}
	}
	return Worktree{}, false, nil
}

// samePath compares two paths as the file system would.
//
// The two reach here from different places -- one from git, the other from
// whatever the window sent back -- so on Windows they can name the same
// directory in different case, which a plain comparison calls different.
func samePath(a, b string) bool { return foldPath(a) == foldPath(b) }

// DefaultWorktreePath suggests where a new worktree for a branch should live:
// a sibling of the repository, named after it, so checkouts stay grouped
// together without nesting inside the repository itself.
//
// A name that is already taken is stepped around rather than suggested. That
// means two things, and only one of them is visible in the file system: a
// directory sitting there, which `git worktree add` refuses, and a worktree
// git still has a record of. The second is what someone who deleted a
// checkout in their file manager leaves behind, and it fails differently --
// "a missing but already registered worktree" -- for a path that looks free.
func DefaultWorktreePath(repoRoot, branch string) string {
	parent := filepath.Dir(repoRoot)
	name := filepath.Base(repoRoot) + "-" + worktreeSegment(branch)

	registered := map[string]bool{}
	if wts, err := List(repoRoot); err == nil {
		for _, wt := range wts {
			registered[foldPath(wt.Path)] = true
		}
	}
	taken := func(path string) bool {
		if registered[foldPath(path)] {
			return true
		}
		_, err := os.Lstat(path)
		return err == nil
	}

	path := filepath.Join(parent, name)
	// The last name tried is checked like the rest, so a hundred collisions
	// end in a name that is merely unlikely rather than one known to be
	// taken -- which is what a loop that stopped on the counter returned.
	for i := 2; taken(path); i++ {
		if i > 99 {
			return filepath.Join(parent, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()))
		}
		path = filepath.Join(parent, fmt.Sprintf("%s-%d", name, i))
	}
	return path
}

// foldPath puts a path in the form two of them can be compared in.
func foldPath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// worktreeSegment turns a branch name into one directory name.
//
// git allows characters in a ref that Windows will not have in a path -- most
// of "<>|\"" and the slashes of a namespaced branch -- and rejects a name
// ending in a dot or a space outright.
func worktreeSegment(branch string) string {
	var b strings.Builder
	var lastDash bool
	for _, r := range branch {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_', r == '.', r == '-':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	name := strings.Trim(b.String(), "-. ")
	if name == "" {
		// A branch named entirely in characters a path cannot hold, or no
		// branch at all for a detached checkout.
		return "worktree"
	}
	return name
}
