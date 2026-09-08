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

	if _, err := Push(repo); err != nil {
		t.Fatalf("first push: %v", err)
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
