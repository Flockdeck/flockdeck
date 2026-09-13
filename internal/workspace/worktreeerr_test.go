package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// TestAWorktreeRefusalSaysWhatGitSaid checks that a checkout git refuses is
// not reported as no repository at all.
//
// "Not a git repository" is the answer only where there is none. A .git that
// git cannot use — here a file that is not a gitfile, as a worktree whose
// repository moved away leaves behind — is a repository the user can see is
// there, and being told otherwise sent them looking in the wrong place.
func TestAWorktreeRefusalSaysWhatGitSaid(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	// git must not wander above the test's own folder into whatever checkout
	// the temporary directory happens to sit in.
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	w := &Workspace{}

	plain := filepath.Join(base, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := w.worktreeFor(plain, "agent/x"); err == nil || !strings.Contains(err.Error(), "is not a git repository") {
		t.Errorf("a folder with no repository gave %v, want it called not a git repository", err)
	}

	broken := filepath.Join(base, "broken")
	if err := os.Mkdir(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("not a gitfile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := w.worktreeFor(broken, "agent/x")
	if err == nil {
		t.Fatal("a checkout git refuses gave no error")
	}
	if strings.Contains(err.Error(), "is not a git repository") {
		t.Errorf("a checkout git refuses was called no repository: %v", err)
	}
	if !strings.Contains(err.Error(), "gitfile") {
		t.Errorf("the refusal left out what git said: %v", err)
	}
}
