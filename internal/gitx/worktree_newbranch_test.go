package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorktreePathsStepAroundEachOther covers worktrees chosen together and
// created side by side, as a fan-out does. Chosen one at a time, each looked
// only at what was on disk: "agent/fix", stepping around a stray directory to
// the -2 name, and "agent/fix-2", whose own name that is, were given the same
// path, and one of the two tasks failed on the other's directory.
func TestWorktreePathsStepAroundEachOther(t *testing.T) {
	repo := newRepo(t)
	stray := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-agent-fix")
	if err := os.Mkdir(stray, 0o700); err != nil {
		t.Fatal(err)
	}

	paths := WorktreePaths(repo, []string{"agent/fix", "agent/fix-2"})
	if samePath(paths[0], paths[1]) {
		t.Fatalf("both worktrees were given %s", paths[0])
	}
	for _, p := range paths {
		if samePath(p, stray) {
			t.Errorf("a worktree was given %s, which is already there", p)
		}
	}
	if one := DefaultWorktreePath(repo, "agent/fix"); !samePath(one, paths[0]) {
		t.Errorf("DefaultWorktreePath = %s, want %s, the first of the set", one, paths[0])
	}
}

// TestAddNewBranchMakesTheBranchAndItsWorktree is the ordinary case.
func TestAddNewBranchMakesTheBranchAndItsWorktree(t *testing.T) {
	repo := newRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if err := AddNewBranch(repo, path, "agent/new"); err != nil {
		t.Fatalf("AddNewBranch: %v", err)
	}
	if got := CurrentBranch(path); got != "agent/new" {
		t.Errorf("the worktree is on %q, want agent/new", got)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Errorf("the worktree has no checkout: %v", err)
	}
}

// TestCommonDirIsSharedByAWorktreeAndItsRepository covers what tells a linked
// worktree to be the same repository as the checkout it was cut from, which
// Root, answering with the worktree itself, does not.
func TestCommonDirIsSharedByAWorktreeAndItsRepository(t *testing.T) {
	repo := newRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if err := AddNewBranch(repo, path, "agent/linked"); err != nil {
		t.Fatalf("AddNewBranch: %v", err)
	}
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	want, err := CommonDir(repo)
	if err != nil {
		t.Fatalf("CommonDir(repo): %v", err)
	}
	for _, dir := range []string{path, sub} {
		got, err := CommonDir(dir)
		if err != nil {
			t.Fatalf("CommonDir(%s): %v", dir, err)
		}
		if !samePath(got, want) {
			t.Errorf("CommonDir(%s) = %s, want %s, the repository's", dir, got, want)
		}
	}
	if other, err := CommonDir(newRepo(t)); err != nil || samePath(other, want) {
		t.Errorf("another repository's CommonDir = %s (%v), want one of its own", other, err)
	}
}
