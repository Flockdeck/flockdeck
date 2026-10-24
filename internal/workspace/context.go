package workspace

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/keytable"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
)

// siblingTaskLimit and ownTaskLimit bound how much of a task is written out.
//
// What another agent is doing is orientation, and a line of it is enough. What
// this agent was asked is not: after a compaction this is the only copy of it
// left, and 160 characters of a longer instruction is worse than none, since
// what survives reads like the whole of it. The cap that remains is there to
// keep a pasted wall of text from crowding out the rest of the context.
const (
	siblingTaskLimit = 160
	ownTaskLimit     = 1200
)

// maxTeam bounds how many panes the "Your team" list names; the rest are counted.
const maxTeam = 12

// maxSiblings bounds how many other agents are named in a pane's context. A
// long list stops being orientation and starts being noise, and it is stale
// the moment it is read anyway.
const maxSiblings = 10

// Sibling is another pane, as described to an agent.
type Sibling struct {
	Name   string
	Tab    string
	Cwd    string
	Branch string
	Status string
	Shell  bool
	Task   string
	// Agent and Model are what the pane is running: the agent's id and the
	// model it was asked for, as the pane header shows them. Which agent is in
	// the next pane changes what is worth asking of it — a plan is worth
	// handing to one, a mechanical edit to another — and neither is anything
	// the reader can work out from a directory and a branch.
	Agent string
	Model string
	// SameCheckout marks a pane working in the reader's own directory. It is
	// the one fact in the list that changes what the agent should do, and it
	// is easy to miss when it has to be read off two long paths.
	SameCheckout bool
	// Repo names the repo this sibling works in, filled in only when the
	// reader's own project spans more than one and this sibling's differs
	// from the reader's own -- see PaneContext.RepoName. A sibling in the
	// reader's own repo, which is every sibling before projects could span
	// more than one, leaves this empty and describe() reads exactly as it
	// always has.
	Repo string
}

// PaneContext is everything an agent is told about the situation it is running
// in: which pane it is, which checkout it has, and who else is working.
//
// It is a snapshot taken on the workspace's own goroutine and rendered
// elsewhere, so it holds values rather than pointers into live state.
type PaneContext struct {
	PaneID   string
	PaneName string
	Shell    bool

	Tab      string
	TabPanes int

	ProjectRoot string
	ProjectName string
	// RepoName is this pane's own repo, as it reads inside its project's
	// member list -- see Workspace.repoLabel. It differs from ProjectName
	// only when the project spans more than one repo, which is when it is
	// worth saying at all; see render.
	RepoName string
	Cwd      string
	Branch   string
	// Worktree reports that the pane works in a checkout of its own — a
	// directory outside the project altogether, which is what fan-out gives
	// each child, since Flockdeck puts a worktree beside the repository it came
	// from rather than inside it.
	Worktree bool
	// Subdirectory reports a working directory below the project root: the
	// same checkout, a directory or two down. It is not a worktree, and an
	// agent told that it is one is told to keep out of the rest of its own
	// repository.
	Subdirectory bool

	// Task is the opening prompt the pane was spawned with, when it was
	// started by a fan-out or by another agent rather than by hand. Which of
	// the two it was is not recorded, so the pane is not told either.
	Task string

	// Baton is the id of the baton the pane was started from, and BatonPath
	// where it is kept, when the pane was started from one. The opening prompt
	// carries the baton itself, but that prompt is summarised away with the
	// rest of the conversation; this line comes back with every briefing, so
	// the agent can still find what it was handed.
	Baton     string
	BatonPath string

	// Role is the id of the role the pane was started as, empty for a pane
	// started without one. RoleName, RoleBriefing, RoleWorktree and RoleSpawns
	// are what the catalog says of it; RoleKnown is false when the catalog has
	// no such role any more, as after an edit of agents.json. Roles are the ids
	// of every role in the catalog, which an agent starting helpers chooses
	// from.
	Role         string
	RoleKnown    bool
	RoleName     string
	RoleBriefing string
	RoleWorktree string
	RoleSpawns   []string
	RolePlain    bool
	Roles        []string

	Siblings []Sibling
	// SiblingsOmitted counts the live panes left out of Siblings once the list
	// hit its limit, so the agent is told the list is partial rather than
	// assuming it has seen everyone.
	SiblingsOmitted int

	OtherProjects []string

	// Team is the agent panes of this pane's project with the role, name, agent and
	// status of each, this pane included and marked. It is empty when this pane is
	// the only agent in its project. It is a snapshot, like Siblings.
	Team []team.Member

	// TaskCounts is what the project's task board holds, from this pane's point of
	// view, and HasCounts says it could be read. A snapshot, like Team.
	TaskCounts tasks.Counts
	HasCounts  bool

	// Notified says this pane's agent is told of a message in the reply to its
	// next prompt or tool call, which Flockdeck can do only for an agent that
	// reports its lifecycle through hooks.
	Notified bool

	// Structured says the pane is an agent behind a protocol, with no terminal
	// and no Flockdeck command line, so it cannot run `inbox` or `msg send`.
	// MailText says its mail is delivered as prompts holding the message text,
	// which the person has to have allowed for its agent type.
	Structured bool
	MailText   bool

	// KnowledgeDir is the project's knowledge bundle in this pane's own checkout,
	// when the briefing should point at it. See knowledgeDir.
	KnowledgeDir string

	// Taken is when the snapshot was made. It is written out only for an agent
	// briefed through its opening prompt, because that briefing arrives once
	// and is never replaced: everything below it is true of the moment the
	// pane started and of no moment after, and an agent that is not told so
	// will read a list of other agents that has since changed as current.
	Taken time.Time

	// CanSpawn reports whether spawning helpers will work from this pane.
	CanSpawn bool
	// SpawnCommand is how Flockdeck itself is run from inside the pane: the name
	// on PATH where there is one, and the binary's own path where there is not.
	SpawnCommand string
}

// PaneContext describes the situation a pane is running in. It reports false
// when the pane is unknown.
//
// Everything here comes from state the workspace already holds — no git or
// filesystem calls — because it is built while a pane's agent session waits
// on its SessionStart hook.
func (w *Workspace) PaneContext(paneID string) (PaneContext, bool) {
	p := w.Pane(paneID)
	if p == nil {
		return PaneContext{}, false
	}

	c := PaneContext{
		PaneID:       p.ID,
		PaneName:     p.Name,
		Shell:        p.Kind == session.KindShell,
		Cwd:          p.Cwd,
		Branch:       p.Branch,
		Task:         p.Task,
		Baton:        p.BatonID,
		Taken:        time.Now(),
		CanSpawn:     w.hookSrv != nil,
		SpawnCommand: w.spawnCmd,
	}

	if c.Baton != "" {
		c.BatonPath = batonPath(c.Baton)
	}
	if p.Role != "" && p.IsAgent() {
		cat := w.agents()
		c.Role = p.Role
		c.Roles = cat.RoleIDs()
		if r, ok := cat.Role(p.Role); ok {
			c.RoleKnown, c.RoleName, c.RoleBriefing = true, r.Name, r.Briefing
			c.RoleWorktree, c.RoleSpawns, c.RolePlain = r.Worktree, r.Spawns, r.Plain
		}
	}

	own := w.tabOf(paneID)
	if own != nil {
		c.Tab = own.Title
		c.TabPanes = len(own.Tree.Panes())
	}
	// The pane's own project, not the project of the tab it is drawn on. A tab
	// can show agents from more than one project, and a borrowed pane told it
	// belongs to the tab's project is told the wrong root — which it would then
	// read as a worktree of a project it has nothing to do with.
	c.ProjectRoot = w.rootOf(paneID)
	// Projects are named the way the switcher names them, so an agent and the
	// user looking at it call the same thing by the same word — and so two
	// checkouts of one repository are not both simply "the project". A
	// project spanning more than one repo -- see ProjectGroup -- is named by
	// its group, not by this pane's own repo alone.
	groups := w.groupsInOrder()
	groupNames := groupDisplayNames(groups)
	myGroup := w.groupOf(c.ProjectRoot)
	c.ProjectName = filepath.Base(c.ProjectRoot)
	if myGroup != nil {
		c.ProjectName = groupNames[myGroup.ID]
	}
	c.RepoName = w.repoLabel(c.ProjectRoot)
	if c.ProjectRoot != "" && !sameDir(c.Cwd, c.ProjectRoot) {
		c.Subdirectory = underDir(c.Cwd, c.ProjectRoot)
		c.Worktree = !c.Subdirectory
	}

	// The list is cut short in a busy project, so what it keeps matters more
	// than the order it keeps it in. An agent working in the reader's own
	// checkout comes first however far away it is drawn: it is the one fact
	// here that changes what the reader should do, and losing it to the cap
	// tells an agent it has its files to itself when it has not. Panes sharing
	// the tab come next, being the ones the user is watching side by side.
	var ownCheckoutSameTab, ownCheckout, sameTab, elsewhere []Sibling
	for _, t := range w.Tabs {
		for _, id := range t.Tree.Panes() {
			if id == paneID {
				continue
			}
			sib := w.Pane(id)
			if sib == nil {
				continue
			}
			// Membership follows the pane rather than the tab, for the same
			// reason the reader's own project does: an agent borrowed by
			// another project's tab is still working in this one, and one
			// borrowed from elsewhere is not. It is membership in the whole
			// project, not the one repo: a sibling working in another member
			// of the reader's own multi-repo project is a sibling, not
			// invisible.
			sibRoot := sib.Root
			if sibRoot == "" {
				sibRoot = t.Root
			}
			sibGroup := w.groupOf(sibRoot)
			if myGroup == nil || sibGroup == nil || sibGroup.ID != myGroup.ID {
				continue
			}
			// The session is taken under the lock. A restore starts its panes
			// on several goroutines at once, and one briefed through its
			// opening prompt is described while the others' sessions are
			// still being put in place.
			st, _ := w.Runtime(sib).Status()
			if st == session.StatusExited {
				continue
			}
			sibAgent, sibModel := paneAgent(sib)
			s := Sibling{
				Name:         sib.Name,
				Tab:          t.Title,
				Cwd:          sib.Cwd,
				Branch:       sib.Branch,
				Status:       st.String(),
				Shell:        sib.Kind == session.KindShell,
				Task:         sib.Task,
				Agent:        sibAgent,
				Model:        sibModel,
				SameCheckout: sameDir(sib.Cwd, c.Cwd),
			}
			if lbl := w.repoLabel(sibRoot); lbl != c.RepoName {
				s.Repo = lbl
			}
			switch onOwnTab := own != nil && t.ID == own.ID; {
			case s.SameCheckout && onOwnTab:
				ownCheckoutSameTab = append(ownCheckoutSameTab, s)
			case s.SameCheckout:
				ownCheckout = append(ownCheckout, s)
			case onOwnTab:
				sameTab = append(sameTab, s)
			default:
				elsewhere = append(elsewhere, s)
			}
		}
	}
	for _, group := range [][]Sibling{ownCheckoutSameTab, ownCheckout, sameTab, elsewhere} {
		c.Siblings = append(c.Siblings, group...)
	}
	if len(c.Siblings) > maxSiblings {
		c.SiblingsOmitted = len(c.Siblings) - maxSiblings
		c.Siblings = c.Siblings[:maxSiblings]
	}

	// One entry per other project, not per repo: a project the reader's own
	// is grouped with names itself once, however many repos it spans, the
	// same noise this removed from the human switcher removed from what the
	// agent is told too.
	for _, g := range groups {
		if myGroup != nil && g.ID == myGroup.ID {
			continue
		}
		c.OtherProjects = append(c.OtherProjects, groupNames[g.ID])
	}
	sort.Strings(c.OtherProjects)

	if p.Structured() {
		c.Structured = true
		c.MailText = w.wakeSettings != nil && wakeTextAllowed(wakeCandidate{Agent: p.Agent, Deliver: func(string) error { return nil }}, w.wakeSettings)
	}

	if !c.Shell && p.IsAgent() && c.Cwd != "" {
		c.KnowledgeDir = w.knowledgeDir(paneID, c.Cwd, c.ProjectRoot)
	}
	if p.IsAgent() && p.Agent != "" {
		if spec, ok := w.agents().Find(p.Agent); ok && spec.Caps.Hooks {
			c.Notified = true
		}
	}
	if c.CanSpawn && !c.Shell {
		// One small file is read here. The briefing is built while the agent
		// waits on its start hook, and this is the one place that reads a file;
		// a board that cannot be read leaves its counts out.
		c.TaskCounts, c.HasCounts = w.TaskCounts(paneID)
		if members, err := w.Roster(paneID); err == nil && len(members) > 1 {
			c.Team = members
		}
	}

	return c, true
}

// OpeningPrompt is what the agent in a pane should be started with: the task
// it was given, with the briefing in front of it when the agent has no hooks
// to answer.
//
// Starting a pane calls this instead of passing the task straight through,
// because the briefing is the same one the hook returns and there is nowhere
// else for it to go for an agent that cannot be asked. Any other mode gets the
// task untouched: an agent with hooks is briefed when it fires its first one,
// and one asking for no context has said so.
//
// It is called on the goroutine that owns the workspace, like every other read
// of it.
func (w *Workspace) OpeningPrompt(paneID, task string, mode agent.ContextMode) string {
	if mode != agent.ContextPrompt && mode != agent.ContextTask {
		return task
	}
	if mode == agent.ContextTask && strings.TrimSpace(task) == "" {
		// Nothing for the briefing to ride on, and this agent answers its
		// opening message once and exits: launch it bare.
		return task
	}
	c, ok := w.PaneContext(paneID)
	if !ok {
		// The pane is not in the workspace yet or is already gone. Either way
		// the agent still has work to do, and a task with no briefing is far
		// better than a briefing with no task.
		return task
	}
	// The task as the caller has it, rather than as the pane recorded it: a
	// pane started by hand has no Task at all, and one restarted has the task
	// it was spawned with long after the prompt it is being given now.
	c.Task = task
	return c.OpeningPrompt()
}

// paneAgent names the agent and the model a pane is running, exactly as the
// server reports them for its header, so the agent reading about a sibling and
// the user looking at it are told the same thing. A shell runs neither.
func paneAgent(p *Pane) (agentID, model string) {
	if !p.IsAgent() {
		return "", ""
	}
	return p.Agent, p.Model
}

// tabOf returns the tab containing a pane, or nil.
func (w *Workspace) tabOf(paneID string) *Tab {
	for _, t := range w.Tabs {
		if t.Tree.Find(paneID) != nil {
			return t
		}
	}
	return nil
}

// foldsCase says whether file names on this system ignore case, which is what
// decides whether two spellings of a path can name one directory. Windows and
// macOS ignore it by default, which is what the store assumes when it keys a
// project's layout; Linux does not, and there /code/Api and /code/api are two
// projects. It is a variable so that a test can ask about the other kind.
var foldsCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// sameDir compares two directory paths for the purposes of telling a pane
// apart from its project root, and one open project from another.
//
// Case is ignored only where the filesystem ignores it. Ignoring it everywhere
// made two projects on Linux whose names differ only in case one project:
// opening the second showed the first, and its own layout, which the store
// keeps apart, was never read.
func sameDir(a, b string) bool {
	return pathKey(a) == pathKey(b)
}

// pathKey is a directory as a map key: cleaned, and case-folded where the
// filesystem folds case, so that two spellings of one directory are one entry.
// The spellings really do arrive — from the command line, from git, from a
// picker — and where case is folded they name one directory.
func pathKey(dir string) string {
	d := filepath.Clean(dir)
	if foldsCase {
		d = strings.ToLower(d)
	}
	return d
}

// underDir reports whether dir is base or sits inside it.
//
// Case is folded for the same reason sameDir folds it: the two paths arrive
// from different places — one from a project as it was opened, the other from
// git or from a directory picker — and where case is folded they can name the
// same directory in different case.
func underDir(dir, base string) bool {
	d, b := filepath.Clean(dir), filepath.Clean(base)
	if foldsCase {
		// Folded before the comparison, not after: filepath.Rel compares the
		// two case sensitively, so folding its answer would be too late.
		d, b = strings.ToLower(d), strings.ToLower(b)
	}
	rel, err := filepath.Rel(b, d)
	if err != nil {
		return false
	}
	// Only a leading path element of ".." means the way out; a directory
	// genuinely named something like "..old" is inside the tree it sits in.
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Render writes the context as the text handed to an agent at session start,
// in answer to its own lifecycle hook.
//
// It is prose rather than JSON because it is read by the model, and it is
// deliberately specific about the two things an agent cannot work out for
// itself: that its conversation is private to one pane among several, and that
// the other panes may be editing other checkouts of the same repository.
func (c PaneContext) Render() string { return c.render(false) }

// OpeningPrompt is what an agent with no hooks to answer is started with: the
// same briefing, fenced, and then the task.
//
// The fence is there so the two can be told apart. Everything inside it is
// Flockdeck describing the pane; everything after it is what the user asked for,
// and an agent that reads the first as part of the second sets about
// documenting the window instead of doing the work. A pane started with no
// task gets the block on its own, which is still worth sending: knowing which
// checkout it has and who else is in the repository changes what the agent
// does with the first thing the user types.
func (c PaneContext) OpeningPrompt() string {
	var b strings.Builder
	b.WriteString("<flockdeck-context>\n")
	b.WriteString(strings.TrimRight(c.render(true), "\n"))
	b.WriteString("\n</flockdeck-context>\n")
	// The task follows whole and unsummarised, which is why the block above
	// leaves out the line that repeats it back: a briefing that quotes the
	// first thousand characters of an instruction printed in full two lines
	// below is a second, shorter copy for the agent to disagree with.
	if task := strings.TrimSpace(c.Task); task != "" {
		b.WriteString("\n" + task + "\n")
	}
	return b.String()
}

// writeRole writes the "Your role" section, for a pane started in a role. A
// pane with none is told nothing about roles: there is no job to describe, and
// the list of them is only of use to an agent that has been given one.
func (c PaneContext) writeRole(b *strings.Builder) {
	if c.Role == "" {
		return
	}
	b.WriteString("\n## Your role\n\n")
	if !c.RoleKnown {
		fmt.Fprintf(b, "You were started as the role %q, which is no longer defined in this installation's "+
			"agents.json, so there is no briefing for it here. Work from your task.\n", c.Role)
	} else {
		name := ""
		if c.RoleName != "" && c.RoleName != c.Role {
			name = " (" + c.RoleName + ")"
		}
		fmt.Fprintf(b, "You were started as the role %q%s.\n", c.Role, name)
		if briefing := strings.TrimSpace(c.RoleBriefing); briefing != "" {
			b.WriteString("\n" + briefing + "\n")
		}
		b.WriteString("\n")
		switch c.RoleWorktree {
		case agent.WorktreeOwn:
			b.WriteString("- Your checkout: meant to be a worktree and branch of your own. Flockdeck does not make one; whoever started you should have used `-worktree`.\n")
		case agent.WorktreeReadOnly:
			b.WriteString("- Your checkout: for reading. Do not change files or commit. Flockdeck does not enforce this, so the task names the branch to read.\n")
		case agent.WorktreeMain:
			b.WriteString("- Your checkout: the project's main checkout, which other agents may be working in too.\n")
		}
		// A role that may start others is told which in the "Starting agents"
		// section; saying so here as well is the same rule twice.
		if len(c.RoleSpawns) == 0 {
			b.WriteString("- You may not start agents in a role.\n")
			if c.RolePlain {
				b.WriteString("- You may start agents with no role.\n")
			} else {
				b.WriteString("- You may not start agents without a role; `flockdeck spawn` needs `-role`.\n")
			}
		}
	}
	if len(c.Roles) > 0 && len(c.RoleSpawns) > 0 {
		fmt.Fprintf(b, "- The roles in this project's catalog: %s.\n", strings.Join(c.Roles, ", "))
	}
}

// keys is the key table the program handed over (SetKeys). With none, every
// action reads as its bare id.
var keys keytable.Table

// SetKeys gives the workspace the key table its prose names actions from. The
// program calls it once, before any workspace is made.
func SetKeys(t keytable.Table) { keys = t }

// how names an action as prose calls it and says how it is reached, from the
// same table the palette and the help pages read.
func how(id string) string {
	k, ok := keys.Lookup(id)
	if !ok {
		// An id that has been renamed out from under this text should read as
		// the gap it is, rather than quietly leaving a sentence describing a
		// feature with no way to reach it.
		return id
	}
	if k.Keys == "" {
		return k.Name() + " (command palette)"
	}
	return k.Name() + " (" + k.Keys + ")"
}

// describe renders one sibling as a single line.
//
// Panes are named after their directory, so several of them share a name; the
// tab is what the user actually calls them, and after a fan-out it is named
// after the task. It is included whenever it says something the name does not.
func (s Sibling) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q", s.Name)
	if s.Tab != "" && !strings.EqualFold(s.Tab, s.Name) {
		fmt.Fprintf(&b, " in the tab %q", s.Tab)
	}
	if s.Shell {
		b.WriteString(" (a shell, not an agent)")
	} else if label := agentLabel(s.Agent, s.Model); label != "" {
		fmt.Fprintf(&b, " running %s", label)
	}
	if s.Branch != "" {
		fmt.Fprintf(&b, " on branch `%s`", s.Branch)
	}
	switch {
	case s.SameCheckout:
		// The path is the reader's own, so repeating it says nothing; that it
		// is shared says everything.
		b.WriteString(" in the same directory as you")
	case s.Repo != "":
		// A different repo within the reader's own project: named, since
		// nothing else here says so, and given the directory too, the way
		// every other sibling is.
		fmt.Fprintf(&b, " in %s, in `%s`", s.Repo, s.Cwd)
	default:
		fmt.Fprintf(&b, " in `%s`", s.Cwd)
	}
	if !s.Shell {
		fmt.Fprintf(&b, " — %s", s.Status)
	}
	if s.Task != "" {
		fmt.Fprintf(&b, "; working on: %s", oneLine(s.Task, siblingTaskLimit))
	}
	return b.String()
}

// agentLabel names an agent and its model the way the pane header does —
// `claude · sonnet`, `codex · gpt-5` — so that the agent reading about a
// sibling and the user looking at it are told the same thing in the same
// words. A model the agent was not asked for is not invented: an empty one
// means whatever that CLI is configured with, which is not Flockdeck's to report.
func agentLabel(agentID, model string) string {
	if agentID == "" || model == "" {
		return agentID
	}
	return agentID + " · " + model
}

// shellWord quotes a command for the shell the examples are written for.
//
// The path to a build that has not been installed is full of characters a
// shell reads as syntax rather than as part of a name: the space in "Program
// Files", the brackets after it. Anything but what a bare path is made of is
// quoted, rather than the space alone.
func shellWord(s string) string {
	// Double quotes keep a space and a bracket, but sh still reads a $, a
	// backtick and a double quote inside them, and a doubled backslash
	// anywhere — so a build run from a Windows share (\\server\...) was mangled
	// even though every character in its path is one a bare path is made of.
	// Single quotes keep every character as it is, and only a single quote
	// itself has to be written around.
	if strings.ContainsAny(s, "$`\"") || strings.Contains(s, `\\`) {
		return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
	}
	if s == "" || !strings.ContainsFunc(s, needsQuoting) {
		return s
	}
	return `"` + s + `"`
}

// needsQuoting reports a character a shell would not read as part of a path.
func needsQuoting(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '.', r == '_', r == '-', r == ':', r == '/', r == '\\':
		return false
	}
	return true
}

// oneLine flattens a prompt onto a single line and shortens it to max runes,
// so a pasted wall of text does not become the bulk of the context.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	// Counted in runes, not bytes: cutting a task written in any non-ASCII
	// script mid-rune would put a replacement character into the context.
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}

// spawnKind is what a pane may be told about starting agents.
type spawnKind int

const (
	// spawnAny is a pane with no role, or one whose role the catalog no longer
	// has or leaves open: the plain examples are right for it.
	spawnAny spawnKind = iota
	// spawnRoles is a pane whose role lists roles it may start, so `spawn` needs
	// -role and the examples must name one.
	spawnRoles
	// spawnNone is a pane whose role may start nothing. `spawn` is refused there,
	// so the briefing says nothing of it beyond what the role section does.
	spawnNone
)

func (c PaneContext) spawnRights() spawnKind {
	switch {
	case c.Role == "" || !c.RoleKnown:
		return spawnAny
	case len(c.RoleSpawns) > 0:
		return spawnRoles
	case c.RolePlain:
		return spawnAny
	}
	return spawnNone
}
