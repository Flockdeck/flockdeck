package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newRepo creates a repository with one commit and returns its path.
func newRepo(t *testing.T) string {
	t.Helper()
	if !Available() {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()

	steps := [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	return dir
}

// TestStatusOfReportsWorkingTreeState covers the summary shown in pane headers
// and the worktree panel.
func TestStatusOfReportsWorkingTreeState(t *testing.T) {
	repo := newRepo(t)

	st := StatusOf(repo)
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if st.Head == "" {
		t.Error("expected a short commit id")
	}
	if st.HasChanges() {
		t.Errorf("a fresh repository should be clean, got dirty=%d untracked=%d", st.Dirty, st.Untracked)
	}

	// An untracked file and a modified tracked file are counted separately.
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st = StatusOf(repo)
	if st.Dirty != 1 {
		t.Errorf("dirty = %d, want 1 modified tracked file", st.Dirty)
	}
	if st.Untracked != 1 {
		t.Errorf("untracked = %d, want 1", st.Untracked)
	}
	if !st.HasChanges() {
		t.Error("HasChanges should be true")
	}
}

// TestStatusOfEmptyRepository covers a repository someone has just created:
// git reports the head as "(initial)", which is not a commit id.
func TestStatusOfEmptyRepository(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init failed (%v): %s", err, out)
	}

	st := StatusOf(dir)
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if !st.Unborn {
		t.Error("a repository with no commits should be reported as unborn")
	}
	if st.Head != "" {
		t.Errorf("head = %q, want empty rather than a truncated \"(initial)\"", st.Head)
	}
}

// TestWorktreeLifecycle covers creating, listing and removing worktrees, which
// is how agents are given separate checkouts to work in.
func TestWorktreeLifecycle(t *testing.T) {
	repo := newRepo(t)

	wtPath := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-feature")
	t.Cleanup(func() { os.RemoveAll(wtPath) })

	if err := AddFrom(repo, wtPath, "feature-x", ""); err != nil {
		t.Fatalf("add worktree: %v", err)
	}

	wts, err := ListDetailed(repo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(wts))
	}
	if !wts[0].Main {
		t.Error("the first worktree should be flagged as the main one")
	}

	var feature *Worktree
	for i := range wts {
		if wts[i].Branch == "feature-x" {
			feature = &wts[i]
		}
	}
	if feature == nil {
		t.Fatalf("new worktree not listed: %+v", wts)
	}
	if feature.Main {
		t.Error("a linked worktree must not be flagged as main")
	}
	if feature.Label() != "feature-x" {
		t.Errorf("label = %q, want feature-x", feature.Label())
	}
	if feature.Status.Branch != "feature-x" {
		t.Errorf("status branch = %q, want feature-x", feature.Status.Branch)
	}

	// The branch list should know the worktree has it checked out, so the UI
	// does not offer to create a second worktree for the same branch.
	branches, err := Branches(repo)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	var seen bool
	for _, b := range branches {
		if b.Name == "feature-x" {
			seen = true
			if b.CheckedIn == "" {
				t.Error("feature-x should report the worktree holding it")
			}
		}
	}
	if !seen {
		t.Errorf("feature-x missing from %+v", branches)
	}

	// Removing a clean worktree needs no force.
	if err := Remove(repo, wtPath, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	wts, _ = ListDetailed(repo)
	if len(wts) != 1 {
		t.Errorf("expected the worktree to be gone, got %d", len(wts))
	}
}

// TestPathsAgreeAcrossCalls covers a Windows trap: git prints paths with
// forward slashes, so a root or worktreepath taken verbatim never compares
// equal to a path the rest of the program built with filepath.
func TestPathsAgreeAcrossCalls(t *testing.T) {
	repo := newRepo(t)
	wtPath := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-paths")
	t.Cleanup(func() { os.RemoveAll(wtPath) })

	if err := AddFrom(repo, wtPath, "paths", ""); err != nil {
		t.Fatalf("add: %v", err)
	}

	root, err := Root(repo)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	if root != filepath.Clean(root) {
		t.Errorf("root = %q, want it in the platform's own form", root)
	}

	wts, err := List(repo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if wts[0].Path != root {
		t.Errorf("main worktree path %q does not match root %q", wts[0].Path, root)
	}

	branches, err := Branches(repo)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	for _, b := range branches {
		if b.Name != "paths" {
			continue
		}
		if b.CheckedIn != filepath.Clean(wtPath) {
			t.Errorf("checked-in path = %q, want %q", b.CheckedIn, filepath.Clean(wtPath))
		}
	}
}

// TestRemoveDirtyWorktreeNeedsForce pins the behaviour the UI warns about
// before discarding someone's work.
func TestRemoveDirtyWorktreeNeedsForce(t *testing.T) {
	repo := newRepo(t)
	wtPath := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-dirty")
	t.Cleanup(func() { os.RemoveAll(wtPath) })

	if err := AddFrom(repo, wtPath, "dirty-branch", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "README.md"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Remove(repo, wtPath, false); err == nil {
		t.Error("removing a worktree with uncommitted changes should fail without force")
	}
	if err := Remove(repo, wtPath, true); err != nil {
		t.Errorf("force remove should succeed: %v", err)
	}
}

// TestAddFromExistingBranchChecksItOut covers the "branch without a worktree"
// shortcut.
func TestAddFromExistingBranchChecksItOut(t *testing.T) {
	repo := newRepo(t)

	cmd := exec.Command("git", "branch", "existing")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create branch: %v: %s", err, out)
	}

	wtPath := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-existing")
	t.Cleanup(func() { os.RemoveAll(wtPath) })

	if err := AddFrom(repo, wtPath, "existing", ""); err != nil {
		t.Fatalf("add from existing branch: %v", err)
	}
	if got := StatusOf(wtPath).Branch; got != "existing" {
		t.Errorf("worktree is on %q, want existing", got)
	}
}

// TestPruneRemovesStaleRecords covers the maintenance button.
func TestPruneRemovesStaleRecords(t *testing.T) {
	repo := newRepo(t)
	wtPath := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-gone")

	if err := AddFrom(repo, wtPath, "gone", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Delete the directory behind git's back, as happens in practice.
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}
	if err := Prune(repo); err != nil {
		t.Fatalf("prune: %v", err)
	}
	wts, _ := List(repo)
	if len(wts) != 1 {
		t.Errorf("expected the stale record to be pruned, got %d worktrees", len(wts))
	}
}
