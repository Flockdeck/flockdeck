package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// TestDiscardWorktreeLeavesOneThisRunDidNotMake covers the data loss a failed
// spawn could cause. The clean-up assumed every worktree a fan-out's jobs were
// given had just been made for them, and force-removed it -- but the worktree a
// branch already had was reused, so the checkout could be somebody else's, with
// their uncommitted work in it.
func TestDiscardWorktreeLeavesOneThisRunDidNotMake(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	path := filepath.Join(t.TempDir(), "theirs")
	if err := gitx.AddFrom(repo, path, "agent/theirs", ""); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	unsaved := filepath.Join(path, "unsaved.txt")
	if err := os.WriteFile(unsaved, []byte("half a day's work\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := discardWorktree(repo, &fanoutJob{task: "x", branch: "agent/theirs", cwd: path}, idle); err != nil {
		t.Errorf("discardWorktree: %v", err)
	}
	if _, err := os.Stat(unsaved); err != nil {
		t.Fatalf("the uncommitted work in a worktree this run did not make was deleted: %v", err)
	}
}

// TestDiscardWorktreeLeavesOneAPaneIsWorkingIn covers a worktree this run made
// that a `flockdeck spawn --worktree` on the same branch has since started a
// helper in. The fan-out's own agent failing to start is no reason to take the
// checkout from under the helper.
func TestDiscardWorktreeLeavesOneAPaneIsWorkingIn(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	path := filepath.Join(t.TempDir(), "shared")
	if err := gitx.AddNewBranch(repo, path, "agent/shared"); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	working := func(p string) (bool, bool) { return p == path, true }

	if err := discardWorktree(repo, &fanoutJob{task: "x", branch: "agent/shared", cwd: path, path: path, created: true}, working); err != nil {
		t.Errorf("discardWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Fatalf("the worktree was removed from under the pane working in it: %v", err)
	}
}

// TestAFanoutMakesNoWorktreesWithoutTheBranchList covers a repository whose
// branches could not be read. Taken as a list of none, every name looked free,
// and a task whose name matched a branch the repository had was handed that
// branch's checkout.
func TestAFanoutMakesNoWorktreesWithoutTheBranchList(t *testing.T) {
	notARepo := t.TempDir()
	c := &controlClient{out: make(chan []byte, 32)}
	jobs := []*fanoutJob{{task: "split the router", cwd: notARepo}}

	if prepareWorktrees(c, notARepo, jobs) {
		t.Fatal("the fan-out went on to make worktrees without knowing which branches the repository has")
	}
	if jobs[0].created || jobs[0].cwd != notARepo {
		t.Errorf("job = %+v, want it left where it was", jobs[0])
	}
	select {
	case raw := <-c.out:
		var msg noticeMsg
		if err := json.Unmarshal(raw, &msg); err != nil || !msg.Error || !strings.Contains(msg.Text, "no worktrees were created") {
			t.Errorf("said %s, want an error saying no worktrees were created", raw)
		}
	default:
		t.Error("the fan-out stopped without saying why")
	}
}

// TestAFanoutsWorktreesAreEachItsOwn covers two tasks whose worktrees were
// placed one at a time while being created together. The first stepped around
// a stray directory to the -2 name, which is the second's own, and neither
// could see the other's choice: one of the two failed on the other's directory.
func TestAFanoutsWorktreesAreEachItsOwn(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	stray := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-agent-fix-the-parser")
	if err := os.Mkdir(stray, 0o700); err != nil {
		t.Fatal(err)
	}
	c := &controlClient{out: make(chan []byte, 32)}
	jobs := []*fanoutJob{{task: "fix the parser", cwd: repo}, {task: "fix the parser 2", cwd: repo}}

	if !prepareWorktrees(c, repo, jobs) {
		t.Fatal("the fan-out stopped before making any worktrees")
	}
	for _, j := range jobs {
		if j.err != nil || !j.created {
			t.Errorf("%q: err = %v, created = %v; want a worktree of its own", j.task, j.err, j.created)
		}
	}
	if samePath(filepath.Clean(jobs[0].cwd), filepath.Clean(jobs[1].cwd)) {
		t.Errorf("both tasks were put in %s", jobs[0].cwd)
	}
}

// TestRepoLockIsSharedByAWorktreeAndItsRepository covers a helper spawned from
// a pane inside a linked worktree while a fan-out works in the checkout it was
// cut from. Both make worktrees in one repository, and each asks from its own
// directory, so a lock kept per directory would hold neither back.
func TestRepoLockIsSharedByAWorktreeAndItsRepository(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if err := gitx.AddNewBranch(repo, path, "agent/linked"); err != nil {
		t.Fatalf("worktree add: %v", err)
	}

	unlock := lockRepo(repo)
	had := make(chan struct{})
	go func() {
		lockRepo(path)()
		close(had)
	}()
	select {
	case <-had:
		unlock()
		t.Fatal("the worktree's lock was had while its repository's was held")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-had:
	case <-time.After(10 * time.Second):
		t.Fatal("the worktree's lock was never had once the repository's was let go")
	}

	// Another repository is not held back by this one.
	defer lockRepo(repo)()
	other := make(chan struct{})
	go func() {
		lockRepo(newTestRepo(t))()
		close(other)
	}()
	select {
	case <-other:
	case <-time.After(10 * time.Second):
		t.Fatal("another repository waited on this one's lock")
	}
}
