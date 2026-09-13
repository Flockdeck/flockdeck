package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitRefusesAConflictWithNoMarkersToEdit covers a merge's conflicts
// that the marker check could not see. "add --all" is what tells git a
// conflict is settled, and only files with <<<<<<< or >>>>>>> in them were
// held back, so these were committed with whichever side happened to be in
// the working tree: a binary file, a file one side deleted, and markers made
// longer than seven by a conflict-marker-size attribute.
func TestCommitRefusesAConflictWithNoMarkersToEdit(t *testing.T) {
	merge := func(t *testing.T, base, ours, theirs func(repo string)) string {
		t.Helper()
		repo := newRepo(t)
		step := func(name string, change func(string)) {
			change(repo)
			gitRun(t, repo, "add", "-A")
			gitRun(t, repo, "commit", "-qm", name)
		}
		step("base", base)
		gitRun(t, repo, "checkout", "-q", "-b", "other")
		step("theirs", theirs)
		gitRun(t, repo, "checkout", "-q", "main")
		step("ours", ours)
		if _, _, err := runCapture(context.Background(), commandTimeout, repo, "merge", "other"); err == nil {
			t.Fatal("the merge should have stopped on a conflict")
		}
		return repo
	}
	put := func(name, body string) func(string) {
		return func(repo string) { write(t, repo, name, body) }
	}
	for _, tc := range []struct {
		name               string
		base, ours, theirs func(string)
		want               string
	}{
		{"binary", put("bin.dat", "\x00base"), put("bin.dat", "\x00ours"), put("bin.dat", "\x00theirs"), "bin.dat (binary)"},
		{"deleted on one side", put("gone.txt", "base\n"), put("gone.txt", "ours\n"),
			func(repo string) { os.Remove(filepath.Join(repo, "gone.txt")) }, "gone.txt (deleted on one side)"},
		{"longer markers", func(repo string) {
			write(t, repo, ".gitattributes", "*.txt conflict-marker-size=10\n")
			write(t, repo, "f.txt", "base\n")
		}, put("f.txt", "ours\n"), put("f.txt", "theirs\n"), "f.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := merge(t, tc.base, tc.ours, tc.theirs)
			head := gitRun(t, repo, "rev-parse", "HEAD")
			err := CommitAll(repo, "from the panel")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a refusal naming %s", err, tc.want)
			}
			if got := gitRun(t, repo, "rev-parse", "HEAD"); got != head {
				t.Fatal("the merge was committed with a side nobody chose")
			}
		})
	}
}
