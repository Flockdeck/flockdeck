package server

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/help"
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
	job := &fanoutJob{task: "never started", cwd: wt, path: wt, created: true}
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

// TestFanoutCatalogSaysWhichAgentsAskAboutTrust covers the fan-out dialog's
// offer to carry folder trust over, which is only worth making for a run whose
// agents ask whether a folder is trusted.
func TestFanoutCatalogSaysWhichAgentsAskAboutTrust(t *testing.T) {
	srv, ws := newTestServer(t)
	specs, _ := ask(srv, func() []agent.Spec {
		specs, _ := ws.Agents()
		return specs
	})
	agents := srv.fanoutCatalog(specs)
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

// TestTrustIsCarriedOnlyForAgentsThatAsk covers a fan-out split between agents.
// Carrying folder trust over writes Claude Code's configuration, and a row run
// by an agent with no such question has nothing to carry.
func TestTrustIsCarriedOnlyForAgentsThatAsk(t *testing.T) {
	asks := &fanoutJob{task: "refactor", agent: "claude"}
	silent := &fanoutJob{task: "rename", agent: "codex"}
	spec := func(id string) (agent.Spec, error) {
		return agent.Spec{ID: id, Caps: agent.Caps{Trust: id == "claude"}}, nil
	}
	got := jobsAskingTrust([]*fanoutJob{asks, silent}, spec)
	if len(got) != 1 || got[0] != asks {
		t.Fatalf("trust would be carried for %+v, want only the claude row", got)
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
		branch := fmt.Sprintf("agent/t%d", i)
		work[i] = &fanoutJob{task: fmt.Sprintf("task %d", i), branch: branch, path: `C:\repo-` + branch}
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
	work := []*fanoutJob{
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

	if err := discardWorktree(repo, &fanoutJob{task: "x", branch: "agent/never-started", cwd: path, path: path, created: true}, idle); err != nil {
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

// The dangerous case: a fan-out without worktrees runs every agent in the
// project itself, so a pane that fails to start must not take the user's own
// checkout with it.
func TestDiscardWorktreeLeavesTheProjectAlone(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)

	discardWorktree(repo, &fanoutJob{task: "x", cwd: repo}, idle)

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

// The children of a fan-out share a tab, so the tab is named after the fan-out
// rather than after one of them. A single task is not a fan-out in that sense:
// its pane gets the tab to itself and is named after the work, exactly as a
// pane spawned any other way is.
func TestFanoutTabTitle(t *testing.T) {
	jobs := []*fanoutJob{{task: "add a health endpoint"}, {task: "write parser tests"}}
	if got := fanoutTabTitle(jobs); got != "Fan out" {
		t.Errorf("fanoutTabTitle of %d tasks = %q, want the fan-out named", len(jobs), got)
	}
	for _, few := range [][]*fanoutJob{jobs[:1], nil} {
		if got := fanoutTabTitle(few); got != "" {
			t.Errorf("fanoutTabTitle of %d tasks = %q, want the task to name its own tab", len(few), got)
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
	jobs, capped := planJobs(fanoutRequest{Tasks: tasks}, "/repo")
	if !capped {
		t.Error("a list past the cap was not reported as capped")
	}
	if len(jobs) != workspace.MaxTasks {
		t.Errorf("planned %d agents, want the cap of %d", len(jobs), workspace.MaxTasks)
	}

	// Exactly the cap is not over it.
	if _, capped := planJobs(fanoutRequest{Tasks: tasks[:workspace.MaxTasks]}, "/repo"); capped {
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
	jobs, capped := planJobs(fanoutRequest{Tasks: tasks}, `C:epo`)
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
		if jobs[i].cwd != `C:epo` {
			t.Errorf("task %d starts in %q, want the project", i, jobs[i].cwd)
		}
	}
}

// A list of nothing but blank rows is not a fan-out of nought agents, it is a
// fan-out that has to say so.
func TestPlanJobsOnAnEmptyList(t *testing.T) {
	jobs, capped := planJobs(fanoutRequest{Tasks: []string{"", "   ", ""}}, "/repo")
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

	taken, err := localBranches(repo)
	if err != nil {
		t.Fatalf("localBranches: %v", err)
	}
	if !taken[base] {
		t.Fatalf("the repository has %s but localBranches reported %v", strings.ToUpper(base), taken)
	}

	jobs := []*fanoutJob{{task: task}}
	nameBranches(jobs, taken)
	if strings.EqualFold(jobs[0].branch, base) {
		t.Errorf("branch = %q, which differs from an existing branch only by case", jobs[0].branch)
	}
}

// The cap is a number the user meets — a plan of twenty tasks starts twelve
// agents and says so — and the help page states it. A page that names a
// different limit than the one enforced is worse than one that names none,
// because the user plans around it.
func TestHelpNamesTheCapTheServerEnforces(t *testing.T) {
	pages, err := help.Pages()
	if err != nil {
		t.Fatalf("help.Pages: %v", err)
	}
	var text string
	for _, p := range pages {
		if p.Slug == "fanout" {
			text = p.Text
		}
	}
	if text == "" {
		t.Fatal("no fan-out help page")
	}
	if want := strconv.Itoa(workspace.MaxTasks); !strings.Contains(text, want) {
		t.Errorf("the fan-out page does not name the cap of %s that the server enforces", want)
	}
}

// A fan-out spends seconds in git before the first pane can exist, and until
// then the window shows nothing at all. It has to say what it is waiting on —
// but only when there is a wait, since one worktree is quick.
func TestPreparingNotice(t *testing.T) {
	for _, n := range []int{0, 1} {
		if got := preparingNotice(n); got != "" {
			t.Errorf("preparingNotice(%d) = %q, want nothing said for a wait this short", n, got)
		}
	}
	got := preparingNotice(workspace.MaxTasks)
	if got == "" {
		t.Fatalf("preparingNotice(%d) said nothing", workspace.MaxTasks)
	}
	if !strings.Contains(got, strconv.Itoa(workspace.MaxTasks)) {
		t.Errorf("preparingNotice(%d) = %q, want it to say how many", workspace.MaxTasks, got)
	}
}

// A fan-out is one run but not necessarily one agent: the dialog offers a
// choice for the whole run and an override on each row, and each has to reach
// the job it was made for.
func TestPlanJobsCarriesTheChosenAgent(t *testing.T) {
	type want struct{ task, agent, model string }
	cases := []struct {
		name string
		req  fanoutRequest
		want []want
	}{{
		name: "nothing chosen is the default agent",
		req:  fanoutRequest{Tasks: []string{"one", "two"}},
		want: []want{{"one", "", ""}, {"two", "", ""}},
	}, {
		name: "one agent for the whole run",
		req:  fanoutRequest{Tasks: []string{"one", "two"}, Agent: "claude", Model: "sonnet"},
		want: []want{{"one", "claude", "sonnet"}, {"two", "claude", "sonnet"}},
	}, {
		// The point of the whole exercise: twelve tasks split between two
		// agents deliberately, in one run.
		name: "a row of its own",
		req: fanoutRequest{
			Tasks: []string{"one", "two"}, Agent: "claude", Model: "sonnet",
			TaskAgents: []string{"", "codex"}, TaskModels: []string{"", "gpt-5"},
		},
		want: []want{{"one", "claude", "sonnet"}, {"two", "codex", "gpt-5"}},
	}, {
		// A row that changes the agent and says nothing about the model does
		// not keep the run's model. It belongs to the run's agent, and asking
		// Codex for "sonnet" is asking for a model it has never heard of.
		name: "a row that names an agent and no model",
		req: fanoutRequest{
			Tasks: []string{"one", "two"}, Agent: "claude", Model: "sonnet",
			TaskAgents: []string{"", "codex"},
		},
		want: []want{{"one", "claude", "sonnet"}, {"two", "codex", ""}},
	}, {
		name: "a row that names only a model stays on the run's agent",
		req: fanoutRequest{
			Tasks: []string{"one", "two"}, Agent: "claude", Model: "sonnet",
			TaskModels: []string{"", "haiku"},
		},
		want: []want{{"one", "claude", "sonnet"}, {"two", "claude", "haiku"}},
	}, {
		// The overrides are positional against the list as it was sent, blank
		// rows included. Counting only the rows that became jobs would slide
		// every override up onto somebody else's task.
		name: "blank rows do not shift the overrides",
		req: fanoutRequest{
			Tasks: []string{"", "one", "  ", "two"}, Agent: "claude",
			TaskAgents: []string{"", "codex", "", ""},
		},
		want: []want{{"one", "codex", ""}, {"two", "claude", ""}},
	}, {
		// A window is free to send a shorter list of overrides, or none.
		name: "fewer overrides than tasks",
		req: fanoutRequest{
			Tasks: []string{"one", "two", "three"}, Agent: "claude",
			TaskAgents: []string{"codex"},
		},
		want: []want{{"one", "codex", ""}, {"two", "claude", ""}, {"three", "claude", ""}},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jobs, capped := planJobs(c.req, "/repo")
			if capped {
				t.Fatal("a short list was reported as capped")
			}
			if len(jobs) != len(c.want) {
				t.Fatalf("planned %d jobs, want %d", len(jobs), len(c.want))
			}
			for i, w := range c.want {
				got := want{jobs[i].task, jobs[i].agent, jobs[i].model}
				if got != w {
					t.Errorf("job %d = %+v, want %+v", i, got, w)
				}
			}
		})
	}
}

// An agent that is not installed stops its own rows and nothing else. Before
// this, one missing CLI was the end of the whole run — which was right while
// there was only ever one agent in it, and is not once a run can be split.
func TestPartitionDropsOnlyTheRowsOfAMissingAgent(t *testing.T) {
	missing := errors.New("the `codex` CLI was not found on PATH")
	jobs := []*fanoutJob{
		{task: "one", agent: "claude"},
		{task: "two", agent: "codex"},
		{task: "three", agent: "codex"},
		{task: "four", agent: "claude"},
	}

	keep, notices, dropped := partitionRunnable(jobs, map[string]error{"codex": missing})
	if dropped != 2 {
		t.Errorf("dropped = %d, want the two codex rows", dropped)
	}
	got := []string{}
	for _, j := range keep {
		got = append(got, j.task)
	}
	if strings.Join(got, ",") != "one,four" {
		t.Errorf("kept %v, want the claude rows in the order of the plan", got)
	}
	// One notice for the agent, not one per row, and it says how many rows it
	// cost so they are not looked for among the panes that did open.
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want one for the one missing agent", notices)
	}
	if !strings.Contains(notices[0], "codex") || !strings.Contains(notices[0], "2 tasks were not started") {
		t.Errorf("notice = %q, want it to name the agent and the two rows", notices[0])
	}
}

// With one agent for the whole run, a missing CLI says exactly what it always
// said: nothing about this has changed for somebody who only runs Claude.
func TestPartitionKeepsTheOldWordsForAWholeRun(t *testing.T) {
	missing := errors.New("the `claude` CLI was not found on PATH")
	jobs := []*fanoutJob{{task: "one", agent: "claude"}, {task: "two", agent: "claude"}}

	keep, notices, dropped := partitionRunnable(jobs, map[string]error{"claude": missing})
	if len(keep) != 0 || dropped != 2 {
		t.Errorf("kept %d and dropped %d, want none kept and both dropped", len(keep), dropped)
	}
	const want = "the `claude` CLI was not found on PATH, so no agents can be started"
	if len(notices) != 1 || notices[0] != want {
		t.Errorf("notices = %v, want exactly [%q]", notices, want)
	}
}

// Two missing agents are named in the order their rows appear, because the map
// they arrive in has none and a notice nobody can line up against the list is
// a notice that has to be read twice.
func TestPartitionNamesMissingAgentsInPlanOrder(t *testing.T) {
	bad := map[string]error{
		"codex":  errors.New("the `codex` CLI was not found on PATH"),
		"gemini": errors.New("the `gemini` CLI was not found on PATH"),
	}
	jobs := []*fanoutJob{
		{task: "one", agent: "gemini"},
		{task: "two", agent: "claude"},
		{task: "three", agent: "codex"},
	}
	for i := 0; i < 20; i++ {
		keep, notices, dropped := partitionRunnable(jobs, bad)
		if len(keep) != 1 || keep[0].task != "two" || dropped != 2 {
			t.Fatalf("kept %d jobs and dropped %d, want only the claude row", len(keep), dropped)
		}
		if len(notices) != 2 {
			t.Fatalf("notices = %v, want one per missing agent", notices)
		}
		if !strings.Contains(notices[0], "gemini") || !strings.Contains(notices[1], "codex") {
			t.Fatalf("notices = %v, want gemini before codex", notices)
		}
	}
}

// Nothing missing changes nothing: the same jobs, in the same order, and not a
// word said about them.
func TestPartitionSaysNothingWhenEveryAgentIsThere(t *testing.T) {
	jobs := []*fanoutJob{{task: "one", agent: "claude"}, {task: "two", agent: "codex"}}
	keep, notices, dropped := partitionRunnable(jobs, nil)
	if len(keep) != len(jobs) || dropped != 0 || notices != nil {
		t.Errorf("partition with nothing missing = %d kept, %d dropped, %v; want everything kept and nothing said",
			len(keep), dropped, notices)
	}
}
