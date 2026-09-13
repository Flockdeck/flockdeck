package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
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
	// Agents is the catalog, read afresh each time the dialog opens so that a
	// hand-edited agents.json takes effect without a restart. Agent and Model
	// are what the run starts on before anybody chooses otherwise.
	Agents []fanoutAgentView `json:"agents,omitempty"`
	Agent  string            `json:"agent,omitempty"`
	Model  string            `json:"model,omitempty"`
	// Routing is the project's routing mode where it routes at all, and
	// Routes what it chose for each task, positional against Tasks and null
	// where a row is left alone. RouteNote says why nothing can be routed
	// from the model the run starts on, where that is so. All three are left
	// out while routing is off, and the dialog is then what it always was.
	Routing   string       `json:"routing,omitempty"`
	Routes    []*routeView `json:"routes,omitempty"`
	RouteNote string       `json:"routeNote,omitempty"`
}

// fanoutAgentView is one agent the dialog can offer, for the whole run or for
// a single row of it.
type fanoutAgentView struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Models []modelView `json:"models,omitempty"`
	// Default is the model this agent is asked for when nothing chooses one.
	Default string `json:"default,omitempty"`
	// Unavailable is why this agent cannot be started on this machine, and
	// Install is where to get it. An agent the machine does not have is
	// offered greyed rather than left out: somebody who has not installed
	// Codex should still learn that Flockdeck would run it.
	Unavailable string `json:"unavailable,omitempty"`
	Install     string `json:"install,omitempty"`
	// AskTrust is whether this agent asks if a folder is trusted before it
	// works in it. Carrying the project's answer over to the worktrees is
	// only offered for a run whose agents ask; for the rest it would do
	// nothing.
	AskTrust bool `json:"askTrust,omitempty"`
}

// fanoutCatalog lists the agents the dialog can offer, and the id of the one
// a run starts on.
//
// It asks whether each agent can be started, which is a search of PATH for
// every one that is not installed. The answers are kept for a few seconds and
// are refreshed only while the window is being redrawn, so the dialog opened
// after a quiet spell asks afresh -- eight searches took up to half a second
// on a Windows machine with an ordinary PATH. It is therefore called off the
// workspace goroutine, where every window's commands would otherwise wait
// behind it. Only the probing is: which agents there are, and which one a run
// starts on, are read on that goroutine and handed in, because the default
// depends on the active project, a field nothing else may read. The probing
// asks about each agent by its id, through AgentSpecByID, which reads nothing
// of the workspace's but the catalog.
func (s *Server) fanoutCatalog(specs []agent.Spec) []fanoutAgentView {
	out := make([]fanoutAgentView, 0, len(specs))
	for _, spec := range specs {
		if spec.Hidden {
			continue
		}
		view := fanoutAgentView{
			ID: spec.ID, Name: spec.Name, Models: modelViews(spec),
			Default: spec.DefaultModel, Install: spec.Install, AskTrust: spec.Caps.Trust,
		}
		if _, err := s.ws.AgentSpecByID(spec.ID); err != nil {
			view.Unavailable = err.Error()
		}
		out = append(out, view)
	}
	return out
}

// planTasks reads a pane's plan. It is a variable so a test can hold the
// preview between the workspace's answer and the probing that follows it.
var planTasks = func(src workspace.PlanSource) ([]string, bool) { return src.Tasks() }

// previewFanout reads a pane's recent output and proposes tasks from it.
func (s *Server) previewFanout(c *controlClient, paneID string) {
	type info struct {
		id    string
		cwd   string
		src   workspace.PlanSource
		specs []agent.Spec
		def   string
		root  string
		model string
	}
	in, ok := ask(s, func() info {
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
		// The default agent is the active project's, and the active project
		// is a field only this goroutine may read. Read from the preview's
		// own goroutine, it raced every project switch -- and a switch landing
		// in between offered the run to the other project's default.
		specs, def := s.ws.Agents()
		// The run starts on the project's own default model where its default
		// agent is the one the run is on: a project set to Sonnet fanned out
		// on whatever the CLI was set to.
		root, model := s.ws.ActiveRoot(), ""
		if d := s.ws.Catalog().DefaultsFor(root); d.Agent == def {
			model = d.Model
		}
		return info{id: id, cwd: cwd, src: s.ws.PlanSourceFor(id), specs: specs, def: def, root: root, model: model}
	})
	if !ok {
		return
	}

	go func() {
		defer s.survive("reading the pane's plan")
		// Reading the transcript touches the disk and asking about the agents
		// searches PATH, which is why both happen here rather than on the
		// goroutine that owns the workspace.
		tasks, fromReply := planTasks(in.src)
		agents, def := s.fanoutCatalog(in.specs), in.def
		msg := fanoutPreviewMsg{
			Type:      "fanoutPreview",
			PaneID:    in.id,
			Cwd:       in.cwd,
			Tasks:     tasks,
			FromReply: fromReply,
			IsRepo:    isRepoDir(in.cwd) || gitRoot(in.cwd) != "",
			Trusted:   session.IsTrusted(in.cwd),
			Project:   filepath.Base(in.cwd),
			Agents:    agents,
			Agent:     def,
			Model:     in.model,
		}
		current := in.model
		if current == "" {
			for _, a := range agents {
				if a.ID == def {
					current = a.Default
				}
			}
		}
		msg.Routes, msg.Routing, msg.RouteNote = routeRows(s.ws.Catalog(), in.root, def, current, tasks)
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

// fanoutRequest is what a window asks for when it turns a plan into agents.
//
// The agent and model chosen for the run are what every row takes unless its
// own line says otherwise. That is the point of the pair: the dialog offers
// one control for the whole fan-out and an override on each row, so twelve
// tasks can be split between two agents deliberately rather than by running
// two fan-outs and hoping they land side by side.
type fanoutRequest struct {
	Parent string
	Tasks  []string
	// Agent and Model are the run's choice. Empty is the default agent, and a
	// model the agent is left to pick for itself.
	Agent string
	Model string
	// TaskAgents and TaskModels are the per-row overrides, positional against
	// Tasks. They are parallel arrays rather than a list of objects so that
	// the tasks stay exactly where they have always been on the wire: a window
	// that knows nothing about agents still sends a fan-out this side reads.
	TaskAgents []string
	TaskModels []string
	// TaskRouted names, positional against Tasks, the routing rule that chose
	// a row's model, for a row that starts on the model routing chose; the
	// model itself is in TaskModels like any other. Overrides are the routed
	// choices the user changed before starting. Both only mark the panes and
	// feed the routing log: nothing here routes a row. A window from before
	// routing sends neither.
	TaskRouted []string
	Overrides  []routeOverride
	Worktrees  bool
	Split      bool
	Trust      bool
}

// overrideAt returns the value list holds for row i, or "" when it holds none.
// The overrides are as long as the window chose to make them, which need not
// be as long as the task list.
func overrideAt(list []string, i int) string {
	if i < len(list) {
		return list[i]
	}
	return ""
}

// fanout starts one agent per task.
//
// The children are gathered into a single tab, arranged as a grid: either the
// parent's tab, when the fan-out was asked to split into it, or one new tab of
// their own. A tab each was what this did before, and a dozen agents is a tab
// bar nobody can read — the fan-out that opens twelve of them is exactly the
// one where seeing them at once is the point.
//
// Worktrees are created before anything touches the workspace, because git is
// slow and the workspace goroutine also serves every window's state.
func (s *Server) fanout(c *controlClient, req fanoutRequest) {
	go func() {
		defer s.survive("fanning out")
		if len(req.Tasks) == 0 {
			c.notify(fanoutSummary(0, 0))
			return
		}

		// Resolve the parent's directory once, off the workspace goroutine.
		type start struct {
			parent string
			cwd    string
			tab    string
			// def is the agent a row naming none runs. Resolving it reads the
			// active project, which only this goroutine may, so it is read
			// here with the rest; see specOrDefault.
			def string
			// shown is the tab on screen as the run begins; see
			// revealFirstChild.
			shown string
		}
		in, ok := ask(s, func() start {
			id := req.Parent
			if id == "" {
				if t := s.ws.CurrentTab(); t != nil {
					id = t.Focus
				}
			}
			cwd := s.ws.ActiveRoot()
			if p := s.ws.Pane(id); p != nil {
				cwd = p.Cwd
			}
			// The tab to put the children in is the parent's own, and only
			// when the fan-out was asked to split into it. Reading it here
			// costs nothing: this is already the one visit to the workspace
			// goroutine that the whole run makes before it starts anything.
			tab := ""
			if req.Split {
				tab = s.ws.TabIDOf(id)
			}
			_, def := s.ws.Agents()
			return start{parent: id, cwd: cwd, tab: tab, def: def, shown: s.ws.ActiveTabID()}
		})
		if !ok {
			return
		}
		parent, baseCwd := in.parent, in.cwd

		jobs, capped := planJobs(req, baseCwd)
		if capped {
			c.notify(fmt.Sprintf("stopped after %d agents; start the rest as a second fan-out", workspace.MaxTasks), true)
		}
		if len(jobs) == 0 {
			c.notify(fanoutSummary(0, 0))
			return
		}
		jobs, failed, ok := s.runnable(c, jobs)
		if !ok {
			return
		}
		if len(jobs) == 0 {
			// Every row was dropped for want of its agent, and runnable has
			// just said so agent by agent. A summary after that counts the
			// same failures a second time -- and for a run on one agent it
			// would be a second line where there has only ever been one.
			return
		}

		var repo string
		if req.Worktrees {
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
			if !prepareWorktrees(c, repo, jobs) {
				return
			}
			if req.Trust {
				inheritTrust(jobsAskingTrust(jobs, s.specOrDefault(in.def)), baseCwd, session.InheritTrust,
					func(text string) { c.notify(text, true) })
			}
		}

		// The tab the children share. When the fan-out is not splitting into
		// the parent's, the first child to start makes one and the rest join
		// it — a tab cannot be created empty, so there is nothing to join
		// until an agent is actually running in it.
		tab := in.tab
		title := fanoutTabTitle(jobs)

		// Where the child landed, so the next one can be sent to the same tab.
		type spawned struct {
			id  string
			tab string
			err error
		}
		// The routed rows that started, and the pane each started in, for the
		// routing log.
		routed := map[*fanoutJob]string{}

		// failed is already counting the rows runnable dropped for want of
		// their agent, so this adds to it rather than starting again.
		started := 0
		for _, j := range jobs {
			if j.err != nil {
				c.notify(fmt.Sprintf("%s: %v", short(j.task), j.err), true)
				failed++
				continue
			}
			first := started == 0
			opts := workspace.SpawnOptions{
				Task:  j.task,
				Cwd:   j.cwd,
				Tab:   tab,
				Split: req.Split,
				Kind:  session.KindClaude,
				Agent: j.agent,
				Model: j.model,
				Title: title,
			}
			if j.routed != "" {
				opts.Routed, opts.RoutedFrom = j.routed, req.Model
			}
			r, ok := ask(s, func() spawned {
				id, err := s.ws.Spawn(parent, opts)
				if err == nil && first {
					s.revealFirstChild(id, in.shown)
				}
				return spawned{id: id, tab: s.ws.TabIDOf(id), err: err}
			})
			if !ok {
				return
			}
			if tab == "" {
				tab = r.tab
			}
			if r.err != nil {
				msg := fmt.Sprintf("%s: %v", short(j.task), r.err)
				if err := discardWorktree(repo, j, s.paneWorkingIn); err != nil {
					msg += fmt.Sprintf(" (%v)", err)
				}
				c.notify(msg, true)
				failed++
				continue
			}
			started++
			routed[j] = r.id
		}
		logRoutes(fanoutRouteLog(req, baseCwd, routed))

		c.notify(fanoutSummary(started, failed))
		s.Wake()
	}()
}

// revealFirstChild shows the person the first agent a fan-out started: its tab
// selected and its pane focused, so that what they type next goes to it. That
// is the tab the fan-out opened for its agents, or the tab it split into, where
// the focus moves from the pane the fan-out was read from to the first of the
// new ones. Left alone, a tab of a dozen agents opened unselected at the far
// end of the tab bar, and the person who had just asked for them had to go and
// find it.
//
// It does so only while the window still shows the tab the run began on.
// Cutting a dozen worktrees takes seconds, and somebody who has gone to another
// tab or project in the meantime is doing something there -- possibly typing
// into a terminal, whose keystrokes would carry on into an agent they had not
// chosen. The fan-out's tab still appears and its closing notice still counts
// what started; it just does not take the window from them.
//
// It runs on the workspace goroutine.
func (s *Server) revealFirstChild(id, shown string) {
	if s.ws.ActiveTabID() != shown {
		return
	}
	s.ws.RevealPane(id)
	s.wakeAsked()
}

// runnable keeps the jobs whose agent can actually be started here, reports
// how many were dropped, and reports whether the server is still open.
//
// Availability is asked once per agent, not once per job: left to Spawn, a
// missing CLI answered a dozen tasks with a dozen copies of the same notice,
// each naming a task as though the task were at fault. Nor is it asked once
// for the whole run any more -- a fan-out divided between two agents starts
// the rows whose agent is installed, and says which agent the rest were
// waiting on.
//
// It happens before a single worktree is cut, so a row that cannot run costs
// nothing but the line that explains it.
func (s *Server) runnable(c *controlClient, jobs []*fanoutJob) ([]*fanoutJob, int, bool) {
	ids := map[string]bool{}
	for _, j := range jobs {
		ids[j.agent] = true
	}
	bad, ok := ask(s, func() map[string]error {
		bad := map[string]error{}
		for id := range ids {
			if _, err := s.ws.AgentSpec(id); err != nil {
				bad[id] = err
			}
		}
		return bad
	})
	if !ok {
		return nil, 0, false
	}
	keep, notices, dropped := partitionRunnable(jobs, bad)
	for _, text := range notices {
		c.notify(text, true)
	}
	return keep, dropped, true
}

// partitionRunnable splits the jobs into the ones whose agent can be
// started here and the ones that cannot, and writes what to say about each
// agent that cannot.
//
// One notice per agent, in the order the agents first appear in the plan: the
// map they arrive in has no order, and two missing agents named in a different
// order each time are two notices nobody can match against the list they were
// looking at.
func partitionRunnable(jobs []*fanoutJob, bad map[string]error) ([]*fanoutJob, []string, int) {
	if len(bad) == 0 {
		return jobs, nil, 0
	}
	counts := map[string]int{}
	for _, j := range jobs {
		if bad[j.agent] != nil {
			counts[j.agent]++
		}
	}

	keep := make([]*fanoutJob, 0, len(jobs))
	var notices []string
	said := map[string]bool{}
	dropped := 0
	for _, j := range jobs {
		err := bad[j.agent]
		if err == nil {
			keep = append(keep, j)
			continue
		}
		dropped++
		if said[j.agent] {
			continue
		}
		said[j.agent] = true
		if len(bad) == 1 && counts[j.agent] == len(jobs) {
			// Word for word what a fan-out has always said when the CLI was
			// missing. With one agent for the whole run, nothing has changed.
			notices = append(notices, fmt.Sprintf("%v, so no agents can be started", err))
			continue
		}
		n := counts[j.agent]
		notices = append(notices, fmt.Sprintf("%v, so %d %s not started", err, n, tasksWere(n)))
	}
	return keep, notices, dropped
}

// tasksWere is "task was" or "tasks were", for a count that is read rather than
// parsed.
func tasksWere(n int) string {
	if n == 1 {
		return "task was"
	}
	return "tasks were"
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
func planJobs(req fanoutRequest, cwd string) ([]*fanoutJob, bool) {
	jobs := make([]*fanoutJob, 0, len(req.Tasks))
	for i, task := range req.Tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		if len(jobs) >= workspace.MaxTasks {
			return jobs, true
		}
		j := &fanoutJob{task: task, cwd: cwd, agent: req.Agent, model: req.Model}
		// A row that names its own agent brings its own model with it, and
		// takes no model at all when it named none. The model chosen for the
		// run belongs to the agent chosen for the run: carrying "sonnet" over
		// to the one row handed to Codex asks Codex for a model it has never
		// heard of, and the pane dies on the spot.
		if a := overrideAt(req.TaskAgents, i); a != "" {
			j.agent, j.model = a, overrideAt(req.TaskModels, i)
		} else if m := overrideAt(req.TaskModels, i); m != "" {
			j.model = m
		}
		j.routed = overrideAt(req.TaskRouted, i)
		jobs = append(jobs, j)
	}
	return jobs, false
}

// fanoutJob is one task of a fan-out, and where its agent will run.
type fanoutJob struct {
	task string
	cwd  string
	// origin is the folder of the checkout the fan-out started from that cwd
	// stands for, once cwd is in a worktree: the same folder, in the checkout
	// the worktree was cut from. Folder trust is carried from there.
	origin string
	// agent and model are the agent this task's pane runs and the model it is
	// asked for: the run's choice, or the row's own override of it.
	agent string
	model string
	// routed names the routing rule that chose model, for a row the dialog
	// started on the model routing pre-filled.
	routed string
	// branch is the branch the agent gets when it is given a worktree, and
	// path is where that worktree goes.
	branch string
	path   string
	// created is whether this run made the job's worktree and its branch,
	// which is what makes them this run's to discard.
	created bool
	// err is why this task could not be prepared. It is reported when the
	// agents are started, so failures appear in the order of the plan rather
	// than in whatever order the preparation happened to finish.
	err error
}

// prepareWorktrees gives every job a branch and a worktree of its own, and
// reports whether the fan-out can go on.
//
// Naming the branches, choosing where their worktrees go and creating them is
// one step, held against everything else that makes worktrees in this
// repository; see lockRepo. Every choice is made against what the repository
// has at that moment, and a `flockdeck spawn --worktree` or a second fan-out
// landing in between could make the same one.
//
// The branches are made here and never borrowed. A name somebody took in the
// meantime fails its task rather than handing it their checkout -- which, when
// its agent then failed to start, this run went on to force-delete, work and
// all. So every worktree a job ends up with is one this run made, and only
// those are ever discarded.
func prepareWorktrees(c *controlClient, repo string, jobs []*fanoutJob) bool {
	defer lockRepo(repo)()
	taken, err := localBranches(repo)
	if err != nil {
		c.notify(fmt.Sprintf("could not read the branches of %s, so no worktrees were created: %v", filepath.Base(repo), err), true)
		return false
	}
	nameBranches(jobs, taken)
	placeWorktrees(repo, jobs)
	from := make(map[*fanoutJob]string, len(jobs))
	for _, j := range jobs {
		from[j] = j.cwd
	}
	makeWorktrees(jobs, func(branch, path string) error {
		return gitx.AddNewBranch(repo, path, branch)
	})
	for _, j := range jobs {
		if j.created {
			j.cwd, j.origin = sameFolderIn(repo, from[j], j.path)
		}
	}
	return true
}

// sameFolderIn returns where in the worktree at wt an agent fanned out from
// base works, and the folder of the checkout at repo that stands for.
//
// That is the same folder base is of its checkout, where the worktree has it.
// An agent planning from repo/sub was planning work in repo/sub, and every
// child it started worked at the top of its worktree instead -- and was given
// trust there, carried over from repo/sub, for a whole checkout nobody had said
// to trust. A folder the worktree does not have, one git does not track, leaves
// the child at the top, standing for the top of the checkout.
func sameFolderIn(repo, base, wt string) (cwd, origin string) {
	rel, err := filepath.Rel(repo, base)
	switch {
	case err != nil, filepath.IsAbs(rel), rel == "..", strings.HasPrefix(rel, ".."+string(filepath.Separator)):
		return wt, repo
	case rel == ".":
		return wt, base
	}
	if fi, err := os.Stat(filepath.Join(wt, rel)); err != nil || !fi.IsDir() {
		return wt, repo
	}
	return filepath.Join(wt, rel), base
}

// nameBranches gives each job a branch nothing else is using.
//
// Branch names are derived from the task text and then truncated, so two tasks
// that begin alike would otherwise land on the same branch, and a branch the
// repository already has is refused rather than made. taken is what the
// repository already has, from localBranches.
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
//
// A failure to read them is returned rather than taken for an empty list. Read
// as nothing, every name looked free, and each task was handed whichever
// existing branch its name matched -- and the worktree already checked out on
// it.
func localBranches(repo string) (map[string]bool, error) {
	if repo == "" {
		return nil, nil
	}
	branches, err := gitx.Branches(repo)
	if err != nil {
		return nil, err
	}
	taken := make(map[string]bool, len(branches))
	for _, b := range branches {
		taken[strings.ToLower(b.Name)] = true
	}
	return taken, nil
}

// placeWorktrees chooses where each job's worktree goes, all in one pass so
// that no two are given the same directory; see gitx.WorktreePaths.
func placeWorktrees(repo string, jobs []*fanoutJob) {
	branches := make([]string, len(jobs))
	for i, j := range jobs {
		branches[i] = j.branch
	}
	for i, path := range gitx.WorktreePaths(repo, branches) {
		jobs[i].path = path
	}
}

// makeWorktrees creates every job's worktree, all at once.
//
// Writing out a working tree is far and away the slowest thing a fan-out does:
// eight of them one after another took 5.9 seconds on a small test repository
// against 1.0 second run together, and a real repository is much worse. One at
// a time that is a stretch of nothing happening before the first agent appears,
// with the last arriving long after the user has looked away.
//
// They are independent: separate directories placeWorktrees has already made
// distinct, separate branches nameBranches has, and one object store that is
// only read. What is not independent stays out of here — naming the branches
// and choosing the directories ask git and the disk what already exists, and
// inheriting folder trust rewrites one shared configuration file.
//
// create makes a job's worktree at its path on its new branch. A job it
// succeeds for works there, and the worktree is marked as this run's own.
func makeWorktrees(jobs []*fanoutJob, create func(branch, path string) error) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := create(j.branch, j.path); err != nil {
				j.err = err
				return
			}
			j.cwd, j.created = j.path, true
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
// Only a worktree this run made is removed. It used to be assumed that every
// one was, since nameBranches hands out only names the repository does not
// have -- but the worktree for a name was reused wherever one existed, and a
// name could be taken between the choosing and the making, or chosen blind
// when the branch list could not be read. A failed spawn then force-deleted
// somebody else's checkout, uncommitted work and all. Now a job's worktree is
// either made by this run or not given to it at all; see prepareWorktrees.
//
// Nor is one removed while a pane is working in it. A `flockdeck spawn
// --worktree` on the same branch reuses the checkout, and holds the same lock
// while it starts its helper, so by the time the lock is had here the helper
// is either in the list of panes or not in the checkout.
//
// With both settled, the removal is forced — there is nothing in it to lose,
// and a checkout can read as modified the instant it is made when the
// repository and the platform disagree about line endings.
//
// Its failure is returned rather than dropped. A freshly written checkout can
// be held for a moment on Windows by whatever scans new files, and one left
// behind that way is exactly the stray worktree this exists to prevent -- so
// it is said, beside the failure that caused it, rather than found later.
//
// working reports whether a pane is working in a path, and whether that could
// be told at all.
func discardWorktree(repo string, j *fanoutJob, working func(path string) (bool, bool)) error {
	if repo == "" || !j.created {
		return nil
	}
	defer lockRepo(repo)()
	// The worktree is j.path. j.cwd is the folder in it the agent was to work
	// in, which is a subfolder for a fan-out started from one.
	busy, known := working(j.path)
	switch {
	case !known:
		return fmt.Errorf("its worktree %s was kept: whether a pane is working in it could not be told", filepath.Base(j.path))
	case busy:
		return nil
	}
	if err := gitx.Remove(repo, j.path, true); err != nil {
		return fmt.Errorf("its worktree %s could not be removed: %w", filepath.Base(j.path), err)
	}
	// The branch goes with it. `git worktree add -b` made it for this job,
	// so it is this run's as much as the directory was, and left behind it
	// is half of the stray this is here to prevent: a branch with nothing on
	// it, offered in every branch picker as though somebody had started it.
	if err := gitx.DeleteBranch(repo, j.branch); err != nil {
		return fmt.Errorf("its branch %s could not be deleted: %w", j.branch, err)
	}
	return nil
}

// paneWorkingIn reports whether any pane is working in path, and whether that
// could be told at all.
func (s *Server) paneWorkingIn(path string) (bool, bool) {
	counts, ok := s.panesPerPath([]string{path})
	return counts[path] > 0, ok
}

// lockRepo holds back everything else that makes worktrees in the repository
// containing dir -- another fan-out, or a `flockdeck spawn --worktree` -- until
// the function it returns is called.
//
// Fan-outs run on goroutines of their own, two windows can start one each, and
// an agent can spawn a helper into a worktree at any moment. Each of those
// asks the repository what is free and then takes it, which is two agents in
// one checkout when two of them ask at once.
//
// The lock is the repository's, not the directory's: a pane inside a linked
// worktree asks from there, and has to wait on the same lock as a fan-out
// started from the main checkout.
func lockRepo(dir string) func() {
	key := dir
	if common, err := gitx.CommonDir(dir); err == nil {
		key = common
	}
	key = filepath.Clean(key)
	if foldPathCase {
		key = strings.ToLower(key)
	}
	repoLocks.Lock()
	mu := repoLocks.held[key]
	if mu == nil {
		mu = &sync.Mutex{}
		repoLocks.held[key] = mu
	}
	repoLocks.Unlock()
	mu.Lock()
	return mu.Unlock
}

// repoLocks is lockRepo's lock for each repository, by its git directory.
var repoLocks = struct {
	sync.Mutex
	held map[string]*sync.Mutex
}{held: map[string]*sync.Mutex{}}

// fanoutTabTitle names the tab a fan-out's children share, or "" to let the
// tab be named the way any other spawned pane's is.
//
// One task is one agent, and its tab is named after the work like every other.
// Several share a tab that is about the fan-out rather than about any one of
// them: named after the first task, eleven agents would sit under a title
// describing the twelfth.
func fanoutTabTitle(jobs []*fanoutJob) string {
	if len(jobs) < 2 {
		return ""
	}
	return "Fan out"
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
// The answer is carried from the folder each child's stands for, its origin,
// which is baseCwd itself unless the child had to work somewhere else in its
// worktree; see sameFolderIn. Trust given to one folder of a repository says
// nothing about the rest of it.
//
// A failure stops the rest. It is a property of the configuration rather than
// of any one worktree, so carrying on would bury the fan-out's own messages
// under a dozen copies of the same complaint.
func inheritTrust(jobs []*fanoutJob, baseCwd string, inherit func(from, to string) error, notify func(string)) {
	trustWrites.Lock()
	defer trustWrites.Unlock()
	for _, j := range jobs {
		if j.err != nil || j.cwd == "" || j.cwd == baseCwd {
			continue
		}
		from := j.origin
		if from == "" {
			from = baseCwd
		}
		if err := inherit(from, j.cwd); err != nil {
			notify("could not carry over folder trust: " + err.Error())
			return
		}
	}
}

// trustWrites keeps two fan-outs' trust passes -- started from two windows at
// once -- from each reading Claude Code's configuration, adding its own
// worktrees and writing the whole file back without the other's. The session
// package writes that file through one fixed temporary name, too, so two
// passes side by side could also rename each other's half-written copy into
// place, over a file that holds all of Claude Code's settings.
var trustWrites sync.Mutex

// specOrDefault looks up a fan-out row's agent away from the workspace
// goroutine. A row on the run's default names no agent, and Workspace.AgentSpec
// resolves an empty id from the active project -- a field only that goroutine
// may read, written there on every project switch. So the default is read there
// with the rest of the run's facts and filled in here, and the lookup is
// AgentSpecByID, which takes only a real id and reads nothing of the
// workspace's but the catalog. AgentSpec itself read the active project for
// every id, empty or not, so asking it from here raced every switch.
func (s *Server) specOrDefault(def string) func(id string) (agent.Spec, error) {
	return func(id string) (agent.Spec, error) {
		if id == "" {
			id = def
		}
		return s.ws.AgentSpecByID(id)
	}
}

// jobsAskingTrust keeps the jobs whose agent asks whether a folder is trusted.
//
// Only those have an answer to carry over, and carrying it means writing into
// Claude Code's own configuration, the one agent whose question Flockdeck knows
// how to answer. A fan-out whose rows run Codex was having trust recorded in
// Claude's file for the worktrees Codex would work in -- answering a question
// nobody asked, in a file that is not Codex's. This is what InheritTrustFor
// does for one spec, applied to a run whose rows may each be a different one.
func jobsAskingTrust(jobs []*fanoutJob, spec func(id string) (agent.Spec, error)) []*fanoutJob {
	var out []*fanoutJob
	for _, j := range jobs {
		if sp, err := spec(j.agent); err == nil && sp.Caps.Trust {
			out = append(out, j)
		}
	}
	return out
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
		// Claude Code is held up until this hook answers, so a busy or wedged
		// workspace loop must not be able to stall a pane's startup: give up
		// and let the agent begin without knowing where it is.
		//
		// One budget covers handing the question over and getting the answer,
		// as it does for the health endpoint. s.do waits without a deadline
		// for room in the queue in front of the workspace, and that queue
		// fills exactly when the workspace is slow: a deadline started after
		// it bounded nothing, and the hook gave up first.
		deadline := time.After(contextDeadline)
		select {
		case s.cmds <- func() {
			c, ok := s.ws.PaneContext(paneID)
			if !ok {
				out <- ""
				return
			}
			out <- c.Render()
		}:
		case <-deadline:
			return ""
		case <-s.closed:
			return ""
		}
		select {
		case text := <-out:
			return text
		case <-deadline:
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
// `flockdeck spawn` inside its pane.
func (s *Server) installSpawnHandler() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	hookSrv.SetSpawnHandler(func(req hooks.SpawnRequest) (hooks.SpawnResult, error) {
		// Work out where the child should run before touching the workspace,
		// and whether its agent can be started here at all. Spawn would say so
		// itself, but only after the worktree below had been cut: refused
		// then, the request left a new branch and a checkout with nothing in
		// it, which looks exactly like one an agent is working in.
		type start struct {
			cwd string
			err error
		}
		in, ok := ask(s, func() start {
			cwd := s.ws.ActiveRoot()
			if p := s.ws.Pane(req.Parent); p != nil {
				cwd = p.Cwd
			}
			var err error
			if !req.Shell {
				_, err = s.ws.AgentSpec(req.Agent)
			}
			return start{cwd, err}
		})
		// The agent's `flockdeck spawn` is blocked on this reply, so a
		// closing workspace has to answer it rather than leave the command
		// hanging in the pane forever.
		if !ok {
			return hooks.SpawnResult{}, errShuttingDown
		}
		if in.err != nil {
			return hooks.SpawnResult{}, in.err
		}
		cwd := in.cwd

		if req.Branch != "" {
			// Held until the helper is running in the worktree, which may be
			// one a fan-out has just made. A fan-out whose own agent there
			// fails to start discards the worktree under the same lock, and
			// has to find this helper in it when it does; see discardWorktree.
			defer lockRepo(cwd)()
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
		r, ok := ask(s, func() result {
			id, err := s.ws.Spawn(req.Parent, workspace.SpawnOptions{
				Task:  req.Task,
				Cwd:   cwd,
				Split: req.Split,
				Kind:  kind,
				Agent: req.Agent,
				Model: req.Model,
			})
			return result{id, err}
		})
		if !ok {
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
