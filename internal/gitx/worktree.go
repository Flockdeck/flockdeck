// Package gitx wraps the few git commands the worktree manager needs.
//
// Worktrees are what let several agents work in parallel without fighting over
// one checkout: each pane gets its own directory and branch.
package gitx

import (
	"bytes"
	"context"
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

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// commandTimeout bounds a git invocation that only reads the local repository,
// so a hung command cannot freeze the UI thread.
const commandTimeout = 20 * time.Second

// networkTimeout bounds push, pull and fetch instead.
//
// Twenty seconds is nothing to a command that talks to a remote: a first push
// of a branch with any history behind it, a fetch of a repository nobody has
// cloned recently, or any of it over a link that is having a bad day. Killing
// those part way through and reporting a hang is worse than waiting, and
// nothing is waiting on them -- they run off the UI thread and tell the panel
// when they are done. Asking for a password is already refused outright, so
// the hang these guard against cannot happen here in the first place.
//
// It is a variable so a test can shorten it; nothing else assigns to it.
var networkTimeout = 10 * time.Minute

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

// run executes git in dir and returns stdout.
func run(dir string, args ...string) (string, error) {
	out, _, err := runCapture(context.Background(), commandTimeout, dir, args...)
	return out, err
}

// runUntil is run for a command whose answer stops being wanted part way
// through, so that cancelling ctx kills the process rather than leaving it to
// finish work nobody will read.
func runUntil(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, err := runCapture(ctx, commandTimeout, dir, args...)
	return out, err
}

// runVerbose returns what git said on both streams, under the network deadline.
//
// push, pull and fetch write their progress and their summary to stderr, so a
// caller that shows the user only stdout shows them nothing at all: stderr
// comes first because that is the order the two were written in. They are also
// the only commands here that talk to anything outside the machine, which is
// why they are the ones given the longer deadline.
func runVerbose(dir string, args ...string) (string, error) {
	out, errText, err := runCapture(context.Background(), networkTimeout, dir, args...)
	if err != nil {
		return "", err
	}
	return cleanProgress(errText + out), nil
}

// cleanProgress collapses git's in-place progress lines -- "Writing objects:
// 33%\rWriting objects: 100%, done." -- down to the state they finished in.
func cleanProgress(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.LastIndex(line, "\r"); i >= 0 {
			line = line[i+1:]
		}
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// runCapture executes git in dir and returns stdout and stderr separately.
func runCapture(parent context.Context, timeout time.Duration, dir string, args ...string) (string, string, error) {
	// An empty Dir does not mean "no repository" to exec: it means the
	// directory this process happens to be running in. Flockdeck is often started
	// from inside a checkout of something, so a caller that lost track of
	// which working tree it meant -- a review panel opened with no project
	// open, a pane whose directory never got set -- would have been answered
	// with a real status for an entirely unrelated repository, and shown it as
	// though it were the project's.
	if strings.TrimSpace(dir) == "" {
		return "", "", fmt.Errorf("git %s: no working tree was named", strings.Join(args, " "))
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Flockdeck on Windows is a GUI program with no console of its own, so
	// without this every one of these -- and the branch labels alone run one
	// per checkout every fifteen seconds -- would open a terminal window.
	sysproc.NoWindow(cmd)
	// Nothing here has a terminal to answer on, so a command that would ask
	// for a password has to fail instead of sitting until the timeout. Giving
	// up the optional index lock also keeps the status polling of several
	// panes from colliding with an agent's own commit.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", "", fmt.Errorf("git %s: gave up after %s", strings.Join(args, " "), timeout)
		}
		if parent.Err() != nil {
			return "", "", parent.Err()
		}
		// Some git subcommands explain themselves on stdout rather than
		// stderr -- "nothing to commit" is the one people hit -- so fall back
		// to it before showing a bare "exit status 1".
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", "", fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLines(msg, 4))
	}
	return out.String(), errb.String(), nil
}

// firstLines keeps an error message short enough to sit in a toast: git can
// answer with a dozen lines of advice, of which the first few carry the point.
func firstLines(msg string, n int) string {
	lines := strings.Split(msg, "\n")
	if len(lines) <= n {
		return msg
	}
	return strings.Join(lines[:n], "\n") + "\n…"
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
	known, err := isWorktree(repoDir, path)
	if err != nil {
		return err
	}
	if !known {
		return &gitError{path + " is not a worktree of this repository"}
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

// isWorktree reports whether path is one of the repository's registered
// worktrees.
func isWorktree(repoDir, path string) (bool, error) {
	wts, err := List(repoDir)
	if err != nil {
		return false, err
	}
	for _, wt := range wts {
		if samePath(wt.Path, path) {
			return true, nil
		}
	}
	return false, nil
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
