package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/perch/internal/gitx"
	"github.com/jmwri/perch/internal/hooks"
	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/workspace"
)

// fanoutPreviewMsg offers the work found in a pane's output for the user to
// edit before anything is started.
type fanoutPreviewMsg struct {
	Type   string   `json:"type"`
	PaneID string   `json:"paneId"`
	Cwd    string   `json:"cwd"`
	IsRepo bool     `json:"isRepo"`
	Tasks  []string `json:"tasks"`
	// FromReply reports whether the tasks were read from what the agent said,
	// rather than scraped off the pane's screen.
	FromReply bool `json:"fromReply"`
	// Trusted reports whether this directory has already been trusted in
	// Claude Code, which is what makes it meaningful to offer the same for the
	// worktrees a fan-out creates.
	Trusted bool   `json:"trusted"`
	Project string `json:"project"`
}

// previewFanout reads a pane's recent output and proposes tasks from it.
func (s *Server) previewFanout(c *controlClient, paneID string) {
	type info struct {
		id  string
		cwd string
		src workspace.PlanSource
	}
	done := make(chan info, 1)
	s.do(func() {
		id := paneID
		if id == "" {
			if t := s.ws.CurrentTab(); t != nil {
				id = t.Focus
			}
		}
		cwd := s.ws.ActiveRoot()
		if p := s.ws.Pane(id); p != nil {
			cwd = p.Cwd
		}
		done <- info{id: id, cwd: cwd, src: s.ws.PlanSourceFor(id)}
	})
	// s.do drops the callback once the server is closing, so every reply from
	// the workspace goroutine has to be waited for with a way out. Without one
	// the receive never returns and takes its caller down with it.
	var in info
	select {
	case in = <-done:
	case <-s.closed:
		return
	}

	go func() {
		// Reading the transcript touches the disk, which is why it happens here
		// rather than on the goroutine that owns the workspace.
		tasks, fromReply := in.src.Tasks()
		msg := fanoutPreviewMsg{
			Type:      "fanoutPreview",
			PaneID:    in.id,
			Cwd:       in.cwd,
			Tasks:     tasks,
			FromReply: fromReply,
			IsRepo:    isRepoDir(in.cwd) || gitRoot(in.cwd) != "",
			Trusted:   session.IsTrusted(in.cwd),
			Project:   filepath.Base(in.cwd),
		}
		c.sendJSON(msg)
	}()
}

// gitRoot reports the repository containing dir, if any.
func gitRoot(dir string) string {
	if !gitx.Available() {
		return ""
	}
	root, err := gitx.Root(dir)
	if err != nil {
		return ""
	}
	return root
}

// runFanout starts one agent per task.
//
// Worktrees are created before anything touches the workspace, because git is
// slow and the workspace goroutine also serves every window's state.
func (s *Server) runFanout(c *controlClient, parent string, tasks []string, worktrees, split, trust bool) {
	go func() {
		if len(tasks) == 0 {
			c.notify(fanoutSummary(0, 0))
			return
		}

		// Resolve the parent's directory once, off the workspace goroutine.
		type start struct {
			cwd    string
			claude bool
		}
		done := make(chan start, 1)
		s.do(func() {
			id := parent
			if id == "" {
				if t := s.ws.CurrentTab(); t != nil {
					id = t.Focus
				}
			}
			cwd := s.ws.ActiveRoot()
			if p := s.ws.Pane(id); p != nil {
				cwd = p.Cwd
			}
			parent = id
			done <- start{cwd: cwd, claude: s.ws.ClaudeAvailable()}
		})
		var in start
		select {
		case in = <-done:
		case <-s.closed:
			return
		}
		// Asked once for the whole fan-out. Left to Spawn it is asked once per
		// task, and a missing CLI answers a dozen tasks with a dozen copies of
		// the same notice, each naming a task as though the task were at fault.
		if !in.claude {
			c.notify("the `claude` CLI was not found on PATH, so no agents can be started", true)
			return
		}
		baseCwd := in.cwd

		jobs, capped := planJobs(tasks, baseCwd)
		if capped {
			c.notify(fmt.Sprintf("stopped after %d agents; start the rest as a second fan-out", workspace.MaxTasks), true)
		}
		if len(jobs) == 0 {
			c.notify(fanoutSummary(0, 0))
			return
		}

		var repo string
		if worktrees {
			// Whether a worktree can be cut at all is a property of the
			// directory, not of any one task. Leaving it to PrepareWorktree
			// answers every task in the list with the same complaint about the
			// directory they all share.
			repo = gitRoot(baseCwd)
			switch {
			case !gitx.Available():
				c.notify("git is not installed, so no worktrees can be created", true)
				return
			case repo == "":
				c.notify(fmt.Sprintf("%s is not in a git repository, so no worktrees can be created", filepath.Base(baseCwd)), true)
				return
			}
			if text := preparingNotice(len(jobs)); text != "" {
				c.notify(text, false)
			}
			nameBranches(jobs, localBranches(repo))
			makeWorktrees(jobs, func(branch string) (string, error) {
				return s.ws.PrepareWorktree(baseCwd, branch)
			})
			if trust {
				inheritTrust(jobs, baseCwd, session.InheritTrust,
					func(text string) { c.notify(text, true) })
			}
		}

		started, failed := 0, 0
		for _, j := range jobs {
			if j.err != nil {
				c.notify(fmt.Sprintf("%s: %v", short(j.task), j.err), true)
				failed++
				continue
			}
			res := make(chan error, 1)
			s.do(func() {
				_, err := s.ws.Spawn(parent, workspace.SpawnOptions{
					Task:  j.task,
					Cwd:   j.cwd,
					Split: split,
					Kind:  session.KindClaude,
				})
				res <- err
			})
			var err error
			select {
			case err = <-res:
			case <-s.closed:
				return
			}
			if err != nil {
				c.notify(fmt.Sprintf("%s: %v", short(j.task), err), true)
				failed++
				discardWorktree(repo, baseCwd, j)
				continue
			}
			started++
		}

		c.notify(fanoutSummary(started, failed))
		s.Wake()
	}()
}

// planJobs turns the edited task list into the jobs a fan-out will run, and
// reports whether it stopped at the cap.
//
// The blank rows and the cap are settled here, before anything is created. The
// list arrives from a user who has been editing it, so it carries empty lines —
// and the cap has to count the tasks actually taken rather than positions in
// the slice, or a handful of empty rows stands in for agents that were never
// started. Every row that is left becomes an agent with a terminal of its own:
// the list the extractor proposed was capped, but nothing between there and
// here held the edited one to a size the machine can actually run.
func planJobs(tasks []string, cwd string) ([]*fanoutJob, bool) {
	jobs := make([]*fanoutJob, 0, len(tasks))
	for _, task := range tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		if len(jobs) >= workspace.MaxTasks {
			return jobs, true
		}
		jobs = append(jobs, &fanoutJob{task: task, cwd: cwd})
	}
	return jobs, false
}

// fanoutJob is one task of a fan-out, and where its agent will run.
type fanoutJob struct {
	task string
	cwd  string
	// branch is the branch the agent gets when it is given a worktree.
	branch string
	// err is why this task could not be prepared. It is reported when the
	// agents are started, so failures appear in the order of the plan rather
	// than in whatever order the preparation happened to finish.
	err error
}

// nameBranches gives each job a branch nothing else is using.
//
// Branch names are derived from the task text and then truncated, so two tasks
// that begin alike would otherwise land on the same branch — and PrepareWorktree
// reuses the worktree a branch already has, which would quietly put two agents
// in one checkout. taken is what the repository already has, from
// localBranches.
func nameBranches(jobs []*fanoutJob, taken map[string]bool) {
	used := map[string]bool{}
	for _, j := range jobs {
		j.branch = uniqueBranch(workspace.BranchNameFor(j.task), taken, used)
		used[j.branch] = true
	}
}

// localBranches is the set of branch names the repository already has, folded
// to lower case.
//
// Read in one go rather than asked about a candidate at a time: on Windows a
// git invocation is most of a tenth of a second of process start-up, so a
// dozen tasks spent 1.2 seconds asking twelve questions that one command
// answers in 0.35 — before the fan-out had created anything at all.
//
// The folding is not tidiness. git stores a loose ref as a file, so on Windows
// and macOS "agent/Fix" and "agent/fix" are the same ref, and `git worktree
// add -b` refuses the second with a lock error the user sees as a task that
// would not start. Where the filesystem does tell them apart, the only cost of
// treating them as one is a branch that gets a number on the end it did not
// strictly need.
func localBranches(repo string) map[string]bool {
	if repo == "" {
		return nil
	}
	branches, err := gitx.Branches(repo)
	if err != nil {
		return nil
	}
	taken := make(map[string]bool, len(branches))
	for _, b := range branches {
		taken[strings.ToLower(b.Name)] = true
	}
	return taken
}

// makeWorktrees creates every job's worktree, all at once.
//
// Writing out a working tree is far and away the slowest thing a fan-out does:
// eight of them one after another took 5.9 seconds on a small test repository
// against 1.0 second run together, and a real repository is much worse. One at
// a time that is a stretch of nothing happening before the first agent appears,
// with the last arriving long after the user has looked away.
//
// They are independent: separate directories, separate branches nameBranches
// has already made distinct, and one object store that is only read. What is
// not independent stays out of here — naming the branches asks git what already
// exists, and inheriting folder trust rewrites one shared configuration file.
func makeWorktrees(jobs []*fanoutJob, prepare func(branch string) (string, error)) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := prepare(j.branch)
			if err != nil {
				j.err = err
				return
			}
			j.cwd = path
		}()
	}
	wg.Wait()
}

// discardWorktree removes the worktree cut for an agent that then failed to
// start.
//
// Left behind, it is a branch and a directory holding a fresh copy of the
// project with nothing done in it — which is exactly what a worktree that an
// agent is working in looks like from the outside. The worktree list offers it
// as somewhere work is going on, and the only way to find out otherwise is to
// go and look.
//
// Removing it is safe in this one place because a fan-out never reuses a
// checkout: nameBranches only hands out branches the repository does not
// already have, so every worktree here was made moments ago and no agent has
// ever run in it. That is also why the removal is forced — there is nothing in
// it to lose, and a checkout can read as modified the instant it is made when
// the repository and the platform disagree about line endings.
func discardWorktree(repo, baseCwd string, j *fanoutJob) {
	if repo == "" || j.cwd == "" || j.cwd == baseCwd {
		return
	}
	_ = gitx.Remove(repo, j.cwd, true)
}

// fanoutSummary is the line a fan-out closes on, and whether it is a failure.
//
// A fan-out opens a screenful of panes, so it has to account for itself at the
// end. Each failure has already been reported on its own, but without a count
// here a task that never started reads as one the user simply lost track of
// among the ones that did — and a run that ends without saying anything at all
// reads as one that was accepted and quietly did the work somewhere. That last
// case is the one that was missing: when every task failed, the fan-out went
// silent at exactly the point it had the most to say.
func fanoutSummary(started, failed int) (string, bool) {
	switch {
	case started == 0 && failed == 0:
		return "no tasks to start", true
	case started == 0 && failed == 1:
		return "the agent could not be started", true
	case started == 0:
		return fmt.Sprintf("none of the %d agents could be started", failed), true
	case failed > 0:
		return fmt.Sprintf("started %d %s, %d could not be started", started, agents(started), failed), true
	default:
		return fmt.Sprintf("started %d %s", started, agents(started)), false
	}
}

// preparingNotice is what a fan-out says before it starts anything, or "" when
// there is nothing worth saying.
//
// Cutting a worktree writes out a whole working tree, so on a repository of any
// size a fan-out spends seconds in git before the first pane can exist. Until
// then there is nothing on screen at all: a button pressed, a dialog closed,
// and silence — which reads as a fan-out that did not take. One agent is quick
// enough not to need explaining; a dozen is not.
func preparingNotice(n int) string {
	if n < 2 {
		return ""
	}
	return fmt.Sprintf("preparing %d worktrees…", n)
}

// agents is "agent" or "agents", for a count that is read rather than parsed.
func agents(n int) string {
	if n == 1 {
		return "agent"
	}
	return "agents"
}

// inheritTrust carries the project's folder trust over to the worktrees.
//
// A brand new worktree is a directory Claude Code has never seen, so it would
// stop and ask whether the folder is trusted before doing any work — once per
// child. Carrying over the answer already given for the project they were cut
// from is what the user asked for by ticking the box.
//
// It happens here, after the worktrees exist and before the first agent does,
// rather than between starting one agent and the next. Claude keeps that answer
// in a single configuration file which it reads and writes whole, and so does
// every agent as it starts: interleaved with the spawning, the last of a dozen
// worktrees was being written while eleven agents were writing the same file
// from a copy each of them read before it. Done in one pass up front, this is
// the only writer there is.
//
// A failure stops the rest. It is a property of the configuration rather than
// of any one worktree, so carrying on would bury the fan-out's own messages
// under a dozen copies of the same complaint.
func inheritTrust(jobs []*fanoutJob, baseCwd string, inherit func(from, to string) error, notify func(string)) {
	for _, j := range jobs {
		if j.err != nil || j.cwd == "" || j.cwd == baseCwd {
			continue
		}
		if err := inherit(baseCwd, j.cwd); err != nil {
			notify("could not carry over folder trust: " + err.Error())
			return
		}
	}
}

// contextDeadline bounds how long a pane's SessionStart hook waits for its
// description. It is shorter than the hook's own timeout so the answer, when
// there is one, always arrives in time to be used.
const contextDeadline = 2 * time.Second

// installContextHandler answers a pane's SessionStart hook with a description
// of the pane its agent is running in.
//
// The reply is built on the goroutine that owns the workspace, like every other
// read of it. Claude Code is waiting on this hook, so the work done there is
// only a walk over state already in memory.
func (s *Server) installContextHandler() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	hookSrv.SetContextHandler(func(paneID string) string {
		out := make(chan string, 1)
		s.do(func() {
			c, ok := s.ws.PaneContext(paneID)
			if !ok {
				out <- ""
				return
			}
			out <- c.Render()
		})
		// Claude Code is held up until this hook answers, so a busy or wedged
		// workspace loop must not be able to stall a pane's startup: give up
		// and let the agent begin without knowing where it is.
		select {
		case text := <-out:
			return text
		case <-time.After(contextDeadline):
			return ""
		case <-s.closed:
			return ""
		}
	})
}

// errShuttingDown is what a spawn is answered with when the window it would
// have opened in is already going away.
var errShuttingDown = errors.New("the workspace is shutting down")

// installSpawnHandler lets an agent start helpers of its own by running
// `perch spawn` inside its pane.
func (s *Server) installSpawnHandler() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	hookSrv.SetSpawnHandler(func(req hooks.SpawnRequest) (hooks.SpawnResult, error) {
		// Work out where the child should run before touching the workspace.
		done := make(chan string, 1)
		s.do(func() {
			cwd := s.ws.ActiveRoot()
			if p := s.ws.Pane(req.Parent); p != nil {
				cwd = p.Cwd
			}
			done <- cwd
		})
		// The agent's `perch spawn` is blocked on this reply, so a
		// closing workspace has to answer it rather than leave the command
		// hanging in the pane forever.
		var cwd string
		select {
		case cwd = <-done:
		case <-s.closed:
			return hooks.SpawnResult{}, errShuttingDown
		}

		if req.Branch != "" {
			path, err := s.ws.PrepareWorktree(cwd, req.Branch)
			if err != nil {
				return hooks.SpawnResult{}, err
			}
			cwd = path
		}

		kind := session.KindClaude
		if req.Shell {
			kind = session.KindShell
		}

		type result struct {
			id  string
			err error
		}
		res := make(chan result, 1)
		s.do(func() {
			id, err := s.ws.Spawn(req.Parent, workspace.SpawnOptions{
				Task:  req.Task,
				Cwd:   cwd,
				Split: req.Split,
				Kind:  kind,
			})
			res <- result{id, err}
		})
		var r result
		select {
		case r = <-res:
		case <-s.closed:
			return hooks.SpawnResult{}, errShuttingDown
		}
		if r.err != nil {
			return hooks.SpawnResult{}, r.err
		}
		s.Wake()
		return hooks.SpawnResult{PaneID: r.id, Cwd: cwd}, nil
	})
}

// uniqueBranch returns base, or base-2, base-3 and so on, until it names a
// branch that the repository does not have and this fan-out has not already
// handed to a sibling.
func uniqueBranch(base string, taken, used map[string]bool) string {
	candidate := base
	for i := 2; ; i++ {
		if !used[candidate] && !taken[strings.ToLower(candidate)] {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

// short trims a task for a one-line message.
func short(task string) string {
	if r := []rune(task); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return task
}
