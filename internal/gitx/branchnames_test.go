package gitx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestABranchNamedLikeATagIsListedByItsName covers a branch sharing its name
// with a tag, as a release branch "v1.2" beside the tag "v1.2" does. git
// shortens a ref to the least that is unambiguous, which for that branch is
// "heads/v1.2", and that is what the worktree panel offered. Pressing it asked
// for a worktree on a branch called "heads/v1.2", which does not exist -- so a
// new branch of that name was made, and the real one was never checked out.
// The branch checked out was read the same way, so Push on it said it "has no
// commits" and sent nothing.
func TestABranchNamedLikeATagIsListedByItsName(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "tag", "v1.2")
	gitRun(t, repo, "branch", "v1.2")

	branches, err := Branches(repo)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range branches {
		if b.Name == "v1.2" {
			found = true
		}
		if !BranchExists(repo, b.Name) {
			t.Errorf("Branches listed %q, which is not the name of a branch", b.Name)
		}
	}
	if !found {
		t.Fatalf("the branch v1.2 was not listed by its name: %+v", branches)
	}

	path := t.TempDir() + string(os.PathSeparator) + "wt"
	if err := AddFrom(repo, path, "v1.2", ""); err != nil {
		t.Fatal(err)
	}
	if got := CurrentBranch(path); got != "v1.2" {
		t.Errorf("the worktree for the listed branch is on %q, want v1.2", got)
	}

	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}
	gitRun(t, repo, "remote", "add", "origin", origin)
	if _, err := Push(path); err != nil {
		t.Fatalf("pushing the branch v1.2: %v", err)
	}
	if out := gitRun(t, origin, "branch", "--list"); !strings.Contains(out, "v1.2") || strings.Contains(out, "heads/") {
		t.Errorf("the remote's branches after the push are %q, want v1.2", out)
	}
}
