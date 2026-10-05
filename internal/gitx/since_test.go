package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSinceListsTheBranchsCommittedFiles(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "add a")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nmore\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-q", "-am", "edit readme")
	// Work on main after the branch was cut is not the branch's.
	gitRun(t, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "later.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "later")
	gitRun(t, dir, "checkout", "-q", "feature")

	files, commits, err := Since(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if commits != 2 {
		t.Errorf("commits = %d, want 2", commits)
	}
	if len(files) != 2 || files[0].Path != "README.md" || files[0].Added != 1 || files[1].Path != "a.txt" || files[1].Added != 2 {
		t.Errorf("files = %+v", files)
	}
	if _, _, err := Since(dir, "no-such-branch"); err == nil {
		t.Error("an unknown base was accepted")
	}
}
