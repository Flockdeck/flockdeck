// Package gitx wraps the few git commands the worktree manager needs.
//
// Worktrees are what let several agents work in parallel without fighting over
// one checkout: each pane gets its own directory and branch.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// commandTimeout bounds every git invocation so a hung command cannot freeze
// the UI thread.
const commandTimeout = 20 * time.Second

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
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
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
			return "", fmt.Errorf("git %s: gave up after %s", strings.Join(args, " "), commandTimeout)
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
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLines(msg, 4))
	}
	return out.String(), nil
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
func CurrentBranch(dir string) string {
	out, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(out)
	if b == "HEAD" {
		return "" // detached
	}
	return b
}

// Dirty reports whether the worktree has uncommitted changes.
func Dirty(dir string) bool {
	out, err := run(dir, "status", "--porcelain")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
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

// Add creates a worktree at path. When branch is non-empty it is created as a
// new branch when it does not already exist, and checked out otherwise.
func Add(repoDir, path, branch string) error {
	args := []string{"worktree", "add"}
	if branch != "" {
		if branchExists(repoDir, branch) {
			args = append(args, path, branch)
		} else {
			args = append(args, "-b", branch, path)
		}
	} else {
		args = append(args, path)
	}
	_, err := run(repoDir, args...)
	return err
}

// BranchExists reports whether a local branch is already present.
func BranchExists(dir, branch string) bool { return branchExists(dir, branch) }

// branchExists reports whether a local branch is already present.
func branchExists(dir, branch string) bool {
	_, err := run(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// Remove deletes a worktree. force discards uncommitted changes in it.
func Remove(repoDir, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := run(repoDir, args...)
	return err
}

// DefaultWorktreePath suggests where a new worktree for a branch should live:
// a sibling of the repository, named after it, so checkouts stay grouped
// together without nesting inside the repository itself.
func DefaultWorktreePath(repoRoot, branch string) string {
	base := filepath.Base(repoRoot)
	safe := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(branch)
	return filepath.Join(filepath.Dir(repoRoot), base+"-"+safe)
}
