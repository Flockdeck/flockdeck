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

	"github.com/jmwri/perch/internal/gitx"
	"github.com/jmwri/perch/internal/workspace"
)

// Writing out a working tree is the slowest thing a fan-out does, and a
// fan-out is a dozen of them. Doing them one after another is a stretch of
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
	work := make([]*fanoutJob, jobs)
	for i := range work {
		work[i] = &fanoutJob{task: fmt.Sprintf("task %d", i), branch: fmt.Sprintf("agent/t%d", i)}
	}

	start := time.Now()
	makeWorktrees(work, func(branch string) (string, error) {
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
		return `C:\repo-` + branch, nil
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
		if want := `C:\repo-agent/t` + fmt.Sprint(i); j.cwd != want {
			t.Errorf("job %d: cwd = %q, want %q", i, j.cwd, want)
		}
	}
}

// A worktree that cannot be created belongs to its own task. The rest are
// still prepared, and the failure is kept where the task order can report it.
func TestMakeWorktreesKeepsFailuresWithTheirTask(t *testing.T) {
	work := []*fanoutJob{
		{task: "first", branch: "agent/first"},
		{task: "second", branch: "agent/second"},
		{task: "third", branch: "agent/third"},
	}
	boom := errors.New("already checked out")
	makeWorktrees(work, func(branch string) (string, error) {
		if branch == "agent/second" {
			return "", boom
		}
		return "/wt/" + branch, nil
	})

	if work[0].cwd != "/wt/agent/first" || work[0].err != nil {
		t.Errorf("first = %+v, want its worktree", work[0])
	}
	if !errors.Is(work[1].err, boom) {
		t.Errorf("second err = %v, want the failure", work[1].err)
	}
	if work[1].cwd != "" {
		t.Errorf("second cwd = %q, want it left alone", work[1].cwd)
	}
	if work[2].cwd != "/wt/agent/third" || work[2].err != nil {
		t.Errorf("third = %+v, want its worktree; one failure must not cancel the rest", work[2])
	}
}

// Tasks that begin alike derive the same branch name once it is truncated, and
// PrepareWorktree reuses the worktree a branch already has — so without this
// two agents would quietly share one checkout.
func TestNameBranchesAreDistinct(t *testing.T) {
	jobs := []*fanoutJob{
		{task: "Add a health endpoint to the HTTP server and wire it up"},
		{task: "Add a health endpoint to the HTTP server and document it"},
		{task: "Add a health endpoint to the HTTP server and test it"},
	}
	// No branch in the repository is taken, leaving the within-fan-out check.
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

	discardWorktree(repo, repo, &fanoutJob{task: "x", branch: "agent/never-started", cwd: path})

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
}

// The dangerous case: a fan-out without worktrees runs every agent in the
// project itself, so a pane that fails to start must not take the user's own
// checkout with it.
func TestDiscardWorktreeLeavesTheProjectAlone(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)

	discardWorktree(repo, repo, &fanoutJob{task: "x", cwd: repo})

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

// A fan-out has to account for itself: it opens a screenful of panes, and the
// user cannot count them. The case that used to be silent is the one where
// nothing started at all — which is when the summary matters most.
func TestFanoutSummary(t *testing.T) {
	cases := []struct {
		started, failed int
		want            string
		wantErr         bool
	}{
		{3, 0, "started 3 agents", false},
		{1, 0, "started 1 agent", false},
		{2, 1, "started 2 agents, 1 could not be started", true},
		{0, 1, "the agent could not be started", true},
		{0, 4, "none of the 4 agents could be started", true},
		{0, 0, "no tasks to start", true},
	}
	for _, c := range cases {
		got, gotErr := fanoutSummary(c.started, c.failed)
		if got != c.want || gotErr != c.wantErr {
			t.Errorf("fanoutSummary(%d, %d) = %q,%v, want %q,%v",
				c.started, c.failed, got, gotErr, c.want, c.wantErr)
		}
	}
}

// Whatever it says, it says something: an outcome the user is never told about
// is the one that reads as an accepted fan-out that quietly did the work.
func TestFanoutSummaryIsNeverSilent(t *testing.T) {
	for started := 0; started < 4; started++ {
		for failed := 0; failed < 4; failed++ {
			if text, _ := fanoutSummary(started, failed); strings.TrimSpace(text) == "" {
				t.Errorf("fanoutSummary(%d, %d) said nothing", started, failed)
			}
		}
	}
}

// Folder trust is carried over for each worktree, and only for the worktrees:
// a task that never got one has nothing to trust, and the project itself is
// already trusted or the box would not have been offered.
func TestInheritTrustCoversEveryWorktree(t *testing.T) {
	base := `C:\repo`
	jobs := []*fanoutJob{
		{task: "one", cwd: `C:\repo-one`},
		{task: "two", err: errors.New("already checked out")},
		{task: "three", cwd: `C:\repo-three`},
		{task: "four", cwd: base},
	}
	var got []string
	inheritTrust(jobs, base, func(from, to string) error {
		if from != base {
			t.Errorf("inherited from %q, want %q", from, base)
		}
		got = append(got, to)
		return nil
	}, func(text string) { t.Errorf("unexpected notice: %s", text) })

	want := []string{`C:\repo-one`, `C:\repo-three`}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trusted %q, want %q", got, want)
	}
}

// The configuration is one file, so a failure to write it is about the file
// rather than about a task. Saying so a dozen times would bury the fan-out's
// own messages under copies of the same complaint.
func TestInheritTrustReportsOneFailure(t *testing.T) {
	jobs := []*fanoutJob{
		{task: "one", cwd: "/wt/one"},
		{task: "two", cwd: "/wt/two"},
		{task: "three", cwd: "/wt/three"},
	}
	calls, notices := 0, 0
	inheritTrust(jobs, "/repo", func(string, string) error {
		calls++
		return errors.New("permission denied")
	}, func(text string) {
		notices++
		if !strings.Contains(text, "permission denied") {
			t.Errorf("notice = %q, want the reason in it", text)
		}
	})

	if calls != 1 {
		t.Errorf("tried %d times after a failure, want 1", calls)
	}
	if notices != 1 {
		t.Errorf("said it %d times, want once", notices)
	}
}

// The cap is the only thing standing between an edited list and a machine
// asked to run a Claude session per line of it. The proposed list was capped,
// but the user edits it before anything starts.
func TestPlanJobsStopsAtTheCap(t *testing.T) {
	var tasks []string
	for i := 0; i < workspace.MaxTasks+5; i++ {
		tasks = append(tasks, fmt.Sprintf("task %d", i))
	}
	jobs, capped := planJobs(tasks, "/repo")
	if !capped {
		t.Error("a list past the cap was not reported as capped")
	}
	if len(jobs) != workspace.MaxTasks {
		t.Errorf("planned %d agents, want the cap of %d", len(jobs), workspace.MaxTasks)
	}

	// Exactly the cap is not over it.
	if _, capped := planJobs(tasks[:workspace.MaxTasks], "/repo"); capped {
		t.Error("a list of exactly the cap was reported as capped")
	}
}

// The cap counts the tasks actually taken, not positions in the list. The list
// comes from a box the user has been editing, so it is full of empty rows, and
// counting those lets a few blank lines stand in for agents never started.
func TestPlanJobsIgnoresBlankRows(t *testing.T) {
	// More blank rows before the first real task than the cap allows agents,
	// which is what a counted-by-position cap turns into no agents at all.
	tasks := make([]string, 0, 40)
	for i := 0; i < workspace.MaxTasks+3; i++ {
		tasks = append(tasks, "")
	}
	tasks = append(tasks, "  split the router	", "", "add a timeout", "   ")
	for i := 0; i < 20; i++ {
		tasks = append(tasks, "")
	}
	jobs, capped := planJobs(tasks, `C:epo`)
	if capped {
		t.Error("blank rows were counted towards the cap")
	}
	if len(jobs) != 2 {
		t.Fatalf("planned %d agents, want 2: %#v", len(jobs), jobs)
	}
	// In the order of the plan, trimmed, and all starting in the project.
	for i, want := range []string{"split the router", "add a timeout"} {
		if jobs[i].task != want {
			t.Errorf("task %d = %q, want %q", i, jobs[i].task, want)
		}
		if jobs[i].cwd != `C:epo` {
			t.Errorf("task %d starts in %q, want the project", i, jobs[i].cwd)
		}
	}
}

// A list of nothing but blank rows is not a fan-out of nought agents, it is a
// fan-out that has to say so.
func TestPlanJobsOnAnEmptyList(t *testing.T) {
	jobs, capped := planJobs([]string{"", "   ", ""}, "/repo")
	if len(jobs) != 0 || capped {
		t.Errorf("planJobs on blank rows = %d jobs, capped=%v; want none and not capped", len(jobs), capped)
	}
}

// A branch the repository already has must not be handed to an agent: the
// worktree for it would be reused rather than created, and two agents would
// share one checkout without anything saying so.
func TestNameBranchesStepAroundExistingOnes(t *testing.T) {
	base := workspace.BranchNameFor("split the router into three files")
	jobs := []*fanoutJob{{task: "split the router into three files"}}

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
// branch names a fan-out derives are always lower case, so the repository's
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

	taken := localBranches(repo)
	if !taken[base] {
		t.Fatalf("the repository has %s but localBranches reported %v", strings.ToUpper(base), taken)
	}

	jobs := []*fanoutJob{{task: task}}
	nameBranches(jobs, taken)
	if strings.EqualFold(jobs[0].branch, base) {
		t.Errorf("branch = %q, which differs from an existing branch only by case", jobs[0].branch)
	}
}
