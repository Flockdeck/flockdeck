package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// A helper cut from a parent working in a subfolder starts in the same subfolder
// of its worktree, and at the worktree's root when the folder is not there.
func TestSameSubdirFollowsTheParentIntoTheWorktree(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run(repo, "init", "--initial-branch=main")
	run(repo, "config", "user.email", "test@example.com")
	run(repo, "config", "user.name", "Test")
	run(repo, "config", "commit.gpgsign", "false")
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "main.go"), []byte("package api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", ".")
	run(repo, "commit", "-m", "initial")
	wt := filepath.Join(t.TempDir(), "wt")
	run(repo, "worktree", "add", "-b", "helper", wt)

	if got, want := SameSubdir(sub, wt), filepath.Join(wt, "services", "api"); got != want {
		t.Errorf("from a subfolder: %s, want %s", got, want)
	}
	if got := SameSubdir(repo, wt); got != wt {
		t.Errorf("from the root: %s, want %s", got, wt)
	}
	// A folder that is new, and so not in the worktree, falls back to its root.
	fresh := filepath.Join(repo, "scratch")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := SameSubdir(fresh, wt); got != wt {
		t.Errorf("from a folder the worktree lacks: %s, want %s", got, wt)
	}
	if got := SameSubdir(t.TempDir(), wt); got != wt {
		t.Errorf("from outside a repository: %s, want %s", got, wt)
	}
}
