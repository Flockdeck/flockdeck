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

// A base that starts with a dash is a name and not an option: "--output=<file>" would
// otherwise make git write its diff to a file of the caller's choosing.
func TestSinceTakesABaseThatStartsWithADashAsAName(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "add a")

	planted := filepath.Join(t.TempDir(), "planted.txt")
	if _, _, err := Since(dir, "--output="+planted); err == nil {
		t.Error("an option was taken for a base")
	}
	if _, err := os.Stat(planted + "...HEAD"); err == nil {
		t.Error("git wrote the file an option named")
	}
	if _, _, err := Since(dir, "--stat"); err == nil {
		t.Error("--stat was taken for a base")
	}

	// A real branch with such a name, made by writing the ref, is found by it.
	gitRun(t, dir, "update-ref", "refs/heads/-dash", "main")
	files, commits, err := Since(dir, "-dash")
	if err != nil {
		t.Fatalf("Since with a base named -dash: %v", err)
	}
	if commits != 1 || len(files) != 1 || files[0].Path != "a.txt" {
		t.Errorf("commits = %d, files = %+v", commits, files)
	}
}
