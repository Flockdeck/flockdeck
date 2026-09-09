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
	// repo "" skips the git lookup, leaving the within-fan-out check.
	nameBranches(jobs, "")

	seen := map[string]bool{}
	for _, j := range jobs {
		if j.branch == "" {
			t.Fatalf("%q got no branch", j.task)
		}
		if seen[j.branch] {
			t.Errorf("%q reuses the branch %s", j.task, j.branch)
		}
		seen[j.branch] = true
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
