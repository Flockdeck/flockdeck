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
	// It is not a base, though: "main" names no commit yet, and a worktree
	// asked to start from it fails, where one given no base starts empty.
	if got := DefaultBase(empty); got != "" {
		t.Errorf("default base = %q, want none", got)
	}

	// A detached checkout has no branch at all.
	repo := newRepo(t)
	gitRun(t, repo, "checkout", "--detach")
	if got := CurrentBranch(repo); got != "" {
		t.Errorf("detached HEAD reported branch %q", got)
	}
}

// TestStatusDuringARebaseKeepsTheBranchName covers an agent that stopped on a
// conflict: git detaches HEAD to replay commits, so without help the pane
// header and the review panel both call the checkout "detached".
func TestStatusDuringARebaseKeepsTheBranchName(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "f.txt", "base\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "base")

	gitRun(t, repo, "checkout", "-b", "topic")
	write(t, repo, "f.txt", "topic\n")
	gitRun(t, repo, "commit", "-am", "topic")
	gitRun(t, repo, "checkout", "main")
	write(t, repo, "f.txt", "main\n")
	gitRun(t, repo, "commit", "-am", "main")
	gitRun(t, repo, "checkout", "topic")

	// The rebase is meant to stop here: both sides changed the same line.
	cmd := exec.Command("git", "rebase", "main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected the rebase to stop on a conflict: %s", out)
	}
	t.Cleanup(func() {
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = repo
		_ = abort.Run()
	})

	st := StatusOf(repo)
	if !st.Detached {
		t.Error("a rebase in progress should still report a detached HEAD")
	}
	if st.Branch != "topic" {
		t.Errorf("branch = %q, want topic -- the branch being rebased", st.Branch)
	}

	// A new worktree started while the rebase is stopped must not be based on
	// the commit the replay is sitting on, which is nowhere once it finishes.
	if got := DefaultBase(repo); got != "topic" {
		t.Errorf("default base = %q mid-rebase, want topic", got)
	}

	// A plain detached checkout has no branch to name, and must not borrow one.
	gitRun(t, repo, "rebase", "--abort")
	gitRun(t, repo, "checkout", "--detach", "main")
	if st := StatusOf(repo); st.Branch != "" {
		t.Errorf("branch = %q on a plain detached HEAD, want empty", st.Branch)
	}
	if got := DefaultBase(repo); got != "HEAD" {
		t.Errorf("default base = %q when detached and not rebasing, want HEAD", got)
	}
}

// A rebase started from a detached HEAD has no branch to go back to, and git
// writes the words "detached HEAD" where the branch's ref would be. They were
// read as a branch called "detached HEAD": in the pane header, and as the base
// the worktree panel offered, which git refuses as no reference at all.
func TestADetachedRebaseIsNoBranch(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "f.txt", "base\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "base")
	gitRun(t, repo, "checkout", "-b", "topic")
	write(t, repo, "f.txt", "topic\n")
	gitRun(t, repo, "commit", "-am", "topic")
	gitRun(t, repo, "checkout", "main")
	write(t, repo, "f.txt", "main\n")
	gitRun(t, repo, "commit", "-am", "main")
	gitRun(t, repo, "checkout", "--detach", "topic")

	cmd := exec.Command("git", "rebase", "main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected the rebase to stop on a conflict: %s", out)
	}
	t.Cleanup(func() {
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = repo
		_ = abort.Run()
	})

	// Still a rebase, though there is no branch to name as the one it is on.
	if st := StatusOf(repo); !st.Detached || st.Branch != "" || st.Operation != "rebasing" {
		t.Errorf("status = %+v; want detached and rebasing, with no branch", st)
	}
	if got := DefaultBase(repo); got == "detached HEAD" {
		t.Errorf("default base = %q, which is no reference", got)
	}
}

// TestNoDirectoryIsNotThisProcessesDirectory covers a caller that has lost
// track of which working tree it meant.
//
// exec reads an empty Dir as "wherever this process is", and Flockdeck is normally
// started from inside a checkout of something, so the panels would have been
// answered with a real branch and a real file list belonging to a repository
// nobody asked about.
func TestNoDirectoryIsNotThisProcessesDirectory(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	// The test binary runs inside this project, which is itself a repository,
	// so an unguarded call here would succeed and answer about it.
	if !IsRepo(".") {
		t.Skip("the tests are not running inside a repository")
	}

	if _, err := Root(""); err == nil {
		t.Error("resolving a root with no directory should fail")
	}
	if IsRepo("") {
		t.Error("nowhere is not a repository")
	}
	if st := StatusOf(""); st.Branch != "" || st.Head != "" {
		t.Errorf("status of nowhere = %+v, want nothing", st)
	}
	if _, err := Changes(""); err == nil {
		t.Error("listing changes with no directory should fail")
	}
	if _, err := Diff("", "README.md"); err == nil {
		t.Error("diffing with no directory should fail")
	}
	if _, err := List(""); err == nil {
		t.Error("listing worktrees with no directory should fail")
	}
	if b := CurrentBranch(""); b != "" {
		t.Errorf("current branch of nowhere = %q", b)
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

// TestWorktreeArgumentsAreNotReadAsOptions covers the path and the starting
// point, both of which arrive from the window and neither of which git tells
// apart from one of its own flags.
func TestWorktreeArgumentsAreNotReadAsOptions(t *testing.T) {
	repo := newRepo(t)

	// "--force" as a starting point is the dangerous shape: read as an option
	// it is not refused, it is obeyed, and the worktree starts from HEAD with
	// a forced checkout instead of from wherever was asked for.
	err := AddFrom(repo, filepath.Join(t.TempDir(), "wt"), "from-a-flag", "--force")
	if err == nil {
		t.Error("a starting point named like an option should be refused, not obeyed")
	}
	if BranchExists(repo, "from-a-flag") {
		t.Error("the branch was created from a starting point git never resolved")
	}

	// A path beginning with a dash is a path, awkward as it is.
	if err := AddFrom(repo, "-dashed", "dashed", ""); err != nil {
		t.Fatalf("a worktree at a dash-leading path: %v", err)
	}
	wts, err := List(repo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, wt := range wts {
		if filepath.Base(wt.Path) == "-dashed" {
			found = true
		}
	}
	if !found {
		t.Errorf("the worktree is not in the list: %+v", wts)
	}
	if err := Remove(repo, filepath.Join(repo, "-dashed"), true); err != nil {
		t.Errorf("removing it again: %v", err)
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
	// git lists paths with their links resolved and Root keeps the caller's
	// spelling, so on macOS, where the temporary directory is reached through
	// /var, the two are the same directory spelled two ways.
	if !samePath(wts[0].Path, root) {
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

// TestDefaultWorktreePathAvoidsRecordsAsWellAsDirectories covers the state a
// checkout deleted in a file manager leaves behind: git still has a record of
// a worktree at a path where there is now nothing to see.
//
// The suggested path looked free, and `git worktree add` then refused it as "a
// missing but already registered worktree" -- an error about a path the person
// never chose and cannot see.
func TestDefaultWorktreePathAvoidsRecordsAsWellAsDirectories(t *testing.T) {
	repo := newRepo(t)
	first := DefaultWorktreePath(repo, "shared")
	if err := AddFrom(repo, first, "shared", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Deleted by hand, as happens; the record stays behind.
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(first) })

	next := DefaultWorktreePath(repo, "shared")
	if next == first {
		t.Fatalf("suggested %q again, which git still has a record of", next)
	}
	t.Cleanup(func() { _ = os.RemoveAll(next) })
	if err := AddFrom(repo, next, "second", ""); err != nil {
		t.Errorf("a worktree at the suggested path: %v", err)
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
	pruned, err := Prune(repo)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("prune reported %d records removed, want 1", pruned)
	}
	wts, _ := List(repo)
	if len(wts) != 1 {
		t.Errorf("expected the stale record to be pruned, got %d worktrees", len(wts))
	}

	// Pressing it again has nothing to do, and the count is how the panel
	// knows to say so rather than claiming it cleaned something up.
	if pruned, err := Prune(repo); err != nil || pruned != 0 {
		t.Errorf("second prune = %d, %v; want nothing removed", pruned, err)
	}
}

// TestDeletedWorktreeIsReportedAsPrunable: a worktree whose directory was
// deleted by hand came back with an empty status, which reads as clean.
func TestDeletedWorktreeIsReportedAsPrunable(t *testing.T) {
	repo := newRepo(t)
	wt := filepath.Join(t.TempDir(), "gone")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "gone", wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	wts, err := ListDetailed(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range wts {
		if gone := samePath(w.Path, wt); w.Prunable != gone {
			t.Errorf("%s: prunable = %v, want %v", w.Path, w.Prunable, gone)
		}
	}
}

// TestRemoveOfALockedWorktreeSaysHowToUnlockIt: git told the panel to run
// "remove -f -f", which no button does.
func TestRemoveOfALockedWorktreeSaysHowToUnlockIt(t *testing.T) {
	repo := newRepo(t)
	wt := filepath.Join(t.TempDir(), "kept")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "kept", wt)
	gitRun(t, repo, "worktree", "lock", wt)
	for _, force := range []bool{false, true} {
		err := Remove(repo, wt, force)
		if err == nil || !strings.Contains(err.Error(), "git worktree unlock") || strings.Contains(err.Error(), "-f -f") {
			t.Errorf("force=%v: err = %v, want it to say how to unlock", force, err)
		}
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("a locked worktree should be left where it is: %v", err)
	}
}

// TestDefaultBaseOfAFreshRepositoryIsEmpty: the panel pre-fills the base with
// this, and "main" before anything is committed to it names no commit, so
// every new worktree in a fresh repository failed.
func TestDefaultBaseOfAFreshRepositoryIsEmpty(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "--initial-branch=main")
	base := DefaultBase(repo)
	if base != "" {
		t.Errorf("default base = %q before the first commit, want none", base)
	}
	err := AddFrom(repo, filepath.Join(t.TempDir(), "wt"), "topic", base)
	if err != nil && strings.Contains(err.Error(), "invalid reference") {
		t.Errorf("a worktree from the offered base failed: %v", err)
	}
	if got := DefaultBase(newRepo(t)); got != "main" {
		t.Errorf("default base = %q in a repository with a commit, want its branch", got)
	}
}

// TestAGoneUpstreamIsNoUpstream: a branch whose upstream was deleted on the
// remote and pruned here was reported as tracking it, level, 0 and 0 -- over
// a commit that had never been pushed.
func TestAGoneUpstreamIsNoUpstream(t *testing.T) {
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", origin)
	gitRun(t, repo, "checkout", "-q", "-b", "feature")
	gitRun(t, repo, "push", "-q", "-u", "origin", "feature")
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "not pushed")
	gitRun(t, repo, "push", "-q", "origin", "--delete", "feature")
	gitRun(t, repo, "fetch", "-q", "--prune")

	if st := StatusOf(repo); st.Upstream != "" {
		t.Errorf("status = %+v, want no upstream once it is gone", st)
	}
	if got := UpstreamOf(repo); got != "" {
		t.Errorf("UpstreamOf = %q, want none, agreeing with the status", got)
	}
}

// TestABranchHeldByADeletedWorktreeSaysToPrune: git said only that the branch
// was "already used by worktree at" a directory that was not there, after a
// line of its own progress, and nothing about how to get past it.
func TestABranchHeldByADeletedWorktreeSaysToPrune(t *testing.T) {
	repo := newRepo(t)
	gone := filepath.Join(t.TempDir(), "gone")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feature", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	err := AddFrom(repo, filepath.Join(t.TempDir(), "again"), "feature", "")
	if err == nil || !strings.Contains(err.Error(), "Prune") || strings.Contains(err.Error(), "Preparing") {
		t.Errorf("err = %v, want it to say Prune clears the stale record", err)
	}
	if _, err := Prune(repo); err != nil {
		t.Fatal(err)
	}
	if err := AddFrom(repo, filepath.Join(t.TempDir(), "again"), "feature", ""); err != nil {
		t.Errorf("after pruning, the branch should check out again: %v", err)
	}
}

// TestRemovingABusyWorktreeSaysWhatIsLeft: on Windows a file held open in the
// worktree lets git drop its record and then fail to delete the folder, with
// "Invalid argument"; pressing remove again answers only that it is not a
// worktree.
func TestRemovingABusyWorktreeSaysWhatIsLeft(t *testing.T) {
	if filepath.Separator == '/' {
		t.Skip("an open file stops a deletion only on Windows")
	}
	repo := newRepo(t)
	wt := filepath.Join(t.TempDir(), "busy")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "busy", wt)
	write(t, wt, "open.txt", "x\n")
	f, err := os.Open(filepath.Join(wt, "open.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = Remove(repo, wt, true)
	if err == nil || !strings.Contains(err.Error(), "could not be deleted") || !strings.Contains(err.Error(), wt) {
		t.Errorf("err = %v, want it to say the folder is left and why", err)
	}
}

// TestABranchNameGitRefusesSaysWhyAndOffersOne: git said only that the name
// was "not a valid branch name", after a line of its own progress.
func TestABranchNameGitRefusesSaysWhyAndOffersOne(t *testing.T) {
	repo := newRepo(t)
	for name, want := range map[string]string{
		"my feature": `"my-feature" would do`,
		"wip:today":  `"wip-today" would do`,
		"topic.lock": `"topic" would do`,
		"trailing/":  `"trailing" would do`,
	} {
		err := AddFrom(repo, filepath.Join(t.TempDir(), "wt"), name, "")
		if err == nil || !strings.Contains(err.Error(), "cannot be a branch name") ||
			!strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "Preparing") {
			t.Errorf("%q: err = %v, want the reason and %s", name, err, want)
		}
	}
	// A name git takes is untouched.
	if err := AddFrom(repo, filepath.Join(t.TempDir(), "wt"), "feature/ok-1", ""); err != nil {
		t.Errorf("a valid name was refused: %v", err)
	}
}

// TestAWorktreeMidRebaseIsLabelledByItsBranch: git's list says only that HEAD
// is detached, and the panel called the worktree "detached@<sha>" while its
// pane header called it by its branch.
func TestAWorktreeMidRebaseIsLabelledByItsBranch(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "f.txt", "base\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "base")
	wt := filepath.Join(t.TempDir(), "topic")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "topic", wt)
	write(t, wt, "f.txt", "topic\n")
	gitRun(t, wt, "commit", "-qam", "topic")
	write(t, repo, "f.txt", "main\n")
	gitRun(t, repo, "commit", "-qam", "main")
	cmd := exec.Command("git", "rebase", "main")
	cmd.Dir = wt
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected the rebase to stop on a conflict: %s", out)
	}
	t.Cleanup(func() {
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = wt
		_ = abort.Run()
	})

	wts, err := ListDetailed(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range wts {
		if samePath(w.Path, wt) && w.Label() != "topic (rebasing)" {
			t.Errorf("label = %q, want the branch being rebased", w.Label())
		}
	}
}

// TestRemovingAWorktreeThatChangedSinceSaysSo: the panel forces a removal
// only when the row it drew showed changes, and git refused one made since
// with "use --force", a flag the panel cannot pass.
func TestRemovingAWorktreeThatChangedSinceSaysSo(t *testing.T) {
	repo := newRepo(t)
	wt := filepath.Join(t.TempDir(), "busy")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "busy", wt)
	write(t, wt, "README.md", "changed since the panel drew it\n")
	write(t, wt, "new.txt", "new\n")
	err := Remove(repo, wt, false)
	if err == nil || !strings.Contains(err.Error(), "1 changed, 1 new") || strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want what is there and how to go on", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("an unforced removal should leave the work where it is: %v", err)
	}
}

// TestABranchStartedFromARemoteOneDoesNotTrackIt: started from origin/main,
// the new branch tracked it, so its header named origin/main as its upstream
// and Push was refused for the names not matching.
func TestABranchStartedFromARemoteOneDoesNotTrackIt(t *testing.T) {
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--bare", "--initial-branch=main")
	cmd.Dir = origin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bare init failed: %v: %s", err, out)
	}
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", origin)
	gitRun(t, repo, "push", "-q", "-u", "origin", "main")

	wt := filepath.Join(t.TempDir(), "fx")
	if err := AddFrom(repo, wt, "feature-x", "origin/main"); err != nil {
		t.Fatal(err)
	}
	if got := UpstreamOf(wt); got != "" {
		t.Errorf("upstream = %q, want none until the branch is pushed", got)
	}
	gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "work")
	if _, err := Push(wt); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := UpstreamOf(wt); got != "origin/feature-x" {
		t.Errorf("upstream after the push = %q, want origin/feature-x", got)
	}
}

// TestDefaultWorktreePathIgnoresATrailingSeparator: the parent of "C:\repo\"
// is "C:\repo" to filepath.Dir, so a project opened with a trailing separator
// had its worktrees suggested inside itself.
func TestDefaultWorktreePathIgnoresATrailingSeparator(t *testing.T) {
	repo := newRepo(t)
	got := DefaultWorktreePath(repo+string(filepath.Separator), "feature")
	if want := DefaultWorktreePath(repo, "feature"); got != want {
		t.Errorf("suggested %q, want %q beside the repository", got, want)
	}
	if filepath.Dir(got) != filepath.Dir(repo) {
		t.Errorf("suggested %q is not beside %q", got, repo)
	}
}

// TestABranchAlreadyCheckedOutSaysWhere: git's answer led with its own
// progress and said the branch was "already used by worktree at" a path,
// without the way on.
func TestABranchAlreadyCheckedOutSaysWhere(t *testing.T) {
	repo := newRepo(t)
	err := AddFrom(repo, filepath.Join(t.TempDir(), "again"), "main", "")
	if err == nil || !strings.Contains(err.Error(), "already checked out in") ||
		!strings.Contains(err.Error(), "open an agent there") || strings.Contains(err.Error(), "Preparing") {
		t.Errorf("err = %v, want where it is checked out and what to do", err)
	}
}

// TestAWorktreeMidBisectIsNamedByItsBranch: a bisect detaches HEAD as a rebase
// does, and the checkout read as "detached" in the pane header and
// "detached@<sha>" in the panel.
func TestAWorktreeMidBisectIsNamedByItsBranch(t *testing.T) {
	repo := newRepo(t)
	for _, body := range []string{"a\n", "b\n", "c\n"} {
		write(t, repo, "n.txt", body)
		gitRun(t, repo, "add", "-A")
		gitRun(t, repo, "commit", "-qm", "step")
	}
	gitRun(t, repo, "bisect", "start", "HEAD", "HEAD~2")
	t.Cleanup(func() {
		reset := exec.Command("git", "bisect", "reset")
		reset.Dir = repo
		_ = reset.Run()
	})

	st := StatusOf(repo)
	if !st.Detached || st.Branch != "main" || st.Operation != "bisecting" {
		t.Errorf("status = %+v, want main, bisecting", st)
	}
	wts, err := ListDetailed(repo)
	if err != nil || len(wts) == 0 {
		t.Fatalf("ListDetailed = %+v, %v", wts, err)
	}
	if got := wts[0].Label(); got != "main (bisecting)" {
		t.Errorf("label = %q, want main (bisecting)", got)
	}
}
