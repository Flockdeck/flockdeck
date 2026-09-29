package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAWorktreeWhoseFolderIsGoneIsNotReused covers a helper spawned with
// --worktree on a branch whose worktree was deleted by hand. git still has a
// record of it, and the record was reused like a checkout that was there: the
// path handed back named no folder, and the helper was started in it. It is
// refused now, saying how to clear the record.
func TestAWorktreeWhoseFolderIsGoneIsNotReused(t *testing.T) {
	isolateConfig(t)
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "t@e.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}

	ws := newTestWorkspace(t, repo)
	branch := BranchNameFor("add a health endpoint")
	path, err := ws.PrepareWorktree(repo, branch)
	if err != nil {
		t.Fatalf("prepare worktree: %v", err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	again, err := ws.PrepareWorktree(repo, branch)
	if err == nil {
		if _, serr := os.Stat(again); serr != nil {
			t.Fatalf("a worktree whose folder is gone was handed out again: %q (%v)", again, serr)
		}
	} else if !strings.Contains(err.Error(), "Prune") {
		t.Errorf("the refusal does not say how to clear the record: %v", err)
	}
}
