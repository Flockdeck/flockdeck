package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChangesReportsWhatMoved covers the review panel's file list.
func TestChangesReportsWhatMoved(t *testing.T) {
	repo := newRepo(t)

	// One modified tracked file, one new file, one deletion.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "doomed.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "doomed.txt")
	gitRun(t, repo, "commit", "-m", "add doomed")
	if err := os.Remove(filepath.Join(repo, "doomed.txt")); err != nil {
		t.Fatal(err)
	}

	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	byPath := map[string]FileChange{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	readme, ok := byPath["README.md"]
	if !ok {
		t.Fatalf("README.md not reported: %+v", files)
	}
	if readme.Label != "modified" {
		t.Errorf("README label = %q, want modified", readme.Label)
	}
	if readme.Added != 1 {
		t.Errorf("README added = %d, want 1 line added", readme.Added)
	}

	newFile, ok := byPath["new.txt"]
	if !ok {
		t.Fatal("new.txt not reported")
	}
	if !newFile.Untracked || newFile.Label != "new" {
		t.Errorf("new.txt = %+v, want an untracked 'new' entry", newFile)
	}
	if newFile.Added != 3 {
		t.Errorf("new.txt added = %d, want 3 (its line count)", newFile.Added)
	}

	if got := byPath["doomed.txt"].Label; got != "deleted" {
		t.Errorf("doomed.txt label = %q, want deleted", got)
	}
}

// TestDiffOfAGlobbyNameIsNotAPattern covers a file whose name contains the
// characters git reads as a pathspec pattern.
func TestDiffOfAGlobbyNameIsNotAPattern(t *testing.T) {
	repo := newRepo(t)

	// "a1.txt" is exactly what the pattern "a[1].txt" matches, so a bare
	// pathspec picks up the sibling and misses the file that was asked for.
	write(t, repo, "a1.txt", "one\n")
	write(t, repo, "a[1].txt", "bracket\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "two files")
	write(t, repo, "a1.txt", "one\nsibling edit\n")
	write(t, repo, "a[1].txt", "bracket\nthe edit under review\n")

	diff, err := Diff(repo, "a[1].txt")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "+the edit under review") {
		t.Errorf("diff missing the file's own change:\n%s", diff)
	}
	if strings.Contains(diff, "sibling edit") || strings.Contains(diff, "a/a1.txt") {
		t.Errorf("diff of a[1].txt also showed a1.txt:\n%s", diff)
	}

	// The same pathspec decides whether a file counts as untracked, which is
	// what makes it render as one addition rather than as a diff.
	write(t, repo, "b[2].txt", "brand new\n")
	diff, err = Diff(repo, "b[2].txt")
	if err != nil {
		t.Fatalf("diff of untracked: %v", err)
	}
	if !strings.Contains(diff, "+brand new") {
		t.Errorf("untracked bracketed file did not render as an addition:\n%s", diff)
	}
}

// write puts a file in the repository, failing the test if it cannot.
func write(t testing.TB, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUntrackedCountsSurviveBeingReadAtOnce checks that a tree full of new
// files still gets each count against the right name.
func TestUntrackedCountsSurviveBeingReadAtOnce(t *testing.T) {
	repo := newRepo(t)

	// More files than there are readers, with a different length each, so a
	// count landing on the wrong entry cannot go unnoticed.
	const n = 60
	for i := 1; i <= n; i++ {
		write(t, repo, fmt.Sprintf("new-%03d.txt", i), strings.Repeat("x\n", i))
	}

	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	byPath := map[string]FileChange{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("new-%03d.txt", i)
		f, ok := byPath[name]
		if !ok {
			t.Fatalf("%s not reported", name)
		}
		if f.Added != i {
			t.Errorf("%s added = %d, want %d", name, f.Added, i)
		}
	}
}

// TestUntrackedCountingStopsAtTheLimit checks that the panel is not made to
// wait on a checkout that has picked up thousands of new files.
func TestUntrackedCountingStopsAtTheLimit(t *testing.T) {
	repo := newRepo(t)
	for i := 1; i <= 5; i++ {
		write(t, repo, fmt.Sprintf("new-%03d.txt", i), strings.Repeat("x\n", i))
	}

	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	for i := range files {
		files[i].Added = 0
	}
	countUntracked(repo, files, 3)

	var counted int
	for _, f := range files {
		if f.Added > 0 {
			counted++
		}
	}
	if counted != 3 {
		t.Errorf("%d files counted, want the limit of 3: %+v", counted, files)
	}
	if len(files) != 5 {
		t.Errorf("%d files listed, want all 5: the limit is on the counting, not the listing", len(files))
	}
}

// TestLineCountsAreGivenUpOnAHugeDiff covers a working tree with more changed
// files in it than anyone is going to read a "+12" beside.
//
// git has to read and diff every one of them to produce those numbers -- 20s
// over a 10,000-file diff, against 139ms for the status call that lists the
// same files -- so past the limit the numbers are abandoned and the list is
// what the panel gets.
func TestLineCountsAreGivenUpOnAHugeDiff(t *testing.T) {
	repo := newRepo(t)
	for i := 0; i < 4; i++ {
		write(t, repo, fmt.Sprintf("f%d.txt", i), "one\n")
	}
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "four files")
	for i := 0; i < 4; i++ {
		write(t, repo, fmt.Sprintf("f%d.txt", i), "one\ntwo\n")
	}

	under, err := changes(repo, 10)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(under) != 4 {
		t.Fatalf("%d files, want 4: %+v", len(under), under)
	}
	for _, f := range under {
		if f.Added != 1 {
			t.Errorf("%s added = %d under the limit, want the count git gives", f.Path, f.Added)
		}
	}

	over, err := changes(repo, 2)
	if err != nil {
		t.Fatalf("changes over the limit: %v", err)
	}
	if len(over) != 4 {
		t.Errorf("%d files over the limit, want all 4 still listed", len(over))
	}
	for _, f := range over {
		if f.Added != 0 || f.Removed != 0 {
			t.Errorf("%s = +%d -%d over the limit, want the counting given up on",
				f.Path, f.Added, f.Removed)
		}
	}
}

// TestDiffCoversTrackedAndUntracked checks both paths the panel needs.
func TestDiffCoversTrackedAndUntracked(t *testing.T) {
	repo := newRepo(t)

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\nextra line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := Diff(repo, "README.md")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "+extra line") {
		t.Errorf("tracked diff missing the addition:\n%s", diff)
	}

	// An untracked file has no diff in git's eyes, so its contents are shown
	// as an addition instead of an empty panel.
	if err := os.WriteFile(filepath.Join(repo, "fresh.txt"), []byte("brand new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err = Diff(repo, "fresh.txt")
	if err != nil {
		t.Fatalf("diff untracked: %v", err)
	}
	if !strings.Contains(diff, "+brand new") {
		t.Errorf("untracked file not rendered as an addition:\n%s", diff)
	}
}

// TestDiffIgnoresTheUsersDiffConfig covers the settings that change what git
// prints, which the panel has no way to recognise once it arrives.
func TestDiffIgnoresTheUsersDiffConfig(t *testing.T) {
	repo := newRepo(t)

	// Every one of these is a setting somebody really keeps: colour through a
	// pager, an external diff tool, and prefixes other than "a/" and "b/".
	gitRun(t, repo, "config", "color.ui", "always")
	gitRun(t, repo, "config", "diff.mnemonicPrefix", "true")
	gitRun(t, repo, "config", "diff.external", "cmd-that-does-not-exist")

	write(t, repo, "README.md", "hello\na second line\n")

	diff, err := Diff(repo, "README.md")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if strings.ContainsRune(diff, 0x1b) {
		t.Errorf("diff carries ANSI escapes, which the panel renders as text:\n%q", diff)
	}
	if !strings.Contains(diff, "+a second line") {
		t.Errorf("added line is not marked as an addition:\n%s", diff)
	}
	if !strings.Contains(diff, "--- a/README.md") || !strings.Contains(diff, "+++ b/README.md") {
		t.Errorf("diff does not use the a/ and b/ prefixes:\n%s", diff)
	}
}

// TestUntrackedSymlinkIsShownAsTheLink covers a new symlink in a working tree.
//
// git records a symlink as the path it points at, so that is what a diff of
// one should show. Reading through it instead prints the contents of whatever
// is on the other end, which is not what was added and need not be inside the
// working tree at all -- node_modules/.bin and a checked-out framework are
// both full of links leading elsewhere.
func TestUntrackedSymlinkIsShownAsTheLink(t *testing.T) {
	repo := newRepo(t)
	private := t.TempDir()
	write(t, private, "private.txt", "TOP SECRET\n")
	elsewhere := filepath.Join(private, "private.txt")
	if err := os.Symlink(elsewhere, filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlinks cannot be created here: %v", err)
	}

	diff, err := Diff(repo, "link")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if strings.Contains(diff, "TOP SECRET") {
		t.Errorf("the diff followed the link and printed what it points at:\n%s", diff)
	}
	if !strings.Contains(diff, "private.txt") {
		t.Errorf("the diff does not show the link's target:\n%s", diff)
	}
}

// TestChangesReportsAwkwardNames covers the names git quotes and escapes: a
// path taken from its quoted form does not name a file that can be opened.
func TestChangesReportsAwkwardNames(t *testing.T) {
	repo := newRepo(t)

	// Windows refuses ">" in a name, so each one is only expected back if the
	// filesystem accepted it in the first place.
	var names []string
	for _, name := range []string{"a file with spaces.txt", "café.txt", "a -> b.txt"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("x\n"), 0o600); err == nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		t.Skip("this filesystem accepted none of the awkward names")
	}

	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.Path] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Errorf("%q not reported under its real name, got %+v", name, files)
		}
	}

	// The diff panel opens the file by the name the list gave it.
	diff, err := Diff(repo, "café.txt")
	if err != nil {
		t.Fatalf("diff of an escaped name: %v", err)
	}
	if !strings.Contains(diff, "+x") {
		t.Errorf("diff did not find the file:\n%s", diff)
	}
}

// TestDiffOfARenameShowsTheRename covers clicking a file that moved.
//
// The panel lists a rename under its new name, and a diff limited to that name
// alone gives git nothing to pair it against: it answers with the whole file
// as a fresh addition, burying whatever actually changed in it.
func TestDiffOfARenameShowsTheRename(t *testing.T) {
	repo := newRepo(t)
	body := strings.Repeat("a settled line\n", 200)
	write(t, repo, "old.txt", body)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "the file before it moved")

	gitRun(t, repo, "mv", "old.txt", "new.txt")
	write(t, repo, "new.txt", body+"the one line that changed\n")
	gitRun(t, repo, "add", "-A")

	diff, err := Diff(repo, "new.txt")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if strings.Contains(diff, "new file mode") {
		t.Errorf("the rename is shown as a brand new file:\n%.400s", diff)
	}
	if !strings.Contains(diff, "rename from old.txt") {
		t.Errorf("diff does not name what the file was called:\n%.400s", diff)
	}
	if !strings.Contains(diff, "+the one line that changed") {
		t.Errorf("diff does not show the change:\n%.400s", diff)
	}
	if added := strings.Count(diff, "\n+"); added > 5 {
		t.Errorf("%d added lines for a rename with one edit in it", added)
	}

	// A file that really is new still reads as one.
	write(t, repo, "fresh.txt", "brand new\n")
	gitRun(t, repo, "add", "fresh.txt")
	fresh, err := Diff(repo, "fresh.txt")
	if err != nil {
		t.Fatalf("diff of a new file: %v", err)
	}
	if !strings.Contains(fresh, "+brand new") {
		t.Errorf("a genuinely new file is no longer shown as added:\n%s", fresh)
	}
}

// TestChangesReportsRenameUnderItsNewName covers a staged rename, whose old
// name git sends as a separate record.
func TestChangesReportsRenameUnderItsNewName(t *testing.T) {
	repo := newRepo(t)

	gitRun(t, repo, "mv", "README.md", "READTHIS.md")

	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("a rename is one entry, got %+v", files)
	}
	if files[0].Path != "READTHIS.md" {
		t.Errorf("path = %q, want the new name", files[0].Path)
	}
	if files[0].Label != "renamed" {
		t.Errorf("label = %q, want renamed", files[0].Label)
	}

	// A rename that also edits the file must carry its line counts: numstat
	// keys those under the new name only when it is read NUL-delimited.
	// The file needs enough lines for git to still see a rename after the edit.
	body := strings.Repeat("a line\n", 20)
	if err := os.WriteFile(filepath.Join(repo, "long.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "long.txt")
	gitRun(t, repo, "commit", "-m", "add long")
	gitRun(t, repo, "mv", "long.txt", "longer.txt")
	if err := os.WriteFile(filepath.Join(repo, "longer.txt"), []byte(body+"one more\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "longer.txt")

	files, err = Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	var renamed *FileChange
	for i := range files {
		if files[i].Path == "longer.txt" {
			renamed = &files[i]
		}
	}
	if renamed == nil {
		t.Fatalf("longer.txt not reported: %+v", files)
	}
	if renamed.Added != 1 {
		t.Errorf("renamed file added = %d, want 1", renamed.Added)
	}
}

// TestChangesDuringAConflictedMerge covers the state an agent most often
// stops in and asks for help: the merge is half done and one file is in
// pieces. Only the code-to-word mapping was tested before, not what the panel
// is actually handed for it.
func TestChangesDuringAConflictedMerge(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "f.txt", "base\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "base")
	gitRun(t, repo, "checkout", "-b", "topic")
	write(t, repo, "f.txt", "from the agent\n")
	gitRun(t, repo, "commit", "-am", "topic")
	gitRun(t, repo, "checkout", "main")
	write(t, repo, "f.txt", "from main\n")
	gitRun(t, repo, "commit", "-am", "main")

	merge := exec.Command("git", "merge", "topic")
	merge.Dir = repo
	if out, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("expected the merge to conflict: %s", out)
	}

	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("%d files, want the one in conflict: %+v", len(files), files)
	}
	if files[0].Path != "f.txt" || files[0].Label != "conflict" {
		t.Errorf("entry = %+v, want f.txt labelled conflict", files[0])
	}

	// A merge does not detach HEAD the way a rebase does, so the branch is
	// still the branch.
	if st := StatusOf(repo); st.Branch != "main" || st.Detached || st.Dirty != 1 {
		t.Errorf("status = %+v, want main, attached, one dirty file", st)
	}

	// The diff of a conflicted file is the conflict, markers and all, which is
	// the thing worth reading at that moment.
	diff, err := Diff(repo, "f.txt")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	for _, want := range []string{"<<<<<<<", "from the agent", ">>>>>>>"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff does not show %q:\n%s", want, diff)
		}
	}
}

// TestChangesReportsADirtySubmodule covers a gitlink, which is neither a file
// with contents nor a directory to walk into: git reports one entry for the
// whole submodule and one line of diff naming the commit it moved to.
func TestChangesReportsADirtySubmodule(t *testing.T) {
	inner := newRepo(t)
	outer := newRepo(t)

	add := exec.Command("git", "-c", "protocol.file.allow=always",
		"submodule", "add", "--", filepath.ToSlash(inner), "mod")
	add.Dir = outer
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("submodules are not usable here: %v: %s", err, out)
	}
	gitRun(t, outer, "commit", "-m", "add the submodule")

	// Moving the submodule on by a commit is what makes the outer repository
	// dirty, without changing a single file the outer one tracks.
	mod := filepath.Join(outer, "mod")
	// The submodule is a clone of its own, and a clone takes none of the
	// configuration newRepo gave the repository it came from. A machine with
	// a global identity hides that; a CI runner has none and refuses to commit.
	gitRun(t, mod, "config", "user.email", "test@example.com")
	gitRun(t, mod, "config", "user.name", "Test")
	gitRun(t, mod, "config", "commit.gpgsign", "false")
	write(t, mod, "README.md", "hello\nand more\n")
	gitRun(t, mod, "commit", "-am", "work inside the submodule")

	files, err := Changes(outer)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("%d files, want the submodule alone: %+v", len(files), files)
	}
	if files[0].Path != "mod" || files[0].Label != "modified" {
		t.Errorf("entry = %+v, want mod reported as modified", files[0])
	}
	if files[0].Untracked {
		t.Error("a submodule that moved is not an untracked file")
	}
	if st := StatusOf(outer); st.Dirty != 1 || st.Untracked != 0 {
		t.Errorf("status = %+v, want one dirty entry and nothing untracked", st)
	}

	diff, err := Diff(outer, "mod")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "Subproject commit") {
		t.Errorf("submodule diff should name the commits it moved between:\n%s", diff)
	}
}

// TestStatusLabelNamesConflicts covers the words shown beside each file. The
// unmerged codes are the ones worth pinning: most of them look like an
// ordinary add or delete if the letters are read one at a time.
func TestStatusLabelNamesConflicts(t *testing.T) {
	cases := map[string]string{
		"??": "new",
		" M": "modified",
		"M ": "modified",
		"A ": "added",
		" D": "deleted",
		"R ": "renamed",
		"UU": "conflict",
		"AA": "conflict",
		"DD": "conflict",
		"AU": "conflict",
		"UD": "conflict",
		"DU": "conflict",
		"UA": "conflict",
	}
	for code, want := range cases {
		if got := statusLabel(code); got != want {
			t.Errorf("statusLabel(%q) = %q, want %q", code, got, want)
		}
	}
}

// TestDiffBeforeTheFirstCommit covers a project someone has just started:
// there is no HEAD to compare a staged file against, and git says so rather
// than treating it as empty.
func TestDiffBeforeTheFirstCommit(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init failed (%v): %s", err, out)
	}

	if err := os.WriteFile(filepath.Join(repo, "first.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "first.txt")

	diff, err := Diff(repo, "first.txt")
	if err != nil {
		t.Fatalf("diff before the first commit: %v", err)
	}
	if !strings.Contains(diff, "+hello") {
		t.Errorf("staged file shows no addition:\n%s", diff)
	}
}

// TestUntrackedTellsTrackedFilesApart covers the test that decides between a
// real diff and showing the whole file as an addition.
func TestUntrackedTellsTrackedFilesApart(t *testing.T) {
	repo := newRepo(t)

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("build/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "build"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "build", "out.txt"), []byte("made\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if untracked(repo, "README.md") {
		t.Error("a committed file is tracked")
	}
	if !untracked(repo, "new.txt") {
		t.Error("a new file is untracked")
	}
	// An ignored file has no diff either, so it is still shown whole.
	if !untracked(repo, "build/out.txt") {
		t.Error("an ignored file has nothing in the index to diff against")
	}
	// Outside a repository the answer is "no", so the caller reports git's
	// error rather than dumping the file as an addition.
	if untracked(t.TempDir(), "anything.txt") {
		t.Error("a directory that is not a repository should not claim a file is untracked")
	}
}

// TestDiffOfHugeUntrackedFileIsBounded covers a generated file dropped in the
// tree: only as much as the panel will show should be read, and the rest
// accounted for rather than silently dropped.
func TestDiffOfHugeUntrackedFileIsBounded(t *testing.T) {
	repo := newRepo(t)

	line := strings.Repeat("x", 99) + "\n"
	lines := (maxDiffBytes / len(line)) * 3
	if err := os.WriteFile(filepath.Join(repo, "generated.log"),
		[]byte(strings.Repeat(line, lines)), 0o600); err != nil {
		t.Fatal(err)
	}

	diff, err := Diff(repo, "generated.log")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff) > 2*maxDiffBytes {
		t.Errorf("diff is %d bytes, want it bounded near %d", len(diff), maxDiffBytes)
	}
	if !strings.Contains(diff, "truncated") {
		t.Error("a diff that stops early should say so")
	}
	if strings.HasSuffix(strings.TrimSuffix(diff, "\n"), "x") {
		t.Error("the diff should not end mid-line")
	}

	// The file list still counts every line, without holding the file.
	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	for _, f := range files {
		if f.Path == "generated.log" && f.Added != lines {
			t.Errorf("added = %d, want %d", f.Added, lines)
		}
	}
}

// TestDiffOfBinaryFileSaysSo covers dropping a screenshot into the tree: the
// panel renders whatever it is given as text, so the bytes must not be sent.
func TestDiffOfBinaryFileSaysSo(t *testing.T) {
	repo := newRepo(t)

	blob := []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a, 0x0a, 0x00, 0xff, 0xfe}
	if err := os.WriteFile(filepath.Join(repo, "shot.png"), blob, 0o600); err != nil {
		t.Fatal(err)
	}

	diff, err := Diff(repo, "shot.png")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "Binary file") {
		t.Errorf("diff = %q, want it to name the file as binary", diff)
	}
	if strings.ContainsRune(diff, 0) {
		t.Error("the raw bytes were sent to the interface")
	}

	// Nor is it counted as though it had lines.
	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	for _, f := range files {
		if f.Path == "shot.png" && f.Added != 0 {
			t.Errorf("binary file reported %d added lines", f.Added)
		}
	}
}

// TestDiffRefusesPathsOutsideTheTree covers a path arriving from the browser:
// it names a file in the working tree, or it is refused.
func TestDiffRefusesPathsOutsideTheTree(t *testing.T) {
	repo := newRepo(t)

	outside := filepath.Join(filepath.Dir(repo), "secret.txt")
	if err := os.WriteFile(outside, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	for _, p := range []string{
		"../secret.txt",
		"sub/../../secret.txt",
		outside,
		"/etc/passwd",
		"",
	} {
		if out, err := Diff(repo, p); err == nil {
			t.Errorf("Diff(%q) was allowed and returned %q", p, out)
		}
	}

	// A path that stays inside is still served, including through a "..".
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Diff(repo, "sub/../README.md"); err != nil {
		t.Errorf("a path that resolves inside the tree should be served: %v", err)
	}
}

// TestTruncateDiffCutsOnALineBoundary covers an enormous diff: a cut at an
// exact byte count leaves half a line, which the panel then colours as though
// it were whole, and can split a multi-byte character.
func TestTruncateDiffCutsOnALineBoundary(t *testing.T) {
	// Lines wide enough that the cap lands in the middle of one.
	line := "+" + strings.Repeat("é", 300) + "\n"
	whole := strings.Repeat(line, (maxDiffBytes/len(line))+10)

	got := truncateDiff(whole, len(whole))
	body, marker, found := strings.Cut(got, "… truncated")
	if !found {
		t.Fatal("a diff over the cap should say it was truncated")
	}
	if !strings.HasSuffix(body, "\n") {
		t.Error("the kept part should end at a line boundary")
	}
	if !strings.Contains(marker, "more bytes") {
		t.Errorf("marker = %q, want it to say how much was dropped", marker)
	}
	for _, l := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if l != strings.TrimSuffix(line, "\n") {
			t.Fatalf("a line was cut in half: %d bytes", len(l))
		}
	}

	// Anything within the cap is passed through untouched.
	small := "--- a\n+++ b\n+one\n"
	if truncateDiff(small, len(small)) != small {
		t.Error("a small diff should not be altered")
	}
}

// TestUntrackedDiffHasNoPhantomLastLine pins the shape of the synthetic diff
// shown for a new file: the trailing newline ends the last line, it does not
// begin an empty one.
func TestUntrackedDiffHasNoPhantomLastLine(t *testing.T) {
	repo := newRepo(t)

	if err := os.WriteFile(filepath.Join(repo, "fresh.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := Diff(repo, "fresh.txt")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	var added int
	for _, line := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		}
	}
	if added != 2 {
		t.Errorf("added lines = %d, want 2:\n%s", added, diff)
	}
	if !strings.Contains(diff, "@@ -0,0 +1,2 @@") {
		t.Errorf("expected a hunk header covering both lines:\n%s", diff)
	}

	// A file with no final newline is marked the way git marks it.
	if err := os.WriteFile(filepath.Join(repo, "bare.txt"), []byte("no newline"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err = Diff(repo, "bare.txt")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, `\ No newline at end of file`) {
		t.Errorf("missing the no-newline marker:\n%s", diff)
	}
}

// TestCommitAllStagesEverything covers the commit button.
func TestCommitAllStagesEverything(t *testing.T) {
	repo := newRepo(t)

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "added.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CommitAll(repo, ""); err == nil {
		t.Error("an empty commit message should be refused rather than opening an editor")
	}
	if err := CommitAll(repo, "agent work"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	files, _ := Changes(repo)
	if len(files) != 0 {
		t.Errorf("working tree should be clean after committing, got %+v", files)
	}
	out := gitRun(t, repo, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(out) != "agent work" {
		t.Errorf("commit subject = %q", strings.TrimSpace(out))
	}
}

// TestCommitAllOnCleanTreeExplainsItself covers the message the user sees when
// they press commit with nothing staged: git says "nothing to commit" on
// stdout, so an error built from stderr alone would read "exit status 1".
func TestCommitAllOnCleanTreeExplainsItself(t *testing.T) {
	repo := newRepo(t)

	err := CommitAll(repo, "nothing here")
	if err == nil {
		t.Fatal("committing a clean tree should fail")
	}
	if !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("error = %q, want it to mention nothing to commit", err)
	}
}

// TestPushSetsUpstreamOnFirstPush is the case that otherwise makes a new
// worktree's branch need a hand-typed command.
func TestPushSetsUpstreamOnFirstPush(t *testing.T) {
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}

	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", origin)

	if !HasRemote(repo) {
		t.Fatal("origin should be reported as a remote")
	}
	if got := StatusOf(repo).Upstream; got != "" {
		t.Fatalf("expected no upstream yet, got %q", got)
	}

	// git writes a push's summary to stderr, so a Push that returned stdout
	// alone left the interface with nothing to report but "done".
	out, err := Push(repo)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("push said %q, want git's own summary of the branch it pushed", out)
	}
	st := StatusOf(repo)
	if st.Upstream == "" {
		t.Error("the first push should set the upstream")
	}
	if st.Ahead != 0 {
		t.Errorf("ahead = %d after pushing, want 0", st.Ahead)
	}

	// A second push, now that an upstream exists, is a plain push.
	if err := os.WriteFile(filepath.Join(repo, "more.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CommitAll(repo, "more"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if StatusOf(repo).Ahead != 1 {
		t.Error("expected to be one commit ahead before pushing")
	}
	if _, err := Push(repo); err != nil {
		t.Fatalf("second push: %v", err)
	}
	if got := StatusOf(repo).Ahead; got != 0 {
		t.Errorf("ahead = %d after the second push, want 0", got)
	}
}

// TestUpstreamOfAgreesWithStatus checks the cheap upstream lookup Push relies
// on against the one that walks the working tree to find out.
func TestUpstreamOfAgreesWithStatus(t *testing.T) {
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}

	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", origin)
	if got := UpstreamOf(repo); got != "" {
		t.Errorf("upstream = %q before anything is pushed, want none", got)
	}

	gitRun(t, repo, "push", "--set-upstream", "origin", "main")
	want := StatusOf(repo).Upstream
	if want == "" {
		t.Fatal("status reports no upstream after one was set")
	}
	if got := UpstreamOf(repo); got != want {
		t.Errorf("upstream = %q, want %q as status reports it", got, want)
	}

	// A branch with no upstream of its own must not borrow the one beside it.
	gitRun(t, repo, "checkout", "-b", "solo")
	if got := UpstreamOf(repo); got != "" {
		t.Errorf("upstream = %q on a branch that tracks nothing", got)
	}
}

// TestRemoteCommandsGetTheLongerDeadline checks that push, pull and fetch are
// not held to the deadline meant for reading the local repository, and that
// reading the local repository is not held to theirs.
func TestRemoteCommandsGetTheLongerDeadline(t *testing.T) {
	repo := newRepo(t)
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}
	gitRun(t, repo, "remote", "add", "origin", origin)
	// With an upstream, so that pull gets as far as asking the remote.
	gitRun(t, repo, "push", "-q", "-u", "origin", "main")

	if networkTimeout <= commandTimeout {
		t.Errorf("network deadline %s is no longer than the local one %s",
			networkTimeout, commandTimeout)
	}

	// Shortening it to nothing is how the deadline a command actually ran
	// under can be read back out of the message it fails with.
	restore := networkTimeout
	t.Cleanup(func() { networkTimeout = restore })
	networkTimeout = time.Nanosecond

	for _, tc := range []struct {
		name string
		run  func() (string, error)
	}{
		{"fetch", func() (string, error) { return Fetch(repo) }},
		{"pull", func() (string, error) { return Pull(repo) }},
	} {
		_, err := tc.run()
		if err == nil {
			t.Errorf("%s: expected the shortened deadline to stop it", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "gave up after 1ns") {
			t.Errorf("%s did not run under the network deadline: %v", tc.name, err)
		}
	}

	// Nothing local borrows it, or every pane header would stop working.
	if got := StatusOf(repo).Branch; got != "main" {
		t.Errorf("branch = %q; a status call should be unaffected", got)
	}
}

// TestCommitGetsTheLongerDeadline is about the hooks a commit runs, which lint
// or test as often as not and were killed at the twenty seconds meant for
// reading the repository.
func TestCommitGetsTheLongerDeadline(t *testing.T) {
	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "added.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := networkTimeout
	t.Cleanup(func() { networkTimeout = restore })
	networkTimeout = time.Nanosecond

	err := CommitAll(repo, "slow hooks")
	if err == nil || !strings.Contains(err.Error(), "gave up after 1ns") {
		t.Errorf("commit did not run under the longer deadline: %v", err)
	}
}

// TestHasRemoteWithoutOrigin covers a repository that cannot be pushed.
func TestHasRemoteWithoutOrigin(t *testing.T) {
	repo := newRepo(t)
	if HasRemote(repo) {
		t.Error("a repository with no remote should not offer a push")
	}
	if _, err := Push(repo); err == nil {
		t.Error("pushing without a remote should fail")
	}
}

// TestRemotesMatchWholeNames covers a repository whose remote is not called
// "origin": a substring test both mismatched "my-origin" and hid a usable
// remote under another name.
func TestRemotesMatchWholeNames(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "my-origin", "https://example.invalid/x.git")

	if got := Remotes(repo); len(got) != 1 || got[0] != "my-origin" {
		t.Errorf("remotes = %v, want [my-origin]", got)
	}
	if !HasRemote(repo) {
		t.Error("a repository with one remote can be pushed, whatever it is called")
	}
	if got, err := pushRemote(repo, "main"); err != nil || got != "my-origin" {
		t.Errorf("pushRemote = %q, %v; want the sole remote", got, err)
	}

	// Once there is a choice and no origin, the user has to make it.
	gitRun(t, repo, "remote", "add", "other", "https://example.invalid/y.git")
	if _, err := pushRemote(repo, "main"); err == nil {
		t.Error("two remotes and no origin should not be guessed at")
	}

	gitRun(t, repo, "remote", "add", "origin", "https://example.invalid/z.git")
	if got, err := pushRemote(repo, "main"); err != nil || got != "origin" {
		t.Errorf("pushRemote = %q, %v; want origin once it exists", got, err)
	}
}

// gitRun runs a git command in dir and returns its output, failing the test on
// error.
func gitRun(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// TestTruncatedUntrackedFileCountsWhatWasNeverRead is about the note a large
// new file's diff ends on. The lines left over were counted in the part that
// was read, so a file with a quarter of a million lines still to go was said
// to have a few tens of thousands.
func TestTruncatedUntrackedFileCountsWhatWasNeverRead(t *testing.T) {
	repo := newRepo(t)
	const size = 800_000
	if err := os.WriteFile(filepath.Join(repo, "short-lines.txt"), []byte(strings.Repeat("x\n", size/2)), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := Diff(repo, "short-lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	var left int
	tail := diff[strings.LastIndex(strings.TrimSuffix(diff, "\n"), "\n")+1:]
	if _, err := fmt.Sscanf(tail, "… truncated, %d more bytes", &left); err != nil {
		t.Fatalf("diff ends %q, want what is left given in bytes", tail)
	}
	shown := strings.Count(diff, "\n+x")
	if want := size - 2*shown; left != want {
		t.Errorf("%d more bytes, want %d", left, want)
	}
}

// TestDiffOfANestedRepositorySaysWhatItIs covers a clone left inside the tree,
// which status lists as one directory. Its diff came back empty, and the
// panel explained an empty diff as a file that matches the last commit.
func TestDiffOfANestedRepositorySaysWhatItIs(t *testing.T) {
	repo := newRepo(t)
	nested := filepath.Join(repo, "vendor", "lib")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, nested, "init", "-q")
	if err := os.WriteFile(filepath.Join(nested, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := Changes(repo)
	if err != nil || len(files) != 1 || files[0].Path != "vendor/lib/" {
		t.Fatalf("changes = %+v, %v; want the nested repository as one entry", files, err)
	}
	diff, err := Diff(repo, files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "separate git repository") {
		t.Errorf("diff = %q, want it to say what the entry is", diff)
	}
}

// TestRemoteFailuresSayWhatToDoNext covers the three ways the panel's push and
// pull commonly fail. Each arrived as git's advice cut down to the lines that
// did not say what to do about it.
func TestRemoteFailuresSayWhatToDoNext(t *testing.T) {
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}
	mine := newRepo(t)
	gitRun(t, mine, "remote", "add", "origin", origin)

	if _, err := Pull(mine); err == nil || !strings.Contains(err.Error(), "push it first") {
		t.Errorf("pull with no upstream: %v", err)
	}
	gitRun(t, mine, "push", "-q", "-u", "origin", "main")

	// Someone else pushes, and this checkout commits without pulling.
	theirs := t.TempDir()
	cmd = exec.Command("git", "clone", "-q", origin, theirs)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	gitRun(t, theirs, "-c", "user.email=o@x", "-c", "user.name=o", "commit", "-q", "--allow-empty", "-m", "theirs")
	gitRun(t, theirs, "push", "-q")
	gitRun(t, mine, "commit", "-q", "--allow-empty", "-m", "mine")

	if _, err := Push(mine); err == nil || !strings.Contains(err.Error(), "Pull them in, then push again") {
		t.Errorf("rejected push: %v", err)
	}
	if _, err := Pull(mine); err == nil || !strings.Contains(err.Error(), "merge or rebase") {
		t.Errorf("pull of diverged branches: %v", err)
	}
}

// TestAddedThenDeletedIsNotAChange: a file staged as new and then deleted is
// in neither the last commit nor the working tree, and was listed as the
// deletion of content that had never been committed.
func TestAddedThenDeletedIsNotAChange(t *testing.T) {
	repo := newRepo(t)
	gone := filepath.Join(repo, "brief.txt")
	if err := os.WriteFile(gone, []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "brief.txt")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if files, err := Changes(repo); err != nil || len(files) != 0 {
		t.Errorf("changes = %+v, %v; want none", files, err)
	}
	if st := StatusOf(repo); st.HasChanges() {
		t.Errorf("status = %+v; want nothing to commit, as the file list says", st)
	}
}

// TestCommitRefusesAMergeWithMarkersInIt: "add --all" is what tells git a
// conflict is resolved, so the commit button committed a merge with the
// conflict markers still in the file.
func TestCommitRefusesAMergeWithMarkersInIt(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "checkout", "-q", "-b", "other")
	write(t, repo, "README.md", "theirs\n")
	gitRun(t, repo, "commit", "-qam", "theirs")
	gitRun(t, repo, "checkout", "-q", "main")
	write(t, repo, "README.md", "ours\n")
	gitRun(t, repo, "commit", "-qam", "ours")
	if _, _, err := runCapture(context.Background(), commandTimeout, repo, "merge", "other"); err == nil {
		t.Fatal("the merge should have stopped on a conflict")
	}
	head := gitRun(t, repo, "rev-parse", "HEAD")

	err := CommitAll(repo, "from the panel")
	if err == nil || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("err = %v, want the conflicted file named", err)
	}
	if got := gitRun(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatal("a commit was made with the markers in it")
	}

	// Sorted out by hand and not added: committing is how the panel adds it.
	write(t, repo, "README.md", "both\n")
	if err := CommitAll(repo, "merged"); err != nil {
		t.Fatalf("a resolved merge should commit: %v", err)
	}
}

// TestLineCountsAreAgainstTheLastCommit: the counts were the staged and the
// unstaged diff added up, which is not what the diff beside them shows or
// what a commit takes.
func TestLineCountsAreAgainstTheLastCommit(t *testing.T) {
	repo := newRepo(t)
	count := func() (int, int) {
		t.Helper()
		files, err := Changes(repo)
		if err != nil || len(files) != 1 {
			t.Fatalf("changes = %+v, %v; want README.md alone", files, err)
		}
		return files[0].Added, files[0].Removed
	}

	// Staged, then taken out again: the file is as the last commit has it.
	write(t, repo, "README.md", "hello\nstaged\n")
	gitRun(t, repo, "add", "README.md")
	write(t, repo, "README.md", "hello\n")
	if a, r := count(); a != 0 || r != 0 {
		t.Errorf("staged and taken out again = +%d -%d, want +0 -0", a, r)
	}

	// Staged, then the same line edited: one line added, not two.
	write(t, repo, "README.md", "hello\nedited\n")
	if a, r := count(); a != 1 || r != 0 {
		t.Errorf("staged and edited again = +%d -%d, want +1 -0", a, r)
	}

	// Before the first commit the two are still counted.
	fresh := t.TempDir()
	gitRun(t, fresh, "init", "-q")
	write(t, fresh, "a.txt", "one\n")
	gitRun(t, fresh, "add", "a.txt")
	write(t, fresh, "a.txt", "one\ntwo\n")
	files, err := Changes(fresh)
	if err != nil || len(files) != 1 || files[0].Added == 0 {
		t.Errorf("counts before the first commit = %+v, %v; want them counted", files, err)
	}
}

// TestDiffOfAFileBackAsItWasCommittedIsEmpty: with the working tree back to
// the last commit and the index not, the diff asked again for the index
// against the working tree, and showed an edit the commit would not contain.
func TestDiffOfAFileBackAsItWasCommittedIsEmpty(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "README.md", "hello\nstaged\n")
	gitRun(t, repo, "add", "README.md")
	write(t, repo, "README.md", "hello\n")
	diff, err := Diff(repo, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(diff) != "" {
		t.Errorf("diff = %q, want nothing: the file is as the last commit has it", diff)
	}
}

// TestAFirstPushToATakenNameDoesNotSendTheReaderInACircle: the push said
// "pull them in", and the pull -- with no upstream, since only a push that
// succeeds sets one -- said "push it first".
func TestAFirstPushToATakenNameDoesNotSendTheReaderInACircle(t *testing.T) {
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}
	theirs := newRepo(t)
	gitRun(t, theirs, "remote", "add", "origin", origin)
	gitRun(t, theirs, "push", "-q", "origin", "main")
	gitRun(t, theirs, "checkout", "-q", "-b", "feature")
	gitRun(t, theirs, "commit", "-q", "--allow-empty", "-m", "their feature")
	gitRun(t, theirs, "push", "-q", "origin", "feature")

	mine := newRepo(t)
	gitRun(t, mine, "remote", "add", "origin", origin)
	gitRun(t, mine, "checkout", "-q", "-b", "feature")
	gitRun(t, mine, "commit", "-q", "--allow-empty", "-m", "my feature")
	_, err := Push(mine)
	if err == nil || !strings.Contains(err.Error(), "already has a branch called feature") ||
		!strings.Contains(err.Error(), "git pull origin feature") {
		t.Errorf("first push to a taken name: %v", err)
	}
}

// TestPushBeforeTheFirstCommitSaysSo: git's answer was "src refspec main does
// not match any", which says nothing to someone who has not committed yet.
func TestPushBeforeTheFirstCommitSaysSo(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	origin := t.TempDir()
	gitRun(t, origin, "init", "-q", "--bare", "--initial-branch=main")
	fresh := t.TempDir()
	gitRun(t, fresh, "init", "-q", "--initial-branch=main")
	gitRun(t, fresh, "remote", "add", "origin", origin)
	_, err := Push(fresh)
	if err == nil || !strings.Contains(err.Error(), "commit something first") || strings.Contains(err.Error(), "refspec") {
		t.Errorf("push before the first commit: %v", err)
	}
}

// TestPushWithNoRemoteToChooseSaysHow: the panel can neither add a remote nor
// pick one, and "push manually" left the reader to work out how.
func TestPushWithNoRemoteToChooseSaysHow(t *testing.T) {
	repo := newRepo(t)
	if _, err := Push(repo); err == nil || !strings.Contains(err.Error(), "git remote add origin") {
		t.Errorf("push with no remote: %v", err)
	}
	gitRun(t, repo, "remote", "add", "fork", t.TempDir())
	gitRun(t, repo, "remote", "add", "upstream", t.TempDir())
	if _, err := Push(repo); err == nil || !strings.Contains(err.Error(), "git push -u fork main") {
		t.Errorf("push with two remotes and no origin: %v", err)
	}
}

// TestACopiedFileIsShownAsTheNewFileItIs: with copy detection turned on in
// the user's config, a copy was labelled "modified", counted 0/0 against its
// source, and its diff carried the source's own edits.
func TestACopiedFileIsShownAsTheNewFileItIs(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "config", "status.renames", "copies")
	gitRun(t, repo, "config", "diff.renames", "copies")
	write(t, repo, "README.md", "one\ntwo\nthree\nfour\n")
	gitRun(t, repo, "commit", "-qam", "longer")
	write(t, repo, "COPY.md", "one\ntwo\nthree\nfour\n")
	write(t, repo, "README.md", "one\ntwo\nthree\nfour\nfive\n")
	gitRun(t, repo, "add", "-A")

	files, err := Changes(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "COPY.md" && (f.Label != "added" || f.Added != 4) {
			t.Errorf("copy = %+v, want added with its 4 lines", f)
		}
	}
	diff, err := Diff(repo, "COPY.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "README.md") {
		t.Errorf("the copy's diff carried its source's edits:\n%s", diff)
	}
}

// TestCommitWaitsOutAnotherGitsLock: an agent's own git command holding the
// index made the panel's Commit fail at once, with the line saying what to do
// cut from the toast.
func TestCommitWaitsOutAnotherGitsLock(t *testing.T) {
	restore := lockWait
	t.Cleanup(func() { lockWait = restore })

	repo := newRepo(t)
	lock := filepath.Join(repo, ".git", "index.lock")

	// Let go of shortly: the commit goes through.
	write(t, repo, "a.txt", "a\n")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lockWait = 5 * time.Second
	go func() { time.Sleep(300 * time.Millisecond); os.Remove(lock) }()
	if err := CommitAll(repo, "after the other one"); err != nil {
		t.Fatalf("a lock let go of should be waited out: %v", err)
	}

	// Never let go of: say so, and name the file.
	write(t, repo, "b.txt", "b\n")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(lock) })
	lockWait = 200 * time.Millisecond
	err := CommitAll(repo, "still locked")
	if err == nil || !strings.Contains(err.Error(), "another git command") || !strings.Contains(err.Error(), "index.lock") {
		t.Errorf("err = %v, want it to say another git command holds the lock", err)
	}
}
