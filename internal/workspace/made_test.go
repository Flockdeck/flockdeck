package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir and fails the test with its output, or skips when git is not usable.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	gitIn(t, repo, "config", "user.email", "t@e.com")
	gitIn(t, repo, "config", "user.name", "T")
	gitIn(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "init")
	return repo
}

func branchTip(t *testing.T, repo, branch string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var errStart = errors.New("the helper did not start")

// A branch the user already had, with no worktree, is checked out by the call. That
// makes a worktree and not a branch: taking it away again must leave the branch and
// every commit on it.
func TestACleanupAfterAFailedStartNeverDeletesABranchThatWasThereBefore(t *testing.T) {
	isolateConfig(t)
	repo := commitRepo(t)
	gitIn(t, repo, "branch", "users-work")
	gitIn(t, repo, "checkout", "-q", "users-work")
	if err := os.WriteFile(filepath.Join(repo, "mine.txt"), []byte("unmerged work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "unmerged commit")
	tip := branchTip(t, repo, "users-work")
	gitIn(t, repo, "checkout", "-q", "main")

	ws := newTestWorkspace(t, repo)
	made, err := ws.PrepareWorktreeNew(repo, "users-work")
	if err != nil {
		t.Fatal(err)
	}
	if !made.WorktreeCreated || made.BranchCreated {
		t.Fatalf("made = %+v: a branch that existed with no worktree makes a worktree and not a branch", made)
	}
	got := ws.DiscardMade(repo, made, errStart)
	if !errors.Is(got, errStart) || !strings.Contains(got.Error(), "was there before and was kept") {
		t.Errorf("DiscardMade said %v", got)
	}
	if _, err := os.Stat(made.Path); err == nil {
		t.Error("the worktree it made is still there")
	}
	if branchTip(t, repo, "users-work") != tip {
		t.Error("the user's own branch, or its unmerged commit, is gone")
	}
}

func TestABranchWithAWorktreeAlreadyIsUsedAndNothingIsTakenAway(t *testing.T) {
	isolateConfig(t)
	repo := commitRepo(t)
	ws := newTestWorkspace(t, repo)
	first, err := ws.PrepareWorktreeNew(repo, "feature-x")
	if err != nil || !first.WorktreeCreated || !first.BranchCreated {
		t.Fatalf("first: %+v, %v", first, err)
	}
	second, err := ws.PrepareWorktreeNew(repo, "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if second.WorktreeCreated || second.BranchCreated {
		t.Fatalf("second = %+v: it made nothing", second)
	}
	if got := ws.DiscardMade(repo, second, errStart); got != errStart {
		t.Errorf("DiscardMade of nothing said %v", got)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Error("the worktree that was already there was removed")
	}
}

func TestAFreshBranchAndWorktreeTheCallMadeAreBothRemoved(t *testing.T) {
	isolateConfig(t)
	repo := commitRepo(t)
	ws := newTestWorkspace(t, repo)
	made, err := ws.PrepareWorktreeNew(repo, "fresh-one")
	if err != nil || !made.WorktreeCreated || !made.BranchCreated || made.Start == "" {
		t.Fatalf("made = %+v, %v", made, err)
	}
	got := ws.DiscardMade(repo, made, errStart)
	if !strings.Contains(got.Error(), "were removed") {
		t.Errorf("DiscardMade said %v", got)
	}
	if _, err := os.Stat(made.Path); err == nil {
		t.Error("the worktree is still there")
	}
	if branchTip(t, repo, "fresh-one") != "" {
		t.Error("the branch is still there")
	}
}

func TestAWorktreeThatWasChangedAfterItWasMadeIsKept(t *testing.T) {
	isolateConfig(t)
	repo := commitRepo(t)
	ws := newTestWorkspace(t, repo)
	made, err := ws.PrepareWorktreeNew(repo, "dirty-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(made.Path, "new.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ws.DiscardMade(repo, made, errStart)
	if !strings.Contains(got.Error(), "was kept") {
		t.Errorf("DiscardMade said %v", got)
	}
	if _, err := os.Stat(filepath.Join(made.Path, "new.txt")); err != nil {
		t.Error("work done in the worktree was removed")
	}
	if branchTip(t, repo, "dirty-one") == "" {
		t.Error("the branch was deleted with the worktree kept")
	}
}

// A branch the call made but that has moved since is not deleted: its new commits are
// somebody's work.
func TestABranchThatMovedAfterItWasMadeIsKept(t *testing.T) {
	isolateConfig(t)
	repo := commitRepo(t)
	ws := newTestWorkspace(t, repo)
	made, err := ws.PrepareWorktreeNew(repo, "moved-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(made.Path, "c.txt"), []byte("c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, made.Path, "add", ".")
	gitIn(t, made.Path, "-c", "user.email=t@e.com", "-c", "user.name=T", "commit", "-m", "later")
	moved := branchTip(t, repo, "moved-one")
	got := ws.DiscardMade(repo, made, errStart)
	if !strings.Contains(got.Error(), "kept") {
		t.Errorf("DiscardMade said %v", got)
	}
	if branchTip(t, repo, "moved-one") != moved {
		t.Error("the commit made after the branch was is gone")
	}
	if _, err := os.Stat(made.Path); err != nil {
		t.Error("the worktree with the later commit was removed")
	}
}
