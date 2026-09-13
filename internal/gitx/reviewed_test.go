package gitx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestAReviewedFileWrittenToAgainIsNotCommittedUnseen covers a file that was
// listed, then written to again before Commit was pressed. Only the names were
// compared, so the commit took content nobody had looked at. Its stamp from
// the listing now goes back with the commit, and one that no longer matches is
// refused -- here with the size kept the same, so it is the modification time
// that gives it away.
func TestAReviewedFileWrittenToAgainIsNotCommittedUnseen(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "README.md", "reviewed\n")
	listed := Reviewed{Files: []string{"README.md"}, Stamps: map[string]string{"README.md": Stamp(repo, "README.md")}}

	write(t, repo, "README.md", "reviewes\n")
	later := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(filepath.Join(repo, "README.md"), later, later); err != nil {
		t.Fatal(err)
	}
	err := CommitReviewed(repo, "reviewed", listed)
	if err == nil || !IsMoved(err) || !strings.Contains(err.Error(), "1 file changed since the list was read") {
		t.Fatalf("a listed file written to after the listing: err = %v, want a refusal saying one file changed", err)
	}
	if got := strings.TrimSpace(gitRun(t, repo, "log", "-1", "--pretty=%s")); got != "initial" {
		t.Fatalf("the refused commit was made: the last commit is %q", got)
	}

	// Listed again, it is what is committed.
	listed.Stamps["README.md"] = Stamp(repo, "README.md")
	if err := CommitReviewed(repo, "reviewed", listed); err != nil {
		t.Fatalf("committing the file as listed again: %v", err)
	}
}

// TestAReviewedCommitStagesWhatWasCheckedAndNoMore covers the moment between
// the check that the tree is as listed and the staging. That staging was "add
// --all", so a file written in that moment was committed unseen. What is
// staged now is exactly what was checked -- which has to get right the entries
// "add --all" handled by itself: a staged deletion, a rename whose new name
// was deleted, and a file staged as new and then deleted, which the list does
// not show and the commit must not add back. A name that reads as a pattern
// is staged as itself, and not as the file it would match.
func TestAReviewedCommitStagesWhatWasCheckedAndNoMore(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "old.txt", "old\n")
	write(t, repo, "gone.txt", "gone\n")
	write(t, repo, "report1.csv", "1\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "base")

	write(t, repo, "README.md", "reviewed\n")
	write(t, repo, "report[1].csv", "a second download\n")
	gitRun(t, repo, "rm", "-q", "gone.txt")
	gitRun(t, repo, "mv", "old.txt", "new.txt")
	if err := os.Remove(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "brief.txt", "staged, then deleted\n")
	gitRun(t, repo, "add", "brief.txt")
	if err := os.Remove(filepath.Join(repo, "brief.txt")); err != nil {
		t.Fatal(err)
	}

	files, err := Changes(repo)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, f := range files {
		listed = append(listed, f.Path)
	}
	afterCheck = func() {
		write(t, repo, "late.txt", "written between the check and the staging\n")
		write(t, repo, "report1.csv", "changed between the check and the staging\n")
	}
	t.Cleanup(func() { afterCheck = func() {} })

	if err := CommitReviewed(repo, "reviewed", Reviewed{Files: listed}); err != nil {
		t.Fatalf("committing the tree as listed: %v", err)
	}
	tree := strings.Fields(gitRun(t, repo, "ls-tree", "-r", "--name-only", "HEAD"))
	sort.Strings(tree)
	if got, want := strings.Join(tree, " "), "README.md report1.csv report[1].csv"; got != want {
		t.Errorf("the commit holds %s; want %s", got, want)
	}
	if got := gitRun(t, repo, "show", "HEAD:report1.csv"); got != "1\n" {
		t.Errorf("report1.csv was committed as %q, written after the check", got)
	}
}

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

// TestANameThatIsNotUTF8IsHeldToHowTheWindowSpellsIt covers a file whose name
// is not UTF-8, which Linux allows. The list reaches the window as JSON, which
// replaces each bad byte, so the name the window sent back never matched
// git's and every commit from the panel was refused as a tree that had moved.
func TestANameThatIsNotUTF8IsHeldToHowTheWindowSpellsIt(t *testing.T) {
	raw := "caf\xe9\xe9.txt"
	sent, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var spelled string
	if err := json.Unmarshal(sent, &spelled); err != nil {
		t.Fatal(err)
	}
	if want := "caf" + string(utf8.RuneError) + string(utf8.RuneError) + ".txt"; spelled != want || jsonName(raw) != want {
		t.Fatalf("JSON spells %q as %q and jsonName as %q; want both %q", raw, spelled, jsonName(raw), want)
	}

	recs := statusRecords("?? " + raw + "\x00 M plain.txt\x00")
	if moved := changedSince(t.TempDir(), recs, Reviewed{Files: []string{spelled, "plain.txt"}}); moved != 0 {
		t.Errorf("the tree as the window was sent it counts %d files as moved; want none", moved)
	}
}
