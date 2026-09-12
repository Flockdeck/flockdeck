// Package gitx is Flockdeck's use of git: the status a pane header shows, the
// review panel's file list, diffs and commit (changes.go, commit.go), push,
// pull and fetch (remote.go), and the worktrees that let several agents work
// in parallel without fighting over one checkout (worktree.go). Every command
// goes through run.go, which gives it a deadline and shapes what it says when
// it fails.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	// A rebase or a bisect detaches HEAD while it runs, and git's list says
	// only that; the status ListDetailed fills in knows which branch it is.
	// The panel called a worktree stopped on a conflict "detached@74a430f"
	// while its pane header, reading the same status, called it by its branch.
	if w.Status.Branch != "" {
		if w.Status.Operation == "" {
			return w.Status.Branch
		}
		return w.Status.Branch + " (" + w.Status.Operation + ")"
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
	return respeller(dir)(filepath.Clean(root)), nil
}

// respeller returns what gives a path git reported back in the spelling base
// is written in.
//
// git names every path with its symlinks resolved, and everything else
// Flockdeck holds about a project -- its tabs, its panes, the layout saved for
// it -- is keyed on the path it was opened with. Where base reaches a path
// through a link, as everything under /var does on macOS, the path is given
// back through the same link, so the two compare equal. Windows has the same
// in short names: a user whose name has a space in it has a temporary
// directory like C:\Users\JOHNSM~1\AppData\Local\Temp, which git names in
// full. A base with nothing of the kind on its way is the common case and
// costs one lookup.
func respeller(base string) func(string) string {
	same := func(p string) string { return p }
	abs, err := filepath.Abs(base)
	if err != nil {
		return same
	}
	if real, err := filepath.EvalSymlinks(abs); err != nil || real == abs {
		return same
	}
	// Each directory on the way up that a link changes, deepest first, so a
	// path is put back under the nearest one it sits in.
	type spelling struct{ given, real string }
	var ways []spelling
	for d := abs; ; d = filepath.Dir(d) {
		if real, err := filepath.EvalSymlinks(d); err == nil && real != d {
			ways = append(ways, spelling{d, real})
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return func(p string) string {
		for _, w := range ways {
			if p == w.real {
				return w.given
			}
			if rest, ok := strings.CutPrefix(p, w.real+string(filepath.Separator)); ok {
				return filepath.Join(w.given, rest)
			}
		}
		return p
	}
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
	spell := respeller(dir)

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
			cur = &Worktree{Path: spell(filepath.Clean(val))}
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
	if err != nil && !force {
		// The panel forces a removal only when the row it drew showed work
		// that would be lost, so a worktree that has changed since was
		// refused with git's "use --force to delete it" -- a flag nobody
		// pressing a button can pass. The status says what is there, in the
		// panel's terms.
		if st := StatusOf(path); st.HasChanges() {
			return &gitError{fmt.Sprintf("%s has uncommitted work now (%d changed, %d new); "+
				"refresh the list and remove it again to discard it, or commit it first",
				filepath.Base(path), st.Dirty, st.Untracked)}
		}
	}
	if err != nil {
		// On Windows a file held open -- an editor, a shell sitting in the
		// folder -- lets git drop its record of the worktree and then fail to
		// delete the folder, with nothing better to say than "Invalid
		// argument". The folder is left behind, no longer a worktree, and
		// pressing remove again only answers that it is not one.
		if _, still, lerr := findWorktree(repoDir, path); lerr == nil && !still {
			if _, serr := os.Lstat(path); serr == nil {
				return &gitError{fmt.Sprintf("%s is no longer a worktree, but its folder could not be deleted: "+
					"something still has a file open in it. Close whatever is using %s, then delete the folder.",
					filepath.Base(path), path)}
			}
		}
	}
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
	// Cleaned first: the parent of "C:\repo\" is "C:\repo" to filepath.Dir, so
	// a project opened with a trailing separator -- which is what a shell's
	// tab completion leaves -- had every new worktree suggested inside the
	// repository, as a directory its own status then listed as untracked.
	repoRoot = filepath.Clean(repoRoot)
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

// foldPath puts a path in the form two of them can be compared in: cleaned,
// with its symlinks resolved, and in one case on Windows.
//
// git reports every path with its symlinks resolved, and a directory can be
// reached through one: on macOS the temporary directory is, since /var is
// /private/var, and so is anything a person keeps under a linked folder.
// Compared as given, a worktree there was never found in git's own list, so
// it could not be removed. A path that is no longer there, a worktree deleted
// by hand, has its folder resolved instead, which is as far as can be told.
func foldPath(path string) string {
	path = filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	} else if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(dir, filepath.Base(path))
	}
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

// ListDetailed returns every worktree with its status filled in.
//
// Each worktree needs its own status call, so they are run concurrently: a
// repository with several worktrees would otherwise make the panel wait for
// them one after another.
func ListDetailed(dir string) ([]Worktree, error) {
	wts, err := List(dir)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	for i := range wts {
		// A worktree whose directory is gone has no status to read; asking
		// only spends a process on an error.
		if wts[i].Bare || wts[i].Prunable {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wts[i].Status = StatusOf(wts[i].Path)
		}(i)
	}
	wg.Wait()
	return wts, nil
}

// Prune removes administrative records for worktrees whose directories have
// been deleted behind git's back, and reports how many went.
//
// The count is what lets the panel say whether the button it just offered
// did anything. git names each record it removes on stderr, one to a line,
// so they are counted as lines rather than looked for by the word they start
// with -- which is translated when git is speaking anything but English.
func Prune(repoDir string) (int, error) {
	_, said, err := runCapture(context.Background(), commandTimeout, repoDir,
		"worktree", "prune", "--verbose")
	if err != nil {
		return 0, err
	}
	var pruned int
	for _, line := range strings.Split(said, "\n") {
		if strings.TrimSpace(line) != "" {
			pruned++
		}
	}
	return pruned, nil
}

// AddFrom creates a worktree at path.
//
// When branch names an existing local branch it is checked out; otherwise a new
// branch is created, starting at base when one is given and at the current HEAD
// when it is not.
// The path and the starting point both arrive from the window, so they are put
// after a "--": without it git reads anything beginning with a dash as one of
// its own options. A base of "--force" was not refused, it was obeyed, and the
// new worktree quietly started from HEAD with a force checkout instead of from
// wherever the user had named.
func AddFrom(repoDir, path, branch, base string) error {
	args := []string{"worktree", "add"}
	switch {
	case branch == "":
		args = append(args, "--detach", "--", path)
		if base != "" {
			args = append(args, base)
		}
	case branchExists(repoDir, branch):
		args = append(args, "--", path, branch)
	default:
		// git's word on a name it will not take is only that it is "not a
		// valid branch name", after a line of its own progress, which leaves
		// the person who typed "my feature" to guess what is wrong with it.
		if _, err := run(repoDir, "check-ref-format", "--branch", branch); err != nil {
			msg := fmt.Sprintf("%q cannot be a branch name: git allows no spaces, \"..\", \":\", \"~\", \"^\", \"?\", \"*\", \"[\" "+
				"or backslash in one, and none ending in \"/\" or \".lock\"", branch)
			if s := branchSuggestion(branch); s != "" && s != branch {
				if _, serr := run(repoDir, "check-ref-format", "--branch", s); serr == nil {
					msg += fmt.Sprintf("; %q would do", s)
				}
			}
			return &gitError{msg}
		}
		// --no-track, because a branch started from a remote one -- a base of
		// "origin/main" -- would otherwise track it: the header said the new
		// branch was "tracking origin/main", and Push, finding an upstream,
		// pushed plainly and was refused for the names not matching. With
		// none, the first push sets up one of its own name, as it does for a
		// branch started anywhere else.
		args = append(args, "-b", branch, "--no-track", "--", path)
		if base != "" {
			args = append(args, base)
		}
	}
	_, err := run(repoDir, args...)
	if err != nil && branch != "" {
		// A branch still recorded as checked out in a worktree whose directory
		// has gone cannot be checked out again until that record is cleared,
		// and git says only that it is "already used by worktree at" a path
		// that is not there -- after a line of its own progress.
		if wts, lerr := List(repoDir); lerr == nil {
			for _, wt := range wts {
				if wt.Branch != branch {
					continue
				}
				if wt.Prunable {
					return &gitError{fmt.Sprintf("%s is still recorded as checked out in %s, which no longer exists; "+
						"Prune in the Worktrees panel clears that record, and then it can be created again", branch, wt.Path)}
				}
				// Somebody who typed the branch they are already on -- the
				// main checkout's, as often as not -- was told only that it
				// was "already used by worktree at" a path.
				return &gitError{fmt.Sprintf("%s is already checked out in %s; open an agent there from the list, "+
					"or give the new worktree a branch of its own", branch, wt.Path)}
			}
		}
	}
	return err
}

// branchSuggestion turns a name git refused into one it would take, for the
// message that says so: runs of what a branch cannot hold become one dash,
// and what it cannot end with is taken off.
func branchSuggestion(name string) string {
	var b strings.Builder
	var dash bool
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == 0x5c || strings.ContainsRune("~^:?*[", r) {
			if !dash {
				b.WriteRune('-')
				dash = true
			}
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	s := b.String()
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	s = strings.ReplaceAll(s, "@{", "@-")
	for {
		trimmed := strings.TrimSuffix(strings.Trim(s, "-/."), ".lock")
		if trimmed == s {
			return s
		}
		s = trimmed
	}
}

// DefaultBase returns a sensible starting point for a new branch: the current
// branch of the main worktree.
//
// A rebase in progress there has to be stepped around. It detaches HEAD while
// it replays commits, so there is no current branch to offer and "HEAD" means
// whichever commit the replay happens to be sitting on -- a starting point
// nobody means, and one that will not exist as anything once the rebase
// finishes. The branch being rebased still points at where it was before the
// rebase started, which is a real place to branch from.
//
// A branch nobody has committed to yet is not one either: it names no commit,
// and the panel's pre-filled base made every new worktree in a fresh
// repository fail with "invalid reference: main". Offering nothing leaves git
// to start the new branch empty, as it does when no base is given.
func DefaultBase(repoDir string) string {
	if b := CurrentBranch(repoDir); b != "" {
		if !branchExists(repoDir, b) {
			return ""
		}
		return b
	}
	if b, _ := operationBranch(context.Background(), repoDir); b != "" {
		return b
	}
	return "HEAD"
}
