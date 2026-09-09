package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo creates a repository with one commit and returns its path.
func newRepo(t testing.TB) string {
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

// TestStatusCountsAgreeWithTheFileList covers the number in a pane header
// against the panel below it: git collapses a new directory into a single
// entry unless it is asked for every file.
func TestStatusCountsAgreeWithTheFileList(t *testing.T) {
	repo := newRepo(t)

	if err := os.MkdirAll(filepath.Join(repo, "newdir", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		if err := os.WriteFile(filepath.Join(repo, "newdir", "sub", name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	st := StatusOf(repo)
	files, err := Changes(repo)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	var listed int
	for _, f := range files {
		if f.Untracked {
			listed++
		}
	}
	if st.Untracked != listed {
		t.Errorf("header says %d untracked, the panel lists %d", st.Untracked, listed)
	}
	if listed != 3 {
		t.Errorf("expected the three files in the new directory, got %d", listed)
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

// TestCurrentBranchOnUnbornAndDetachedHeads covers the two heads that are not
// an ordinary branch with commits on it.
func TestCurrentBranchOnUnbornAndDetachedHeads(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}

	// A repository nobody has committed to is still on a branch.
	empty := t.TempDir()
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = empty
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init failed (%v): %s", err, out)
	}
	if got := CurrentBranch(empty); got != "main" {
		t.Errorf("branch of an empty repository = %q, want main", got)
	}
	if got := DefaultBase(empty); got != "main" {
		t.Errorf("default base = %q, want main", got)
	}

	// A detached checkout has no branch at all.
	repo := newRepo(t)
	gitRun(t, repo, "checkout", "--detach")
	if got := CurrentBranch(repo); got != "" {
		t.Errorf("detached HEAD reported branch %q", got)
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

// TestRemoveWorktreeAlreadyDeleted covers pressing remove on an entry whose
// directory someone deleted in the file manager: git refuses to remove a path
// that is not there, but the record it left behind can still go.
func TestRemoveWorktreeAlreadyDeleted(t *testing.T) {
	repo := newRepo(t)
	wtPath := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-vanished")

	if err := AddFrom(repo, wtPath, "vanished", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}

	if err := Remove(repo, wtPath, false); err != nil {
		t.Fatalf("removing an already-deleted worktree: %v", err)
	}
	wts, _ := List(repo)
	if len(wts) != 1 {
		t.Errorf("expected the record to be gone, got %d worktrees", len(wts))
	}

	// An empty path would otherwise prune every record in the repository.
	if err := Remove(repo, "  ", false); err == nil {
		t.Error("remove with no path should be refused")
	}
}

// TestRemoveOfAnUnknownPathLeavesOtherRecordsAlone covers what removing by
// path can reach when the path is not a worktree at all.
//
// Removing a directory that is not there is answered with a prune, and a prune
// takes the record of every worktree whose directory is missing -- an external
// drive that is unplugged, a network share that is down. A path git has never
// heard of must not be able to set that off, and it never removed anything, so
// it should not report success either.
func TestRemoveOfAnUnknownPathLeavesOtherRecordsAlone(t *testing.T) {
	repo := newRepo(t)
	offline := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-offline")
	if err := AddFrom(repo, offline, "offline", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(offline) })
	// Stand in for a drive that is not mounted: the record is still good, the
	// directory is simply not reachable at the moment.
	if err := os.RemoveAll(offline); err != nil {
		t.Fatal(err)
	}

	stranger := filepath.Join(t.TempDir(), "never-a-worktree")
	if err := Remove(repo, stranger, false); err == nil {
		t.Error("removing a path that is not a worktree should be refused")
	}

	wts, err := List(repo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(wts) != 2 {
		t.Errorf("the unrelated worktree's record was pruned: %d records left, want 2", len(wts))
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

// TestDefaultWorktreePathIsUsable covers the path offered in the new-worktree
// dialog: it has to be a name the filesystem accepts and a directory that is
// not already there.
func TestDefaultWorktreePathIsUsable(t *testing.T) {
	repo := t.TempDir()
	project := filepath.Join(repo, "proj")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}

	if got := filepath.Base(DefaultWorktreePath(project, "feature/login")); got != "proj-feature-login" {
		t.Errorf("base = %q, want proj-feature-login", got)
	}
	// Windows will not accept these in a name, and git allows them in a ref.
	if got := filepath.Base(DefaultWorktreePath(project, `fix|bug<1>`)); strings.ContainsAny(got, `|<>`) {
		t.Errorf("base = %q, want the hostile characters replaced", got)
	}
	// Nor a name that ends in a dot or a space.
	for _, branch := range []string{"trailing.", "trailing ", "..", "///"} {
		got := filepath.Base(DefaultWorktreePath(project, branch))
		if strings.HasSuffix(got, ".") || strings.HasSuffix(got, " ") || got == "proj-" {
			t.Errorf("branch %q gave %q", branch, got)
		}
	}

	// A leftover directory is stepped around rather than suggested, because
	// git worktree add refuses a path that already exists.
	first := DefaultWorktreePath(project, "taken")
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	second := DefaultWorktreePath(project, "taken")
	if second == first {
		t.Errorf("suggested %q again although it already exists", second)
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
