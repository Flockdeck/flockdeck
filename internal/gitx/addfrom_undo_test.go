package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAFailedAddFromLeavesNoBranchBehind covers a new worktree whose checkout
// failed: here on a folder already at its path, and as readily on a file name
// Windows will not have, a path too long for it, or the deadline. `git
// worktree add -b` makes the branch before the checkout and leaves it when the
// checkout fails, so the branch stayed, at the base first asked for. Trying
// again with another base then found it "existing", checked it out as it was,
// and the new worktree started from the wrong place without a word.
func TestAFailedAddFromLeavesNoBranchBehind(t *testing.T) {
	repo := newRepo(t)
	first := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	write(t, repo, "later.txt", "later\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "later")

	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, occupied, "somebody-elses.txt", "x\n")
	if err := AddFrom(repo, occupied, "feature", "HEAD"); err == nil {
		t.Fatal("a worktree was made over a folder that was already there")
	}
	if BranchExists(repo, "feature") {
		t.Error("the failed worktree left its new branch behind")
	}

	path := filepath.Join(t.TempDir(), "wt")
	if err := AddFrom(repo, path, "feature", first); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitRun(t, path, "rev-parse", "HEAD")); got != first {
		t.Errorf("the worktree asked to start at %s starts at %s", first[:7], got[:7])
	}
}

// TestABranchMadeByAnotherIsNotUndone covers the failure of `worktree add -b`
// on a branch that somebody else made between the existence check and the
// add. The call did not make that branch, so it must not be deleted as the
// leftover of its own attempt.
func TestABranchMadeByAnotherIsNotUndone(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "branch", "taken")
	tip := strings.TrimSpace(gitRun(t, repo, "rev-parse", "taken"))

	// Skip the existence check, as the losing side of the race does.
	path := filepath.Join(t.TempDir(), "wt")
	err := addWorktree(repo, path, "taken", true, "worktree", "add", "-b", "taken", "--no-track", "--", path)
	if err == nil {
		t.Fatal("git made a branch that was already there")
	}
	if !branchTakenFirst(err) {
		t.Fatalf("error not recognised as a branch that was taken first: %v", err)
	}
	if branchTakenFirst(&gitError{"fatal: '/x' already exists"}) {
		t.Error("a path that exists was taken for a branch that exists")
	}
	if got := strings.TrimSpace(gitRun(t, repo, "rev-parse", "taken")); got != tip {
		t.Errorf("branch moved: %s, want %s", got, tip)
	}
}
