package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestARenameInTheWorkingTreeIsOneEntry covers a file moved and its new name
// added with intent to add (`git add -N`), as some agents do so a new file
// shows in their diffs. git then pairs the two in the working-tree column, " R",
// and sends the old name as a record of its own, just as it does for a staged
// rename. Only a staged one was expected to have it, so the old name was read
// as an entry in its own right: "a.txt" became a phantom file "txt" with a code
// of "a.", and a commit of the list tried to stage it and failed.
func TestARenameInTheWorkingTreeIsOneEntry(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "a.txt", strings.Repeat("a line of the file\n", 20))
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "base")
	if err := os.Rename(filepath.Join(repo, "a.txt"), filepath.Join(repo, "b.txt")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "-N", "b.txt")

	files, err := Changes(repo)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, f := range files {
		listed = append(listed, f.Path)
	}
	if len(files) != 1 || files[0].Path != "b.txt" {
		t.Fatalf("a rename in the working tree was listed as %q (%+v), want only b.txt", listed, files)
	}

	// Its diff is the rename, not the whole file as a fresh addition.
	diff, err := Diff(repo, "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if isWholeFileAddition(diff) || !strings.Contains(diff, "rename from a.txt") {
		t.Errorf("the diff of a rename in the working tree is not the rename:\n%s", diff)
	}

	if err := CommitReviewed(repo, "moved", Reviewed{Files: listed}); err != nil {
		t.Fatalf("committing the rename as listed: %v", err)
	}
	if out := strings.TrimSpace(gitRun(t, repo, "status", "--porcelain")); out != "" {
		t.Errorf("the rename was not all committed; left over:\n%s", out)
	}
	if out := strings.TrimSpace(gitRun(t, repo, "ls-tree", "--name-only", "HEAD")); out != "README.md\nb.txt" {
		t.Errorf("the commit holds %q, want README.md and b.txt", out)
	}
}
