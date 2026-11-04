package gitx

import "testing"

// The lookup of whether a file is untracked keeps its failure, so Diff can
// report it and not show an empty diff for a new file.
func TestIsUntrackedKeepsTheFailureOfTheLookup(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	write(t, repo, "new.txt", "x\n")
	if yes, err := isUntracked(repo, "new.txt"); err != nil || !yes {
		t.Errorf("isUntracked(new file) = %v, %v", yes, err)
	}
	if yes, err := isUntracked(repo, "README.md"); err != nil || yes {
		t.Errorf("isUntracked(tracked file) = %v, %v", yes, err)
	}
	if _, err := isUntracked(t.TempDir(), "new.txt"); err == nil {
		t.Error("a failed lookup was reported as an answer")
	}
}
