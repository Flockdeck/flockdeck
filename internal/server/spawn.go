package server

import (
	"context"
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
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// agentChoiceView is one agent the dialog can offer, for the whole run or for
// a single row of it.
type agentChoiceView struct {
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

// agentChoices lists the agents the dialog can offer, and the id of the one
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
func (s *Server) agentChoices(specs []agent.Spec) []agentChoiceView {
	out := make([]agentChoiceView, 0, len(specs))
	for _, spec := range specs {
		if spec.Hidden {
			continue
		}
		view := agentChoiceView{
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

// startAgentRateLimit and startAgentRateWindow bound how often one window
// may call startAgent -- see controlClient.startAgentLimit.
const (
	startAgentRateLimit  = 5
	startAgentRateWindow = 10 * time.Second
)

// startAgent starts one fresh agent in an already open project and answers
// the window that asked for it with the new pane's id, so it can open that
// pane's chat straight away -- what lets somebody start an agent from their
// phone and land in it, rather than having to go and find it on the desk.
//
// Unlike newTab, root need not be the active project: a phone
// names whichever open project it is looking at, which the desk may be
// showing something else entirely. And unlike a spawned helper, the
// pane this starts is never one -- it carries no Parent -- so it
// notifies its user exactly as one opened by hand would; see
// workspace.StartAgent.
//
// A window that is not reached through the relay can do everything this does
// through newTab already (see control.go's command dispatch), so this adds no
// power a window at the desk did not already have -- only a way to reach it
// without the desk.
//
// It is rate-limited per window (startAgentRateLimit within
// startAgentRateWindow): this starts one agent -- and, with worktree:true,
// cuts one worktree -- per call, with nothing else to stop a window calling it as fast as it can
// send frames.
func (s *Server) startAgent(c *controlClient, cmd command) {
	if !c.startAgentLimit.allow(startAgentRateLimit, startAgentRateWindow) {
		c.notify("Too many agents started too quickly -- wait a moment and try again", true)
		return
	}
	task := strings.TrimSpace(cmd.Task)
	if task == "" {
		c.notify("A task is required to start an agent", true)
		return
	}
	// Settled before anything as slow as a worktree is cut, so a bad root or
	// an agent that does not exist is refused at once rather than after a
	// wait that was always going to end in the same refusal.
	type facts struct {
		root, agent, model string
		err                error
	}
	f, ok := ask(s, func() facts {
		agentID, model := cmd.Agent, cmd.Model
		if cmd.Role != "" {
			// A window is not a pane in a role, so no spawn rights limit it;
			// the role must still exist and an agent is not a shell.
			if cmd.Kind == "shell" {
				return facts{err: errRoleWithShell}
			}
			defaultAgent, _ := s.ws.SpawnTarget("", cmd.Root, "", "")
			var err error
			agentID, model, err = chooseInRole(s.ws.Catalog(), "", cmd.Role, agentID, model, defaultAgent)
			if err != nil {
				return facts{err: err}
			}
		}
		root, _, err := s.ws.ValidateStartAgent(cmd.Root, agentID, model)
		return facts{root: root, agent: agentID, model: model, err: err}
	})
	if !ok {
		return
	}
	if f.err != nil {
		c.notify(f.err.Error(), true)
		return
	}
	root := f.root

	go func() {
		defer s.surviveFor(c, "starting an agent")
		// job carries the branch and worktree StartAgent should run in, and
		// nothing else: it is the same shape a spawned helper's worktree has,
		// reused here for its naming rather than duplicated.
		job := &worktreeJob{task: task, cwd: root}
		var repo string
		if cmd.Worktree {
			repo = gitRoot(root)
			switch {
			case !gitx.Available():
				c.notify("Git is not installed, so no worktree can be created", true)
				return
			case repo == "":
				c.notify(fmt.Sprintf("%s is not in a git repository, so no worktree can be created", filepath.Base(root)), true)
				return
			}
			if !prepareWorktrees(c, repo, []*worktreeJob{job}) {
				return
			}
			if job.err != nil {
				c.notify(job.err.Error(), true)
				return
			}
		}

		type spawned struct {
			id  string
			err error
		}
		r, ok := ask(s, func() spawned {
			id, err := s.ws.StartAgentAs(root, f.agent, f.model, job.cwd, task, cmd.Role)
			return spawned{id: id, err: err}
		})
		if !ok {
			return
		}
		if r.err != nil {
			msg := r.err.Error()
			if err := discardWorktree(repo, job, s.paneWorkingIn); err != nil {
				msg += fmt.Sprintf(" (%v)", err)
			}
			c.notify(msg, true)
			return
		}
		c.sendJSON(map[string]any{"type": "agentStarted", "paneId": r.id, "id": cmd.ID})
		s.Wake()
	}()
}

// worktreeJob is one agent start that gets a worktree of its own, and where
// its agent will run.
type worktreeJob struct {
	task string
	cwd  string
	// origin is the folder of the checkout the start came from that cwd
	// stands for, once cwd is in a worktree: the same folder, in the checkout
	// the worktree was cut from. Folder trust is carried from there.
	origin string
	// agent and model are the agent this task's pane runs and the model it is
	// asked for: the run's choice, or the row's own override of it.
	agent string
	model string
	// routed names the routing rule that chose model, where one did.
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
// reports whether the start can go on.
//
// Naming the branches, choosing where their worktrees go and creating them is
// one step, held against everything else that makes worktrees in this
// repository; see lockRepo. Every choice is made against what the repository
// has at that moment, and a `flockdeck spawn --worktree` or a second start
// landing in between could make the same one.
//
// The branches are made here and never borrowed. A name somebody took in the
// meantime fails its task rather than handing it their checkout -- which, when
// its agent then failed to start, this run went on to force-delete, work and
// all. So every worktree a job ends up with is one this run made, and only
// those are ever discarded.
func prepareWorktrees(c *controlClient, repo string, jobs []*worktreeJob) bool {
	defer lockRepo(repo)()
	taken, err := localBranches(repo)
	if err != nil {
		c.notify(fmt.Sprintf("Could not read the branches of %s, so no worktrees were created: %v", filepath.Base(repo), err), true)
		return false
	}
	nameBranches(jobs, taken)
	placeWorktrees(repo, jobs)
	from := make(map[*worktreeJob]string, len(jobs))
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
func nameBranches(jobs []*worktreeJob, taken map[string]bool) {
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
// answers in 0.35 — before anything had been created at all.
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
func placeWorktrees(repo string, jobs []*worktreeJob) {
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
// Writing out a working tree is far and away the slowest thing starting several agents at once does:
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
func makeWorktrees(jobs []*worktreeJob, create func(branch, path string) error) {
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
func discardWorktree(repo string, j *worktreeJob, working func(path string) (bool, bool)) error {
	if repo == "" || !j.created {
		return nil
	}
	defer lockRepo(repo)()
	// The worktree is j.path. j.cwd is the folder in it the agent was to work
	// in, which is a subfolder for a start made from one.
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
// containing dir -- another start from the window, or a `flockdeck spawn --worktree` -- until
// the function it returns is called.
//
// Starts run on goroutines of their own, two windows can make one each, and
// an agent can spawn a helper into a worktree at any moment. Each of those
// asks the repository what is free and then takes it, which is two agents in
// one checkout when two of them ask at once.
//
// The lock is the repository's, not the directory's: a pane inside a linked
// worktree asks from there, and has to wait on the same lock as a start
// from the main checkout.
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

// errRoleWithShell is the refusal for a role asked of a shell.
var errRoleWithShell = errors.New("a role is for an agent, and -shell starts a shell")

// applyRole checks the role a spawn asks for and folds the role's agent and
// model into the request where it named none of its own. It must run on the
// workspace goroutine.
//
// The role must exist, and the pane asking must be allowed to start it: a pane
// in a role may start only the roles that role lists, and may start an agent
// with no role only if its role is Plain. A pane with no role is not limited.
// A request that names a parent pane that is not open is refused, since its
// role cannot be known; one with no parent comes from outside any pane.
func (s *Server) applyRole(req *hooks.SpawnRequest) error {
	if req.Role != "" && req.Shell {
		return errRoleWithShell
	}
	cat := s.ws.Catalog()
	parentRole := ""
	if req.Parent != "" {
		p := s.ws.Pane(req.Parent)
		if p == nil {
			return fmt.Errorf("the pane %q that asked is not open, so its role cannot be checked", req.Parent)
		}
		parentRole = p.Role
	}
	if req.Role == "" {
		if req.Shell {
			return nil
		}
		return cat.MayStart(parentRole, "")
	}
	defaultAgent, _ := s.ws.SpawnTarget(req.Parent, "", "", "")
	var err error
	req.Agent, req.Model, err = chooseInRole(cat, parentRole, req.Role, req.Agent, req.Model, defaultAgent)
	return err
}

// chooseInRole checks a start in the role roleID by a pane in parentRole ("" for
// none) and returns the agent and model it gets: the ones asked for, with the
// role's filled in where neither was named. It is what `flockdeck spawn -role`
// and the window's own role start share, so the two cannot differ on which
// roles exist, who may start them or which agent they get.
func chooseInRole(cat *agent.Catalog, parentRole, roleID, agentID, model, defaultAgent string) (string, string, error) {
	role, err := cat.CheckRole(roleID)
	if err != nil {
		return "", "", err
	}
	if err := cat.MayStart(parentRole, role.ID); err != nil {
		return "", "", err
	}
	agentID, model = cat.Choose(role, agentID, model, defaultAgent)
	return agentID, model, nil
}

// errShuttingDown is what a spawn is answered with when the window it would
// have opened in is already going away.
var errShuttingDown = errors.New("the workspace is shutting down")

// installSpawnHandler lets an agent start helpers of its own by running
// `flockdeck spawn` inside its pane.
func (s *Server) installSpawnHandler() {
	// Trouble with a baton's housekeeping goes where the server's other trouble goes.
	workspace.SetBatonLogf(func(format string, args ...any) { logf(format, args...) })
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
		var tabErr error
		if req.Tab, tabErr = hooks.CleanTabName(req.Tab); tabErr != nil {
			return hooks.SpawnResult{}, tabErr
		}
		in, ok := ask(s, func() start {
			cwd := s.ws.ActiveRoot()
			if p := s.ws.Pane(req.Parent); p != nil {
				cwd = p.Cwd
			}
			// The role is folded into the agent and model first, so that what
			// it names is checked like anything the caller named.
			err := s.applyRole(&req)
			// Before a worktree is cut or a baton asked about: a start that
			// would be refused for want of room must leave nothing behind.
			if err == nil {
				err = s.ws.HelperRoom(req.Parent)
			}
			if err == nil && !req.Shell {
				_, err = s.ws.AgentSpec(req.Agent)
			}
			if err == nil && req.Record {
				err = recordSpawnRefusal(req.Shell, store.LoadPrefs().RecordingAcknowledged)
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

		// Resolved before a worktree is cut, so a reference that does not
		// resolve is refused without leaving a checkout behind.
		var handoff *handoff
		if req.Baton != "" {
			if req.Shell {
				return hooks.SpawnResult{}, errors.New("a baton starts an agent, and -shell does not")
			}
			var err error
			if handoff, err = s.resolveBaton(req.Parent, req.Baton); err != nil {
				return hooks.SpawnResult{}, err
			}
		}

		// A helper asked for neither --agent nor --model can be routed, only
		// ever in "auto" mode -- see routeSpawnChoice -- and decided here,
		// before the worktree below, on the project the helper is starting
		// in rather than on whatever path it ends up cut into.
		var routed route.Decision
		var baseAgent, baseModel string
		if !req.Shell && req.Agent == "" && req.Model == "" {
			routed, baseAgent, baseModel = routeSpawnChoice(s.ws.Catalog(), cwd, req.Task)
		}

		// A baton goes to the agent it starts. When that is a different company the
		// agent has to ask for it with -baton-send-elsewhere, and asking is all it
		// does: the user is shown a notice in the window and has to allow it there.
		// That stops a mistake and a request nobody saw. It does not stop a hostile
		// process running as the same user, which can do what the window does (see
		// approval.go). Asked before a worktree is cut.
		// cleanup says what became of a worktree made for a helper that did not start,
		// and is nil when none was made.
		cleanup := func(cause error) error { return cause }
		var approved *workspace.ApprovedTarget
		if handoff != nil {
			// Where Spawn will be given to run the helper, which with -worktree is the
			// worktree and not the folder the agent is in: the project, and so the
			// default agent, can differ. It is worked out, not made.
			spawnCwd := cwd
			if req.Branch != "" {
				spawnCwd = s.ws.PlannedWorktree(cwd, req.Branch)
			}
			// The agent and model Spawn will be given, routing included.
			agentID, modelID := req.Agent, req.Model
			if routed.Routed {
				modelID = routed.Model
				if routed.Agent != "" {
					agentID = routed.Agent
				}
			}
			type verdict struct {
				change   string
				info     approvalInfo
				approved *workspace.ApprovedTarget
			}
			v, ok := ask(s, func() verdict {
				change, info, ap := s.batonChange(handoff, req.Parent, spawnCwd, cwd, req.Branch, agentID, modelID, req.Task)
				return verdict{change, info, ap}
			})
			if !ok {
				return hooks.SpawnResult{}, errShuttingDown
			}
			// Every baton spawn carries what was worked out into Spawn, which works it out
			// again where the helper really is. Whether the user was asked is said in it.
			approved = v.approved
			if change := v.change; change != "" {
				if !req.BatonElsewhere {
					return hooks.SpawnResult{}, errors.New(batonRefusal(change))
				}
				ctx := req.Ctx
				if ctx == nil {
					ctx = context.Background()
				}
				if err := s.approveElsewhere(ctx, v.info); err != nil {
					return hooks.SpawnResult{}, err
				}
				// What the user was asked about is what must run.
				if approved != nil {
					approved.ApprovalRequired = true
				}
			}
			spawnApproved(approved)
		}

		if req.Branch != "" {
			// Held until the helper is running in the worktree, which may be
			// one a start from the window has just made. One whose own agent
			// there fails to start discards the worktree under the same lock,
			// and has to find this helper in it when it does; see discardWorktree.
			defer lockRepo(cwd)()
			repo, _ := gitx.Root(cwd)
			made, err := s.ws.PrepareWorktreeNew(cwd, req.Branch)
			if err != nil {
				return hooks.SpawnResult{}, err
			}
			cwd = made.Path
			// Only what this request made is this request's to take away again if the
			// helper then does not start (the lock above is held, so nothing else is in
			// it), and only if it is as it was left: see DiscardMade.
			if made.WorktreeCreated && repo != "" {
				cleanup = func(cause error) error { return s.ws.DiscardMade(repo, made, cause) }
			}
		}

		kind := session.KindClaude
		if req.Shell {
			kind = session.KindShell
		}

		// Scrubbed once more, saved and framed here, off the workspace goroutine;
		// Spawn only starts the agent with the prompt.
		var prepared *workspace.BatonPrompt
		if handoff != nil {
			bp, err := s.ws.PrepareBaton(cwd, handoff.Baton, req.Task, handoff.Scrubber)
			if err != nil {
				return hooks.SpawnResult{}, cleanup(err)
			}
			prepared = &bp
			_ = recordSource(bp.ID, handoff.Source)
		}

		type result struct {
			id  string
			err error
		}
		r, ok := ask(s, func() result {
			opts := workspace.SpawnOptions{
				Task:           req.Task,
				Cwd:            cwd,
				Split:          req.Split,
				TabName:        req.Tab,
				Kind:           kind,
				Agent:          req.Agent,
				Model:          req.Model,
				Role:           req.Role,
				SpawnedByAgent: true,
				Record:         req.Record,
				Baton:          prepared,
				Approved:       approved,
			}
			if routed.Routed {
				opts.Model, opts.Routed, opts.RoutedFrom = routed.Model, routed.Rule, baseModel
				if routed.Agent != "" {
					opts.Agent, opts.RoutedFromAgent = routed.Agent, baseAgent
				}
			}
			id, err := s.ws.Spawn(req.Parent, opts)
			return result{id, err}
		})
		if !ok {
			return hooks.SpawnResult{}, errShuttingDown
		}
		if r.err != nil {
			return hooks.SpawnResult{}, cleanup(r.err)
		}
		s.Wake()
		res := hooks.SpawnResult{PaneID: r.id, Cwd: cwd, Role: req.Role}
		if prepared != nil {
			res.BatonID, res.BatonPath = prepared.ID, workspace.BatonPath(prepared.ID)
		}
		return res, nil
	})
}

// uniqueBranch returns base, or base-2, base-3 and so on, until it names a
// branch that the repository does not have and this start has not already
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

// spawnApproved is told what every baton spawn carries into Spawn: a test looks at it.
var spawnApproved = func(*workspace.ApprovedTarget) {}
