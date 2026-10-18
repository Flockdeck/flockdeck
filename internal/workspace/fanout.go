package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// maxTaskBytes bounds the task text an agent is started with. The same text is
// written into the saved layout, which is no place for a document. The limit
// is far above any brief anyone writes.
const maxTaskBytes = 16 << 10

// AgentSpec resolves the agent a pane was asked to run, and says why it cannot
// be run when it cannot. An empty id means the default agent of the project on
// screen, which is a field only the goroutine that owns the workspace may read;
// AgentSpecByID asks the same about an agent named by id, from anywhere.
//
// The question asked here is about one spec rather than about Claude. A
// fan-out may now give four of its tasks to one agent and eight to another,
// and the half whose agent is installed should start whatever is true of the
// other half -- which it cannot while the only question Flockdeck knows how to ask
// is whether Claude Code is on this machine.
func (w *Workspace) AgentSpec(id string) (agent.Spec, error) {
	if id == "" {
		id = w.defaultAgent()
	}
	return w.AgentSpecByID(id)
}

// AgentSpecByID is AgentSpec for an agent named by its id, and is safe to call
// from any goroutine. It reads the catalog, which has a lock of its own, and
// the claude CLI found when the workspace was made, and nothing else: not the
// active project, which is written on the workspace goroutine at every project
// switch. AgentSpec read that for every id -- empty or not, since it was an
// argument -- so the fan-out dialog probing agents, and a fan-out deciding
// which rows ask about folder trust, raced every switch. An empty id names no
// agent here.
func (w *Workspace) AgentSpecByID(id string) (agent.Spec, error) {
	spec, ok := w.agents().Find(id)
	if !ok {
		return agent.Spec{}, fmt.Errorf("there is no agent called %q", id)
	}
	if w.agentAvailable(spec) {
		return spec, nil
	}
	if spec.Exe == "" {
		return spec, fmt.Errorf("%s cannot be started on this machine", spec.Name)
	}
	// Word for word what the pane that would have failed says, how to install
	// the agent included: a refused spawn goes back to the agent that asked
	// for it, and a fan-out's notice to the user, and either is where somebody
	// learns the agent is missing.
	return spec, missingCLI(spec)
}

// missingCLI is what somebody is told when the CLI an agent runs is not
// installed — in the pane that could not start, and in the refusal of a spawn
// or a fan-out that would have needed it: what is wrong, and where the catalog
// knows it, how to put it right, worded as the session package words the same
// failure.
//
// Being told only "not found on PATH" left them to go and look up what the
// picker beside them already says.
func missingCLI(spec agent.Spec) error {
	if spec.Install == "" {
		return fmt.Errorf("the `%s` CLI was not found on PATH", spec.Exe)
	}
	return fmt.Errorf("the `%s` CLI was not found on PATH; install %s first (%s)", spec.Exe, spec.Name, spec.Install)
}

// agentAvailable reports whether a spec can be started on this machine.
//
// The catalog remembers what it probed for a few seconds, which is what keeps
// a twelve-row fan-out from walking PATH twelve times over to be told the same
// thing.
func (w *Workspace) agentAvailable(spec agent.Spec) bool {
	if spec.Exe == "claude" && w.claudeExe != "" {
		// Flockdeck looks the claude CLI up once at start-up, and that answer is
		// older and cheaper than any probe.
		return true
	}
	return agent.Available(spec)
}

// SpawnOptions describes a child agent to start.
type SpawnOptions struct {
	// Task is given to the agent as its opening prompt.
	Task string
	// Cwd is where it works. Empty means the parent's directory.
	Cwd string
	// Branch, when set, puts the child in its own git worktree on that branch
	// so parallel children cannot collide over one checkout.
	Branch string
	// Split places the child beside its parent instead of in a new tab.
	Split bool
	// Tab names an existing tab for the child to join, where it is arranged
	// into a grid with the panes already there. It is how a fan-out gathers a
	// dozen children into one tab rather than scattering them a tab each, and
	// it takes precedence over Split. A tab that has since been closed is
	// ignored, and the child is placed as it would have been without it.
	Tab string
	// TabName is the title of the tab to join, in the project the child is
	// started in: the tab with that title if there is one, otherwise a new tab
	// given it. It is how a manager keeps the panes for one piece of work in
	// one tab. When it is set, Tab and Split are not used.
	TabName string
	Kind    session.Kind
	Title   string
	// Agent names the agent.Spec the pane runs, and Model the model that agent
	// is asked for. Empty means the default, which is how a fan-out started
	// without a thought about either keeps running what Flockdeck already ran. An
	// empty model is not "no model": it leaves the choice to the agent, so a
	// CLI keeps whatever it was configured with.
	Agent string
	Model string
	// Role is the id of the role the child is started as, recorded on the pane
	// and put in its environment. The caller has already checked it exists and
	// folded the role's agent and model into Agent and Model.
	Role string
	// Routed names the routing rule that chose Model, and RoutedFrom the
	// model the child would otherwise have run. They are recorded on the pane
	// so its header says the model was routed, and why. RoutedFromAgent names
	// the agent RoutedFrom belongs to, set only when routing moved the child
	// to another agent, not only another model.
	Routed          string
	RoutedFrom      string
	RoutedFromAgent string
	// SpawnedByAgent marks a child started by its parent's own `flockdeck
	// spawn`, so the pane records the parent as its Pane.Parent -- as opposed
	// to a fan-out the user ran themselves from the window, whose children are
	// the user's own and carry no parent. Only installSpawnHandler sets it.
	SpawnedByAgent bool
	// Record starts the child with Pane.Recording on. Only the spawn handler
	// sets it, and only once the user has agreed to recording in the window.
	Record bool
	// Approved is the agent and company a baton was approved to go to. Spawn
	// resolves the agent itself, and refuses to start when it would now run another
	// or another company's (the catalog or a default changed while the user was
	// being asked), so what was approved is what runs.
	Approved *ApprovedTarget
	// Baton is a handoff the child starts from. It is a baton the caller has
	// already resolved and scrubbed, not text from the request: an agent
	// naming a baton in `flockdeck spawn --baton` hands the server a reference,
	// and the server resolves it (see internal/server), so a task string
	// cannot be used to carry unscrubbed text past the scrubber. It is made by
	// PrepareBaton, off the workspace goroutine, from the baton and Task; Spawn
	// starts the agent with its prompt and records its id on the pane.
	Baton *BatonPrompt
}

// Spawn starts a child agent, optionally in a worktree of its own.
//
// The task is handed to the agent as its opening argument rather than typed into
// the terminal: typing into a TUI means guessing when it is ready, while an
// argument is submitted by the agent itself the moment it starts.
// spawnPlace is where a helper spawned by a pane runs, and the project its parent
// is in: the folder asked for, or the parent's, or the active project's. A
// worktree sits beside its repository, under no open project, and a helper working
// in one belongs to the agent that asked for it rather than to whichever project
// happens to be on screen. It must run on the workspace goroutine.
func (w *Workspace) spawnPlace(parentPaneID, askedCwd string) (cwd, home string) {
	parent := w.Pane(parentPaneID)
	cwd = askedCwd
	if cwd == "" && parent != nil {
		cwd = parent.Cwd
	}
	if cwd == "" {
		cwd = w.activeRoot
	}
	home = w.activeRoot
	if parent != nil {
		home = w.rootOf(parentPaneID)
	}
	return cwd, home
}

// SpawnTarget is the agent and model Spawn starts a helper with, for a pane that
// spawns it with the agent and model asked for (either may be empty): the same
// project and the same resolution, in one place, so that what is checked before a
// spawn and what the spawn runs cannot differ. It must run on the workspace
// goroutine.
func (w *Workspace) SpawnTarget(parentPaneID, askedCwd, agentID, model string) (string, string) {
	cwd, home := w.spawnPlace(parentPaneID, askedCwd)
	return w.resolveChoice(w.helperProject(cwd, home), agentID, model)
}

// ApprovedTarget is the agent, and the company it sends what it is given to, that a
// baton was approved for.
type ApprovedTarget struct {
	Agent string
	// Provider is the company of the agent the helper was to run, as it was worked out
	// before it started; "" is not known.
	Provider string
	// SourceProvider is the company the baton came from; "" is not known.
	SourceProvider string
	// ApprovalRequired says the user was asked about this pair of companies and allowed
	// it. Without it nothing was asked, and the helper must run for the company the baton
	// came from.
	ApprovalRequired bool
	// From are the folders the company was judged from besides the one the helper runs
	// in: the checkout a worktree is cut from, whose project settings it will have. The
	// check at the start reads the same ones.
	From []string
}

// checkApproved refuses a spawn that would run a different agent, or the same agent
// for a different company, than the one a baton was approved for.
func (w *Workspace) checkApproved(a *ApprovedTarget, agentID, cwd string) error {
	if a == nil {
		return nil
	}
	spec, ok := w.agents().Find(agentID)
	if !ok {
		return fmt.Errorf("there is no agent called %q any more, so the baton that was approved for %q was not sent", agentID, a.Agent)
	}
	// Worked out again, now that the folder it runs in is really there: what it holds
	// can differ from what was read to decide.
	provider, _ := BatonProviderDetail(spec, cwd, a.From...)
	if spec.ID != a.Agent {
		return fmt.Errorf("the helper would now run %s, not the agent the baton was approved for (%s): the settings changed while it was being decided. Nothing was started; ask again", spec.Name, a.Agent)
	}
	if a.ApprovalRequired {
		// The user saw one company going to another: it is that company, whichever settings
		// said so.
		if provider != a.Provider {
			return fmt.Errorf("the helper would now run for %s, not the company the baton was approved for (%s): the settings changed while it was being decided. Nothing was started; ask again", companyWord(provider), companyWord(a.Provider))
		}
		return nil
	}
	if provider != a.SourceProvider {
		return fmt.Errorf("the helper would run for %s, and the baton came from %s, with nothing approved for that: the folder it works in holds settings that change it. Nothing was started", companyWord(provider), companyWord(a.SourceProvider))
	}
	return nil
}

// companyWord says a company for a message, never empty.
func companyWord(p string) string {
	if p == "" {
		return "a company that is not known"
	}
	return ProviderHost(p)
}

// BranchSettings are the settings files that the worktree for a branch will hold that Claude
// Code reads, from the object store: .claude/settings.json and .claude/settings.local.json as
// committed (the second is usually not tracked, but a repository may commit it). For a branch
// that exists it is the branch's; for one that does not it is the commit the new branch is cut
// from, which is the HEAD of the checkout cwd is in. The checkout's own working copy is read
// besides, by the folders beside the worktree, and what either says counts. Names are matched
// without regard to case (.Claude/Settings.json), and a .claude committed as a link to a folder
// in the same commit is followed; a link that leaves the repository or cannot be resolved,
// a settings file that is a link, over the limit, or that git cannot give, make unknown not
// empty: it is the reason, and the company is not known.
func (w *Workspace) BranchSettings(cwd, branch string) (blobs [][]byte, unknown string) {
	repo, err := gitx.Root(cwd)
	if err != nil {
		return nil, ""
	}
	rev := "HEAD"
	if gitx.BranchExists(repo, branch) {
		// The branch itself: a tag of the same name would come first for a plain name.
		rev = "refs/heads/" + branch
	}
	fail := func(why string) ([][]byte, string) {
		return nil, "the settings the commit " + rev + " holds could not be read: " + why
	}
	top, err := gitx.TreeEntries(repo, rev)
	if err != nil {
		return fail(err.Error())
	}
	for _, e := range top {
		if !strings.EqualFold(e.Name, ".claude") {
			continue
		}
		treeish := ""
		switch {
		case e.Type == "tree":
			treeish = rev + ":" + e.Name
		case e.Mode == "160000":
			return fail(".claude is a submodule in the commit")
		case e.Mode == "120000":
			target, err := gitx.BlobLimited(repo, e.Sha, 4096)
			if err != nil {
				return fail(".claude is a link that cannot be read: " + err.Error())
			}
			sha, why := resolveClaudeLink(repo, rev, target)
			if why != "" {
				return fail(why)
			}
			treeish = sha
		default:
			continue // a file called .claude holds no settings
		}
		files, err := gitx.TreeEntries(repo, treeish)
		if err != nil {
			return fail(err.Error())
		}
		for _, f := range files {
			if !strings.EqualFold(f.Name, "settings.json") && !strings.EqualFold(f.Name, "settings.local.json") {
				continue
			}
			if f.Mode != "100644" && f.Mode != "100755" {
				return fail(f.Name + " is not a regular file in the commit")
			}
			data, err := gitx.BlobLimited(repo, f.Sha, settingsFileMax)
			if err != nil {
				return fail(f.Name + ": " + err.Error())
			}
			blobs = append(blobs, data)
		}
	}
	return blobs, ""
}

// PlannedWorktree is where a worktree for branch of the repository cwd is in will
// be, or is: the one the branch already has, or the one that would be made. Nothing
// is made. It is for working out where a helper will run before it is started.
func (w *Workspace) PlannedWorktree(cwd, branch string) string {
	repo, err := gitx.Root(cwd)
	if err != nil {
		return cwd
	}
	if path, ok := existingWorktree(repo, branch); ok {
		return path
	}
	return gitx.DefaultWorktreePath(repo, branch)
}

// existingWorktree is the worktree a branch already has, other than the checkout itself.
func existingWorktree(repo, branch string) (string, bool) {
	if wts, err := gitx.List(repo); err == nil {
		for _, wt := range wts {
			if wt.Branch == branch && !wt.Prunable && filepath.Clean(wt.Path) != filepath.Clean(repo) {
				return wt.Path, true
			}
		}
	}
	return "", false
}

// MadeWorktree is what PrepareWorktreeNew did, exactly: the worktree's path, whether this
// call made the worktree, whether it made the branch (the branch did not exist before),
// and the commit the branch and the worktree were at when the call finished. Only what
// the call made is the caller's to take away again, and only if it is as it was left.
type MadeWorktree struct {
	Path            string
	WorktreeCreated bool
	BranchCreated   bool
	Branch          string
	Start           string
}

// PrepareWorktreeNew is PrepareWorktree that also says what this call made. A branch
// that existed with no worktree is checked out by it, which makes a worktree and not a
// branch; one that has a worktree is used as it is and nothing is made.
func (w *Workspace) PrepareWorktreeNew(cwd, branch string) (MadeWorktree, error) {
	repo, rerr := gitx.Root(cwd)
	if rerr != nil {
		path, err := w.worktreeFor(cwd, branch)
		return MadeWorktree{Path: path}, err
	}
	// Looked at before anything that could make either.
	branchExisted := gitx.BranchExists(repo, branch)
	_, hadWorktree := existingWorktree(repo, branch)
	path, err := w.worktreeFor(cwd, branch)
	m := MadeWorktree{Path: path, Branch: branch}
	if err != nil {
		return m, err
	}
	m.WorktreeCreated = !hadWorktree
	m.BranchCreated = !branchExisted && !hadWorktree
	if m.WorktreeCreated {
		m.Start, _ = gitx.Head(path)
	}
	return m, nil
}

// DiscardMade takes away what PrepareWorktreeNew made, for a helper that then did not
// start, and returns cause with what became of it said. A worktree is removed only if
// the call made it and nothing has changed in it since (no changed or new files, still
// at the commit it was made at); a branch only if the call made it and it is still at
// that commit, deleted only if so, with git told the commit it expects. Anything else is
// kept, and said to be kept. A branch that existed before is never touched.
func (w *Workspace) DiscardMade(repo string, m MadeWorktree, cause error) error {
	if repo == "" || !m.WorktreeCreated {
		return cause
	}
	name := filepath.Base(m.Path)
	// Two looks, and either one is enough to keep it. git status (ignored files
	// included) finds changes to what was checked out and files git knows of. A walk of the
	// files against the ones git checked out finds the rest, from the user, a hook or a
	// tool: empty folders too, which git does not show at all, and which a removal that is
	// not forced deletes.
	extra, total, xerr := extrasOf(m.Path, 5)
	clean, cerr := gitx.IsClean(m.Path)
	hidden, herr := hiddenOf(m.Path, 5)
	if err := errors.Join(xerr, cerr, herr); err != nil {
		return fmt.Errorf("%w (its worktree %s was kept: it could not be checked for files that came since: %v)", cause, name, err)
	}
	// A file marked assume-unchanged reads as clean to git status whatever was done to it.
	if len(hidden) > 0 {
		return fmt.Errorf("%w (its worktree %s was kept: git is told not to look at %s (assume-unchanged or skip-worktree))", cause, name, strings.Join(hidden, ", "))
	}
	if total > 0 {
		more := ""
		if total > len(extra) {
			more = fmt.Sprintf(" and %d more", total-len(extra))
		}
		return fmt.Errorf("%w (its worktree %s was kept: it holds files that were not checked out, %s%s)", cause, name, strings.Join(extra, ", "), more)
	}
	if !clean {
		return fmt.Errorf("%w (its worktree %s was kept: it has changes in it that were made since)", cause, name)
	}
	if head, err := gitx.Head(m.Path); err != nil || head != m.Start {
		return fmt.Errorf("%w (its worktree %s was kept: it has moved on from where it was made)", cause, name)
	}
	if err := gitx.Remove(repo, m.Path, false); err != nil {
		return fmt.Errorf("%w (its worktree %s could not be removed: %v)", cause, name, err)
	}
	if !m.BranchCreated {
		return fmt.Errorf("%w (the worktree %s that was made for it was removed; the branch %s was there before and was kept)", cause, name, m.Branch)
	}
	// The look is for a clear message; what protects the branch is that git is told the
	// commit it must still be at when it deletes the ref, since the branch can move
	// between the look and the delete (another process committing to it).
	if tip, err := gitx.Tip(repo, m.Branch); err != nil || tip != m.Start {
		return fmt.Errorf("%w (the worktree %s was removed; the branch %s was kept: it has moved since it was made)", cause, name, m.Branch)
	}
	beforeBranchDelete(repo, m)
	if err := gitx.DeleteBranchAt(repo, m.Branch, m.Start); err != nil {
		return fmt.Errorf("%w (the worktree %s was removed; the branch %s was kept: it moved just now, or could not be deleted: %v)", cause, name, m.Branch, err)
	}
	return fmt.Errorf("%w (the worktree and branch %s that were made for it were removed)", cause, m.Branch)
}

// MaxHelpers is the most panes that may be open below one agent that started
// them with `flockdeck spawn`, counting the helpers it started and those they
// started in turn, and counting a helper whose process has exited until its pane
// is closed. A manager that delegates on its own has no user approving each
// start, so this is what keeps a plan from fanning out without end, including
// through a helper with no role, which is not limited by its role. Closing a
// helper that is done makes room.
const MaxHelpers = 8

// maxHelperDepth bounds the walk up a chain of parents.
const maxHelperDepth = 16

// ancestorsLocked lists the panes that started p, and the ones that started
// them: those recorded when p was started, which survive the close of a pane
// in the middle of the chain, and the chain of Parent ids, which a restored pane
// still has. The caller holds w.mu.
func (w *Workspace) ancestorsLocked(p *Pane) []string {
	out := slices.Clone(p.Ancestors)
	for id, depth := p.Parent, 0; id != "" && depth < maxHelperDepth; depth++ {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
		q := w.panes[id]
		if q == nil {
			break
		}
		id = q.Parent
	}
	return out
}

// helpersBelow counts the open panes that descend from the pane root, whether
// or not the panes between are still open. The caller holds w.mu.
func (w *Workspace) helpersBelow(root string) int {
	n := 0
	for _, p := range w.panes {
		if slices.Contains(w.ancestorsLocked(p), root) {
			n++
		}
	}
	return n
}

// HelperRoom says whether the agent in the pane parent may start another
// helper: neither it nor any agent that started it, up the chain, may already
// have MaxHelpers open below it. The error says whose limit was reached and how
// to make room. A start from outside any pane has no limit.
//
// Spawn checks again, under the lock that adds the pane, so two starts at once
// cannot both take the last place; this is the early answer, given before
// anything is made for the helper.
func (w *Workspace) HelperRoom(parent string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.helperRoomLocked(parent)
}

func (w *Workspace) helperRoomLocked(parent string) error {
	p := w.panes[parent]
	if parent == "" || p == nil {
		return nil
	}
	if n := w.helpersBelow(parent); n >= MaxHelpers {
		return fmt.Errorf("this pane already has %d helpers open, the most it may have; close the ones that are done with `flockdeck close <pane-id>` or wait for them to finish", n)
	}
	for _, id := range w.ancestorsLocked(p) {
		if n := w.helpersBelow(id); n >= MaxHelpers {
			return fmt.Errorf("the agent that started this pane already has %d helpers open below it, the most it may have; ask it to close the ones that are done", n)
		}
	}
	return nil
}

func (w *Workspace) Spawn(parentPaneID string, o SpawnOptions) (string, error) {
	if o.SpawnedByAgent {
		if err := w.HelperRoom(parentPaneID); err != nil {
			return "", err
		}
	}
	if o.Baton != nil && o.Kind == session.KindShell {
		return "", fmt.Errorf("a baton starts an agent, and a shell is not one")
	}
	if strings.TrimSpace(o.Task) == "" && o.Kind != session.KindShell && o.Baton == nil {
		return "", fmt.Errorf("a task is required")
	}
	if len(o.Task) > maxTaskBytes {
		// The length said is in characters, which is what the person who wrote
		// the task can check it against; the limit is in bytes, so it is not
		// quoted, since in most scripts the two would never agree.
		return "", fmt.Errorf("the task is %d characters, too long to start an agent with; save the detail to a file in the checkout and give the agent a task that points at it",
			utf8.RuneCountInString(o.Task))
	}
	parent := w.Pane(parentPaneID)
	cwd, home := w.spawnPlace(parentPaneID, o.Cwd)

	// Asked about the agent this pane will actually run rather than about
	// Claude, because a fan-out may now hand half its tasks to one agent and
	// half to another, and "the `claude` CLI was not found" is a nonsense
	// answer to a task that was never going to run Claude. One asked for no
	// agent runs its own project's default, which need not be the default of
	// the project on screen. It is asked before any worktree is made for a
	// helper that could not start in it.
	agentID, model := o.Agent, o.Model
	if o.Kind != session.KindShell {
		agentID, model = w.SpawnTarget(parentPaneID, o.Cwd, o.Agent, o.Model)
		if _, err := w.AgentSpec(agentID); err != nil {
			return "", err
		}
		if err := w.checkApproved(o.Approved, agentID, cwd); err != nil {
			return "", err
		}
	}

	if o.Branch != "" {
		path, err := w.worktreeFor(cwd, o.Branch)
		if err != nil {
			return "", err
		}
		cwd = path
	}

	title := o.Title
	if title == "" {
		title = summarisePrompt(o.Task)
	}
	if title == "" && o.Baton != nil {
		title = o.Baton.Title
	}
	if title == "" {
		title = filepath.Base(cwd)
	}

	// With a baton the opening prompt is the baton and then the task; Task
	// stays what the pane was asked, which is what its header and briefing say.
	initial, batonID, batonPrompt := o.Task, "", ""
	if o.Baton != nil {
		initial, batonID, batonPrompt = o.Baton.Prompt, o.Baton.ID, o.Baton.Prompt
	}

	p := &Pane{
		ID:      uuid.NewString(),
		Kind:    o.Kind,
		Cwd:     cwd,
		Name:    filepath.Base(cwd),
		Root:    w.helperProject(cwd, home),
		Branch:  branchOf(cwd),
		initial: initial,
		Task:    o.Task,
		BatonID: batonID,
		Role:    o.Role,
		// Kept in memory so a restart before the first turn asks the same thing.
		batonPrompt: batonPrompt,
		// A child inherits the pane it was started from trusting auto-review
		// or not. Trust the boss has already extended to one pane -- by
		// turning auto-review on for it -- carries to the fan-out or the
		// helper it starts, rather than needing to be found and flipped on a
		// dozen new panes one at a time. See Pane.AutoReview.
		//
		// Not for a helper an agent started on its own: that would hand an
		// agent that is acting without a user approving each step helpers that
		// approve their own tool calls. Such a helper starts with the
		// installation's default, and the user turns it on for it.
		AutoReview: parent != nil && parent.AutoReview && !o.SpawnedByAgent,
		Recording:  o.Record && o.Kind != session.KindShell,
	}
	if o.SpawnedByAgent && parent != nil {
		p.Parent = parentPaneID
		w.mu.RLock()
		p.Ancestors = append(w.ancestorsLocked(parent), parentPaneID)
		w.mu.RUnlock()
	}
	// A pane started with no parent pane behind it at all -- opened by hand,
	// or a fan-out with no boss pane to speak of -- starts AutoReview from
	// the installation's own default rather than off unconditionally. This
	// is parent == nil, not p.Parent == "": a window-driven fan-out row has
	// a real parent (the pane whose plan it came from) and inherits that
	// pane's live AutoReview above, even though p.Parent itself is left
	// empty for it (see the assignment above and Pane.Parent) -- checking
	// the field here instead of the pointer would silently overwrite that
	// inheritance with the installation default for every such row.
	if parent == nil || o.SpawnedByAgent {
		p.AutoReview = store.LoadPrefs().AutoReviewDefault
	}
	if p.IsAgent() {
		p.Agent, p.Model = agentID, model
		// o.Agent is "" for same-agent routing, which never touches it -- a
		// fan-out row or a spawned helper left to the project's own default
		// agent -- so only an o.Agent that was actually given has to match
		// what this resolved to. Requiring an exact match unconditionally
		// dropped the badge on every same-agent route a helper had, since
		// spawn only ever routes a helper asked for no agent of its own.
		if o.Routed != "" && model == o.Model && (o.Agent == "" || agentID == o.Agent) {
			p.Routed, p.RoutedFrom, p.RoutedFromAgent = o.Routed, o.RoutedFrom, o.RoutedFromAgent
		}
	}
	w.mu.Lock()
	if o.SpawnedByAgent {
		// Checked and added under one lock: of two starts at once, only one
		// can take the last place.
		if err := w.helperRoomLocked(parentPaneID); err != nil {
			w.mu.Unlock()
			return "", err
		}
	}
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, false)
	if p.Err != nil {
		err := p.Err
		w.destroyPane(p.ID)
		return "", err
	}

	// Place it: in the tab it was asked to join, beside its parent, or as a tab
	// of its own.
	placed := false
	var named *Tab
	if o.TabName != "" {
		root := w.activeRoot
		if parent != nil {
			for _, t := range w.Tabs {
				if t.Tree.Find(parentPaneID) != nil {
					root = t.Root
					break
				}
			}
		}
		for _, t := range w.Tabs {
			// Only a tab an earlier -tab made: never one the user made by hand,
			// one named after the directory, or the tab of the pane asking.
			if t.Root == root && t.Spawned && strings.EqualFold(t.Title, o.TabName) {
				named = t
				break
			}
		}
		if named == nil {
			title = o.TabName
		}
	}
	if named != nil {
		t := named
		t.Tree = layout.Grid(append(t.Tree.Panes(), p.ID))
		t.Zoom = false
		t.Delegated = true
		placed = true
	} else if t := w.Tab(o.Tab); o.TabName == "" && t != nil {
		// Rebuilt as a grid rather than split off whichever pane happens to be
		// there, because this is the path a fan-out arrives on: a dozen panes
		// added one at a time, each of which would otherwise halve the last.
		t.Tree = layout.Grid(append(t.Tree.Panes(), p.ID))
		t.Zoom = false
		t.Delegated = true
		placed = true
	}
	if !placed && o.TabName == "" && o.Split && parent != nil {
		for _, t := range w.Tabs {
			if t.Tree.Find(parentPaneID) == nil {
				continue
			}
			if t.Tree.Split(parentPaneID, p.ID, layout.Vertical) {
				t.Zoom = false
				placed = true
			}
			break
		}
	}
	if !placed {
		root := w.activeRoot
		if parent != nil {
			for _, t := range w.Tabs {
				if t.Tree.Find(parentPaneID) != nil {
					root = t.Root
					break
				}
			}
		}
		t := &Tab{
			ID:        uuid.NewString(),
			Root:      root,
			Title:     title,
			Tree:      layout.NewLeaf(p.ID),
			Focus:     p.ID,
			Delegated: true,
			Spawned:   o.TabName != "",
		}
		w.Tabs = append(w.Tabs, t)
	}
	w.wake()
	return p.ID, nil
}

// resolveStartAgent settles what StartAgent needs before it can run: root as
// an already open project, spelled as it was opened, and the agent and model
// that choice resolves to for that project. Nothing is created; a caller
// asks this first to refuse a bad request -- an unknown root or agent --
// before doing anything as slow as cutting a worktree for it, and StartAgent
// asks it again right before it creates the pane, so a project closed or an
// agents.json edited in between is still caught.
func (w *Workspace) resolveStartAgent(root, agentID, model string) (openRoot, resolvedAgent, resolvedModel string, err error) {
	open, ok := w.openRootFor(root)
	if !ok {
		return "", "", "", fmt.Errorf("%s is not an open project", filepath.Base(root))
	}
	agentID, model = w.resolveChoice(open, agentID, model)
	if _, err := w.AgentSpecByID(agentID); err != nil {
		return "", "", "", err
	}
	return open, agentID, model, nil
}

// ValidateStartAgent reports what StartAgent would run for root, agentID and
// model, or the error it would refuse with, without starting anything.
func (w *Workspace) ValidateStartAgent(root, agentID, model string) (openRoot, resolvedAgent string, err error) {
	open, agentID, _, err := w.resolveStartAgent(root, agentID, model)
	return open, agentID, err
}

// StartAgentAs is StartAgent for a pane started in the role with the id role,
// or in none when it is empty. The caller has checked the role and settled
// agentID and model from it; see Catalog.Choose.
func (w *Workspace) StartAgentAs(root, agentID, model, cwd, task, role string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("a task is required")
	}
	if len(task) > maxTaskBytes {
		return "", fmt.Errorf("the task is %d characters, too long to start an agent with; save the detail to a file in the checkout and give the agent a task that points at it",
			utf8.RuneCountInString(task))
	}
	open, agentID, model, err := w.resolveStartAgent(root, agentID, model)
	if err != nil {
		return "", err
	}
	if cwd == "" {
		cwd = open
	}

	title := summarisePrompt(task)
	if title == "" {
		title = filepath.Base(cwd)
	}

	p := &Pane{
		ID:      uuid.NewString(),
		Kind:    session.KindClaude,
		Cwd:     cwd,
		Name:    filepath.Base(cwd),
		Root:    open,
		Branch:  branchOf(cwd),
		initial: task,
		Task:    task,
		Agent:   agentID,
		Model:   model,
		Role:    role,
		// Never given a Parent (see the doc comment above), so it starts
		// AutoReview from the installation's own default like any other pane
		// the user started themselves.
		AutoReview: store.LoadPrefs().AutoReviewDefault,
	}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, false)
	if p.Err != nil {
		err := p.Err
		w.destroyPane(p.ID)
		return "", err
	}

	w.Tabs = append(w.Tabs, &Tab{
		ID:    uuid.NewString(),
		Root:  open,
		Title: title,
		Tree:  layout.NewLeaf(p.ID),
		Focus: p.ID,
	})
	w.wake()
	return p.ID, nil
}

// PrepareWorktree creates (or reuses) a worktree for a branch and returns its
// path. It only runs git, so it is safe to call away from the goroutine that
// owns the workspace — which matters, because git is slow.
func (w *Workspace) PrepareWorktree(cwd, branch string) (string, error) {
	return w.worktreeFor(cwd, branch)
}

// worktreeFor creates (or reuses) a worktree for a branch and returns its path.
func (w *Workspace) worktreeFor(cwd, branch string) (string, error) {
	if !gitx.Available() {
		return "", fmt.Errorf("git is not installed, so a worktree cannot be created")
	}
	repo, err := gitx.Root(cwd)
	if err != nil {
		// Only git's plain "not a git repository (or any of the parent
		// directories)" means there is no repository here. Every other
		// refusal is about one that is here — a worktree whose repository
		// has moved, a .git file that is not what it should be, a checkout
		// git will not trust because another account owns it — and calling
		// all of them "not a git repository" sent the user looking for a
		// repository they could see was there. git's own words say which.
		if strings.Contains(err.Error(), "or any of the parent directories") {
			return "", fmt.Errorf("%s is not a git repository", cwd)
		}
		return "", fmt.Errorf("a worktree cannot be made from %s: %w", cwd, err)
	}
	// A branch that already has a worktree is reused rather than duplicated.
	if wts, err := gitx.List(repo); err == nil {
		for _, wt := range wts {
			// One whose folder is gone is only git's record of a checkout,
			// and handed back it started the helper in a folder that is not
			// there. Left to AddFrom, it is refused with how to clear it.
			if wt.Branch != branch || wt.Prunable {
				continue
			}
			// Reusing a sibling worktree is the point. Handing back the
			// checkout the request came from is not: the child would work in
			// the very directory the worktree was asked for to keep it out of,
			// and nothing would say so.
			if filepath.Clean(wt.Path) == filepath.Clean(repo) {
				return "", fmt.Errorf("%s is checked out in %s itself, so a worktree of its own cannot be made for it", branch, filepath.Base(repo))
			}
			return wt.Path, nil
		}
	}
	path := gitx.DefaultWorktreePath(repo, branch)
	if err := gitx.AddFrom(repo, path, branch, ""); err != nil {
		return "", err
	}
	return path, nil
}

// maxBranchName bounds the derived part of a branch name. A task description
// is a sentence, and a branch that long is unreadable in `git branch`.
const maxBranchName = 32

// BranchNameFor derives a git branch name from a task description.
//
// Letters are letters in every script. Keeping only a-z threw away every
// character of a task written in Cyrillic, Greek, Japanese or anything else,
// which left nothing at all — so each of those tasks came out as the fallback
// name, and a whole fan-out landed on agent/task, agent/task-2, agent/task-3,
// naming nothing. git takes UTF-8 in a ref, and the worktree directory derived
// from it keeps the same characters already.
func BranchNameFor(task string) string {
	var b strings.Builder
	lastDash := true
	written := 0
	for _, r := range strings.ToLower(task) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if lastDash {
				continue
			}
			b.WriteByte('-')
			lastDash = true
		}
		// One character past the limit, which is what tells a name that only
		// just fits from one that was cut short.
		written++
		if written > maxBranchName {
			break
		}
	}
	// Counted in characters rather than in bytes, because what the limit is for
	// is a name that can be read in `git branch` — and because measuring in
	// bytes would both halve a Cyrillic name and, worse, cut one in the middle
	// of a character, handing git a ref that is not UTF-8 at all.
	name := strings.Trim(b.String(), "-")
	if r := []rune(name); len(r) > maxBranchName {
		cut := maxBranchName
		// A cut that lands inside a word backs up to the boundary before it, so
		// the branch reads as words rather than as a fragment. A cut that lands
		// on one is already where it should be, and backing up from there would
		// throw away a word the name had room for.
		if r[cut] != '-' {
			if i := lastDashBefore(r, cut); i > 8 {
				cut = i
			}
		}
		name = strings.Trim(string(r[:cut]), "-")
	}
	if name == "" {
		name = "task"
	}
	return "agent/" + name
}

// lastDashBefore reports the index of the last dash in r before cut, or -1.
func lastDashBefore(r []rune, cut int) int {
	for i := cut - 1; i >= 0; i-- {
		if r[i] == '-' {
			return i
		}
	}
	return -1
}

// hiddenOf is gitx.AssumeUnchanged; a test replaces it to make the look fail.
var hiddenOf = gitx.AssumeUnchanged

// extrasOf is gitx.Extras; a test replaces it to make the look at the files fail.
var extrasOf = gitx.Extras

// beforeBranchDelete runs between looking at a made branch and deleting it; a test moves
// the branch there, as another process could.
var beforeBranchDelete = func(repo string, m MadeWorktree) {}
