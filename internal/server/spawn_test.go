package server

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestDiscardingAWorktreeSaysWhenItCannot covers the clean-up after an agent
// that could not start. A worktree it cannot remove stays behind looking like
// one an agent is working in, so the failure has to reach the notice rather
// than be dropped.
func TestDiscardingAWorktreeSaysWhenItCannot(t *testing.T) {
	notARepo := t.TempDir()
	wt := t.TempDir()
	job := &worktreeJob{task: "never started", cwd: wt, path: wt, created: true}
	if err := discardWorktree(notARepo, job, idle); err == nil {
		t.Fatal("removing a worktree from somewhere that is not a repository reported nothing")
	}
	// Nothing was cut, so there is nothing to remove and nothing to say.
	if err := discardWorktree("", job, idle); err != nil {
		t.Errorf("a job with no worktree of its own reported %v", err)
	}
}

// idle is a pane count that finds nobody working anywhere.
func idle(string) (bool, bool) { return false, true }

// TestAgentChoicesSayWhichAgentsAskAboutTrust covers the offer to carry folder
// trust over, which is only worth making for a run whose agents ask whether a
// folder is trusted.
func TestAgentChoicesSayWhichAgentsAskAboutTrust(t *testing.T) {
	srv, ws := newTestServer(t)
	specs, _ := ask(srv, func() []agent.Spec {
		specs, _ := ws.Agents()
		return specs
	})
	agents := srv.agentChoices(specs)
	asking := 0
	for _, a := range agents {
		spec, _ := ws.Catalog().Find(a.ID)
		if a.AskTrust != spec.Caps.Trust {
			t.Errorf("%s: askTrust = %v, want %v", a.ID, a.AskTrust, spec.Caps.Trust)
		}
		if a.AskTrust {
			asking++
		}
	}
	if asking == 0 {
		t.Error("no agent in the catalog is said to ask about trust, though Claude Code does")
	}
}

// TestRefusedSpawnCutsNoWorktree covers `flockdeck spawn --worktree` asking for
// an agent this machine cannot start. The refusal used to come after git had
// made the branch and its checkout, which were then left behind with nothing
// in them -- indistinguishable, in the worktree panel, from work in progress.
func TestRefusedSpawnCutsNoWorktree(t *testing.T) {
	srv, ws, repo := newRepoServer(t)
	parent := make(chan string, 1)
	srv.do(func() { parent <- ws.CurrentTab().Focus })

	hookSrv := ws.HookServer()
	_, err := hooks.Spawn(hookSrv.BaseURL(), hookSrv.Token(), <-parent, hooks.SpawnRequest{
		Task: "fix the parser", Branch: "fix-parser", Agent: "no-such-agent",
	})
	if err == nil || !strings.Contains(err.Error(), "no-such-agent") {
		t.Fatalf("spawn = %v, want a refusal naming the agent", err)
	}

	if wts, err := gitx.List(repo); err != nil || len(wts) != 1 {
		t.Errorf("a refused spawn left the repository with worktrees %+v (%v)", wts, err)
	}
	if taken, _ := localBranches(repo); taken["fix-parser"] {
		t.Error("a refused spawn left its branch behind")
	}
}

// Writing out a working tree is the slowest thing starting agents does, and a
// batch of starts is a dozen of them. Doing them one after another is a stretch of
// nothing happening before the first agent appears, so they have to overlap.
func TestMakeWorktreesRunTogether(t *testing.T) {
	const (
		jobs   = 8
		each   = 100 * time.Millisecond
		serial = jobs * each
	)
	var (
		mu      sync.Mutex
		running int
		peak    int
	)
	work := make([]*worktreeJob, jobs)
	for i := range work {
		branch := fmt.Sprintf("agent/t%d", i)
		work[i] = &worktreeJob{task: fmt.Sprintf("task %d", i), branch: branch, path: `C:\repo-` + branch}
	}

	start := time.Now()
	makeWorktrees(work, func(branch, path string) error {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		mu.Unlock()
		time.Sleep(each)
		mu.Lock()
		running--
		mu.Unlock()
		return nil
	})
	elapsed := time.Since(start)

	if peak < 2 {
		t.Errorf("never more than %d worktree at a time; they are not overlapping", peak)
	}
	// Generous: the point is that it is nothing like the sum of the parts.
	if elapsed > serial/2 {
		t.Errorf("took %s for %d worktrees of %s each; serial would be %s", elapsed, jobs, each, serial)
	}
	for i, j := range work {
		if j.err != nil {
			t.Errorf("job %d: %v", i, j.err)
		}
		if want := `C:\repo-agent/t` + fmt.Sprint(i); j.cwd != want || !j.created {
			t.Errorf("job %d: cwd = %q, created = %v, want %q, created", i, j.cwd, j.created, want)
		}
	}
}

// A worktree that cannot be created belongs to its own task. The rest are
// still prepared, and the failure is kept where the task order can report it.
func TestMakeWorktreesKeepsFailuresWithTheirTask(t *testing.T) {
	work := []*worktreeJob{
		{task: "first", branch: "agent/first", path: "/wt/agent/first"},
		{task: "second", branch: "agent/second", path: "/wt/agent/second"},
		{task: "third", branch: "agent/third", path: "/wt/agent/third"},
	}
	boom := errors.New("already checked out")
	makeWorktrees(work, func(branch, path string) error {
		if branch == "agent/second" {
			return boom
		}
		return nil
	})

	if work[0].cwd != "/wt/agent/first" || work[0].err != nil {
		t.Errorf("first = %+v, want its worktree", work[0])
	}
	if !errors.Is(work[1].err, boom) {
		t.Errorf("second err = %v, want the failure", work[1].err)
	}
	if work[1].cwd != "" || work[1].created {
		t.Errorf("second cwd = %q, created = %v, want it left alone and not its own", work[1].cwd, work[1].created)
	}
	if work[2].cwd != "/wt/agent/third" || work[2].err != nil {
		t.Errorf("third = %+v, want its worktree; one failure must not cancel the rest", work[2])
	}
}

// Tasks that begin alike derive the same branch name once it is truncated, and
// PrepareWorktree reuses the worktree a branch already has — so without this
// two agents would quietly share one checkout.
func TestNameBranchesAreDistinct(t *testing.T) {
	jobs := []*worktreeJob{
		{task: "Add a health endpoint to the HTTP server and wire it up"},
		{task: "Add a health endpoint to the HTTP server and document it"},
		{task: "Add a health endpoint to the HTTP server and test it"},
	}
	// No branch in the repository is taken, leaving the within-batch check.
	nameBranches(jobs, nil)

	base := workspace.BranchNameFor(jobs[0].task)
	if workspace.BranchNameFor(jobs[1].task) != base {
		t.Fatal("the tasks no longer derive the same name; the test needs them to collide")
	}
	// The first sibling keeps the plain name and the rest are numbered, so the
	// branches read as a set rather than as three unrelated ones.
	want := []string{base, base + "-2", base + "-3"}
	for i, j := range jobs {
		if j.branch != want[i] {
			t.Errorf("%q got branch %q, want %q", j.task, j.branch, want[i])
		}
	}
}

// A worktree is cut before the agent that will work in it is started, so a
// pane that fails to open leaves a branch and a fresh copy of the project
// with nothing in it — indistinguishable, from the worktree list, from a
// checkout an agent is busy in.
func TestDiscardWorktreeRemovesTheOneThatWasJustMade(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	if err := gitx.AddFrom(repo, path, "agent/never-started", ""); err != nil {
		t.Fatalf("worktree add: %v", err)
	}

	if err := discardWorktree(repo, &worktreeJob{task: "x", branch: "agent/never-started", cwd: path, path: path, created: true}, idle); err != nil {
		t.Errorf("discardWorktree: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree directory is still there: %v", err)
	}
	wts, err := gitx.List(repo)
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	for _, wt := range wts {
		if wt.Branch == "agent/never-started" {
			t.Errorf("git still lists the worktree at %s", wt.Path)
		}
	}
	// The branch was made for the worktree, and is as stray without it.
	if gitx.BranchExists(repo, "agent/never-started") {
		t.Error("the worktree was removed and its branch left behind")
	}
}

// The dangerous case: a start without worktrees runs every agent in the
// project itself, so a pane that fails to start must not take the user's own
// checkout with it.
func TestDiscardWorktreeLeavesTheProjectAlone(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)

	discardWorktree(repo, &worktreeJob{task: "x", cwd: repo}, idle)

	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatalf("the project checkout was removed: %v", err)
	}
}

// newTestRepo creates a repository with one commit and returns its path.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	steps := [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps = append(steps, []string{"add", "."}, []string{"commit", "-m", "initial"})
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	return dir
}

// A branch the repository already has must not be handed to an agent: the
// worktree for it would be reused rather than created, and two agents would
// share one checkout without anything saying so.
func TestNameBranchesStepAroundExistingOnes(t *testing.T) {
	base := workspace.BranchNameFor("split the router into three files")
	jobs := []*worktreeJob{{task: "split the router into three files"}}

	nameBranches(jobs, map[string]bool{base: true})
	if jobs[0].branch == base {
		t.Errorf("branch = %q, which the repository already has", jobs[0].branch)
	}
	if jobs[0].branch != base+"-2" {
		t.Errorf("branch = %q, want %q", jobs[0].branch, base+"-2")
	}
}

// git keeps a loose ref as a file, so on Windows and macOS "agent/Fix" and
// "agent/fix" are one ref — and `git worktree add -b` refuses the second with
// a lock error, which reaches the user as a task that would not start. The
// branch names a start derives are always lower case, so the repository's
// own are folded to match.
func TestLocalBranchesFoldCase(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	const task = "split the router into three files"
	base := workspace.BranchNameFor(task)

	cmd := exec.Command("git", "branch", strings.ToUpper(base))
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git branch failed (%v): %s", err, out)
	}

	taken, err := localBranches(repo)
	if err != nil {
		t.Fatalf("localBranches: %v", err)
	}
	if !taken[base] {
		t.Fatalf("the repository has %s but localBranches reported %v", strings.ToUpper(base), taken)
	}

	jobs := []*worktreeJob{{task: task}}
	nameBranches(jobs, taken)
	if strings.EqualFold(jobs[0].branch, base) {
		t.Errorf("branch = %q, which differs from an existing branch only by case", jobs[0].branch)
	}
}
