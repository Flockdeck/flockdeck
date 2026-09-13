package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestAddNewBranchLeavesNoBranchWhenTheCheckoutFails covers `git worktree add
// -b`, which makes the branch before the checkout and keeps it when the
// checkout then fails. A fan-out task that could not be prepared left a branch
// with no worktree and nothing on it.
func TestAddNewBranchLeavesNoBranchWhenTheCheckoutFails(t *testing.T) {
	repo := newRepo(t)
	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "keep.txt"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := AddNewBranch(repo, occupied, "agent/occupied"); err == nil {
		t.Fatal("a worktree was made over a directory that was already there")
	}
	if BranchExists(repo, "agent/occupied") {
		t.Error("the failed worktree left its new branch behind")
	}
	if _, err := os.Stat(filepath.Join(occupied, "keep.txt")); err != nil {
		t.Errorf("what was already in the directory is gone: %v", err)
	}
}

// TestAddNewBranchLeavesAnExistingBranchAlone covers a branch somebody made
// between a fan-out choosing the name and creating it. Refused, and then
// cleaned up as though it were a failed -b of the fan-out's own, it would be
// their branch deleted outright.
func TestAddNewBranchLeavesAnExistingBranchAlone(t *testing.T) {
	repo := newRepo(t)
	cmd := exec.Command("git", "branch", "agent/theirs")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git branch failed (%v): %s", err, out)
	}
	path := filepath.Join(t.TempDir(), "wt")

	if err := AddNewBranch(repo, path, "agent/theirs"); err == nil {
		t.Fatal("a branch that already existed was taken for a new worktree")
	}
	if !BranchExists(repo, "agent/theirs") {
		t.Error("the branch that was already there was deleted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a worktree was made at %s anyway: %v", path, err)
	}
}
