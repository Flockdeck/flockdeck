package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	if got, err := pushRemote(repo); err != nil || got != "my-origin" {
		t.Errorf("pushRemote = %q, %v; want the sole remote", got, err)
	}

	// Once there is a choice and no origin, the user has to make it.
	gitRun(t, repo, "remote", "add", "other", "https://example.invalid/y.git")
	if _, err := pushRemote(repo); err == nil {
		t.Error("two remotes and no origin should not be guessed at")
	}

	gitRun(t, repo, "remote", "add", "origin", "https://example.invalid/z.git")
	if got, err := pushRemote(repo); err != nil || got != "origin" {
		t.Errorf("pushRemote = %q, %v; want origin once it exists", got, err)
	}
}

// gitRun runs a git command in dir and returns its output, failing the test on
// error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
