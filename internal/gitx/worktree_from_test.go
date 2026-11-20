package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// AddNewBranchFrom starts the new branch at the base it is given, and takes a
// base that begins with a dash as a name and not as an option.
func TestAddNewBranchFromStartsAtTheBase(t *testing.T) {
	repo := newRepo(t)
	first := gitIn(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "two.txt"), []byte("2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "second")
	gitIn(t, repo, "tag", "first", first)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := AddNewBranchFrom(repo, wt, "feature/from-base", "first"); err != nil {
		t.Fatalf("AddNewBranchFrom: %v", err)
	}
	if got := gitIn(t, wt, "rev-parse", "HEAD"); got != first {
		t.Errorf("the worktree is at %s, want the base %s", got, first)
	}
	if got := gitIn(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature/from-base" {
		t.Errorf("the worktree is on %q", got)
	}

	// A branch that exists is never taken.
	if err := AddNewBranchFrom(repo, filepath.Join(t.TempDir(), "again"), "feature/from-base", "first"); err == nil {
		t.Error("an existing branch was taken for a new worktree")
	}
	// A base that is an option is a name git cannot find, not a flag it obeys.
	odd := filepath.Join(t.TempDir(), "odd")
	if err := AddNewBranchFrom(repo, odd, "feature/odd", "--force"); err == nil {
		t.Error("a base of --force was accepted")
	}
	if _, err := os.Stat(odd); err == nil {
		t.Error("a worktree was left behind for a base that does not exist")
	}
	if out := gitIn(t, repo, "branch", "--list", "feature/odd"); out != "" {
		t.Errorf("a branch was left behind: %q", out)
	}
	// A name git will not take is refused before anything is made.
	if err := AddNewBranchFrom(repo, filepath.Join(t.TempDir(), "bad"), "bad name", ""); err == nil {
		t.Error("a branch name with a space was accepted")
	}
}
